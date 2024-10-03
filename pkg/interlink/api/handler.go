package api

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/containerd/containerd/log"

	trace "go.opentelemetry.io/otel/trace"

	"github.com/intertwin-eu/interlink/pkg/interlink"
	types "github.com/intertwin-eu/interlink/pkg/interlink"
)

type InterLinkHandler struct {
	Config          interlink.Config
	Ctx             context.Context
	SidecarEndpoint string
	// TODO: http client with TLS
}

func DoReq(ctx context.Context, req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func ReqWithError(
	ctx context.Context,
	req *http.Request,
	w http.ResponseWriter,
	start int64,
	span trace.Span,
) error {

	req.Header.Set("Content-Type", "application/json")
	resp, err := DoReq(ctx, req)

	if err != nil {
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		log.G(ctx).Error(err)
		return err
	}

	if resp.StatusCode != http.StatusOK {
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		ret, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		_, err = w.Write(ret)
		if err != nil {
			return err
		}
		return fmt.Errorf("Call exit status: %d. Body: ", statusCode, ret)
	}

	returnValue, err := io.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.G(ctx).Error(err)
		return err
	}
	log.G(ctx).Debug(string(returnValue))

	w.WriteHeader(resp.StatusCode)
	types.SetDurationSpan(start, span, types.WithHTTPReturnCode(resp.StatusCode))
	_, err = w.Write(returnValue)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.G(ctx).Error(err)
		return err
	}
	return nil
}
