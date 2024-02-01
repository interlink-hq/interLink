package main

import (
	"context"
	"net/http"
	"strconv"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	docker "github.com/intertwin-eu/interlink/pkg/sidecars/docker"
)

func main() {
	logger := logrus.StandardLogger()

	interLinkConfig, err := commonIL.NewInterLinkConfig()
	if err != nil {
		log.L.Fatal(err)
	}

	if interLinkConfig.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if interLinkConfig.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	Ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log.G(Ctx).Debug("Debug level: " + strconv.FormatBool(interLinkConfig.VerboseLogging))

	SidecarAPIs := docker.SidecarHandler{
		Config: interLinkConfig,
		Ctx:    Ctx,
	}

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", SidecarAPIs.StatusHandler)
	mutex.HandleFunc("/create", SidecarAPIs.CreateHandler)
	mutex.HandleFunc("/delete", SidecarAPIs.DeleteHandler)
	mutex.HandleFunc("/getLogs", SidecarAPIs.GetLogsHandler)
	err = http.ListenAndServe(":"+interLinkConfig.Sidecarport, mutex)

	if err != nil {
		log.G(Ctx).Fatal(err)
	}
}
