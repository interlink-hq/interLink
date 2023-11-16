package main

import (
	"context"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	docker "github.com/intertwin-eu/interlink/pkg/sidecars/docker"
)

func main() {
	var cancel context.CancelFunc
	logger := logrus.StandardLogger()

	commonIL.NewInterLinkConfig()

	if commonIL.InterLinkConfigInst.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if commonIL.InterLinkConfigInst.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))

	docker.Ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", docker.StatusHandler)
	mutex.HandleFunc("/create", docker.CreateHandler)
	mutex.HandleFunc("/delete", docker.DeleteHandler)
	mutex.HandleFunc("/getLogs", docker.GetLogsHandler)
	err := http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Sidecarport, mutex)

	if err != nil {
		log.L.Fatal(err)
	}
}
