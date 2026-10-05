package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

// newSpanRecorder installs a tracer provider that keeps finished spans in memory,
// so a test can assert on the outcome the API server actually exports.
func newSpanRecorder(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := trace.NewTracerProvider(trace.WithSyncer(exporter))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		require.NoError(t, tp.Shutdown(context.Background()))
	})

	return exporter
}

func exportedSpan(t *testing.T, exporter *tracetest.InMemoryExporter, name string) tracetest.SpanStub {
	t.Helper()

	var names []string
	for _, s := range exporter.GetSpans() {
		if s.Name == name {
			return s
		}
		names = append(names, s.Name)
	}
	t.Fatalf("no span named %q was exported, got %v", name, names)
	return tracetest.SpanStub{}
}

func spanAttrInt(stub tracetest.SpanStub, key string) (int64, bool) {
	for _, a := range stub.Attributes {
		if string(a.Key) == key {
			return a.Value.AsInt64(), true
		}
	}
	return 0, false
}

// newSidecarHandler returns an InterLinkHandler wired to a sidecar test server
// that always answers with the given status code.
func newSidecarHandler(t *testing.T, sidecarStatus int) *InterLinkHandler {
	t.Helper()

	server, _, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(sidecarStatus)
		if _, err := io.WriteString(w, `[]`); err != nil {
			panic(err)
		}
	}))
	t.Cleanup(server.Close)

	return &InterLinkHandler{
		Ctx:             context.Background(),
		SidecarEndpoint: "http+unix://",
		ClientHTTP:      client,
	}
}

func testPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "span-pod", Namespace: "ns", UID: "span-pod-uid"},
		Status:     v1.PodStatus{Phase: v1.PodRunning},
	}
}

func mustJSON(t *testing.T, v any) *bytes.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return bytes.NewReader(b)
}

