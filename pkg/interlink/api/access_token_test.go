package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testAccessToken = "caller-access-token"

// captureLogs sends the logs at the given level and above to a buffer for the rest of
// the test.
func captureLogs(t *testing.T, level logrus.Level) *bytes.Buffer {
	t.Helper()
	logger := logrus.StandardLogger()
	out, previous := logger.Out, logger.GetLevel()
	var logs bytes.Buffer
	logger.SetOutput(&logs)
	logger.SetLevel(level)
	t.Cleanup(func() {
		logger.SetOutput(out)
		logger.SetLevel(previous)
	})
	return &logs
}

// tokenLog records, by path, the access token each request reached a server with.
type tokenLog struct {
	mu     sync.Mutex
	tokens map[string]string
}

func (l *tokenLog) record(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.tokens == nil {
		l.tokens = make(map[string]string)
	}
	l.tokens[r.URL.Path] = r.Header.Get(accessTokenHeader)
}

// at returns the token the request for path arrived with, and whether one arrived.
func (l *tokenLog) at(path string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	token, ok := l.tokens[path]
	return token, ok
}

// sendWithToken sends a GET to url built as the handlers build their requests to the
// sidecar, from an inbound request carrying the caller's access token.
func sendWithToken(t *testing.T, client *http.Client, url string) error {
	t.Helper()
	inbound := httptest.NewRequest(http.MethodGet, "/", nil)
	inbound.Header.Set(accessTokenHeader, testAccessToken)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	ForwardAccessToken(inbound, req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func TestForwardAccessToken(t *testing.T) {
	outbound := func() *http.Request {
		req, err := http.NewRequest(http.MethodGet, "http+unix:///status", nil)
		require.NoError(t, err)
		return req
	}

	t.Run("copies the caller's token", func(t *testing.T) {
		inbound := httptest.NewRequest(http.MethodGet, "/status", nil)
		inbound.Header.Set(accessTokenHeader, testAccessToken)
		out := outbound()

		ForwardAccessToken(inbound, out)

		assert.Equal(t, testAccessToken, out.Header.Get(accessTokenHeader))
	})

	t.Run("adds nothing without a token", func(t *testing.T) {
		out := outbound()

		ForwardAccessToken(httptest.NewRequest(http.MethodGet, "/status", nil), out)

		assert.NotContains(t, out.Header, accessTokenHeader)
	})

	t.Run("ignores a missing request", func(t *testing.T) {
		assert.NotPanics(t, func() {
			ForwardAccessToken(nil, outbound())
			ForwardAccessToken(httptest.NewRequest(http.MethodGet, "/status", nil), nil)
		})
	})
}

// ReqWithError used to write the whole request to the debug log, headers included, and
// with them the caller's access token.
func TestReqWithErrorDoesNotLogTheAccessToken(t *testing.T) {
	tp, cleanup := setupTestTracer()
	defer cleanup()
	ctx, span := tp.Tracer("test").Start(context.Background(), "test-span")
	defer span.End()

	sidecar, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer sidecar.Close()
	req, err := http.NewRequest(http.MethodPost, baseURL+"create", nil)
	require.NoError(t, err)
	req.Header.Set(accessTokenHeader, testAccessToken)
	logs := captureLogs(t, logrus.DebugLevel)

	_, err = ReqWithError(ctx, req, httptest.NewRecorder(), time.Now().UnixMicro(), span, true, false, "Request-test", client)

	require.NoError(t, err)
	assert.NotContains(t, logs.String(), testAccessToken)
}

// A sidecar answering the status call with something that is not JSON used to get the
// whole request it was sent, token included, into an error: logged at error level, so
// even with debug logging off.
func TestStatusHandlerDoesNotLogTheAccessTokenOnABadReply(t *testing.T) {
	var forwarded atomic.Value
	sidecar, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded.Store(r.Header.Get(accessTokenHeader))
		if _, err := io.WriteString(w, "not json"); err != nil {
			panic(err)
		}
	}))
	defer sidecar.Close()
	h := &InterLinkHandler{
		Ctx:             context.Background(),
		SidecarEndpoint: strings.TrimSuffix(baseURL, "/"),
		ClientHTTP:      client,
	}
	body, err := json.Marshal([]*v1.Pod{{
		ObjectMeta: metav1.ObjectMeta{Name: "nb-0", Namespace: "ns", UID: "uid-bad-status-reply"},
		Status:     v1.PodStatus{Phase: v1.PodPending},
	}})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodGet, "/status", bytes.NewReader(body))
	r.Header.Set(accessTokenHeader, testAccessToken)
	logs := captureLogs(t, logrus.InfoLevel)

	h.StatusHandler(httptest.NewRecorder(), r)

	assert.Equal(t, testAccessToken, forwarded.Load(), "the sidecar is the one that should get the token")
	assert.Contains(t, logs.String(), "error doing Unmarshal() in StatusHandler()")
	assert.NotContains(t, logs.String(), testAccessToken)
}

