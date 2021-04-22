package densify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/densify-quick-start/helm-optimize-resources/support"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	densifyURL  string
	densifyUser string
	densifyPass string
	analysisId  string
	analysisEP  = "/CIRBA/api/v2/analysis/containers/kubernetes"
	authorizeEP = "/CIRBA/api/v2/authorize"
	systemsEP   = "/CIRBA/api/v2/systems"
)

////////////////////////////////////////////////////////
////////////////EXTERNAL FUNCTIONS//////////////////////
////////////////////////////////////////////////////////

//Initialize will initilize the densify secrets k8s object, if it doesn't exist in the current-context.
func Initialize() error {

	//check stored secret
	storedSecrets := support.RetrieveSecrets("helm-optimize-plugin")
	if storedSecrets != nil && storedSecrets["adapter"] == "Densify" {
		if _, ok := storedSecrets["densifyURL"]; ok {
			densifyURL = storedSecrets["densifyURL"]
			densifyUser = storedSecrets["densifyUser"]
			densifyPass = storedSecrets["densifyPass"]

			if err := validateSecrets(); err == nil {
				return nil
			}
		}
	}

	//resolve creds from data forwarder
	support.LoadConfigMap()
	if support.Config != nil {

		var host, protocol, port string
		var ok bool

		if protocol, ok = support.Config.Get("protocol"); ok {
			densifyURL = protocol + "://"
			if host, ok = support.Config.Get("host"); ok {
				densifyURL += host + ":"
				if port, ok = support.Config.Get("port"); ok {
					densifyURL += port
				} else {
					densifyURL = ""
				}
			} else {
				densifyURL = ""
			}
		} else {
			densifyURL = ""
		}

	}

	//if we can't resolve creds, then fetch from user
	if densifyURL != "" {
		fmt.Println("Densify URL: " + densifyURL)
		fmt.Print("Is this your Densify URL (y/n)? [y]: ")
		var correctURL string = ""
		fmt.Scanln(&correctURL)
		if !(correctURL == "" || correctURL == "y") {
			densifyURL = ""
		}
	}
	if densifyURL == "" {
		fmt.Print("Enter Densify URL: ")
		fmt.Scanln(&densifyURL)
		densifyURL = strings.TrimSuffix(densifyURL, "/")
	}

	fmt.Print("Enter Densify Username: ")
	fmt.Scanln(&densifyUser)

	fmt.Print("Enter Densify Password: ")
	pass, _ := terminal.ReadPassword(0)
	densifyPass = string(pass)
	fmt.Println("")

	if err := validateSecrets(); err != nil {
		support.RemoveSecretData("helm-optimize-plugin", "densifyURL")
		support.RemoveSecretData("helm-optimize-plugin", "densifyUser")
		support.RemoveSecretData("helm-optimize-plugin", "densifyPass")
		return err
	}

	storeSecrets()

	return nil

}

//GetInsight gets an insight from densify based on the keys cluster, namespace, objType, objName and containerName
func GetInsight(cluster string, namespace string, objType string, objName string, containerName string) (map[string]map[string]string, string, error) {

	insight, err := lookupInsight(cluster, namespace, objType, objName, containerName)
	if err != nil {
		return nil, "", errors.New("unable to locate resource spec")
	}

	var insightObj = map[string]map[string]string{}
	insightObj["limits"] = map[string]string{}
	insightObj["requests"] = map[string]string{}

	approvalSetting, err := getAttribute(insight["entityId"].(string), "attr_ApprovalSetting")
	if err != nil {
		approvalSetting = "Not Approved"
	}

	if approvalSetting != "Not Approved" && support.InMap(insight, []string{"recommendedCpuLimit", "recommendedMemLimit", "recommendedCpuRequest", "recommendedMemRequest"}) && insight["recommendedCpuLimit"].(float64) > 0 && insight["recommendedMemLimit"].(float64) > 0 && insight["recommendedCpuRequest"].(float64) > 0 && insight["recommendedMemRequest"].(float64) > 0 {

		approvalSetting = "Approved"

		insightObj["limits"]["cpu"] = strconv.FormatFloat(insight["recommendedCpuLimit"].(float64), 'f', -1, 64) + "m"
		insightObj["limits"]["memory"] = strconv.FormatFloat(insight["recommendedMemLimit"].(float64), 'f', -1, 64) + "Mi"
		insightObj["requests"]["cpu"] = strconv.FormatFloat(insight["recommendedCpuRequest"].(float64), 'f', -1, 64) + "m"
		insightObj["requests"]["memory"] = strconv.FormatFloat(insight["recommendedMemRequest"].(float64), 'f', -1, 64) + "Mi"

	} else if approvalSetting == "Not Approved" && support.InMap(insight, []string{"currentCpuLimit", "currentMemLimit", "currentCpuRequest", "currentMemRequest"}) && insight["currentCpuLimit"].(float64) > 0 && insight["currentMemLimit"].(float64) > 0 && insight["currentCpuRequest"].(float64) > 0 && insight["currentMemRequest"].(float64) > 0 {

		insightObj["limits"]["cpu"] = strconv.FormatFloat(insight["currentCpuLimit"].(float64), 'f', -1, 64) + "m"
		insightObj["limits"]["memory"] = strconv.FormatFloat(insight["currentMemLimit"].(float64), 'f', -1, 64) + "Mi"
		insightObj["requests"]["cpu"] = strconv.FormatFloat(insight["currentCpuRequest"].(float64), 'f', -1, 64) + "m"
		insightObj["requests"]["memory"] = strconv.FormatFloat(insight["currentMemRequest"].(float64), 'f', -1, 64) + "Mi"

	} else {

		return nil, "", errors.New("invalid resource specs received from repository")

	}

	return insightObj, approvalSetting, nil

}

