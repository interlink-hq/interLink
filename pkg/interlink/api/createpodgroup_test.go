package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"

	types "github.com/interlink-hq/interlink/pkg/interlink"
)

const (
	testGroupUID           = "0b7c6f6e-5c1d-4d6a-9f3e-2a1b0c9d8e7f"
	testPodGroupSession    = "Request-podgroup-123"
	testMainContainer      = "main"
	testSidecarContainer   = "sidecar"
	testConfigMapName      = "settings"
	testSecretName         = "creds"
	testProjectedVolume    = "token"
	testCreatePodGroupPath = "/createPodGroup"
)

// sidecarCall is what the fake sidecar received in one call.
type sidecarCall struct {
	path    string
	session string
	token   string
	body    types.RetrievedPodGroupData
}

// newPodGroupSidecar starts a fake sidecar that records every call and answers each one
// with statusCode and answer, and returns an InterLinkHandler pointed at it.
func newPodGroupSidecar(t *testing.T, statusCode int, answer string) (*InterLinkHandler, chan sidecarCall) {
	t.Helper()
	calls := make(chan sidecarCall, 4)
	sidecar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := sidecarCall{path: r.URL.Path, session: r.Header.Get("InterLink-Http-Session"), token: r.Header.Get(accessTokenHeader)}
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&call.body))
		calls <- call

		w.WriteHeader(statusCode)
		if _, err := io.WriteString(w, answer); err != nil {
			panic(err)
		}
	}))
	t.Cleanup(sidecar.Close)

	handler := &InterLinkHandler{
		Ctx:             context.Background(),
		SidecarEndpoint: sidecar.URL,
		ClientHTTP:      sidecar.Client(),
	}
	return handler, calls
}

// podGroupPod returns a pod of the test group with two containers: the main one mounts
// the ConfigMap and the projected volume, the sidecar one mounts the Secret only. The
// projected volume holds the pod name, as downward API files do.
func podGroupPod(name string) types.PodCreateRequests {
	return types.PodCreateRequests{
		Pod: v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testDefaultNamespace,
				UID:       k8stypes.UID("uid-" + name),
			},
			Spec: v1.PodSpec{
				Containers: []v1.Container{
					{Name: testMainContainer, VolumeMounts: []v1.VolumeMount{{Name: "config"}, {Name: testProjectedVolume}}},
					{Name: testSidecarContainer, VolumeMounts: []v1.VolumeMount{{Name: "secret"}}},
				},
				Volumes: []v1.Volume{
					{Name: "config", VolumeSource: v1.VolumeSource{ConfigMap: &v1.ConfigMapVolumeSource{
						LocalObjectReference: v1.LocalObjectReference{Name: testConfigMapName},
					}}},
					{Name: "secret", VolumeSource: v1.VolumeSource{Secret: &v1.SecretVolumeSource{SecretName: testSecretName}}},
					{Name: testProjectedVolume, VolumeSource: v1.VolumeSource{Projected: &v1.ProjectedVolumeSource{}}},
				},
			},
		},
		ConfigMaps: []v1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: testConfigMapName}}},
		Secrets:    []v1.Secret{{ObjectMeta: metav1.ObjectMeta{Name: testSecretName}}},
		ProjectedVolumeMaps: []v1.ConfigMap{{
			ObjectMeta: metav1.ObjectMeta{Name: testProjectedVolume},
			Data:       map[string]string{"pod-name": name},
		}},
	}
}

// podGroupRequest returns a POST /createPodGroup of the test group: a launcher pod and two
// workers, in rank order.
func podGroupRequest(t *testing.T, mutate func(*types.PodCreateRequests)) *http.Request {
	t.Helper()
	request := types.PodGroupCreateRequests{
		GroupUID: testGroupUID,
		Pods:     []types.PodCreateRequests{podGroupPod("launcher-0"), podGroupPod("worker-0"), podGroupPod("worker-1")},
	}
	if mutate != nil {
		for i := range request.Pods {
			mutate(&request.Pods[i])
		}
	}
	body, err := json.Marshal(request)
	require.NoError(t, err)

	r := httptest.NewRequest(http.MethodPost, testCreatePodGroupPath, bytes.NewReader(body))
	r.Header.Set("InterLink-Http-Session", testPodGroupSession)
	return r
}

func TestCreatePodGroupHandler_ForwardsGroup(t *testing.T) {
	_, cleanup := setupTestTracer()
	defer cleanup()

	answer := `[{"PodUID":"uid-launcher-0","PodJID":"42"},{"PodUID":"uid-worker-0","PodJID":"42"},{"PodUID":"uid-worker-1","PodJID":"42"}]`
	handler, calls := newPodGroupSidecar(t, http.StatusOK, answer)

	w := httptest.NewRecorder()
	handler.CreatePodGroupHandler(w, podGroupRequest(t, nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, answer, w.Body.String())

	require.Len(t, calls, 1)
	call := <-calls
	assert.Equal(t, testCreatePodGroupPath, call.path)
	assert.Equal(t, testPodGroupSession, call.session)
	assert.Equal(t, testGroupUID, call.body.GroupUID)

	pods := call.body.Pods
	require.Len(t, pods, 3)
	for i, name := range []string{"launcher-0", "worker-0", "worker-1"} {
		pod := pods[i]
		assert.Equal(t, name, pod.Pod.Name)
		require.Len(t, pod.Containers, 2)

		mainData, sidecarData := pod.Containers[0], pod.Containers[1]
		assert.Equal(t, testMainContainer, mainData.Name)
		require.Len(t, mainData.ConfigMaps, 1)
		assert.Equal(t, testConfigMapName, mainData.ConfigMaps[0].Name)
		assert.Empty(t, mainData.Secrets)
		require.Len(t, mainData.ProjectedVolumeMaps, 1)
		assert.Equal(t, name, mainData.ProjectedVolumeMaps[0].Data["pod-name"], "each pod keeps its own projected volume")

		assert.Equal(t, testSidecarContainer, sidecarData.Name)
		require.Len(t, sidecarData.Secrets, 1)
		assert.Equal(t, testSecretName, sidecarData.Secrets[0].Name)
		assert.Empty(t, sidecarData.ConfigMaps)
		assert.Empty(t, sidecarData.ProjectedVolumeMaps)
	}
}

func TestCreatePodGroupHandler_RelaysSidecarErrors(t *testing.T) {
	_, cleanup := setupTestTracer()
	defer cleanup()

	tests := []struct {
		name       string
		statusCode int
		answer     string
	}{
		{
			name:       "transient refusal",
			statusCode: http.StatusServiceUnavailable,
			answer:     "group still runs as job 42",
		},
		{
			name:       "permanent refusal",
			statusCode: http.StatusBadRequest,
			answer:     "this plugin does not run pod groups",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, calls := newPodGroupSidecar(t, tt.statusCode, tt.answer)

			w := httptest.NewRecorder()
			handler.CreatePodGroupHandler(w, podGroupRequest(t, nil))

			assert.Equal(t, tt.statusCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.answer)
			assert.Len(t, calls, 1)
		})
	}
}

