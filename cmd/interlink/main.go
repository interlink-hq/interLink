package main

import (
	"fmt"
	"log"
	"net/http"

	commonIL "github.com/cloud-pg/interlink/pkg/common"
	"github.com/cloud-pg/interlink/pkg/interlink"
)

var Url string

func main() {

	commonIL.NewInterLinkConfig()

	mutex := http.NewServeMux()
	mutex.HandleFunc("/status", interlink.StatusHandler)
	mutex.HandleFunc("/create", interlink.CreateHandler)
	mutex.HandleFunc("/delete", interlink.DeleteHandler)
	mutex.HandleFunc("/setKubeCFG", interlink.SetKubeCFGHandler)

	fmt.Println(commonIL.InterLinkConfigInst)

	err := http.ListenAndServe(":"+commonIL.InterLinkConfigInst.Interlinkport, mutex)
	if err != nil {
		log.Fatal(err)
	}
}
