package densify

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"

	support "../support"
	"golang.org/x/crypto/ssh/terminal"
)

var insightCache = make(map[string][]Insight)

//Insight this struct holds a recommendation
type Insight struct {
	Container       string  `json:"container"`
	RecommFirstSeen int64   `json:"recommFirstSeen"`
	Cluster         string  `json:"cluster"`
	HostName        string  `json:"hostName,omitempty"`
	PredictedUptime float64 `json:"predictedUptime,omitempty"`
	ControllerType  string  `json:"controllerType"`
	DisplayName     string  `json:"displayName"`
	RecommLastSeen  int64   `json:"recommLastSeen"`
	EntityID        string  `json:"entityId"`
	PodService      string  `json:"podService"`
	AuditInfo       struct {
		DataCollection struct {
			DateFirstAudited int64 `json:"dateFirstAudited"`
			AuditCount       int   `json:"auditCount"`
			DateLastAudited  int64 `json:"dateLastAudited"`
		} `json:"dataCollection"`
		WorkloadDataLast30 struct {
			TotalDays int   `json:"totalDays"`
			SeenDays  int   `json:"seenDays"`
			FirstDate int64 `json:"firstDate"`
			LastDate  int64 `json:"lastDate"`
		} `json:"workloadDataLast30"`
	} `json:"auditInfo,omitempty"`
	RecommendedCPULimit   int    `json:"recommendedCpuLimit,omitempty"`
	RecommendedMemRequest int    `json:"recommendedMemRequest,omitempty"`
	CurrentCount          int    `json:"currentCount"`
	RecommSeenCount       int    `json:"recommSeenCount"`
	Namespace             string `json:"namespace"`
	RecommendedMemLimit   int    `json:"recommendedMemLimit,omitempty"`
	RecommendationType    string `json:"recommendationType"`
	RecommendedCPURequest int    `json:"recommendedCpuRequest,omitempty"`
	CurrentMemLimit       int    `json:"currentMemLimit,omitempty"`
	CurrentMemRequest     int    `json:"currentMemRequest,omitempty"`
	CurrentCPULimit       int    `json:"currentCpuLimit,omitempty"`
	CurrentCPURequest     int    `json:"currentCpuRequest,omitempty"`
}

var (
	densifyURL  string
	densifyUser string
	densifyPass string
	analysisEP  = "/CIRBA/api/v2/analysis/containers/kubernetes"
	authorizeEP = "/CIRBA/api/v2/authorize"
)

//Initialize will initilize the densify secrets k8s object, if it doesn't exist in the current-context.
func Initialize(create bool) error {

	//Extract Secrets
	//Check if optimize-plugin-secrets already exists
	//_, _, err := support.ExecuteCommand("kubectl", []string{"get", "secret", "optimize-plugin-secrets"})
	if create {
		getSecretsFromUser()
		_, stdErr, err := support.ExecuteCommand("kubectl", []string{"create", "secret", "generic", "optimize-plugin-secrets", "--from-literal=adapter=densify", "--from-literal=densifyURL=" + densifyURL, "--from-literal=densifyUser=" + densifyUser, "--from-literal=densifyPass=" + densifyPass})
		support.CheckErr(stdErr, err)
	} else {
		extractSecretsFromK8s()
	}

	//Validate the credentials.
	resp, err := validateSecrets()
	if err != nil {

		if resp != "" {
			fmt.Println(resp)
		} else {
			fmt.Println(err)
		}
		_, _, _ = support.ExecuteSingleCommand([]string{"kubectl", "delete", "secret", "optimize-plugin-secrets", "--ignore-not-found"})

		var tryAgain string
		fmt.Print("Would you like to try again [y]: ")
		fmt.Scanln(&tryAgain)
		if tryAgain == "y" || tryAgain == "" {
			Initialize(true)
		}

		return err
	}

	return nil

}

