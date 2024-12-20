package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/containerd/containerd/log"
	"github.com/google/uuid"

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

func AddSessionContext(req *http.Request, sessionContext string) {
	req.Header.Set("InterLink-Http-Session", sessionContext)
}

func GetSessionContext(r *http.Request) string {
	sessionContext := r.Header.Get("InterLink-Http-Session")
	if sessionContext == "" {
		id := uuid.New()
		sessionContext = "Request-" + id.String()
	}
	return sessionContext
}

func GetSessionContextMessage(sessionContext string) string {
	return "HTTP InterLink session " + sessionContext + ": "
}

func DoReq(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// respondWithReturn: if false, return nil. Useful when body is too big to be contained in one big string.
// sessionNumber: integer number for debugging purpose, generated from InterLink VK, to follow HTTP request from end-to-end.
func ReqWithError(
	ctx context.Context,
	req *http.Request,
	w http.ResponseWriter,
	start int64,
	span trace.Span,
	respondWithValues bool,
	respondWithReturn bool,
	sessionContext string,
	logHTTPClient *http.Client,
) ([]byte, error) {
	req.Header.Set("Content-Type", "application/json")

	sessionContextMessage := GetSessionContextMessage(sessionContext)
	log.G(ctx).Debug(sessionContextMessage, "doing request: ", fmt.Sprintf("%#v", req))

	// Add session number for end-to-end from API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, sessionContext)

	log.G(ctx).Debug(sessionContextMessage, "before DoReq()")
	resp, err := logHTTPClient.Do(req)
	if err != nil {
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		errWithContext := fmt.Errorf(sessionContextMessage+"error doing DoReq() of ReqWithErrorWithSessionNumber error %w", err)
		return nil, errWithContext
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	// Flush headers ASAP so that the client is not blocked in request.
	if f, ok := w.(http.Flusher); ok {
		log.G(ctx).Debug(sessionContextMessage, "Flushing client...")
		f.Flush()
	} else {
		log.G(ctx).Error(sessionContextMessage, "could not flush because server does not support Flusher.")
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Error(sessionContextMessage, "HTTP request in error.")
		statusCode := http.StatusInternalServerError
		w.WriteHeader(statusCode)
		ret, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf(sessionContextMessage+"HTTP request in error and could not read body response error: %w", err)
		}
		errHTTP := fmt.Errorf(sessionContextMessage+"call exit status: %d. Body: %s", statusCode, ret)
		log.G(ctx).Error(errHTTP)
		_, err = w.Write([]byte(errHTTP.Error()))
		if err != nil {
			return nil, fmt.Errorf(sessionContextMessage+"HTTP request in error and could not write all body response to InterLink Node error: %w", err)
		}
		return nil, errHTTP
	}

	types.SetDurationSpan(start, span, types.WithHTTPReturnCode(resp.StatusCode))

	if respondWithReturn {

		log.G(ctx).Debug(sessionContextMessage, "reading all body once for all")
		returnValue, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return nil, fmt.Errorf(sessionContextMessage+"error doing ReadAll() of ReqWithErrorComplex see error %w", err)
		}

		if respondWithValues {
			_, err = w.Write(returnValue)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return nil, fmt.Errorf(sessionContextMessage+"error doing Write() of ReqWithErrorComplex see error %w", err)
			}
		}

		return returnValue, nil
	}

	// Case no return needed.

	if respondWithValues {
		// Because no return needed, we can write continuously instead of writing one big block of data.
		// Useful to get following logs.
		log.G(ctx).Debug(sessionContextMessage, "in respondWithValues loop, reading body continuously until EOF")

		// In this case, we return continuously the values in the w, instead of reading it all. This allows for logs to be followed.
		bodyReader := bufio.NewReader(resp.Body)

		// 4096 is bufio.NewReader default buffer size.
		bufferBytes := make([]byte, 4096)

		// Looping until we get EOF from sidecar.
		for {
			log.G(ctx).Debug(sessionContextMessage, "trying to read some bytes from InterLink sidecar "+req.RequestURI)
			n, err := bodyReader.Read(bufferBytes)
			if err != nil {
				if err == io.EOF {
					log.G(ctx).Debug(sessionContextMessage, "received EOF and read number of bytes: "+strconv.Itoa(n))

					// EOF but we still have something to read!
					if n != 0 {
						_, err = w.Write(bufferBytes[:n])
						if err != nil {
							w.WriteHeader(http.StatusInternalServerError)
							return nil, fmt.Errorf(sessionContextMessage+"could not write during ReqWithError() error: %w", err)
						}
					}
					return nil, nil
				}
				// Error during read.
				w.WriteHeader(http.StatusInternalServerError)
				return nil, fmt.Errorf(sessionContextMessage+"could not read HTTP body: see error %w", err)
			}
			log.G(ctx).Debug(sessionContextMessage, "received some bytes from InterLink sidecar")
			_, err = w.Write(bufferBytes[:n])
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return nil, fmt.Errorf(sessionContextMessage+"could not write during ReqWithError() error: %w", err)
			}

			// Flush otherwise it will take time to appear in kubectl logs.
			if f, ok := w.(http.Flusher); ok {
				log.G(ctx).Debug(sessionContextMessage, "Wrote some logs, now flushing...")
				f.Flush()
			} else {
				log.G(ctx).Error(sessionContextMessage, "could not flush because server does not support Flusher.")
			}
		}
	}

	// Case no respondWithValue no respondWithReturn , it means we are doing a request and not using response.
	return nil, nil
}
