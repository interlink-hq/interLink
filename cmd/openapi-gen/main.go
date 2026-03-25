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
	version := flag.String("version", "0.6.0", "generate API spec for this version")
	flag.Parse()

	generateInterlinkSpec(*version)
	generatePluginSpec(*version)
}

// generateInterlinkSpec generates the OpenAPI spec for the Virtual Kubelet to interLink API
// server communication and writes it to ./docs/openapi/interlink-openapi.json.
func generateInterlinkSpec(version string) {
	reflector := openapi3.Reflector{}
	reflector.Spec = &openapi3.Spec{Openapi: "3.0.3"}
	reflector.Spec.Info.
		WithTitle("interLink server API").
		WithVersion(version).
		WithDescription("This is the API spec for the Virtual Kubelet to interLink API server communication")

	// CREATE: VK sends PodCreateRequests; interLink proxies back the plugin's CreateStruct response.
	createOp, err := reflector.NewOperationContext(http.MethodPost, "/create")
	if err != nil {
		panic(err)
	}
	createOp.AddReqStructure(new(interlink.PodCreateRequests))
	createOp.AddRespStructure(new(interlink.CreateStruct), func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })
	if err = reflector.AddOperation(createOp); err != nil {
		panic(err)
	}

	// DELETE: VK sends DELETE to interLink.
	deleteOp, err := reflector.NewOperationContext(http.MethodDelete, "/delete")
	if err != nil {
		panic(err)
	}
	deleteOp.AddReqStructure(new(corev1.Pod))
	deleteOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })
	if err = reflector.AddOperation(deleteOp); err != nil {
		panic(err)
	}

	// Ping
	pingOp, err := reflector.NewOperationContext(http.MethodPost, "/pinglink")
	if err != nil {
		panic(err)
	}
	pingOp.AddReqStructure(nil)
	pingOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })
	if err = reflector.AddOperation(pingOp); err != nil {
		panic(err)
	}

	// Status: VK uses GET with a JSON body.
	statusOp, err := reflector.NewOperationContext(http.MethodGet, "/status")
	if err != nil {
		panic(err)
	}
	statusOp.AddReqStructure(new([]corev1.Pod))
	statusOp.AddRespStructure(new([]interlink.PodStatus), func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })
	if err = reflector.AddOperation(statusOp); err != nil {
		panic(err)
	}

	// Logs: VK uses GET with a JSON body; response is streamed plain text.
	logsOp, err := reflector.NewOperationContext(http.MethodGet, "/getLogs")
	if err != nil {
		panic(err)
	}
	logsOp.AddReqStructure(new(interlink.LogStruct))
	logsOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) {
		cu.HTTPStatus = http.StatusOK
		cu.ContentType = "text/plain"
	})
	if err = reflector.AddOperation(logsOp); err != nil {
		panic(err)
	}

	writeSpec(reflector, "./docs/openapi/interlink-openapi.json")
}

// generatePluginSpec generates the OpenAPI spec for the interLink API server to plugin
// (sidecar) communication and writes it to ./docs/openapi/plugin-openapi.json.
func generatePluginSpec(version string) {
	reflector := openapi3.Reflector{}
	reflector.Spec = &openapi3.Spec{Openapi: "3.0.3"}
	reflector.Spec.Info.
		WithTitle("interLink Plugin API").
		WithVersion(version).
		WithDescription("This is the API spec for the interLink API server to plugin (sidecar) communication")

	// CREATE: interLink sends RetrievedPodData (including jobConfig and jobScript); plugin returns CreateStruct.
	createOp, err := reflector.NewOperationContext(http.MethodPost, "/create")
	if err != nil {
		panic(err)
	}
	createOp.AddReqStructure(new(interlink.RetrievedPodData))
	createOp.AddRespStructure(new(interlink.CreateStruct), func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })
	if err = reflector.AddOperation(createOp); err != nil {
		panic(err)
	}

	// DELETE
	deleteOp, err := reflector.NewOperationContext(http.MethodPost, "/delete")
	if err != nil {
		panic(err)
	}
	deleteOp.AddReqStructure(new(corev1.Pod))
	deleteOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })
	if err = reflector.AddOperation(deleteOp); err != nil {
		panic(err)
	}

	// Status: interLink calls the plugin with GET and a JSON body.
	statusOp, err := reflector.NewOperationContext(http.MethodGet, "/status")
	if err != nil {
		panic(err)
	}
	statusOp.AddReqStructure(new([]corev1.Pod))
	statusOp.AddRespStructure(new([]interlink.PodStatus), func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })
	if err = reflector.AddOperation(statusOp); err != nil {
		panic(err)
	}

	// Logs: interLink calls the plugin with GET; response is streamed plain text.
	logsOp, err := reflector.NewOperationContext(http.MethodGet, "/getLogs")
	if err != nil {
		panic(err)
	}
	logsOp.AddReqStructure(new(interlink.LogStruct))
	logsOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) {
		cu.HTTPStatus = http.StatusOK
		cu.ContentType = "text/plain"
	})
	if err = reflector.AddOperation(logsOp); err != nil {
		panic(err)
	}

	writeSpec(reflector, "./docs/openapi/plugin-openapi.json")
}

// writeSpec marshals the reflector's spec to JSON and writes it to the given file path.
func writeSpec(reflector openapi3.Reflector, path string) {
	schema, err := reflector.Spec.MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}

	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	if _, err = file.Write(schema); err != nil {
		panic(err)
	}

	fmt.Printf("Successfully wrote to %s\n", path)
}
