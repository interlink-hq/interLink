package interlink

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	statusCode := http.StatusOK
	log.G(Ctx).Info("InterLink: received GetLogs call")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	var req2 commonIL.LogStruct //incoming request. To be used in interlink API. req is directly forwarded to sidecar
	err = json.Unmarshal(bodyBytes, &req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}

	pod, err := Clientset.CoreV1().Pods(req2.Namespace).Get(Ctx, req2.PodName, metav1.GetOptions{})
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}
	req2.PodUID = string(pod.UID)

	if (req2.Opts.Tail != 0 && req2.Opts.LimitBytes != 0) || (req2.Opts.SinceSeconds != 0 && !req2.Opts.SinceTime.IsZero()) {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		if req2.Opts.Tail != 0 && req2.Opts.LimitBytes != 0 {
			w.Write([]byte("Both Tail and LimitBytes set. Set only one of them"))
		} else {
			w.Write([]byte("Both SinceSeconds and SinceTime set. Set only one of them"))
		}
		log.G(Ctx).Error(errors.New("Check Opts configurations"))
		return
	}

	bodyBytes, err = json.Marshal(req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/getLogs", reader)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	log.G(Ctx).Info("InterLink: forwarding GetLogs call to sidecar")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.L.Error("Unexpected error occured. Status code: " + strconv.Itoa(resp.StatusCode) + ". Check Sidecar's logs for further informations")
		statusCode = http.StatusInternalServerError
	}

	returnValue, _ := io.ReadAll(resp.Body)
	log.G(Ctx).Debug("InterLink: logs " + string(returnValue))

	w.WriteHeader(statusCode)
	w.Write(returnValue)
}
