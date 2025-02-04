package interlink

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	trace "go.opentelemetry.io/otel/trace"
)

func WithHTTPReturnCode(code int) SpanOption {
	return func(cfg *SpanConfig) {
		cfg.HTTPReturnCode = code
		cfg.SetHTTPCode = true
	}
}

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
