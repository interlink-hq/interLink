package main

import (
	"context"
	"fmt"
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

	logger := logrus.StandardLogger()
	logger.SetLevel(logrus.DebugLevel)
	log.L = logruslogger.FromLogrus(logrus.NewEntry(logger))

	interlink.Ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	commonIL.NewInterLinkConfig()

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", interlink.StatusHandler)
	mutex.HandleFunc("/create", interlink.CreateHandler)
	mutex.HandleFunc("/delete", interlink.DeleteHandler)
	mutex.HandleFunc("/setKubeCFG", interlink.SetKubeCFGHandler)

	fmt.Println(commonIL.InterLinkConfigInst)

	err := http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Interlinkport, mutex)
	if err != nil {
		log.G(interlink.Ctx).Fatal(err)
	}
}
