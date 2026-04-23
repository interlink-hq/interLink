package virtualkubelet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestClearConflictingNetworkAnnotations(t *testing.T) {
	t.Run("full mesh removes wstunnel command annotation", func(t *testing.T) {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annWSTunnelClientCmds: "wstunnel-command",
					annWGClientSnippet:    "wireguard-snippet",
					"keep":                "value",
				},
			},
		}

		clearConflictingNetworkAnnotations(pod, true)

		assert.NotContains(t, pod.Annotations, annWSTunnelClientCmds)
		assert.Contains(t, pod.Annotations, annWGClientSnippet)
		assert.Equal(t, "value", pod.Annotations["keep"])
	})

	t.Run("non mesh removes wireguard snippet annotation", func(t *testing.T) {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					annWSTunnelClientCmds: "wstunnel-command",
					annWGClientSnippet:    "wireguard-snippet",
					"keep":                "value",
				},
			},
		}

		clearConflictingNetworkAnnotations(pod, false)

		assert.Contains(t, pod.Annotations, annWSTunnelClientCmds)
		assert.NotContains(t, pod.Annotations, annWGClientSnippet)
		assert.Equal(t, "value", pod.Annotations["keep"])
	})

	t.Run("nil-safe", func(t *testing.T) {
		assert.NotPanics(t, func() {
			clearConflictingNetworkAnnotations(nil, true)
			clearConflictingNetworkAnnotations(&v1.Pod{}, false)
		})
	})
}
