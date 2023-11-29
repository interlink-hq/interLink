package main

import (
	"context"
	"net/http"
	"strconv"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	slurm "github.com/intertwin-eu/interlink/pkg/sidecars/slurm"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
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
	log.G(context.Background()).Debug("Debug level: " + strconv.FormatBool(commonIL.InterLinkConfigInst.VerboseLogging))

	slurm.Ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", slurm.StatusHandler)
	mutex.HandleFunc("/create", slurm.SubmitHandler)
	mutex.HandleFunc("/delete", slurm.StopHandler)
	mutex.HandleFunc("/getLogs", slurm.GetLogsHandler)

	slurm.CreateDirectories()
	slurm.Load_JIDs()

	err := http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Sidecarport, mutex)
	if err != nil {
		log.G(slurm.Ctx).Fatal(err)
	}
}