// net/http drops Authorization and cookies when a redirect leaves the host it was sent
// to, but copies every other header, and it copies them again on every hop from the
// original request: without the sidecar client's redirect policy, a redirect would hand
// the caller's access token to whatever host it names. The sidecar here sits behind a
// unix socket, as in the default setup, with a client that routes like the one of
// cmd/interlink.
func TestDropAccessTokenOnRedirect(t *testing.T) {
	var seen tokenLog
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		if r.URL.Path == "/elsewhere-then-back" {
			http.Redirect(w, r, "http://unix/back", http.StatusTemporaryRedirect)
		}
	}))
	defer other.Close()

	sidecar, baseURL, client := newUnixTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		switch r.URL.Path {
		case "/same-host":
			http.Redirect(w, r, "/same-host-again", http.StatusTemporaryRedirect)
		case "/same-host-again":
			http.Redirect(w, r, "/landed", http.StatusTemporaryRedirect)
		case "/other-host":
			http.Redirect(w, r, other.URL+"/elsewhere", http.StatusTemporaryRedirect)
		case "/out-and-back":
			http.Redirect(w, r, other.URL+"/elsewhere-then-back", http.StatusTemporaryRedirect)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusTemporaryRedirect)
		}
	}))
	defer sidecar.Close()
	client.CheckRedirect = DropAccessTokenOnRedirect
	// A policy that never stops would loop until this, rather than until the test times out.
	client.Timeout = 5 * time.Second

	for _, path := range []string{"same-host", "other-host", "out-and-back"} {
		require.NoError(t, sendWithToken(t, client, baseURL+path))
	}

	for _, want := range []struct {
		path, token, why string
	}{
		{"/same-host", testAccessToken, "the sidecar gets the token"},
		{"/landed", testAccessToken, "redirects on the sidecar's own host keep it"},
		{"/elsewhere", "", "a redirect to another host drops it"},
		{"/elsewhere-then-back", "", "a redirect to another host drops it"},
		{"/back", "", "once the chain has left the sidecar, it stays off, even back on the sidecar"},
	} {
		token, ok := seen.at(want.path)
		require.True(t, ok, "%s was never reached", want.path)
		assert.Equal(t, want.token, token, want.why)
	}

	assert.ErrorContains(t, sendWithToken(t, client, baseURL+"loop"), "stopped after 10 redirects")
}

// Another port is another service, even on the same address.
func TestDropAccessTokenOnRedirectToAnotherPort(t *testing.T) {
	var seen tokenLog
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen.record(r)
	}))
	defer other.Close()
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.record(r)
		http.Redirect(w, r, other.URL+"/other-port", http.StatusTemporaryRedirect)
	}))
	defer sidecar.Close()
	client := &http.Client{CheckRedirect: DropAccessTokenOnRedirect}

	require.NoError(t, sendWithToken(t, client, sidecar.URL+"/status"))

	token, ok := seen.at("/status")
	require.True(t, ok)
	assert.Equal(t, testAccessToken, token, "the sidecar gets the token")
	token, ok = seen.at("/other-port")
	require.True(t, ok, "the redirect was never followed")
	assert.Equal(t, "", token, "a redirect to another port drops it")
}
