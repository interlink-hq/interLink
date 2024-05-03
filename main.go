package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"

	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
	"github.com/intertwin-eu/interlink/pkg/interlink/api"
)

func main() {
	var cancel context.CancelFunc
	api.PodStatuses.Statuses = make(map[string]commonIL.PodStatus)

	interLinkConfig, err := commonIL.NewInterLinkConfig()
	if err != nil {
		panic(err)
	}
	logger := logrus.StandardLogger()

	if interLinkConfig.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if interLinkConfig.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.G(ctx).Info(interLinkConfig)

	sidecarEndpoint := ""
	if strings.HasPrefix(interLinkConfig.Sidecarurl, "unix://") {
		sidecarEndpoint = interLinkConfig.Sidecarurl
	} else if strings.HasPrefix(interLinkConfig.Sidecarurl, "http://") {
		sidecarEndpoint = interLinkConfig.Sidecarurl + ":" + interLinkConfig.Sidecarport
	} else {
		log.G(ctx).Fatal("Sidecar URL should either start per unix:// or http://")
	}

	interLinkAPIs := api.InterLinkHandler{
		Config:          interLinkConfig,
		Ctx:             ctx,
		SidecarEndpoint: sidecarEndpoint,
	}

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", interLinkAPIs.StatusHandler)
	mutex.HandleFunc("/create", interLinkAPIs.CreateHandler)
	mutex.HandleFunc("/delete", interLinkAPIs.DeleteHandler)
	mutex.HandleFunc("/pinglink", interLinkAPIs.Ping)
	mutex.HandleFunc("/getLogs", interLinkAPIs.GetLogsHandler)
	mutex.HandleFunc("/updateCache", interLinkAPIs.UpdateCacheHandler)
	err = http.ListenAndServe(":"+interLinkConfig.Interlinkport, mutex)

	if err != nil {
		log.G(ctx).Fatal(err)
	}
}
