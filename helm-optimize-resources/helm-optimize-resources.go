package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/densify-quick-start/helm-optimize-resources/densify"
	"github.com/densify-quick-start/helm-optimize-resources/ssm"
	"github.com/densify-quick-start/helm-optimize-resources/support"
	"github.com/ghodss/yaml"
)

//VARIABLE DECLARATIONS
var availableAdapters = map[int]string{
	1: "Densify",
	2: "Parameter Store",
}
var adapter string
var localCluster string
var remoteCluster string
var namespace string
var objTypeContainerPath = map[string]string{
	"Pod":                   "{.spec.containers}",
	"CronJob":               "{.spec.jobTemplate.spec.template.spec.containers}",
	"DaemonSet":             "{.spec.template.spec.containers}",
	"Job":                   "{.spec.template.spec.containers}",
	"ReplicaSet":            "{.spec.template.spec.containers}",
	"ReplicationController": "{.spec.template.spec.containers}",
	"StatefulSet":           "{.spec.template.spec.containers}",
	"Deployment":            "{.spec.template.spec.containers}",
}

//HelmBin location of helm installation
var HelmBin string = os.Getenv("HELM_BIN")

//KubectlBin location of kubectl installation
var KubectlBin string = "kubectl"

////////////////////////////////////////////////////////
/////////////////ADAPTER FUNCTIONS//////////////////////
////////////////////////////////////////////////////////

func initializeAdapter() error {

	if adapter == "" {
		if val, ok := support.RetrieveSecrets("helm-optimize-plugin")["adapter"]; ok {
			adapter = val
		} else {
			adapter = "Densify"
		}
	}

	var err error
	switch adapter {
	case "Densify":
		err = densify.Initialize()
	case "Parameter Store":
		err = ssm.Initialize()
	}

	if err != nil {
		fmt.Println(err)
		var tryAgain string
		fmt.Print("Would you like to try again (y/n): ")
		fmt.Scanln(&tryAgain)
		if tryAgain == "y" {
			return initializeAdapter()
		}
	}

	return err

}

func getInsight(cluster string, namespace string, objType string, objName string, containerName string) (map[string]map[string]string, string, error) {

	var insight map[string]map[string]string
	var approvalSetting string
	var err error

	switch adapter {
	case "Densify":
		insight, approvalSetting, err = densify.GetInsight(cluster, namespace, objType, objName, containerName)
	case "Parameter Store":
		insight, approvalSetting, err = ssm.GetInsight(cluster, namespace, objType, objName, containerName)
	}

	if err != nil {
		return nil, "Not Approved", err
	}

	return insight, approvalSetting, nil

}

func updateApprovalSetting(approved bool, cluster string, namespace string, objType string, objName string, containerName string) error {

	var err error

	switch adapter {
	case "Densify":
		err = densify.UpdateApprovalSetting(approved, cluster, namespace, objType, objName, containerName)
	case "Parameter Store":
		err = ssm.UpdateApprovalSetting(approved, cluster, namespace, objType, objName, containerName)
	}

	return err

}

func getApprovalSetting(cluster string, namespace string, objType string, objName string, containerName string) (string, error) {

	var approvalSetting string
	var err error

	switch adapter {
	case "Densify":
		approvalSetting, err = densify.GetApprovalSetting(cluster, namespace, objType, objName, containerName)
	case "Parameter Store":
		approvalSetting, err = ssm.GetApprovalSetting(cluster, namespace, objType, objName, containerName)
	}

	return approvalSetting, err

}

////////////////////////////////////////////////////////
/////////////////SUPPORTING FUNCTIONS///////////////////
////////////////////////////////////////////////////////