func TestCreatePodGroupHandler_RefusesBadRequests(t *testing.T) {
	_, cleanup := setupTestTracer()
	defer cleanup()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "malformed body",
			body: `{"groupUID":`,
		},
		{
			name: "no pods",
			body: `{"groupUID":"` + testGroupUID + `"}`,
		},
		{
			name: "empty pods",
			body: `{"groupUID":"` + testGroupUID + `","pods":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, calls := newPodGroupSidecar(t, http.StatusOK, "[]")

			r := httptest.NewRequest(http.MethodPost, testCreatePodGroupPath, bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			handler.CreatePodGroupHandler(w, r)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.NotEmpty(t, w.Body.String())
			assert.Empty(t, calls, "the sidecar must not be called")
		})
	}
}

func TestCreatePodGroupHandler_JobScriptTemplate(t *testing.T) {
	_, cleanup := setupTestTracer()
	defer cleanup()

	templatePath := filepath.Join(t.TempDir(), "job.sh.tmpl")
	require.NoError(t, os.WriteFile(templatePath, []byte("run {{.Pod.Name}}"), 0o600))

	handler, calls := newPodGroupSidecar(t, http.StatusOK, "[]")
	handler.Config.JobScriptTemplate = templatePath

	w := httptest.NewRecorder()
	handler.CreatePodGroupHandler(w, podGroupRequest(t, nil))

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, calls, 1)
	for _, pod := range (<-calls).body.Pods {
		assert.Equal(t, "run "+pod.Pod.Name, pod.JobScript)
	}
}

func TestCreatePodGroupHandler_JobScriptBuilder(t *testing.T) {
	_, cleanup := setupTestTracer()
	defer cleanup()

	tests := []struct {
		name          string
		builderStatus int
		wantStatus    int
	}{
		{
			name:          "builder answers",
			builderStatus: http.StatusOK,
			wantStatus:    http.StatusOK,
		},
		{
			name:          "builder unavailable",
			builderStatus: http.StatusServiceUnavailable,
			wantStatus:    http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var data types.RetrievedPodData
				assert.NoError(t, json.NewDecoder(r.Body).Decode(&data))
				assert.Equal(t, testPodGroupSession, r.Header.Get("InterLink-Http-Session"))

				w.WriteHeader(tt.builderStatus)
				if _, err := io.WriteString(w, "built for "+data.Pod.Name); err != nil {
					panic(err)
				}
			}))
			defer builder.Close()

			handler, calls := newPodGroupSidecar(t, http.StatusOK, "[]")
			handler.Config.JobScriptBuildConfig = &types.ScriptBuildConfig{}

			w := httptest.NewRecorder()
			handler.CreatePodGroupHandler(w, podGroupRequest(t, func(pod *types.PodCreateRequests) {
				pod.JobScriptBuilderURL = builder.URL
			}))

			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantStatus != http.StatusOK {
				assert.Contains(t, w.Body.String(), "built for launcher-0")
				assert.Empty(t, calls, "the sidecar must not be called")
				return
			}
			require.Len(t, calls, 1)
			for _, pod := range (<-calls).body.Pods {
				assert.Equal(t, "built for "+pod.Pod.Name, pod.JobScript)
			}
		})
	}
}

// The caller's access token reaches the sidecar, as on /create, and never the job script
// builder, whose URL comes from the pod spec.
func TestCreatePodGroupHandler_ForwardsAccessTokenToSidecarOnly(t *testing.T) {
	_, cleanup := setupTestTracer()
	defer cleanup()

	var builderTokens tokenLog
	builder := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		builderTokens.record(r)
		if _, err := io.WriteString(w, "script"); err != nil {
			panic(err)
		}
	}))
	defer builder.Close()

	handler, calls := newPodGroupSidecar(t, http.StatusOK, "[]")
	handler.Config.JobScriptBuildConfig = &types.ScriptBuildConfig{}

	r := podGroupRequest(t, func(pod *types.PodCreateRequests) {
		pod.JobScriptBuilderURL = builder.URL
	})
	r.Header.Set(accessTokenHeader, testAccessToken)
	w := httptest.NewRecorder()
	handler.CreatePodGroupHandler(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	require.Len(t, calls, 1)
	assert.Equal(t, testAccessToken, (<-calls).token)
	token, called := builderTokens.at("/")
	require.True(t, called)
	assert.Empty(t, token, "the builder must not get the caller's token")
}
