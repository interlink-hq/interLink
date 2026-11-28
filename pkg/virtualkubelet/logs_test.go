package virtualkubelet

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

const logsTestPod = "logs-pod"

// newLogsServer starts a /getLogs server answering with status and body, and returns
// the config pointing at it.
func newLogsServer(t *testing.T, status int, body string) Config {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing the logs response failed: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)

	return Config{InterlinkURL: "http://" + host, InterlinkPort: port}
}

// closeTrackingConn records when the client side of a connection is closed.
type closeTrackingConn struct {
	net.Conn
	closed *atomic.Bool
}

func (c closeTrackingConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

// closeTrackingTransport returns a transport whose connections set closed when the
// client closes them, which happens once the response body is closed.
func closeTrackingTransport(closed *atomic.Bool) *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return closeTrackingConn{Conn: conn, closed: closed}, nil
		},
	}
}

func TestLogRetrievalErrorStatusClosesBody(t *testing.T) {
	config := newLogsServer(t, http.StatusInternalServerError, "container main not found\n")
	var closed atomic.Bool

	logs, err := LogRetrieval(context.Background(), config, types.LogStruct{PodName: logsTestPod}, closeTrackingTransport(&closed), "GetLogs#1")
	require.Error(t, err)
	assert.Nil(t, logs, "no reader should be returned with the error, nobody would close it")
	assert.Contains(t, err.Error(), "HTTP 500")
	assert.Contains(t, err.Error(), "container main not found")

	// Keep-alives are disabled for logs, so the client closes the connection as soon
	// as the response body is closed.
	assert.Eventually(t, closed.Load, 5*time.Second, 10*time.Millisecond,
		"the connection of the failed log request is still open, the response body was not closed")
}

func TestLogRetrievalErrorStatusBoundsMessage(t *testing.T) {
	config := newLogsServer(t, http.StatusBadGateway, strings.Repeat("x", 3*maxLogErrorBodyBytes))

	_, err := LogRetrieval(context.Background(), config, types.LogStruct{PodName: logsTestPod}, &http.Transport{}, "GetLogs#2")
	require.Error(t, err)
	assert.Less(t, len(err.Error()), 2*maxLogErrorBodyBytes)
}

func TestLogRetrievalSuccessReturnsBody(t *testing.T) {
	config := newLogsServer(t, http.StatusOK, "line 1\nline 2\n")

	logs, err := LogRetrieval(context.Background(), config, types.LogStruct{PodName: logsTestPod}, &http.Transport{}, "GetLogs#3")
	require.NoError(t, err)
	defer logs.Close()

	content, err := io.ReadAll(logs)
	require.NoError(t, err)
	assert.Equal(t, "line 1\nline 2\n", string(content))
}
