package virtualkubelet

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/stretchr/testify/require"
)

func TestMeshTemplateExportsContactHost(t *testing.T) {
	content, err := meshScriptTemplate.ReadFile("templates/mesh.sh")
	require.NoError(t, err)

	tmpl, err := template.New("mesh").Parse(string(content))
	require.NoError(t, err)

	var rendered bytes.Buffer
	err = tmpl.Execute(&rendered, MeshScriptTemplateData{
		MeshContactHost: "worker-tunnel.pods-wstunnel.svc.cluster.local",
	})
	require.NoError(t, err)
	require.Contains(t, rendered.String(), `export INTERLINK_MESH_CONTACT_HOST="worker-tunnel.pods-wstunnel.svc.cluster.local"`)
}
