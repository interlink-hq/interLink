package common

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"k8s.io/client-go/kubernetes"

	"github.com/containerd/containerd/log"
	"gopkg.in/yaml.v2"
)

var InterLinkConfigInst InterLinkConfig
var Clientset *kubernetes.Clientset

// TODO: implement factory design

// NewInterLinkConfig returns a variable of type InterLinkConfig, used in many other functions and the first encountered error.
func NewInterLinkConfig() (InterLinkConfig, error) {
	if !InterLinkConfigInst.set {
		var path string
		verbose := flag.Bool("verbose", false, "Enable or disable Debug level logging")
		errorsOnly := flag.Bool("errorsonly", false, "Prints only errors if enabled")
		InterLinkConfigPath := flag.String("interlinkconfigpath", "", "Path to InterLink config")
		flag.Parse()

		if *verbose {
			InterLinkConfigInst.VerboseLogging = true
			InterLinkConfigInst.ErrorsOnlyLogging = false
		} else if *errorsOnly {
			InterLinkConfigInst.VerboseLogging = false
			InterLinkConfigInst.ErrorsOnlyLogging = true
		}

		if *InterLinkConfigPath != "" {
			path = *InterLinkConfigPath
		} else if os.Getenv("INTERLINKCONFIGPATH") != "" {
			path = os.Getenv("INTERLINKCONFIGPATH")
		} else {
			path = "/etc/interlink/InterLinkConfig.yaml"
		}

		if _, err := os.Stat(path); err != nil {
			log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting INTERLINKCONFIGPATH. Exiting...")
			return InterLinkConfig{}, err
		}

		log.G(context.Background()).Info("Loading InterLink config from " + path)
		yfile, err := os.ReadFile(path)
		if err != nil {
			log.G(context.Background()).Error("Error opening config file, exiting...")
			return InterLinkConfig{}, err
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

		if os.Getenv("SIDECARPORT") != "" {
			InterLinkConfigInst.Sidecarport = os.Getenv("SIDECARPORT")
		}

		if os.Getenv("SBATCHPATH") != "" {
			InterLinkConfigInst.Sbatchpath = os.Getenv("SBATCHPATH")
		}

		if os.Getenv("SCANCELPATH") != "" {
			InterLinkConfigInst.Scancelpath = os.Getenv("SCANCELPATH")
		}

		if os.Getenv("POD_IP") != "" {
			InterLinkConfigInst.PodIP = os.Getenv("POD_IP")
		}

		if os.Getenv("TSOCKS") != "" {
			if os.Getenv("TSOCKS") != "true" && os.Getenv("TSOCKS") != "false" {
				fmt.Println("export TSOCKS as true or false")
				return InterLinkConfig{}, err
			}
			if os.Getenv("TSOCKS") == "true" {
				InterLinkConfigInst.Tsocks = true
			} else {
				InterLinkConfigInst.Tsocks = false
			}
		}

		if os.Getenv("TSOCKSPATH") != "" {
			path = os.Getenv("TSOCKSPATH")
			if _, err := os.Stat(path); err != nil {
				log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting TSOCKSPATH. Exiting...")
				return InterLinkConfig{}, err
			}

			InterLinkConfigInst.Tsockspath = path
		}

		if os.Getenv("VKTOKENFILE") != "" {
			path = os.Getenv("VKTOKENFILE")
			if _, err := os.Stat(path); err != nil {
				log.G(context.Background()).Error("File " + path + " doesn't exist. You can set a custom path by exporting VKTOKENFILE. Exiting...")
				return InterLinkConfig{}, err
			}

			InterLinkConfigInst.VKTokenFile = path
		} else {
			path = InterLinkConfigInst.DataRootFolder + "token"
			InterLinkConfigInst.VKTokenFile = path
		}

		InterLinkConfigInst.set = true
	}
	return InterLinkConfigInst, nil
}

// PingInterLink pings the InterLink API and returns true if there's an answer. The second return value is given by the answer provided by the API.
func PingInterLink(ctx context.Context) (bool, int, error) {
	log.G(ctx).Info("Pinging: " + InterLinkConfigInst.Interlinkurl + ":" + InterLinkConfigInst.Interlinkport + "/pinglink")
	retVal := -1
	req, err := http.NewRequest(http.MethodPost, InterLinkConfigInst.Interlinkurl+":"+InterLinkConfigInst.Interlinkport+"/pinglink", nil)

	if err != nil {
		log.G(ctx).Error(err)
	}

	token, err := os.ReadFile(InterLinkConfigInst.VKTokenFile) // just pass the file name
	if err != nil {
		log.G(ctx).Error(err)
		return false, retVal, err
	}
	req.Header.Add("Authorization", "Bearer "+string(token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, retVal, err
	}

	if resp.StatusCode == http.StatusOK {
		retBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			log.G(ctx).Error(err)
			return false, retVal, err
		}
		retVal, err = strconv.Atoi(string(retBytes))
		if err != nil {
			log.G(ctx).Error(err)
			return false, retVal, err
		}
		return true, retVal, nil
	} else {
		log.G(ctx).Error("server error: " + fmt.Sprint(resp.StatusCode))
		return false, retVal, nil
	}
}
