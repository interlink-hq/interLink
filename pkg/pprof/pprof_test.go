package pprof

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestPprofStart(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find a free port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx, true, addr, "127.0.0.1:6060")

	time.Sleep(100 * time.Millisecond)

	url := fmt.Sprintf("http://%s/debug/pprof/", addr)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("Failed to request pprof endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 OK, got %d", resp.StatusCode)
	}
}

func TestPprofDisabled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find a free port: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx, false, addr, "127.0.0.1:6060")

	time.Sleep(100 * time.Millisecond)

	// Verify that the server is NOT listening/responding
	url := fmt.Sprintf("http://%s/debug/pprof/", addr)
	_, err = http.Get(url)
	if err == nil {
		t.Error("Expected connection failure for disabled pprof server, but request succeeded")
	}
}
