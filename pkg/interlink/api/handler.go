// Package api provides HTTP handlers for the interLink API server.
// These handlers implement the core REST API endpoints that the Virtual Kubelet
// uses to communicate with interLink for pod lifecycle management.
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

	"github.com/interlink-hq/interlink/pkg/interlink"
)

// InterLinkHandler handles HTTP requests for the interLink API server.
// It acts as a proxy between the Virtual Kubelet and sidecar plugins,
// forwarding requests and managing pod lifecycle operations.
type InterLinkHandler struct {
	// Config holds the interLink configuration
	Config interlink.Config
	// Ctx is the context for request processing
	Ctx context.Context
	// SidecarEndpoint is the URL of the sidecar plugin
	SidecarEndpoint string
	// ClientHTTP is the HTTP client for communicating with the sidecar
	ClientHTTP *http.Client
}

// AddSessionContext adds a session identifier to the HTTP request headers.
// This enables end-to-end tracing of requests from Virtual Kubelet through
// interLink API to the sidecar plugin.
func AddSessionContext(req *http.Request, sessionContext string) {
	req.Header.Set("InterLink-Http-Session", sessionContext)
}

// GetSessionContext retrieves or generates a session context identifier for request tracing.
// If no session context exists in the request headers, a new UUID-based identifier is generated.
// Returns the session context string for use in logging and tracing.
func GetSessionContext(r *http.Request) string {
	sessionContext := r.Header.Get("InterLink-Http-Session")
	if sessionContext == "" {
		// Generate a new session ID if none exists
		id := uuid.New()
		sessionContext = "Request-" + id.String()
	}
	return sessionContext
}

// GetSessionContextMessage formats a session context into a standardized log message prefix.
// This ensures consistent logging format across all HTTP operations.
func GetSessionContextMessage(sessionContext string) string {
	return "HTTP InterLink session " + sessionContext + ": "
}

// ReqWithError executes an HTTP request to a sidecar plugin and handles the response.
// This function provides comprehensive error handling, request tracing, and response streaming.
// It supports both buffered and streaming response modes for efficient handling of large responses.
//
// Parameters:
//   - ctx: Request context for cancellation and tracing
//   - req: HTTP request to execute
//   - w: Response writer to stream results to the client
//   - start: Start timestamp for performance measurement
//   - span: OpenTelemetry span for distributed tracing
//   - respondWithValues: If true, write response data to the ResponseWriter
//   - respondWithReturn: If true, return response data as bytes (use false for large responses)
//   - sessionContext: Session identifier for request tracing
//   - clientHTTP: HTTP client to use for the request
//
// Returns:
//   - []byte: Response body (only if respondWithReturn is true)
//   - error: Any error encountered during request processing
func ReqWithError(
	ctx context.Context,
	req *http.Request,
	w http.ResponseWriter,
	start int64,
	span trace.Span,
	respondWithValues bool,
	respondWithReturn bool,
	sessionContext string,
	clientHTTP *http.Client,
) ([]byte, error) {
	req.Header.Set("Content-Type", "application/json")

	sessionContextMessage := GetSessionContextMessage(sessionContext)
	log.G(ctx).Debug(sessionContextMessage, "doing request: ", fmt.Sprintf("%#v", req))

	// Add session number for end-to-end from API to InterLink plugin (eg interlink-slurm-plugin)
	AddSessionContext(req, sessionContext)

	resp, err := clientHTTP.Do(req)
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

	interlink.SetDurationSpan(start, span, interlink.WithHTTPReturnCode(resp.StatusCode))

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
