package pprof

import (
	"context"
	"net"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // G108: profiling endpoint exposure is intentional
	"time"

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
		isPortOnly := true
		for _, r := range addr {
			if r < '0' || r > '9' {
				isPortOnly = false
				break
			}
		}
		if isPortOnly {
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logrus.Errorf("pprof server on %s shutdown error: %v", addr, err)
		}
	}()

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrus.Errorf("pprof server on %s failed: %v", addr, err)
		}
	}()
}
