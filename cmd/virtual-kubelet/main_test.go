package main

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	commonIL "github.com/interlink-hq/interlink/pkg/virtualkubelet"
)

// The interLink API server closes a connection idle for its ReadTimeout (30 s, cmd/interlink):
// the virtual kubelet's transport must drop it first, or a ping sent on a connection the server
// is closing fails with EOF and the node goes NotReady. The unix-socket mode must keep working.
func TestAPITransportDropsIdleConnectionsBeforeTheServer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "interlink.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	transport := createHTTPTransport(context.Background(), commonIL.Config{InterlinkURL: "unix://" + sock}, commonIL.Config{})
	if transport.IdleConnTimeout <= 0 || transport.IdleConnTimeout >= 30*time.Second {
		t.Errorf("idle connections kept for %v, want less than the API server's 30 s", transport.IdleConnTimeout)
	}

	client := &http.Client{Transport: &UnixSocketRoundTripper{Transport: transport}}
	resp, err := client.Post("http+unix://interlink/pinglink", "", nil)
	if err != nil {
		t.Fatalf("ping over the unix socket: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping over the unix socket: %d", resp.StatusCode)
	}
}
