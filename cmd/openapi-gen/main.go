package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi3"
	corev1 "k8s.io/api/core/v1"
)

func main() {
	version := flag.String("version", "0.4.0", "generate API spec for this version")
	flag.Parse()

	reflector := openapi3.Reflector{}
	reflector.Spec = &openapi3.Spec{Openapi: "3.0.3"}
	reflector.Spec.Info.
		WithTitle("interLink server API").
		WithVersion(*version).
		WithDescription("This is the API spec for the Virtual Kubelet to interLink API server communication")

	createOp, err := reflector.NewOperationContext(http.MethodPost, "/create")
	if err != nil {
		panic(err)
	}

	// CREATE
	createOp.AddReqStructure(new(interlink.PodCreateRequests))
	createOp.AddRespStructure(new(interlink.RetrievedPodData), func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })

	err = reflector.AddOperation(createOp)
	if err != nil {
		panic(err)
	}

	// DELETE
	deleteOp, err := reflector.NewOperationContext(http.MethodPost, "/delete")
	if err != nil {
		panic(err)
	}

	deleteOp.AddReqStructure(new(corev1.Pod))
	deleteOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })

	err = reflector.AddOperation(deleteOp)
	if err != nil {
		panic(err)
	}

	// Ping
	pingOp, err := reflector.NewOperationContext(http.MethodPost, "/pinglink")
	if err != nil {
		panic(err)
	}

	pingOp.AddReqStructure(nil)
	pingOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })

	err = reflector.AddOperation(pingOp)
	if err != nil {
		panic(err)
	}

	// Status
	statusOp, err := reflector.NewOperationContext(http.MethodPost, "/status")
	if err != nil {
		panic(err)
	}

	statusOp.AddReqStructure(new([]corev1.Pod))
	statusOp.AddRespStructure(new([]interlink.PodStatus), func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })

	err = reflector.AddOperation(statusOp)
	if err != nil {
		panic(err)
	}

	// Logs
	logsOp, err := reflector.NewOperationContext(http.MethodPost, "/getLogs")
	if err != nil {
		panic(err)
	}

	logsOp.AddReqStructure(new(interlink.LogStruct))
	logsOp.AddRespStructure(new(string), func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })

	err = reflector.AddOperation(logsOp)
	if err != nil {
		panic(err)
	}

	schema, err := reflector.Spec.MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(schema))

	// Write the JSON data to the file
	file, err := os.Create("./docs/openapi/interlink-openapi.json")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = file.Write(schema)
	if err != nil {
		panic(err)
	}

	fmt.Println("Successfully wrote to ./docs/openapi/interlink-openapi.json")
}
