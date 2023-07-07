package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"

	"gopkg.in/yaml.v2"
)

var InterLinkConfigInst InterLinkConfig

func NewInterLinkConfig() {
	if InterLinkConfigInst.set == false {
		var path string
		if os.Getenv("INTERLINKCONFIGPATH") != "" {
			path = os.Getenv("INTERLINKCONFIGPATH")
		} else {
			path = "/etc/interlink/InterLinkConfig.yaml"
		}

		if _, err := os.Stat(path); err != nil {
			log.Println("File " + path + " doesn't exist. You can set a custom path by exporting INTERLINKCONFIGPATH. Exiting...")
			os.Exit(-1)
		}

		yfile, err := os.ReadFile(path)
		if err != nil {
			log.Println("Error opening config file, exiting...")
			os.Exit(1)
		}
		yaml.Unmarshal(yfile, &InterLinkConfigInst)

		if os.Getenv("INTERLINKURL") != "" {
			InterLinkConfigInst.Interlinkurl = os.Getenv("INTERLINKURL")
		}

		if os.Getenv("SIDECARURL") != "" {
			InterLinkConfigInst.Sidecarurl = os.Getenv("SIDECARURL")
		}

		if os.Getenv("INTERLINKPORT") != "" {
			InterLinkConfigInst.Interlinkport = os.Getenv("INTERLINKPORT")
		}

		if os.Getenv("SIDECARSERVICE") != "" {
			if os.Getenv("SIDECARSERVICE") != "docker" && os.Getenv("SIDECARSERVICE") != "slurm" {
				fmt.Println("export SIDECARSERVICE as docker or slurm")
				os.Exit(-1)
			}
			InterLinkConfigInst.Sidecarservice = os.Getenv("SIDECARSERVICE")
		} else if InterLinkConfigInst.Sidecarservice != "docker" && InterLinkConfigInst.Sidecarservice != "slurm" {
			fmt.Println("Set \"docker\" or \"slurm\" in config file or export SIDECARSERVICE as ENV")
			os.Exit(-1)
		}

		if os.Getenv("SIDECARPORT") != "" && os.Getenv("SIDECARSERVICE") == "" {
			InterLinkConfigInst.Sidecarport = os.Getenv("SIDECARPORT")
			InterLinkConfigInst.Sidecarservice = "Custom Service"
		} else {
			switch InterLinkConfigInst.Sidecarservice {
			case "docker":
				InterLinkConfigInst.Sidecarport = "4000"

			case "slurm":
				InterLinkConfigInst.Sidecarport = "4001"

			default:
				fmt.Println("Define in InterLinkConfig.yaml one service between docker and slurm")
				os.Exit(-1)
			}
		}

		if os.Getenv("SBATCHPATH") != "" {
			InterLinkConfigInst.Sbatchpath = os.Getenv("SBATCHPATH")
		}

		if os.Getenv("SCANCELPATH") != "" {
			InterLinkConfigInst.Scancelpath = os.Getenv("SCANCELPATH")
		}

		if os.Getenv("TSOCKS") != "" {
			if os.Getenv("TSOCKS") != "true" && os.Getenv("TSOCKS") != "false" {
				fmt.Println("export TSOCKS as true or false")
				os.Exit(-1)
			}
			if os.Getenv("TSOCKS") == "true" {
				InterLinkConfigInst.Tsocks = true
			} else {
				InterLinkConfigInst.Tsocks = false
			}
		}

		if os.Getenv("TSOCKSPATH") != "" {
			path := os.Getenv("TSOCKSPATH")
			if _, err := os.Stat(path); err != nil {
				log.Println("File " + path + " doesn't exist. You can set a custom path by exporting TSOCKSPATH. Exiting...")
				os.Exit(-1)
			}

			InterLinkConfigInst.Tsockspath = path
		}

		if os.Getenv("VKTOKENFILE") != "" {
			path := os.Getenv("VKTOKENFILE")
			if _, err := os.Stat(path); err != nil {
				log.Println("File " + path + " doesn't exist. You can set a custom path by exporting VKTOKENFILE. Exiting...")
				os.Exit(-1)
			}

			InterLinkConfigInst.VKTokenFile = path
		} else {
			path = "/tmp/token"
			InterLinkConfigInst.VKTokenFile = path
		}

		InterLinkConfigInst.set = true
	}
}

