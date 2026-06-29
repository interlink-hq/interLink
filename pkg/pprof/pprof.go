package pprof

import (
	"context"
	"net"
	"net/http"
	_ "net/http/pprof"

	"github.com/sirupsen/logrus"
)

// Start starts a pprof HTTP server in a background goroutine if enabled.
// It listens on listenAddr, falling back to defaultAddr if listenAddr is empty.
// The server stops when the provided context is canceled.
func Start(ctx context.Context, enabled bool, listenAddr string, defaultAddr string) {
	if !enabled {
		return
	}

	addr := listenAddr
	if addr == "" {
		addr = defaultAddr
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		if !net.ParseIP(addr).IsLoopback() && !net.ParseIP(addr).IsUnspecified() {
			addr = ":" + addr
		}
	}

	logrus.Infof("Starting pprof server on http://%s/debug/pprof/", addr)

	server := &http.Server{
		Addr: addr,
	}

	go func() {
		<-ctx.Done()
		logrus.Infof("Shutting down pprof server on %s", addr)
		_ = server.Close()
	}()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Errorf("pprof server on %s failed: %v", addr, err)
		}
	}()
}
