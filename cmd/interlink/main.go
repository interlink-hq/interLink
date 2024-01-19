package main

import (
	"context"
	"net/http"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	"github.com/intertwin-eu/interlink/pkg/interlink"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
)

var Url string

func main() {
	var cancel context.CancelFunc
	interlink.PodStatuses.Statuses = make(map[string]commonIL.PodStatus)

	commonIL.NewInterLinkConfig()
	logger := logrus.StandardLogger()

	if commonIL.InterLinkConfigInst.VerboseLogging {
		logger.SetLevel(logrus.DebugLevel)
	} else if commonIL.InterLinkConfigInst.ErrorsOnlyLogging {
		logger.SetLevel(logrus.ErrorLevel)
	} else {
		logger.SetLevel(logrus.InfoLevel)
	}

	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))
	interlink.Ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	log.G(interlink.Ctx).Info(commonIL.InterLinkConfigInst)

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", interlink.StatusHandler)
	mutex.HandleFunc("/create", interlink.CreateHandler)
	mutex.HandleFunc("/delete", interlink.DeleteHandler)
	mutex.HandleFunc("/ping", interlink.Ping)
	mutex.HandleFunc("/getLogs", interlink.GetLogsHandler)
	mutex.HandleFunc("/updateCache", interlink.UpdateCacheHandler)
	err := http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Interlinkport, mutex)
	if err != nil {
		log.G(interlink.Ctx).Fatal(err)
	}
}
