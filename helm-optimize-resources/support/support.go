package support

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/magiconair/properties"
	"golang.org/x/crypto/ssh/terminal"
)

//Config holds the configMap from the data forwarder
var Config *properties.Properties = nil

//KubectlBin holds location of kubectl
var KubectlBin = "kubectl"

var secretNamespace string

//LoadConfigMap loads the config map from the densify forwarder
func LoadConfigMap() {

	var stdOut, stdErr string
	var err error
	if stdOut, stdErr, err = ExecuteSingleCommand([]string{KubectlBin, "get", "configmaps", "-A", "-o", "json"}); err != nil {
		fmt.Println(stdErr)
		return
	}

	var configMaps map[string]interface{}
	if err := json.Unmarshal([]byte(stdOut), &configMaps); err != nil {
		fmt.Println(err)
		return
	}

	for _, configMap := range configMaps["items"].([]interface{}) {
		if val, ok := configMap.(map[string]interface{})["data"].(map[string]interface{})["config.properties"]; ok && strings.Contains(val.(string), "Densify Inc. D/B/A Densify #  All Rights Reserved.") {
			Config = properties.MustLoadString(val.(string))
			return
		}
	}

}

//CheckError will validate whether error is not nil
func CheckError(message string, err error, exit bool) bool {
	if err != nil {
		if message != "" {
			fmt.Println(message)
		}
		fmt.Println(err)
		if exit {
			os.Exit(1)
		}
		return true
	}
	return false
}

//LocateConfigNamespace will identify which namespace the configuration secret is stored.
func LocateConfigNamespace(secretName string) {

	cmd := exec.Command(KubectlBin, "get", "secrets", "-o", "json", "--all-namespaces")
	out, err := cmd.CombinedOutput()
	if err != nil {
		secretNamespace = ""
	}

	var secretsMapEncoded map[string]interface{}
	json.Unmarshal(out, &secretsMapEncoded)

	for _, val := range secretsMapEncoded["items"].([]interface{}) {
		name := val.(map[string]interface{})["metadata"].(map[string]interface{})["name"]
		if name == secretName {
			secretNamespace = val.(map[string]interface{})["metadata"].(map[string]interface{})["namespace"].(string)
			return
		}

	}

	secretNamespace = os.Getenv("HELM_NAMESPACE")

}

//DeleteSecret deletes the specified k8s secret
func DeleteSecret(secretName string) {
	_, _, _ = ExecuteSingleCommand([]string{KubectlBin, "delete", "secret", secretName, "--namespace", secretNamespace, "--ignore-not-found"})
}

//RemoveSecretData deletes the specified k8s secret
func RemoveSecretData(secretName string, secretKey string) error {

	secrets := RetrieveSecrets(secretName)
	if _, ok := secrets[secretKey]; ok {
		delete(secrets, secretKey)
	}

	DeleteSecret(secretName)
	if StoreSecrets(secretName, secrets) {
		return nil
	} else {
		return errors.New("failed to remove secret data")
	}

}

//StoreSecrets stores the specified k8s secret
func StoreSecrets(secretName string, secrets map[string]string) bool {

	existingSecrets := RetrieveSecrets(secretName)
	if existingSecrets == nil {
		existingSecrets = make(map[string]string)
	}

	for key, val := range secrets {
		existingSecrets[key] = val
	}

	createCmd := []string{KubectlBin, "create", "secret", "generic", secretName, "--namespace", secretNamespace}
	for key, val := range existingSecrets {
		createCmd = append(createCmd, "--from-literal="+key+"="+val)
	}

	DeleteSecret(secretName)
	_, _, err := ExecuteSingleCommand(createCmd)
	if err != nil {
		return false
	}

	return true

}

