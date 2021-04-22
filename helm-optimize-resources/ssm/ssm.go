package ssm

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/densify-quick-start/helm-optimize-resources/support"
)

var (
	prefix  string
	profile string
	region  string
)

var supportedRegions = []string{"us-east-2", "us-east-1", "us-west-1", "us-west-2", "af-south-1", "ap-east-1", "ap-south-1", "ap-northeast-3", "ap-northeast-2", "ap-southeast-1", "ap-southeast-2", "ap-northeast-1", "ca-central-1", "cn-north-1", "cn-northwest-1", "eu-central-1", "eu-west-1", "eu-west-2", "eu-south-1", "eu-west-3", "eu-north-1", "me-south-1", "sa-east-1", "us-gov-east-1", "us-gov-west-1"}

////////////////////////////////////////////////////////
////////////////EXTERNAL FUNCTIONS//////////////////////
////////////////////////////////////////////////////////

//Initialize will ready the adapter to serve insight extraction from AWS parameter store.
func Initialize() error {

	//Check dependancies
	if _, _, err := support.ExecuteSingleCommand([]string{"aws", "--version"}); err != nil {
		return errors.New("aws-cli is not available - please install before trying again")
	}

	//check stored secret
	storedSecrets := support.RetrieveSecrets("helm-optimize-plugin")
	if storedSecrets != nil && storedSecrets["adapter"] == "Parameter Store" {
		if _, ok := storedSecrets["region"]; ok {
			region = storedSecrets["region"]
			prefix = storedSecrets["prefix"]
			profile = storedSecrets["profile"]
			return nil
		}
	}

	//extract ssm secrets from user
	for {
		fmt.Print("What is your preferred parameter key prefix [no prefix]: ")
		fmt.Scanln(&prefix)
		if prefix != "" {
			if res1, _ := regexp.MatchString("^/{0,1}(aws|ssm)", prefix); res1 {
				fmt.Println("Parameter name: can't be prefixed with \"aws\" or \"ssm\" (case-insensitive).")
				continue
			}

			if res1, _ := regexp.MatchString("^(/{1}[a-zA-Z0-9_.-]+)*$", prefix); !res1 {
				fmt.Println("Only a mix of letters, numbers and the following 3 symbols .-_ are allowed.  e.g /prefix/path")
				continue
			}
		}
		break
	}

	for {
		fmt.Print("What is your preferred AWS profile [default]: ")
		fmt.Scanln(&profile)
		if profile == "" {
			profile = "default"
		}
		_, stdErr, err := support.ExecuteSingleCommand([]string{"aws", "sts", "get-caller-identity", "--profile", profile})
		if found := support.CheckError(stdErr, err, false); !found {
			break
		}
	}

	for {
		fmt.Print("What is your preferred AWS region [us-east-1]: ")
		fmt.Scanln(&region)
		if region == "" {
			region = "us-east-1"
		}
		if _, ok := support.InSlice(supportedRegions, region); !ok {
			fmt.Println("Invalid entry.  Check for valid regions here https://aws.amazon.com/about-aws/global-infrastructure/regions_az/.")
			continue
		}
		break
	}

	storeSecrets()

	return nil

}

//GetInsight gets an insight from parameter store based on the keys cluster, namespace, objType, objName and containerName
func GetInsight(cluster string, namespace string, objType string, objName string, containerName string) (map[string]map[string]string, string, error) {

	ssmKey := prefix + "/" + cluster + "/" + namespace + "/" + objType + "/" + objName + "/" + containerName + "/resourceSpec"

	insight, insightVersion, err := getParameterValue(ssmKey)
	if err != nil {
		return nil, "", errors.New("could not locate resource spec")
	}

	//Validate and acquire resource spec
	var parsedInsight map[string]map[string]string
	json.Unmarshal([]byte(insight), &parsedInsight)

	if cpuLimit, err := strconv.Atoi(parsedInsight["limits"]["cpu"]); err != nil || cpuLimit < 1 {
		return nil, "", errors.New("invalid resource specs received from repository")
	}

	if memLimit, err := strconv.Atoi(parsedInsight["limits"]["memory"]); err != nil || memLimit < 1 {
		return nil, "", errors.New("invalid resource specs received from repository")
	}

	if cpuRequest, err := strconv.Atoi(parsedInsight["requests"]["cpu"]); err != nil || cpuRequest < 1 {
		return nil, "", errors.New("invalid resource specs received from repository")
	}

	if memRequest, err := strconv.Atoi(parsedInsight["requests"]["memory"]); err != nil || memRequest < 1 {
		return nil, "", errors.New("invalid resource specs received from repository")
	}

	parsedInsight["limits"]["cpu"] = parsedInsight["limits"]["cpu"] + "m"
	parsedInsight["limits"]["memory"] = parsedInsight["limits"]["memory"] + "Mi"
	parsedInsight["requests"]["cpu"] = parsedInsight["requests"]["cpu"] + "m"
	parsedInsight["requests"]["memory"] = parsedInsight["requests"]["memory"] + "Mi"

	//Acquire approval setting
	approvalSetting, err := getParameterLabel(ssmKey, insightVersion)
	if err != nil {
		return nil, "", errors.New("unable to read approval setting")
	}

	return parsedInsight, approvalSetting, nil

}

