package main

import (
	"context"
	"net/http"

	commonIL "github.com/intertwin-eu/interlink/pkg/common"
	slurm "github.com/intertwin-eu/interlink/pkg/sidecars/slurm"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	logruslogger "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
)

func main() {
	var cancel context.CancelFunc

	logger := logrus.StandardLogger()
	logger.SetLevel(logrus.DebugLevel)
	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))

	slurm.Ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	commonIL.NewInterLinkConfig()

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", slurm.StatusHandler)
	mutex.HandleFunc("/create", slurm.SubmitHandler)
	mutex.HandleFunc("/delete", slurm.StopHandler)

	err := http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Sidecarport, mutex)
	if err != nil {
		log.G(slurm.Ctx).Fatal(err)
	}
}
