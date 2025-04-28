package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/interlink-hq/interlink/pkg/interlink"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi3"
)

func main() {
	reflector := openapi3.Reflector{}
	reflector.Spec = &openapi3.Spec{Openapi: "3.0.3"}
	reflector.Spec.Info.
		WithTitle("Things API").
		WithVersion("1.2.3").
		WithDescription("Put something here")

	putOp, err := reflector.NewOperationContext(http.MethodPut, "/things")
	if err != nil {
		panic(err)
	}

	putOp.AddReqStructure(new(interlink.PodCreateRequests))
	putOp.AddRespStructure(nil, func(cu *openapi.ContentUnit) { cu.HTTPStatus = http.StatusOK })

	err = reflector.AddOperation(putOp)
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
