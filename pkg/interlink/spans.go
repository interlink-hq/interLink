package interlink

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
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

// SetHTTPReturnCode records the HTTP status code reported to the client on the
// span, without touching timing information. Use it when the code becomes known
// at a different moment from the end of the operation.
func SetHTTPReturnCode(span trace.Span, statusCode int) {
	span.SetAttributes(attribute.Int("exit.code", statusCode))
}

// SetSpanError marks the span as failed, records err on it, and — when
// statusCode is non-zero — stores the HTTP status reported to the client.
//
// The span status is the only outcome signal that generic tracing consumers
// understand: the OpenTelemetry Collector spanmetrics connector, for instance,
// exposes it as the status_code dimension used to build error-rate metrics. A
// failing path that does not call this produces a span indistinguishable from a
// successful one, so every error path must call it before returning.
//
// The SDK never downgrades an Ok status back to Error, so this must run before
// any SetSpanOK on the same span. In practice that means calling it on the error
// path immediately before returning.
func SetSpanError(span trace.Span, statusCode int, err error) {
	if statusCode != 0 {
		SetHTTPReturnCode(span, statusCode)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return
	}
	span.SetStatus(codes.Error, http.StatusText(statusCode))
}

// SetSpanOK marks the span as successful and — when statusCode is non-zero —
// stores the HTTP status reported to the client.
//
// The SDK treats Ok as final and ignores any later attempt to set Error, so call
// this only once the handler has genuinely completed, after every error path has
// already returned.
func SetSpanOK(span trace.Span, statusCode int) {
	if statusCode != 0 {
		SetHTTPReturnCode(span, statusCode)
	}
	span.SetStatus(codes.Ok, "")
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