//UpdateApprovalSetting will update the approval setting accordingly
func UpdateApprovalSetting(approved bool, cluster string, namespace string, objType string, objName string, containerName string) error {

	ssmKey := prefix + "/" + cluster + "/" + namespace + "/" + objType + "/" + objName + "/" + containerName + "/resourceSpec"

	resp, _, err := support.ExecuteSingleCommand([]string{"aws", "ssm", "list-tags-for-resource", "--resource-type", "Parameter", "--resource-id", ssmKey, "--profile", profile, "--region", region, "--query", "TagList"})
	if err != nil {
		return errors.New("unable to update approval setting")
	}

	var tagMap []map[string]string
	json.Unmarshal([]byte(resp), &tagMap)

	currentSettings := make(map[string]map[string]string)
	recommendedSettings := make(map[string]map[string]string)
	currentSettings["limits"] = make(map[string]string)
	currentSettings["requests"] = make(map[string]string)
	recommendedSettings["limits"] = make(map[string]string)
	recommendedSettings["requests"] = make(map[string]string)
	for _, val := range tagMap {
		if val["Key"] == "currentCpuLimit" {
			currentSettings["limits"]["cpu"] = val["Value"]
		} else if val["Key"] == "currentMemLimit" {
			currentSettings["limits"]["memory"] = val["Value"]
		} else if val["Key"] == "currentCpuRequest" {
			currentSettings["requests"]["cpu"] = val["Value"]
		} else if val["Key"] == "currentMemRequest" {
			currentSettings["requests"]["memory"] = val["Value"]
		} else if val["Key"] == "recommendedCpuLimit" {
			recommendedSettings["limits"]["cpu"] = val["Value"]
		} else if val["Key"] == "recommendedMemLimit" {
			recommendedSettings["limits"]["memory"] = val["Value"]
		} else if val["Key"] == "recommendedCpuRequest" {
			recommendedSettings["requests"]["cpu"] = val["Value"]
		} else if val["Key"] == "recommendedMemRequest" {
			recommendedSettings["requests"]["memory"] = val["Value"]
		}
	}

	var err1, err2 error
	if approved == true {
		recommendedSettingsJSON, err := json.Marshal(recommendedSettings)
		if err != nil {
			return errors.New("unable to update approval setting")
		}
		_, _, err1 = support.ExecuteSingleCommand([]string{"aws", "ssm", "put-parameter", "--name", ssmKey, "--type", "String", "--value", string(recommendedSettingsJSON), "--overwrite", "--profile", profile, "--region", region})
		if err1 == nil {
			_, _, err2 = support.ExecuteSingleCommand([]string{"aws", "ssm", "label-parameter-version", "--name", ssmKey, "--labels", "Approved", "--profile", profile, "--region", region})
		}
	} else {
		currentSettingsJSON, err := json.Marshal(currentSettings)
		if err != nil {
			return errors.New("unable to update approval setting")
		}
		_, _, err1 = support.ExecuteSingleCommand([]string{"aws", "ssm", "put-parameter", "--name", ssmKey, "--type", "String", "--value", string(currentSettingsJSON), "--overwrite", "--profile", profile, "--region", region})
		if err1 == nil {
			_, _, err2 = support.ExecuteSingleCommand([]string{"aws", "ssm", "label-parameter-version", "--name", ssmKey, "--labels", "NotApproved", "--profile", profile, "--region", region})
		}
	}

	if err1 != nil || err2 != nil {
		return errors.New("unable to update approval setting")
	}

	return nil

}

//GetApprovalSetting will acquire the current approval setting
func GetApprovalSetting(cluster string, namespace string, objType string, objName string, containerName string) (string, error) {

	ssmKey := prefix + "/" + cluster + "/" + namespace + "/" + objType + "/" + objName + "/" + containerName + "/resourceSpec"

	_, insightVersion, err := getParameterValue(ssmKey)
	if err != nil {
		return "", errors.New("unable to read approval setting")
	}

	//Acquire approval setting
	approvalSetting, err := getParameterLabel(ssmKey, insightVersion)
	if err != nil {
		return "", errors.New("unable to read approval setting")
	}

	return approvalSetting, nil

}

////////////////////////////////////////////////////////
///////////////////LOCAL FUNCTIONS//////////////////////
////////////////////////////////////////////////////////

func getParameterValue(ssmKey string) (string, string, error) {

	insight, _, err := support.ExecuteSingleCommand([]string{"aws", "ssm", "get-parameter", "--with-decryption", "--name", ssmKey, "--profile", profile, "--region", region})
	if err != nil {
		return "", "", errors.New("could not locate resource spec")
	}

	var insightMap map[string]map[string]interface{}
	json.Unmarshal([]byte(insight), &insightMap)

	return insightMap["Parameter"]["Value"].(string), strconv.FormatFloat(insightMap["Parameter"]["Version"].(float64), 'E', -1, 64), nil

}

func getParameterLabel(ssmKey string, version string) (string, error) {

	paramHistory, _, err := support.ExecuteSingleCommand([]string{"aws", "ssm", "get-parameter-history", "--with-decryption", "--name", ssmKey, "--profile", profile, "--region", region, "--query", "Parameters"})
	if err != nil {
		return "", errors.New("unable to read approval setting")
	}

	var paramHistoryMap []map[string]interface{}
	json.Unmarshal([]byte(paramHistory), &paramHistoryMap)
	for _, val := range paramHistoryMap {
		if strconv.FormatFloat(val["Version"].(float64), 'E', -1, 64) == version && len(val["Labels"].([]interface{})) == 1 {
			if val["Labels"].([]interface{})[0].(string) == "NotApproved" {
				return "Not Approved", nil
			} else if val["Labels"].([]interface{})[0].(string) == "Approved" {
				return "Approved", nil
			}
		}
	}

	return "", errors.New("unable to read parameter label")

}

func storeSecrets() {

	secrets := make(map[string]string)
	secrets["adapter"] = "Parameter Store"
	secrets["profile"] = profile
	secrets["prefix"] = prefix
	secrets["region"] = region
	support.StoreSecrets("helm-optimize-plugin", secrets)

}