//RetrieveSecrets will retreive the specified secret
func RetrieveSecrets(secretName string) map[string]string {

	stdOut, _, err := ExecuteSingleCommand([]string{KubectlBin, "get", "secret", secretName, "--namespace", secretNamespace, "-o", "jsonpath={.data}"})
	if err != nil {
		return nil
	}

	var secretsMapEncoded map[string]string
	json.Unmarshal([]byte(stdOut), &secretsMapEncoded)

	secretsMapDecoded := make(map[string]string)
	for key, encodedVal := range secretsMapEncoded {
		decodedVal, _ := base64.StdEncoding.DecodeString(encodedVal)
		secretsMapDecoded[key] = string(decodedVal)
	}

	return secretsMapDecoded

}

//ExecuteSingleCommand this function executes a given command.
func ExecuteSingleCommand(command []string) (string, string, error) {

	var cmd *exec.Cmd
	if len(command) == 1 {
		cmd = exec.Command(command[0])
	} else if len(command) > 1 {
		cmd = exec.Command(command[0], command[1:]...)
	} else {
		return "", "", errors.New("no command submitted")
	}

	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()

	err := cmd.Start()

	scannerErr := bufio.NewScanner(stderr)
	errStr := ""
	for scannerErr.Scan() {
		errStr += scannerErr.Text() + "\n"
	}

	scannerOut := bufio.NewScanner(stdout)
	outStr := ""
	for scannerOut.Scan() {
		outStr += scannerOut.Text() + "\n"
	}

	if len(outStr) > 0 {
		outStr = outStr[:len(outStr)-1]
	}
	if len(errStr) > 0 {
		errStr = errStr[:len(errStr)-1]
	}

	err = cmd.Wait()
	return outStr, errStr, err

}

//FileExists will check if a file (not directory) exists in the specified path.
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

//DirExists checks to see if directory exists
func DirExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return info.IsDir()
}

//InSlice will check if an element exists in the slice
func InSlice(slice []string, val string) (int, bool) {
	for i, item := range slice {
		if item == val {
			return i, true
		}
	}
	return -1, false
}

func InMap(inputMap map[string]interface{}, keys []string) bool {

	for _, key := range keys {

		if _, ok := inputMap[key]; !ok {
			return false
		}

	}

	return true

}

func CheckMap(inputMap map[string]interface{}, list ...string) string {

	if len(list) > 0 {

		if _, ok := inputMap[list[0]]; ok {

			if len(list) > 1 {
				return CheckMap(inputMap[list[0]].(map[string]interface{}), list[1:]...)
			} else {
				return inputMap[list[0]].(string)
			}

		} else {
			return ""
		}

	}

	return ""

}

func PrintCharAcrossScreen(char string) {
	if width, _, err := terminal.GetSize(0); err != nil {
		fmt.Println(strings.Repeat(char, 100))
	} else {
		fmt.Println(strings.Repeat(char, width))
	}
}

//WriteToTempFile writes the contents of the string to a temp file
func WriteToTempFile(content string) (string, error) {

	tmpFile, err := ioutil.TempFile(os.TempDir(), "densify-")

	text := []byte(content)
	if _, err = tmpFile.Write(text); err != nil {
		return tmpFile.Name(), err
	}

	// Close the file
	if err := tmpFile.Close(); err != nil {
		return tmpFile.Name(), err
	}

	return tmpFile.Name(), nil

}

//DeleteFile deletes a file specified in the path
func DeleteFile(filepath string) error {

	err := os.Remove(filepath)
	if err != nil {
		return err
	}

	return nil

}

//HTTPRequest send a REST api request to an end point
func HTTPRequest(method string, endpoint string, authStr string, body []byte) (string, error) {

	auth := base64.StdEncoding.EncodeToString([]byte(authStr))
	req, _ := http.NewRequest(method, endpoint, bytes.NewBuffer(body))
	req.Header.Add("Authorization", "Basic "+auth)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode == 200 {
		return string(bodyBytes), nil
	}

	return "", errors.New(string(bodyBytes))

}
