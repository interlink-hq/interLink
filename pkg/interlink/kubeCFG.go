package interlink

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	commonIL "github.com/cloud-pg/interlink/pkg/common"
)

func SetKubeCFGHandler(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		log.Fatal(err)
	}

	var req *http.Request
	reader := bytes.NewReader(bodyBytes)
	var returnValue []byte

	for {
		req, err = http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/setKubeCFG", reader)

		if err != nil {
			log.Fatal(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Println(err)
			time.Sleep(5 * time.Second)
		}

		returnValue, _ = ioutil.ReadAll(resp.Body)
		fmt.Println(string(returnValue))

		if string(returnValue) == "200" {
			break
		}
	}

	w.Write(returnValue)
}