func selectAdapter() {

	//get adapter selection from user
	for {
		fmt.Println("Select Adapter")
		i := 1
		for range availableAdapters {
			fmt.Println("  " + strconv.Itoa(i) + ". " + availableAdapters[i])
			i++
		}
		fmt.Print("Selection: ")

		var selectedValue string
		fmt.Scanln(&selectedValue)

		var userSelection int
		var err error
		if userSelection, err = strconv.Atoi(selectedValue); err != nil || (userSelection < 1 || userSelection > len(availableAdapters)) {
			fmt.Println("Incorrect adapter selection.  Try again.")
			continue
		}
		adapter = availableAdapters[userSelection]
		break
	}

}

func processPluginSwitches(args []string) {

	//Check if user requesting help
	if len(args) == 0 || args[0] == "-h" || args[0] == "help" || args[0] == "--help" {
		printHowToUse()
		os.Exit(0)
	}

	if args[0] == "-c" && len(args) == 2 {
		//Check if user is configuring adapter
		if args[1] == "--adapter" {
			selectAdapter()
			initializeAdapter()
			os.Exit(0)
		}

		//Check if user is configuring adapter
		if args[1] == "--cluster-mapping" {
			remoteCluster = ""
			fmt.Print("Please specify remote cluster [" + localCluster + "]: ")
			fmt.Scanln(&remoteCluster)
			if remoteCluster == "" {
				remoteCluster = localCluster
			}
			support.StoreSecrets("helm-optimize-plugin", map[string]string{"remoteCluster": remoteCluster})
			os.Exit(0)
		}

		//check if user is clearing config
		if args[1] == "--clear-config" {
			support.LocateConfigNamespace("helm-optimize-plugin")
			support.DeleteSecret("helm-optimize-plugin")
			os.Exit(0)
		}

	}

	if args[0] == "-a" && len(args) > 1 {

		if err := initializeAdapter(); err != nil {
			os.Exit(0)
		}

		stdOut, stdErr, err := support.ExecuteSingleCommand(append([]string{HelmBin, "template"}, args[1:]...))
		support.CheckError(stdErr, err, true)

		support.PrintCharAcrossScreen("-")
		fmt.Println("LOCAL CLUSTER: " + localCluster)
		fmt.Println("REMOTE CLUSTER: " + remoteCluster)
		fmt.Println("ADAPTER: " + adapter)

		for _, manifest := range strings.Split(stdOut, "---") {

			objType, objName, objNamespace, containers, _, err := validateManifest([]byte(manifest))
			if err != nil {
				continue
			}

			fmt.Println("\nnamespace[" + objNamespace + "] objType[" + objType + "] objName[" + objName + "]")
			for i, container := range containers {

				containerName := container.(map[string]interface{})["name"].(string)
				approvalSetting, err := getApprovalSetting(remoteCluster, objNamespace, objType, objName, containerName)
				if err != nil {
					fmt.Println(strconv.Itoa(i+1) + "." + containerName + " not found in repository.")
					continue
				}
				fmt.Print(strconv.Itoa(i+1) + "." + containerName + " [" + approvalSetting + "] ")
				var approval string
				if approvalSetting == "Not Approved" {
					fmt.Print("Approve this insight (y/n) [y]: ")
					fmt.Scanln(&approval)
					if approval == "y" || approval == "" {
						if err := updateApprovalSetting(true, remoteCluster, objNamespace, objType, objName, containerName); err != nil {
							fmt.Print("  " + err.Error())
						}
					}
				} else {
					fmt.Print("Unapprove this insight (y/n) [y]: ")
					fmt.Scanln(&approval)
					if approval == "y" || approval == "" {
						if err := updateApprovalSetting(false, remoteCluster, objNamespace, objType, objName, containerName); err != nil {
							fmt.Print("  " + err.Error())
						}
					}
				}

			}
		}

		support.PrintCharAcrossScreen("-")
		os.Exit(0)

	}

	//Check for errors
	if args[0] == "-c" || args[0] == "-a" {
		fmt.Println("incorrect optimize-plugin command - refer to help menu")
		os.Exit(0)
	}

}

