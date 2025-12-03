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

	log.G(ctx).Infof("[ReqWithError] Starting request to %s | respondWithValues=%v | respondWithReturn=%v | session=%s",
		req.URL.String(), respondWithValues, respondWithReturn, sessionContext)

	req.Header.Set("Content-Type", "application/json")

	sessionContextMessage := GetSessionContextMessage(sessionContext)
	log.G(ctx).Debug(sessionContextMessage, "doing request: ", fmt.Sprintf("%#v", req))

	// Add session number for end-to-end trace
	AddSessionContext(req, sessionContext)

	resp, err := clientHTTP.Do(req)
	if err != nil {
		statusCode := http.StatusInternalServerError
		log.G(ctx).Errorf("%s HTTP client.Do() failed: %v", sessionContextMessage, err)
		w.WriteHeader(statusCode)
		errWithContext := fmt.Errorf(sessionContextMessage+
			"error doing DoReq() of ReqWithErrorWithSessionNumber error %w", err)
		return nil, errWithContext
	}
	defer func() {
		log.G(ctx).Debugf("%s Closing response body", sessionContextMessage)
		resp.Body.Close()
	}()

	log.G(ctx).Infof("%s Received response: HTTP %d %s", sessionContextMessage, resp.StatusCode, http.StatusText(resp.StatusCode))

	// Always write the response code to client
	log.G(ctx).Debugf("%s Writing response header: %d", sessionContextMessage, resp.StatusCode)
	w.WriteHeader(resp.StatusCode)

	// Flush headers immediately
	if f, ok := w.(http.Flusher); ok {
		log.G(ctx).Debug(sessionContextMessage, "Flushing headers to client...")
		f.Flush()
	} else {
		log.G(ctx).Warn(sessionContextMessage, "Server does not support Flusher.")
	}

	if resp.StatusCode != http.StatusOK {
		log.G(ctx).Errorf("%s Non-OK status from JobScriptBuilder: %d", sessionContextMessage, resp.StatusCode)
		statusCode := http.StatusInternalServerError

		// ❗This is likely the cause of “superfluous WriteHeader” (double call)
		// Add a debug to confirm
		log.G(ctx).Debugf("%s Writing error header: %d (may trigger 'superfluous WriteHeader')", sessionContextMessage, statusCode)
		w.WriteHeader(statusCode)

		ret, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf(sessionContextMessage+
				"HTTP request in error and could not read body response error: %w", err)
		}

		errHTTP := fmt.Errorf("%s call exit status: %d. Body: %s", sessionContextMessage, statusCode, ret)
		log.G(ctx).Error(errHTTP)

		_, err = w.Write([]byte(errHTTP.Error()))
		if err != nil {
			return nil, fmt.Errorf(sessionContextMessage+
				"HTTP request in error and could not write all body response to InterLink Node error: %w", err)
		}

		return nil, errHTTP
	}

	interlink.SetDurationSpan(start, span, interlink.WithHTTPReturnCode(resp.StatusCode))

	// ---------------------------
	// CASE: respondWithReturn == true
	// ---------------------------
	if respondWithReturn {
		log.G(ctx).Debug(sessionContextMessage, "RespondWithReturn mode: reading full body once")

		returnValue, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			log.G(ctx).Errorf("%s Error reading response body: %v", sessionContextMessage, err)
			return nil, fmt.Errorf(sessionContextMessage+
				"error doing ReadAll() of ReqWithErrorComplex see error %w", err)
		}

		log.G(ctx).Debugf("%s Response body (len=%d): %.500s", sessionContextMessage, len(returnValue), string(returnValue))

		if respondWithValues {
			log.G(ctx).Debug(sessionContextMessage, "Writing returnValue to client")
			_, err = w.Write(returnValue)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.G(ctx).Errorf("%s Error writing response body to client: %v", sessionContextMessage, err)
				return nil, fmt.Errorf(sessionContextMessage+
					"error doing Write() of ReqWithErrorComplex see error %w", err)
			}
		}

		log.G(ctx).Infof("%s Completed request successfully (RespondWithReturn=true)", sessionContextMessage)
		return returnValue, nil
	}

	// ---------------------------
	// CASE: respondWithValues == true
	// ---------------------------
	if respondWithValues {
		log.G(ctx).Debug(sessionContextMessage, "RespondWithValues mode: streaming response")

		bodyReader := bufio.NewReader(resp.Body)
		bufferBytes := make([]byte, 4096)

		for {
			n, err := bodyReader.Read(bufferBytes)
			if err != nil {
				if err == io.EOF {
					log.G(ctx).Debugf("%s EOF reached, read %d bytes", sessionContextMessage, n)
					if n > 0 {
						_, err = w.Write(bufferBytes[:n])
						if err != nil {
							w.WriteHeader(http.StatusInternalServerError)
							return nil, fmt.Errorf(sessionContextMessage+
								"could not write during ReqWithError() error: %w", err)
						}
					}
					log.G(ctx).Infof("%s Completed request successfully (stream mode)", sessionContextMessage)
					return nil, nil
				}
				w.WriteHeader(http.StatusInternalServerError)
				log.G(ctx).Errorf("%s Error reading HTTP body: %v", sessionContextMessage, err)
				return nil, fmt.Errorf(sessionContextMessage+
					"could not read HTTP body: see error %w", err)
			}

			log.G(ctx).Debugf("%s Read %d bytes from response", sessionContextMessage, n)
			_, err = w.Write(bufferBytes[:n])
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				log.G(ctx).Errorf("%s Error writing response chunk: %v", sessionContextMessage, err)
				return nil, fmt.Errorf(sessionContextMessage+
					"could not write during ReqWithError() error: %w", err)
			}

			if f, ok := w.(http.Flusher); ok {
				f.Flush()
				log.G(ctx).Debug(sessionContextMessage, "Flushed response chunk to client")
			}
		}
	}

	// ---------------------------
	// CASE: no response needed
	// ---------------------------
	log.G(ctx).Infof("%s Completed request (no response mode)", sessionContextMessage)
	return nil, nil
}
