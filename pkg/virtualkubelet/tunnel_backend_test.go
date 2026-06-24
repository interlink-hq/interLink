package virtualkubelet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTunnelBackend(t *testing.T) {
	tests := []struct {
		name       string
		tunnelType string
		wantType   string
		wantErr    bool
	}{
		{name: "default empty", tunnelType: "", wantType: "wstunnel"},
		{name: "wstunnel", tunnelType: "wstunnel", wantType: "wstunnel"},
		{name: "rathole", tunnelType: "rathole", wantType: tunnelTypeRathole},
		{name: "ssh", tunnelType: "ssh", wantType: tunnelTypeSSH},
		{name: "unknown", tunnelType: "invalid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend, err := newTunnelBackend(Network{TunnelType: tt.tunnelType}, nil)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, backend.Name())
		})
	}
}