func printHowToUse() error {

	content, err := ioutil.ReadFile(os.Getenv("HELM_PLUGIN_DIR") + "/plugin.yaml")
	support.CheckError("", err, true)

	var pluginYAML map[string]interface{}
	yaml.Unmarshal(content, &pluginYAML)
	support.PrintCharAcrossScreen("-")
	fmt.Println("NAME: Optimize Plugin")
	fmt.Println("VERSION: " + pluginYAML["version"].(string))
	support.PrintCharAcrossScreen("-")
	fmt.Println(pluginYAML["description"].(string))
	support.PrintCharAcrossScreen("-")

	return nil

}

func checkGeneralDependancies() {

	for {
		if _, _, err := support.ExecuteSingleCommand([]string{KubectlBin}); err != nil {
			fmt.Print("[" + KubectlBin + "] is not available -- enter new path: ")
			fmt.Scanln(&KubectlBin)
			fmt.Println("")
		} else {
			if stdOut, stdErr, err := support.ExecuteSingleCommand([]string{KubectlBin, "cluster-info"}); err != nil {
				fmt.Println(stdOut)
				fmt.Println(stdErr)
				os.Exit(0)
			}
			support.KubectlBin = KubectlBin
			break
		}
	}

}

func scanFlagsForChartDetails(args []string) (string, int, error) {

	stdOut, stdErr, err := support.ExecuteSingleCommand([]string{HelmBin, args[0], "-h"})
	if err != nil {
		return "", 0, errors.New(stdErr)
	}

	re := regexp.MustCompile("Flags:(.*\n)+")
	match := re.FindStringSubmatch(stdOut)

	re2 := regexp.MustCompile(`\r?\n`)
	match2 := re2.ReplaceAllString(match[0], " ")

	re3 := regexp.MustCompile("(--[a-zA-Z]*([-]{0,1}[a-zA-Z]*)*   )+")
	match3 := re3.FindAllString(match2, -1)

	var flags []string
	for _, val := range match3 {
		flag := strings.Trim(val, " ")
		flags = append(flags, flag)
		re4 := regexp.MustCompile("-[a-z], " + flag)
		match4 := re4.FindAllString(match2, -1)
		if len(match4) > 0 {
			flags = append(flags, strings.Split(match4[0], ",")[0])
		}
	}

	argstemp := args[1:]

	type input struct {
		value    string
		position int
	}
	var results []input = nil

	for i, arg := range argstemp {

		if !strings.HasPrefix(arg, "-") {

			if i == 0 {
				results = append(results, input{arg, i + 1})
				continue
			} else if i == 1 {
				if _, ok := support.InSlice(flags, argstemp[i-1]); ok || !strings.HasPrefix(argstemp[i-1], "-") {
					results = append(results, input{arg, i + 1})
					continue
				}
			} else {
				if strings.HasPrefix(argstemp[i-1], "-") {
					if _, ok := support.InSlice(flags, argstemp[i-1]); ok {
						results = append(results, input{arg, i + 1})
						continue
					}
				} else {
					results = append(results, input{arg, i + 1})
					continue
				}
			}
		}
	}

	if results != nil {
		return results[len(results)-1].value, results[len(results)-1].position, nil
	}

	return "", 0, errors.New("could not locate chart path -- try helm optimize (install/upgrade) [NAME] [CHART] [flags]")

}

