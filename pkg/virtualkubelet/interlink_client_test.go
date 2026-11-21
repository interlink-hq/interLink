package virtualkubelet

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testInterlinkURL = "http://interlink.example.com"

func TestResolveInterlinkEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		port    string
		want    string
		wantErr bool
	}{
		{name: "http", url: "http://vk.interlink.local", port: "3000", want: "http://vk.interlink.local:3000"},
		{name: "https", url: "https://interlink-api.example.com", port: "8443", want: "https://interlink-api.example.com:8443"},
		{name: "http without port", url: "http://interlink-api.example.com", port: "", want: "http://interlink-api.example.com"},
		{name: "unix socket", url: "unix:///run/interlink/vk.sock", port: "", want: "http://unix"},
		{name: "unsupported scheme", url: "ftp://files.interlink.local", port: "21", wantErr: true},
		{name: "no url", url: "", port: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveInterlinkEndpoint(tt.url, tt.port)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewInterlinkClient(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts a remote http endpoint", func(t *testing.T) {
		client, err := NewInterlinkClient(ctx, Config{InterlinkURL: testInterlinkURL, InterlinkPort: "3000"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "http://interlink.example.com:3000", client.endpoint)
		assert.Same(t, client.httpClient, client.logHTTPClient, "logs share the client when no transport is given")
	})

	t.Run("accepts a unix socket", func(t *testing.T) {
		client, err := NewInterlinkClient(ctx, Config{InterlinkURL: "unix:///run/interlink/vk.sock"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "http://unix", client.endpoint)
	})

	t.Run("keeps the token file and the job script builder", func(t *testing.T) {
		client, err := NewInterlinkClient(ctx, Config{
			InterlinkURL:        testInterlinkURL,
			VKTokenFile:         "/var/run/token",
			JobScriptBuilderURL: "http://builder.example.com",
		}, nil)
		require.NoError(t, err)
		assert.Equal(t, "/var/run/token", client.tokenFile)
		assert.Equal(t, "http://builder.example.com", client.jobScriptBuilderURL)
	})

	t.Run("rejects an unsupported scheme", func(t *testing.T) {
		_, err := NewInterlinkClient(ctx, Config{InterlinkURL: "ftp://files.interlink.local"}, nil)
		assert.Error(t, err)
	})

	t.Run("accepts localhost", func(t *testing.T) {
		client, err := NewInterlinkClient(ctx, Config{InterlinkURL: "http://localhost", InterlinkPort: "3000"}, nil)
		require.NoError(t, err)
		assert.Equal(t, "http://localhost:3000", client.endpoint)
	})

	t.Run("reports TLS configuration errors", func(t *testing.T) {
		_, err := NewInterlinkClient(ctx, Config{
			InterlinkURL: "https://interlink.example.com",
			TLS:          TLSConfig{Enabled: true, CACertFile: "/non/existent/ca.crt"},
		}, nil)
		assert.ErrorContains(t, err, "TLS HTTP client")
	})

	t.Run("logs use a dedicated transport without keep-alives and the input is left untouched", func(t *testing.T) {
		transport := &http.Transport{}
		client, err := NewInterlinkClient(ctx, Config{InterlinkURL: testInterlinkURL}, transport)
		require.NoError(t, err)

		logTransport, ok := client.logHTTPClient.Transport.(*http.Transport)
		require.True(t, ok)
		assert.True(t, logTransport.DisableKeepAlives)
		assert.NotSame(t, transport, logTransport)
		assert.False(t, transport.DisableKeepAlives)
	})
}