// A failing sidecar must produce spans that a metrics pipeline can tell apart
// from successful ones. Before this was fixed every span was exported Unset.
func TestHandlerSpansAreMarkedErrorWhenSidecarFails(t *testing.T) {
	PodStatuses.Statuses = make(map[string]types.PodStatus)

	cases := []struct {
		spanName string
		call     func(h *InterLinkHandler, w http.ResponseWriter)
	}{
		{
			spanName: "PingAPI",
			call: func(h *InterLinkHandler, w http.ResponseWriter) {
				h.Ping(w, httptest.NewRequest(http.MethodGet, "/pinglink", nil))
			},
		},
		{
			spanName: "DeleteAPI",
			call: func(h *InterLinkHandler, w http.ResponseWriter) {
				h.DeleteHandler(w, httptest.NewRequest(http.MethodPost, "/delete", mustJSON(t, testPod())))
			},
		},
		{
			spanName: "StatusAPI",
			call: func(h *InterLinkHandler, w http.ResponseWriter) {
				h.StatusHandler(w, httptest.NewRequest(http.MethodGet, "/status", mustJSON(t, []*v1.Pod{testPod()})))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.spanName, func(t *testing.T) {
			exporter := newSpanRecorder(t)
			h := newSidecarHandler(t, http.StatusInternalServerError)

			tc.call(h, httptest.NewRecorder())

			span := exportedSpan(t, exporter, tc.spanName)
			assert.Equal(t, codes.Error, span.Status.Code, "a failed call must not export an Unset span")
			assert.NotEmpty(t, span.Status.Description, "the failure reason must be recorded")

			code, ok := spanAttrInt(span, "exit.code")
			assert.True(t, ok, "exit.code must be recorded on failure, not only on success")
			assert.NotZero(t, code)
		})
	}
}

func TestHandlerSpansAreMarkedOkWhenSidecarSucceeds(t *testing.T) {
	PodStatuses.Statuses = make(map[string]types.PodStatus)

	cases := []struct {
		spanName string
		call     func(h *InterLinkHandler, w http.ResponseWriter)
	}{
		{
			spanName: "PingAPI",
			call: func(h *InterLinkHandler, w http.ResponseWriter) {
				h.Ping(w, httptest.NewRequest(http.MethodGet, "/pinglink", nil))
			},
		},
		{
			spanName: "DeleteAPI",
			call: func(h *InterLinkHandler, w http.ResponseWriter) {
				h.DeleteHandler(w, httptest.NewRequest(http.MethodPost, "/delete", mustJSON(t, testPod())))
			},
		},
		{
			spanName: "StatusAPI",
			call: func(h *InterLinkHandler, w http.ResponseWriter) {
				h.StatusHandler(w, httptest.NewRequest(http.MethodGet, "/status", mustJSON(t, []*v1.Pod{testPod()})))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.spanName, func(t *testing.T) {
			exporter := newSpanRecorder(t)
			h := newSidecarHandler(t, http.StatusOK)

			tc.call(h, httptest.NewRecorder())

			span := exportedSpan(t, exporter, tc.spanName)
			assert.Equal(t, codes.Ok, span.Status.Code)

			code, ok := spanAttrInt(span, "exit.code")
			assert.True(t, ok, "exit.code must be recorded on success")
			assert.Equal(t, int64(http.StatusOK), code)
		})
	}
}

// A StatusAPI call served entirely from cache never reaches the sidecar. It used
// to record no exit.code at all, which made it indistinguishable from a failure.
func TestStatusHandlerRecordsOutcomeWhenServedFromCache(t *testing.T) {
	exporter := newSpanRecorder(t)

	pod := testPod()
	pod.Status.Phase = v1.PodSucceeded

	PodStatuses.Statuses = map[string]types.PodStatus{
		string(pod.UID): {PodUID: string(pod.UID), PodName: pod.Name, PodNamespace: pod.Namespace},
	}

	// No sidecar is configured: reaching it would fail the request outright.
	h := &InterLinkHandler{Ctx: context.Background(), SidecarEndpoint: "http+unix://", ClientHTTP: http.DefaultClient}

	w := httptest.NewRecorder()
	h.StatusHandler(w, httptest.NewRequest(http.MethodGet, "/status", mustJSON(t, []*v1.Pod{pod})))

	require.Equal(t, http.StatusOK, w.Code)

	span := exportedSpan(t, exporter, "StatusAPI")
	assert.Equal(t, codes.Ok, span.Status.Code)

	code, ok := spanAttrInt(span, "exit.code")
	assert.True(t, ok, "a cache-served call must still record its return code")
	assert.Equal(t, int64(http.StatusOK), code)
}

// Errors detected by the handler itself never reach ReqWithError, so they are
// only observable if the handler marks the span.
func TestHandlerSpansAreMarkedErrorOnRequestErrors(t *testing.T) {
	t.Run("CreateAPI rejects an unparseable body", func(t *testing.T) {
		exporter := newSpanRecorder(t)
		h := newSidecarHandler(t, http.StatusOK)

		w := httptest.NewRecorder()
		h.CreateHandler(w, httptest.NewRequest(http.MethodPost, "/create", bytes.NewReader([]byte("not json"))))

		require.Equal(t, http.StatusInternalServerError, w.Code)

		span := exportedSpan(t, exporter, "CreateAPI")
		assert.Equal(t, codes.Error, span.Status.Code)
		code, ok := spanAttrInt(span, "exit.code")
		assert.True(t, ok)
		assert.Equal(t, int64(http.StatusInternalServerError), code)
	})

	t.Run("GetLogsAPI rejects an unparseable body", func(t *testing.T) {
		exporter := newSpanRecorder(t)
		h := newSidecarHandler(t, http.StatusOK)

		w := httptest.NewRecorder()
		h.GetLogsHandler(w, httptest.NewRequest(http.MethodGet, "/getLogs", bytes.NewReader([]byte("not json"))))

		require.Equal(t, http.StatusBadRequest, w.Code)

		span := exportedSpan(t, exporter, "GetLogsAPI")
		assert.Equal(t, codes.Error, span.Status.Code)
		code, ok := spanAttrInt(span, "exit.code")
		assert.True(t, ok)
		assert.Equal(t, int64(http.StatusBadRequest), code)
	})
}

// ReqWithError used to record the return code only after its non-OK early
// return, so failures carried no exit.code at all.
func TestReqWithErrorRecordsOutcomeOnBothPaths(t *testing.T) {
	for _, tc := range []struct {
		name         string
		sidecar      int
		expectStatus codes.Code
		expectErr    bool
	}{
		{"sidecar failure", http.StatusInternalServerError, codes.Error, true},
		{"sidecar success", http.StatusOK, codes.Unset, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exporter := newSpanRecorder(t)

			server, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.sidecar)
				if _, err := io.WriteString(w, "body"); err != nil {
					panic(err)
				}
			}))
			defer server.Close()

			tracer := otel.Tracer("interlink-API")
			ctx, span := tracer.Start(context.Background(), "ReqWithErrorTest")

			req, err := http.NewRequest(http.MethodGet, baseURL, nil)
			require.NoError(t, err)

			_, err = ReqWithError(ctx, req, httptest.NewRecorder(), 0, span, false, true, "session", client)
			if tc.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			span.End()

			stub := exportedSpan(t, exporter, "ReqWithErrorTest")

			// ReqWithError deliberately never sets Ok: the SDK treats Ok as final
			// and would then ignore an error raised later by the caller.
			assert.Equal(t, tc.expectStatus, stub.Status.Code)

			code, ok := spanAttrInt(stub, "exit.code")
			assert.True(t, ok, "the return code must be recorded on both outcomes")
			assert.Equal(t, int64(tc.sidecar), code)
		})
	}
}
