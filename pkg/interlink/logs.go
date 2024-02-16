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
)

func (h *InterLinkHandler) GetLogsHandler(w http.ResponseWriter, r *http.Request) {
	statusCode := http.StatusOK
	log.G(Ctx).Info("InterLink: received GetLogs call")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	log.G(Ctx).Info("InterLink: unmarshal GetLogs request")
	var req2 commonIL.LogStruct //incoming request. To be used in interlink API. req is directly forwarded to sidecar
	err = json.Unmarshal(bodyBytes, &req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}

	log.G(Ctx).Info("InterLink: new GetLogs podUID: now ", string(req2.PodUID))
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

	log.G(Ctx).Info("InterLink: marshal GetLogs request ")

	bodyBytes, err = json.Marshal(req2)
	if err != nil {
		statusCode = http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(Ctx).Error(err)
		return
	}
	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodPost, h.Config.Sidecarurl+":"+h.Config.Sidecarport+"/getLogs", reader)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")
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