func main() {

	startTime := time.Now()

	//set environment variables
	args := os.Args[1:]

	if !(len(args) == 1 && args[0] == "-h") {
		checkGeneralDependancies()
		interpolateContext()
	}
	processPluginSwitches(args)

	//initialize the adapter
	if adapter == "" {
		if err := initializeAdapter(); err != nil {
			os.Exit(0)
		}
	}

	//if helm command is not install, upgrade, then just pass along to helm.
	if args[0] != "install" && args[0] != "upgrade" && args[0] != "template" {

		stdOut, stdErr, err := support.ExecuteSingleCommand(append([]string{HelmBin}, args...))
		support.CheckError(stdErr, err, true)
		fmt.Println(stdOut)
		os.Exit(0)

	} else {

		//validate whether the command is legal
		_, stdErr, err := support.ExecuteSingleCommand(append(append([]string{HelmBin}, args...), "--dry-run"))
		support.CheckError(stdErr, err, true)

		chart, argPos, err := scanFlagsForChartDetails(os.Args[1:])
		support.CheckError("", err, true)

		support.PrintCharAcrossScreen("-")
		fmt.Println("LOCAL CLUSTER: " + localCluster)
		fmt.Println("REMOTE CLUSTER: " + remoteCluster)
		fmt.Println("ADAPTER: " + adapter + "\n")

		absChartPath, _ := filepath.Abs(chart)
		chartDirName := filepath.Base(absChartPath)

		//create temporary chart directory
		tempChartDir, err := ioutil.TempDir("", "")
		support.CheckError("", err, true)

		//check to see if valid chart directory passed in.
		//if not pull from repo
		if support.FileExists(chart + "/Chart.yaml") {
			_, stdErr, err := support.ExecuteSingleCommand([]string{"cp", "-a", absChartPath, tempChartDir})
			support.CheckError(stdErr, err, true)
		} else {
			_, stdErr, err := support.ExecuteSingleCommand([]string{HelmBin, "pull", chart, "--untar", "--untardir", tempChartDir})
			support.CheckError(stdErr, err, true)
		}

		chartYaml, err := ioutil.ReadFile(tempChartDir + "/" + chartDirName + "/Chart.yaml")
		support.CheckError("", err, true)

		var chartMap map[string]interface{}
		err = yaml.Unmarshal([]byte(chartYaml), &chartMap)
		support.CheckError("", err, true)

		//render chart and output to temporary directory
		_, stdErr, err = support.ExecuteSingleCommand(append(append([]string{HelmBin, "template"}, args[1:]...), "--output-dir", tempChartDir))
		support.CheckError(stdErr, err, true)

		//check if rendered charts are in diff directory.  if they are copy them to temp directory.
		if chartDirName != chartMap["name"].(string) {
			_, stdErr, err := support.ExecuteSingleCommand([]string{"cp", "-a", tempChartDir + "/" + chartMap["name"].(string) + "/.", tempChartDir + "/" + chartDirName})
			support.CheckError(stdErr, err, true)
		}

		processChart(tempChartDir+"/"+chartDirName, args)

		fmt.Printf("EXECUTION TIME: %.2fs\n", time.Now().Sub(startTime).Seconds())
		support.PrintCharAcrossScreen("-")

		args[argPos] = tempChartDir + "/" + chartDirName
		stdOut, stdErr, err := support.ExecuteSingleCommand(append([]string{HelmBin}, args...))
		support.CheckError(stdErr, err, false)
		if err == nil {
			fmt.Println(stdOut)
		}

		//delete temporary chart directory
		_, stdErr, err = support.ExecuteSingleCommand([]string{"rm", "-rf", tempChartDir})
		support.CheckError(stdErr, err, true)

	}
}

func processChart(chartPath string, args []string) error {

	objs, err := ioutil.ReadDir(chartPath)
	if err == nil {
		for _, obj := range objs {
			if obj.IsDir() {
				err := processChart(chartPath+"/"+obj.Name(), args)
				if err != nil {
					fmt.Println(err)
				}
			}
		}
	}

	//check if path provided contains valid structure to analyze templates
	chartFileContents, err := ioutil.ReadFile(chartPath + "/Chart.yaml")
	if err != nil {
		return nil
	}

	//unmarshal Chart.yaml to check whether it's a valid yaml file
	var chartStruct map[string]interface{}
	if err := yaml.Unmarshal(chartFileContents, &chartStruct); err != nil {
		return errors.New("'Chart.yaml' not a valid yaml file")
	}

	//if Chart.yaml has a name field, then print chart name
	if _, ok := chartStruct["name"]; ok {
		printData := "CHART: " + chartStruct["name"].(string)
		fmt.Println(printData + "\n" + strings.Repeat("=", len(printData)))
	} else {
		return errors.New("'Chart.yaml' does not contain name field")
	}

	//if templates directory exists, then process all files in that directory
	if support.DirExists(chartPath + "/templates") {
		return processTemplates(chartPath+"/templates", args)
	}

	return errors.New("templates directory doesn't exist for this chart - skipping\n\n")

}

