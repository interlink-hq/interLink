package main

import (
	"context"
	"net/http"

	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"

	"github.com/intertwin-eu/interlink/pkg/interlink"
	commonIL "github.com/intertwin-eu/interlink/pkg/interlink"
)

func main() {
	var cancel context.CancelFunc
	interlink.PodStatuses.Statuses = make(map[string]commonIL.PodStatus)

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

	interLinkAPIs := VKapi.InterLinkHandler{
		Config: interLinkConfig,
		Ctx:    ctx,
	}

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", interLinkAPIs.StatusHandler)
	mutex.HandleFunc("/create", interLinkAPIs.CreateHandler)
	mutex.HandleFunc("/delete", interLinkAPIs.DeleteHandler)
	mutex.HandleFunc("/pinglink", interLinkAPIs.Ping)
	mutex.HandleFunc("/getLogs", interLinkAPIs.GetLogsHandler)
	mutex.HandleFunc("/updateCache", interlink.UpdateCacheHandler)
	err = http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Interlinkport, mutex)

	if err != nil {
		log.G(interlink.Ctx).Fatal(err)
	}
}