//UpdateApprovalSetting this will update the approval status for a specific recommendation
func UpdateApprovalSetting(approved bool, cluster string, namespace string, objType string, objName string, containerName string) error {

	insight, err := lookupInsight(cluster, namespace, objType, objName, containerName)
	if err != nil {
		return errors.New("unable to update approval setting")
	}

	if approved == true {
		_, err = support.HTTPRequest("PUT", densifyURL+systemsEP+"/"+insight["entityId"].(string)+"/attributes", densifyUser+":"+densifyPass, []byte("[{\"name\": \"Approval Setting\", \"value\": \"Approve Specific Change\"}]"))
	} else {
		_, err = support.HTTPRequest("PUT", densifyURL+systemsEP+"/"+insight["entityId"].(string)+"/attributes", densifyUser+":"+densifyPass, []byte("[{\"name\": \"Approval Setting\", \"value\": \"Not Approved\"}]"))
	}

	return err

}

//GetApprovalSetting this will update the approval status for a specific recommendation
func GetApprovalSetting(cluster string, namespace string, objType string, objName string, containerName string) (string, error) {

	insight, err := lookupInsight(cluster, namespace, objType, objName, containerName)
	if err != nil {
		return "", errors.New("unable to get approval setting")
	}

	approvalSetting, err := getAttribute(insight["entityId"].(string), "attr_ApprovalSetting")
	if err != nil {
		approvalSetting = "Not Approved"
	}

	if approvalSetting != "Not Approved" {
		approvalSetting = "Approved"
	}

	return approvalSetting, nil

}

////////////////////////////////////////////////////////
///////////////////LOCAL FUNCTIONS//////////////////////
////////////////////////////////////////////////////////

func lookupInsight(cluster string, namespace string, objType string, objName string, containerName string) (map[string]interface{}, error) {

	//locate analysisId if not yet located.
	if analysisId == "" {

		resp, err := support.HTTPRequest("GET", densifyURL+analysisEP, densifyUser+":"+densifyPass, nil)
		if err != nil {
			return nil, errors.New("unable to load analysis")
		}

		var analyses []interface{}
		json.Unmarshal([]byte(resp), &analyses)

		found := false
		for _, analysis := range analyses {
			if analysis.(map[string]interface{})["analysisName"].(string) == cluster {
				analysisId = analysis.(map[string]interface{})["analysisId"].(string)
				found = true
				break
			}
		}

		if !found {
			return nil, errors.New("unable to load analysis")
		}

	}

	resp, err := support.HTTPRequest("GET", densifyURL+analysisEP+"/"+analysisId+"/results?cluster="+cluster+"&namespace="+namespace+"&container="+containerName+"&podService="+objName+"&controllerType="+objType, densifyUser+":"+densifyPass, nil)
	if err != nil {
		return nil, err
	}

	var insights []map[string]interface{}
	json.Unmarshal([]byte(resp), &insights)

	if len(insights) != 1 {
		return nil, errors.New("unable to locate insight")
	}

	return insights[0], nil

}

func getAttribute(entityID string, attrID string) (string, error) {

	resp, err := support.HTTPRequest("GET", densifyURL+systemsEP+"/"+entityID, densifyUser+":"+densifyPass, nil)
	if err != nil {
		return "", errors.New("error locating attribute[" + attrID + "]")
	}

	var respMap map[string]interface{}
	json.Unmarshal([]byte(resp), &respMap)

	for _, val := range respMap["attributes"].([]interface{}) {
		if val.(map[string]interface{})["id"] == attrID {
			return val.(map[string]interface{})["value"].(string), nil
		}
	}

	return "", errors.New("error locating attribute[" + attrID + "]")

}

func validateSecrets() error {

	jsonReq, err := json.Marshal(map[string]string{
		"userName": densifyUser,
		"pwd":      densifyPass,
	})
	if err != nil {
		return err
	}

	_, err = support.HTTPRequest("POST", densifyURL+authorizeEP, densifyUser+":"+densifyPass, jsonReq)
	if err != nil {
		return err
	}

	return nil

}

func storeSecrets() {

	storeSecrets := make(map[string]string)
	storeSecrets["adapter"] = "Densify"
	storeSecrets["densifyURL"] = densifyURL
	storeSecrets["densifyUser"] = densifyUser
	storeSecrets["densifyPass"] = densifyPass
	support.StoreSecrets("helm-optimize-plugin", storeSecrets)

}