func processTemplates(templatePath string, args []string) error {

	templates, err := ioutil.ReadDir(templatePath)
	if err != nil {
		return nil
	}

	for _, template := range templates {

		if template.IsDir() {

			processTemplates(templatePath+"/"+template.Name(), args)

		} else {

			manifest, err := ioutil.ReadFile(templatePath + "/" + template.Name())
			if err != nil {
				continue
			}

			objType, objName, objNamespace, containers, manifestMap, err := validateManifest(manifest)
			if err != nil {
				continue
			}

			fmt.Println("namespace[" + objNamespace + "] objType[" + objType + "] objName[" + objName + "]")
			var i int = 1
			for _, container := range containers {

				var containerName string
				if containerName = support.CheckMap(container.(map[string]interface{}), "name"); containerName == "" {
					continue
				}

				fmt.Print(strconv.Itoa(i) + "." + containerName + ": ")

				//try to get recommendation from repo
				insight, approvalSetting, err := getInsight(remoteCluster, objNamespace, objType, objName, containerName)
				if err != nil {
					fmt.Println(err)
				} else {
					fmt.Print("[" + approvalSetting + "] ")
					fmt.Println(insight)
					container.(map[string]interface{})["resources"] = insight
					i++
					continue
				}

				//try to get recommendation from k8s
				fmt.Print("  Checking Cluster: ")
				insight, err = extractResourceSpecFromK8S(remoteCluster, objNamespace, objType, objName, containerName)
				if err != nil {
					fmt.Println(err)
				} else {
					fmt.Println(insight)
					container.(map[string]interface{})["resources"] = insight
					i++
					continue
				}

				//try to get defaults from user
				fmt.Print("  Checking Defaults: ")
				var defaultConfig map[string]interface{} = nil
				if val, ok := container.(map[string]interface{})["resources"].(map[string]interface{}); ok && len(val) > 0 {
					defaultConfig = container.(map[string]interface{})["resources"].(map[string]interface{})
					fmt.Println(defaultConfig)
				} else {
					fmt.Println("*WARNING* No default config present!")
				}

				i++

			}

			manifestYAMLStr, err := yaml.Marshal(manifestMap)
			support.CheckError("", err, true)
			err = ioutil.WriteFile(templatePath+"/"+template.Name(), manifestYAMLStr, 0644)
			fmt.Println("")

		}

	}

	return nil

}

