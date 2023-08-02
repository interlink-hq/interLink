package interlink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	v1 "k8s.io/api/core/v1"
)

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("InterLink: received Create call")
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}

	var req *http.Request //request to forward to sidecar
	var req2 []*v1.Pod    //request for interlink
	json.Unmarshal(bodyBytes, &req2)

	var retrieved_data []commonIL.RetrievedPodData
	for _, pod := range req2 {
		data := []commonIL.RetrievedPodData{}
		if commonIL.InterLinkConfigInst.ExportPodData {
			data, err = getData(pod)
			if err != nil {
				w.Write([]byte("500"))
				return
			}
			log.Print(data)
		}

		if data == nil {
			data = append(data, commonIL.RetrievedPodData{Pod: *pod})
		}

		retrieved_data = append(retrieved_data, data...)
	}

	bodyBytes, err = json.Marshal(retrieved_data)
	fmt.Println(retrieved_data)
	fmt.Println(string(bodyBytes))
	reader := bytes.NewReader(bodyBytes)

	switch commonIL.InterLinkConfigInst.Sidecarservice {
	case "docker":
		req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/create", reader)

	case "slurm":
		req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/submit", reader)

	default:
		break
	}

	if err != nil {
		log.Fatal(err)
	}

	log.Println("InterLink: forwarding Create call to sidecar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	returnValue, _ := ioutil.ReadAll(resp.Body)
	fmt.Println(string(returnValue))

	w.Write(returnValue)
}
