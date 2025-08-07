package interlink

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

// WithHTTPReturnCode sets the HTTP return code in a span configuration.
// It is used to annotate spans with the HTTP status code returned by a request.
func WithHTTPReturnCode(code int) SpanOption {
	return func(cfg *SpanConfig) {
		cfg.HTTPReturnCode = code
		cfg.SetHTTPCode = true
	}
}

// SetDurationSpan calculates and records the duration of a span.
// It accepts optional SpanOptions to set additional attributes, like HTTP return codes.
func SetDurationSpan(startTime int64, span trace.Span, opts ...SpanOption) {
	endTime := time.Now().UnixMicro()
	config := &SpanConfig{}

	for _, opt := range opts {
		opt(config)
	}

	duration := endTime - startTime
	span.SetAttributes(attribute.Int64("end.timestamp", endTime),
		attribute.Int64("duration", duration))

	if config.SetHTTPCode {
		span.SetAttributes(attribute.Int("exit.code", config.HTTPReturnCode))
	}
}

// SetInfoFromHeaders extracts tracing-related information from HTTP headers
// and sets these attributes on the span, such as X-Forwarded-Email and X-Forwarded-User.
func SetInfoFromHeaders(span trace.Span, h *http.Header) {
	var xForwardedEmail, xForwardedUser string
	if xForwardedEmail = h.Get("X-Forwarded-Email"); xForwardedEmail == "" {
		xForwardedEmail = "unknown"
	}
	if xForwardedUser = h.Get("X-Forwarded-User"); xForwardedUser == "" {
		xForwardedUser = "unknown"
	}
	span.SetAttributes(
		attribute.String("X-Forwarded-Email", xForwardedEmail),
		attribute.String("X-Forwarded-User", xForwardedUser),
	)
}