func validateManifest(manifest []byte) (string, string, string, []interface{}, map[string]interface{}, error) {

	var manifestMap map[string]interface{}
	if err := yaml.Unmarshal(manifest, &manifestMap); err != nil {
		return "", "", "", nil, nil, errors.New("unable to unmarshal manifest")
	}

	var objType, objName, objNamespace string

	if objType = support.CheckMap(manifestMap, "kind"); objType == "" {
		return "", "", "", nil, nil, errors.New("manifest does not contain valid k8s objType")
	}

	if objName = support.CheckMap(manifestMap, "metadata", "name"); objName == "" {
		return "", "", "", nil, nil, errors.New("manifest does not contain valid k8s objName")
	}

	if objNamespace = support.CheckMap(manifestMap, "metadata", "namespace"); objNamespace == "" {
		objNamespace = namespace
	}

	if _, ok := objTypeContainerPath[objType]; !ok {
		return "", "", "", nil, nil, errors.New("manifest contains objType that's not supported")
	}

	if val := support.CheckMap(manifestMap, "metadata", "annotations", "helm.sh/hook"); strings.HasPrefix(val, "test") {
		return "", "", "", nil, nil, errors.New("manifest is for helm test pod")
	}

	var containers []interface{}
	switch objType {
	case "Pod":
		containers = manifestMap["spec"].(map[string]interface{})["containers"].([]interface{})
	case "CronJob":
		containers = manifestMap["spec"].(map[string]interface{})["jobTemplate"].(map[string]interface{})["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]interface{})
	default:
		containers = manifestMap["spec"].(map[string]interface{})["template"].(map[string]interface{})["spec"].(map[string]interface{})["containers"].([]interface{})
	}

	return objType, objName, objNamespace, containers, manifestMap, nil

}

func extractResourceSpecFromK8S(cluster string, objNamespace string, objType string, objName string, containerName string) (map[string]map[string]string, error) {

	jsonPath := objTypeContainerPath[objType]

	stdOut, stdErr, err := support.ExecuteSingleCommand([]string{KubectlBin, "get", objType, objName, "-o=jsonpath=" + jsonPath, "--cluster=" + cluster, "--namespace=" + objNamespace})
	if err != nil {
		return nil, errors.New(stdErr)
	}

	var containerDefs []map[string]interface{}
	json.Unmarshal([]byte(stdOut), &containerDefs)

	for _, containerDef := range containerDefs {
		if containerName == containerDef["name"].(string) {
			if _, ok := containerDef["resources"]; !ok {
				break
			}

			jsonStr, err := json.Marshal(containerDef["resources"])
			if err != nil || string(jsonStr) == "{}" {
				break
			}

			var parsedInsight map[string]map[string]string
			json.Unmarshal([]byte(jsonStr), &parsedInsight)

			return parsedInsight, nil
		}
	}

	return nil, errors.New("could not locate resource spec")

}

func interpolateContext() {

	//extract working context-info (cluster and namespace)
	kubeconfig := os.Getenv("KUBECONFIG")
	kubecontext := os.Getenv("HELM_KUBECONTEXT")
	namespace = os.Getenv("HELM_NAMESPACE")

	var stdErr string
	var err error
	if kubeconfig != "" {
		kubeconfig, stdErr, err = support.ExecuteSingleCommand([]string{"cat", kubeconfig})
	} else {
		kubeconfig, stdErr, err = support.ExecuteSingleCommand([]string{KubectlBin, "config", "view"})
	}
	support.CheckError(stdErr, err, true)

	var kubeconfigYAML map[string]interface{}
	err = yaml.Unmarshal([]byte(kubeconfig), &kubeconfigYAML)
	support.CheckError("", err, true)

	//determine current-context
	if kubecontext == "" {
		kubecontext = kubeconfigYAML["current-context"].(string)
	}

	//determine local cluster
	contextList := kubeconfigYAML["contexts"].([]interface{})
	for _, context := range contextList {
		if context.(map[string]interface{})["name"] == kubecontext {
			localCluster = context.(map[string]interface{})["context"].(map[string]interface{})["cluster"].(string)
		}
	}

	support.LocateConfigNamespace("helm-optimize-plugin")

	if val, ok := support.RetrieveSecrets("helm-optimize-plugin")["remoteCluster"]; ok {
		remoteCluster = val
	} else {

		support.LoadConfigMap()
		if support.Config != nil {
			if clusterName, ok := support.Config.Get("cluster_name"); ok {
				remoteCluster = clusterName
			} else if clusterName, ok := support.Config.Get("prometheus_address"); ok {
				remoteCluster = clusterName
			} else {
				fmt.Println("could not resolve remote cluster -- please configure manually using 'helm optimize -c --cluster-mapping'")
				os.Exit(0)
			}
		} else {
			fmt.Println("could not resolve remote cluster -- please configure manually using 'helm optimize -c --cluster-mapping'")
			os.Exit(0)
		}

		support.StoreSecrets("helm-optimize-plugin", map[string]string{"remoteCluster": remoteCluster})

	}

}
