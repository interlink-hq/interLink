package interlink

import (
	"bytes"
	"io"
	"net/http"
	"strconv"

	"github.com/containerd/containerd/log"
	commonIL "github.com/intertwin-eu/interlink/pkg/common"
)

func StatusHandler(w http.ResponseWriter, r *http.Request) {
	statusCode := http.StatusOK
	log.G(Ctx).Info("InterLink: received GetStatus call")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	reader := bytes.NewReader(bodyBytes)
	req, err := http.NewRequest(http.MethodGet, commonIL.InterLinkConfigInst.Sidecarurl+":"+commonIL.InterLinkConfigInst.Sidecarport+"/status", reader)
	if err != nil {
		log.G(Ctx).Fatal(err)
	}

	log.G(Ctx).Info("InterLink: forwarding GetStatus call to sidecar")
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
	log.G(Ctx).Debug("InterLink: status " + string(returnValue))

	w.WriteHeader(statusCode)
	w.Write(returnValue)
}