func NewServiceAccount() {

	var path string

	if os.Getenv("CUSTOMKUBECONF") != "" {
		path = os.Getenv("CUSTOMKUBECONF")
	} else {
		path = "/tmp/sa.kubeconfig"
		sa := "apiVersion: v1\n" +
			"clusters:\n" +
			"- cluster:\n" +
			"    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJlRENDQVIyZ0F3SUJBZ0lCQURBS0JnZ3Foa2pPUFFRREFqQWpNU0V3SHdZRFZRUUREQmhyTTNNdGMyVnkKZG1WeUxXTmhRREUyTmpreU56azJPVE13SGhjTk1qSXhNVEkwTURnME9ERXpXaGNOTXpJeE1USXhNRGcwT0RFegpXakFqTVNFd0h3WURWUVFEREJock0zTXRjMlZ5ZG1WeUxXTmhRREUyTmpreU56azJPVE13V1RBVEJnY3Foa2pPClBRSUJCZ2dxaGtqT1BRTUJCd05DQUFUTjZMZzJSaFBtV09pUTdEUkNzenlJeDFnaERLR3l1K3hEaEhyR21BSU4KUkh3R0RqdEVJWEtuYXQrdmhIOE9wSVJPS0ZLK2xKNThDc3J0TW4vSGxkb3VvMEl3UURBT0JnTlZIUThCQWY4RQpCQU1DQXFRd0R3WURWUjBUQVFIL0JBVXdBd0VCL3pBZEJnTlZIUTRFRmdRVXQ2RzBzV3ZYbXVnTCtyYlN4V2lHCk1oNjNGT0l3Q2dZSUtvWkl6ajBFQXdJRFNRQXdSZ0loQU54NU1RUCt4SHBpL0NxVm1BVzBzOXZhaTlxYVZqb0UKNmg4dEJpQWxZU1dZQWlFQXJLQTdiR2poRjByT0cvZTN5YUNTQmNxeFhLUHRCZUE1S2hWSEdDeHpqbGs9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0K\n" +
			"    server: https://0.0.0.0:43295\n" +
			"  name: k3d-mycluster\n" +
			"contexts:\n" +
			"- context:\n" +
			"    cluster: k3d-mycluster\n" +
			"    user: admin@k3d-mycluster\n" +
			"  name: k3d-mycluster\n" +
			"current-context: k3d-mycluster\n" +
			"kind: Config\n" +
			"preferences: {}\n" +
			"users:\n" +
			"- name: admin@k3d-mycluster\n" +
			"  user:\n" +
			"    client-certificate-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUJrakNDQVRlZ0F3SUJBZ0lJWG1WcWJRTkgzSnN3Q2dZSUtvWkl6ajBFQXdJd0l6RWhNQjhHQTFVRUF3d1kKYXpOekxXTnNhV1Z1ZEMxallVQXhOalk1TWpjNU5qa3pNQjRYRFRJeU1URXlOREE0TkRneE0xb1hEVEl6TVRFeQpOREE0TkRneE0xb3dNREVYTUJVR0ExVUVDaE1PYzNsemRHVnRPbTFoYzNSbGNuTXhGVEFUQmdOVkJBTVRESE41CmMzUmxiVHBoWkcxcGJqQlpNQk1HQnlxR1NNNDlBZ0VHQ0NxR1NNNDlBd0VIQTBJQUJCbm93YWxNV212Rm8waXoKY004bm5GaEQ3dnZaOUFnRW8zWFFyeVlrdWxleHlwbSswZmVjdjFLQWZheDQzTDJJYm4xbDZ1UVR6eUpJUDJMdQo5TFJYVXJ5alNEQkdNQTRHQTFVZER3RUIvd1FFQXdJRm9EQVRCZ05WSFNVRUREQUtCZ2dyQmdFRkJRY0RBakFmCkJnTlZIU01FR0RBV2dCVHdUL05tQjV0NGF0UXhvbVlTZHVET3lOTWZtekFLQmdncWhrak9QUVFEQWdOSkFEQkcKQWlFQWlIMm9IT0pVU3B1NW9iWXBrR2R2cmlkT0ZTbmpjUXoyUy9SYXNQUGNBYmdDSVFEbm50aUlIUXdmOEcxegpQR1VLekhkTjBydVE5S0lHSEFpdDFBRFhMSWJOdkE9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCi0tLS0tQkVHSU4gQ0VSVElGSUNBVEUtLS0tLQpNSUlCZHpDQ0FSMmdBd0lCQWdJQkFEQUtCZ2dxaGtqT1BRUURBakFqTVNFd0h3WURWUVFEREJock0zTXRZMnhwClpXNTBMV05oUURFMk5qa3lOemsyT1RNd0hoY05Nakl4TVRJME1EZzBPREV6V2hjTk16SXhNVEl4TURnME9ERXoKV2pBak1TRXdId1lEVlFRRERCaHJNM010WTJ4cFpXNTBMV05oUURFMk5qa3lOemsyT1RNd1dUQVRCZ2NxaGtqTwpQUUlCQmdncWhrak9QUU1CQndOQ0FBVE9BSU90WUJMcW9TQzEzTXhocEd0Tk95QU1zWjNFTElhTnJIeWU5TWt4CnlQY1lEcEtMZ08yWWVsZ2JFZ3FHeDc2dGFqMElJcjMzMFBLdWQrR1NKbmErbzBJd1FEQU9CZ05WSFE4QkFmOEUKQkFNQ0FxUXdEd1lEVlIwVEFRSC9CQVV3QXdFQi96QWRCZ05WSFE0RUZnUVU4RS96WmdlYmVHclVNYUptRW5iZwp6c2pUSDVzd0NnWUlLb1pJemowRUF3SURTQUF3UlFJaEFQSWdlSG01bFdkUlJMZitXdElzV29ySVdNREVkYytsCnVNN3hnUUxOY1luZEFpQVhCdnNwTjlub3pvTTkrdXdJdDZaTmxZMkdzNHIwellpTERPSHlGejBjM0E9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg==\n" +
			"    client-key-data: LS0tLS1CRUdJTiBFQyBQUklWQVRFIEtFWS0tLS0tCk1IY0NBUUVFSUlhbXpId04zMkZCUUI3K2cyamZ2ekpKZnI4dDFzdy9EeStuQXFOYXBFVDdvQW9HQ0NxR1NNNDkKQXdFSG9VUURRZ0FFR2VqQnFVeGFhOFdqU0xOd3p5ZWNXRVB1KzluMENBU2pkZEN2SmlTNlY3SEttYjdSOTV5LwpVb0I5ckhqY3ZZaHVmV1hxNUJQUElrZy9ZdTcwdEZkU3ZBPT0KLS0tLS1FTkQgRUMgUFJJVkFURSBLRVktLS0tLQo=\n"
		os.WriteFile(path, []byte(sa), 0644)
	}
	for {
		returnedVal := SendKubeConfig(path)
		if returnedVal == "200" {
			break
		} else {
			fmt.Println(returnedVal)
		}
	}
}

func SendKubeConfig(body string) string {
	var returnValue, _ = json.Marshal("Error")
	request := GenericRequestType{Body: body}

	bodyBytes, err := json.Marshal(request)
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, InterLinkConfigInst.Interlinkurl+":"+InterLinkConfigInst.Interlinkport+"/setKubeCFG", reader)

	if err != nil {
		log.Println(err)
	}

	token, err := os.ReadFile(InterLinkConfigInst.VKTokenFile) // just pass the file name
	req.Header.Add("Authorization", "Bearer "+string(token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println(err)
		time.Sleep(5 * time.Second)
	} else {
		returnValue, _ = ioutil.ReadAll(resp.Body)

		if string(returnValue) == "200" {
			return "200"
		}
	}

	return "400"
}