//GetInsight gets an insight from densify based on the keys cluster, namespace, objType, objName and containerName
func GetInsight(cluster string, namespace string, objType string, objName string, containerName string) (string, error) {

	if _, ok := insightCache[cluster]; !ok {
		resp, err := getRequest(analysisEP)
		if err != nil {
			return "", err
		}
		var analyses []interface{}
		found := false
		json.Unmarshal([]byte(resp), &analyses)
		for _, analysis := range analyses {
			if analysis.(map[string]interface{})["analysisName"].(string) == cluster {
				resp, err = getRequest(analysisEP + "/" + analysis.(map[string]interface{})["analysisId"].(string) + "/results")
				if err != nil {
					return "", err
				}

				var insights []Insight
				json.Unmarshal([]byte(resp), &insights)
				insightCache[cluster] = insights
				found = true
				break
			}
		}
		if found == false {
			return "", errors.New("unable to locate insight")
		}
	}

	for _, insight := range insightCache[cluster] {

		if insight.Cluster == cluster && insight.Namespace == namespace && insight.ControllerType == objType &&
			insight.PodService == objName && insight.Container == containerName && insight.RecommendedCPULimit > 0 &&
			insight.RecommendedCPURequest > 0 && insight.RecommendedMemLimit > 0 && insight.RecommendedMemRequest > 0 {
			return "{\"limits\":{\"cpu\":\"" + strconv.Itoa(insight.RecommendedCPULimit) + "m\",\"memory\":\"" + strconv.Itoa(insight.RecommendedMemLimit) + "Mi\"},\"requests\":{\"cpu\":\"" + strconv.Itoa(insight.RecommendedCPURequest) + "m\",\"memory\":\"" + strconv.Itoa(insight.RecommendedMemRequest) + "Mi\"}}", nil
		}

	}

	return "", errors.New("unable to locate insight")

}

func getSecretsFromUser() {

	fmt.Print("Densify URL: ")
	fmt.Scanln(&densifyURL)
	strings.TrimSuffix(densifyURL, "/")

	fmt.Print("Densify Username: ")
	fmt.Scanln(&densifyUser)

	fmt.Print("Densify Password: ")
	pass, _ := terminal.ReadPassword(0)
	densifyPass = string(pass)
	fmt.Println("")

}

func extractSecretsFromK8s() {

	densifyURLEncoded, _, _ := support.ExecuteCommand("kubectl", []string{"get", "secret", "optimize-plugin-secrets", "-o", "jsonpath='{.data.densifyURL}'"})
	densifyURLEncoded = densifyURLEncoded[1 : len(densifyURLEncoded)-1]
	densifyURLDecoded, _ := base64.StdEncoding.DecodeString(densifyURLEncoded)
	densifyURL = string(densifyURLDecoded)

	densifyUserEncoded, _, _ := support.ExecuteCommand("kubectl", []string{"get", "secret", "optimize-plugin-secrets", "-o", "jsonpath='{.data.densifyUser}'"})
	densifyUserEncoded = densifyUserEncoded[1 : len(densifyUserEncoded)-1]
	densifyUserDecoded, _ := base64.StdEncoding.DecodeString(densifyUserEncoded)
	densifyUser = string(densifyUserDecoded)

	densifyPassEncoded, _, _ := support.ExecuteCommand("kubectl", []string{"get", "secret", "optimize-plugin-secrets", "-o", "jsonpath='{.data.densifyPass}'"})
	densifyPassEncoded = densifyPassEncoded[1 : len(densifyPassEncoded)-1]
	densifyPassDecoded, _ := base64.StdEncoding.DecodeString(densifyPassEncoded)
	densifyPass = string(densifyPassDecoded)

}

func validateSecrets() (string, error) {

	jsonReq, err := json.Marshal(map[string]string{
		"userName": densifyUser,
		"pwd":      densifyPass,
	})
	if err != nil {
		return "", err
	}

	resp, err := postRequest(authorizeEP, jsonReq)
	if err != nil {
		return resp, err
	}

	return resp, nil

}

func getRequest(endpoint string) (string, error) {

	auth := base64.StdEncoding.EncodeToString([]byte(densifyUser + ":" + densifyPass))
	req, err := http.NewRequest(http.MethodGet, densifyURL+endpoint, nil)
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
	} else {
		return string(bodyBytes), errors.New("")
	}
}

func postRequest(endpoint string, body []byte) (string, error) {

	auth := base64.StdEncoding.EncodeToString([]byte(densifyUser + ":" + densifyPass))
	req, _ := http.NewRequest(http.MethodPost, densifyURL+endpoint, bytes.NewBuffer(body))
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
	} else {
		return string(bodyBytes), errors.New("")
	}

}
