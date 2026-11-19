package virtualkubelet

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCurl writes a fake wstunnel binary to the -o path and records each call.
const fakeCurl = `#!/bin/sh
echo call >> "$FAKE_LOG_DIR/curl"
out=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-o" ]; then out="$2"; shift; fi
  shift
done
printf '#!/bin/sh\necho "$@" >> "$FAKE_LOG_DIR/wstunnel"\n' > "$out"
`

func TestDefaultWstunnelCommandCachesBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}

	for _, tc := range []struct {
		name    string
		command string
		url     string
	}{
		{name: "ws", command: DefaultWstunnelCommand, url: "ws://endpoint.example.com:80"},
		{name: "wss", command: DefaultWstunnelCommandTLS, url: "wss://endpoint.example.com:443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			logDir := t.TempDir()
			cacheDir := t.TempDir()
			workDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(binDir, "curl"), []byte(fakeCurl), 0o755)) // #nosec G306 -- the fake curl has to be executable

			cmdline := fmt.Sprintf(tc.command, "secret", "-R tcp://0.0.0.0:8080:localhost:8080", "endpoint.example.com")
			require.NotContains(t, cmdline, "%!")

			run := func() {
				cmd := exec.Command("sh", "-c", cmdline+" wait") // #nosec G204 -- runs the default command built from constants
				cmd.Dir = workDir
				cmd.Env = append(os.Environ(),
					"PATH="+binDir+":"+os.Getenv("PATH"),
					"XDG_CACHE_HOME="+cacheDir,
					"FAKE_LOG_DIR="+logDir,
				)
				out, err := cmd.CombinedOutput()
				require.NoError(t, err, string(out))
			}
			run()
			run()

			curlCalls, err := os.ReadFile(filepath.Join(logDir, "curl"))
			require.NoError(t, err)
			assert.Equal(t, 1, strings.Count(string(curlCalls), "call"), "binary should be downloaded once and reused")

			clientCalls, err := os.ReadFile(filepath.Join(logDir, "wstunnel"))
			require.NoError(t, err)
			want := "client --http-upgrade-path-prefix secret -R tcp://0.0.0.0:8080:localhost:8080 " + tc.url
			assert.Equal(t, want+"\n"+want+"\n", string(clientCalls))

			assert.FileExists(t, filepath.Join(cacheDir, "interlink", "wstunnel-"+WstunnelVersion, "wstunnel"))
			entries, err := os.ReadDir(workDir)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing should be written to the working directory")
		})
	}
}
