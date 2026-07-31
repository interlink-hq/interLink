package virtualkubelet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/containerd/containerd/log"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

// shadowResourcePrefix is prepended to shadow resource names created in the
// offloaded pod's own namespace, so they cannot collide with the pod itself.
const shadowResourcePrefix = "shadow-"

// shadowNamespaceSuffix is appended to the offloaded pod's namespace to build
// the dedicated namespace shadow resources live in by default.
const shadowNamespaceSuffix = "-shadow"

// shadowNodeConfigMapSuffix names the per-shadow ConfigMap carrying the remote
// compute node the workload was allocated on.
const shadowNodeConfigMapSuffix = "-node"

// shadowNodeNameKey is the key inside that ConfigMap holding the node name.
const shadowNodeNameKey = "compute-node"

// maxComputeNodeNameLen is the longest DNS name, and so the longest thing a plugin
// can legitimately report as a compute node.
const maxComputeNodeNameLen = 253

// computeNodeNamePattern matches a hostname or an IP literal and nothing else. The
// shadow interpolates the reported node into a shell command, both locally and on
// the login node, so anything outside this set has to be refused rather than
// escaped: a name containing a space injects an extra ssh argument
// (`-oProxyCommand=...` runs a command in the shadow), and one containing a quote
// or `$(` breaks out of the relay command in exec mode.
var computeNodeNamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._:-]*[A-Za-z0-9])?$`)

// isValidComputeNodeName reports whether a plugin-supplied node name is safe to put
// in front of ssh.
func isValidComputeNodeName(nodeName string) bool {
	return len(nodeName) <= maxComputeNodeNameLen && computeNodeNamePattern.MatchString(nodeName)
}

func sanitizeDNSName(name string) string {
	// Convert to lowercase
	name = strings.ToLower(name)

	// Replace any invalid characters with hyphens
	var builder strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		} else {
			builder.WriteRune('-')
		}
	}
	name = builder.String()

	// Remove leading and trailing hyphens
	name = strings.Trim(name, "-")

	// Collapse consecutive hyphens into a single hyphen
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}

	// Truncate to 63 characters (max label length)
	if len(name) > 63 {
		name = name[:63]
		// Ensure we don't end with a hyphen after truncation
		name = strings.TrimRight(name, "-")
	}

	// If the result is empty, provide a default
	if name == "" {
		name = "default"
	}

	return name
}

// sanitizeFullDNSName sanitizes a full DNS name (with dots) to ensure it meets RFC 1123 requirements
func sanitizeFullDNSName(fullName string) string {
	// Split by dots to handle each label separately
	labels := strings.Split(fullName, ".")

	// Sanitize each label
	sanitizedLabels := make([]string, 0, len(labels))
	for _, label := range labels {
		if label == "" {
			continue
		}
		sanitized := sanitizeDNSName(label)
		if sanitized != "" {
			sanitizedLabels = append(sanitizedLabels, sanitized)
		}
	}

	// Rejoin with dots
	result := strings.Join(sanitizedLabels, ".")

	// Ensure total length doesn't exceed 253 characters
	if len(result) > 253 {
		// Truncate from the beginning (keeping the domain suffix)
		excess := len(result) - 253
		result = result[excess:]
		// Make sure we don't start with a dot after truncation
		result = strings.TrimLeft(result, ".")
	}

	return result
}

// uniqueTruncate shortens a name and adds an 8-character hash to prevent naming collisions.
func uniqueTruncate(s string, maxLen int, full string) string {
	if len(s) <= maxLen {
		return s
	}
	h := sha256.Sum256([]byte(full))
	suffix := hex.EncodeToString(h[:4])
	keep := maxLen - len(suffix) - 1
	return strings.TrimRight(s[:keep], "-") + "-" + suffix
}

type shadowResourceIdentity struct {
	Name      string
	Namespace string
}

func isShadowSameNamespace(pod *v1.Pod) bool {
	if pod == nil || pod.Annotations == nil {
		return false
	}
	return pod.Annotations["interlink.eu/shadow-same-ns"] == "true"
}

func computeShadowResourceIdentity(pod *v1.Pod) (shadowResourceIdentity, error) {
	if pod == nil {
		return shadowResourceIdentity{}, fmt.Errorf("pod is nil")
	}
	if pod.Namespace == "" {
		return shadowResourceIdentity{}, fmt.Errorf("pod namespace is empty")
	}

	var name, namespace string
	if isShadowSameNamespace(pod) {
		name, namespace = computeShadowResourceNamesForSameNamespace(pod.Name, pod.Namespace)
	} else {
		name, namespace = computeShadowResourceNames(pod.Name, pod.Namespace)
	}

	identity := shadowResourceIdentity{Name: name, Namespace: namespace}
	if len(fmt.Sprintf("%s-%s", identity.Name, identity.Namespace)) > 63 {
		return shadowResourceIdentity{}, fmt.Errorf("shadow ingress hostname label %q exceeds 63 characters; shorten pod/namespace or disable interlink.eu/shadow-same-ns", fmt.Sprintf("%s-%s", identity.Name, identity.Namespace))
	}

	return identity, nil
}

func computeShadowResourceNamesForSameNamespace(podName, podNamespace string) (resourceBaseName, namespace string) {
	// Sanitize namespace and pod name for DNS compliance
	sanitizedNamespace := sanitizeDNSName(podNamespace)
	sanitizedPodName := sanitizeDNSName(podName)

	// Use the original namespace. Do not truncate it: in same-namespace mode
	// resources must be created in the pod's real namespace.
	namespace = podNamespace

	// Create a unique resource name to avoid conflicts in the same namespace
	resourceBaseName = shadowResourcePrefix + sanitizedPodName + "-" + sanitizedNamespace
	// Hash on the unsanitized names: sanitizeDNSName truncates to 63 chars, long pods could still collide
	fullBaseName := shadowResourcePrefix + podName + "-" + podNamespace

	// Ensure resourceBaseName doesn't exceed 63 characters
	if len(resourceBaseName) > 63 {
		// Truncate while keeping some of both names
		maxPodNameLen := 28
		maxNsLen := 28
		if len(sanitizedPodName) > maxPodNameLen {
			sanitizedPodName = uniqueTruncate(sanitizedPodName, maxPodNameLen, fullBaseName)
		}
		if len(sanitizedNamespace) > maxNsLen {
			sanitizedNamespace = sanitizedNamespace[:maxNsLen]
		}
		resourceBaseName = shadowResourcePrefix + sanitizedPodName + "-" + sanitizedNamespace
		resourceBaseName = strings.TrimRight(resourceBaseName, "-")
	}

	// Additional check for total length after combining with namespace
	ingressFirstLabel := fmt.Sprintf("%s-%s", resourceBaseName, namespace)
	if len(ingressFirstLabel) > 63 {
		maxNameLen := 63 - len(namespace) - 1
		if maxNameLen > 9 && len(resourceBaseName) > maxNameLen { // >9: room for the 8-char hash suffix
			resourceBaseName = uniqueTruncate(resourceBaseName, maxNameLen, fullBaseName)
		}
	}

	return resourceBaseName, namespace
}

func computeShadowResourceNames(podName, podNamespace string) (resourceBaseName, shadowNamespace string) {
	// Sanitize namespace and pod name for DNS compliance
	sanitizedNamespace := sanitizeDNSName(podNamespace)
	sanitizedPodName := sanitizeDNSName(podName)

	shadowNamespace = sanitizedNamespace + shadowNamespaceSuffix
	// Ensure shadowNamespace is valid (max 63 chars for namespace)
	if len(shadowNamespace) > 63 {
		shadowNamespace = sanitizedNamespace[:min(63-len(shadowNamespaceSuffix), len(sanitizedNamespace))] + shadowNamespaceSuffix
	}

	resourceBaseName = sanitizedPodName + "-" + sanitizedNamespace
	fullBaseName := podName + "-" + podNamespace
	// Ensure resourceBaseName doesn't exceed 63 characters
	if len(resourceBaseName) > 63 {
		// Truncate while keeping some of both names
		maxPodNameLen := 31
		maxNsLen := 31
		if len(sanitizedPodName) > maxPodNameLen {
			sanitizedPodName = uniqueTruncate(sanitizedPodName, maxPodNameLen, fullBaseName)
		}
		if len(sanitizedNamespace) > maxNsLen {
			sanitizedNamespace = sanitizedNamespace[:maxNsLen]
		}
		resourceBaseName = sanitizedPodName + "-" + sanitizedNamespace
		resourceBaseName = strings.TrimRight(resourceBaseName, "-")
	}

	ingressFirstLabel := fmt.Sprintf("%s-%s", resourceBaseName, shadowNamespace)
	if len(ingressFirstLabel) > 63 {
		// If combined length exceeds 63, we need to truncate
		// Strategy: keep both parts but truncate proportionally
		maxNameLen := 31
		maxNsLen := 31

		truncatedName := resourceBaseName
		if len(truncatedName) > maxNameLen {
			truncatedName = uniqueTruncate(truncatedName, maxNameLen, fullBaseName)
		}

		truncatedNs := shadowNamespace
		if len(truncatedNs) > maxNsLen {
			truncatedNs = truncatedNs[:maxNsLen]
			truncatedNs = strings.TrimRight(truncatedNs, "-")
		}

		resourceBaseName = truncatedName
		shadowNamespace = truncatedNs
	}

	return resourceBaseName, shadowNamespace
}

// hasShadow reports whether a shadow Deployment is rendered for this pod, either
// because the pod exposes ports over a tunnel or because mesh networking wraps
// every offloaded pod.
func (p *Provider) hasShadow(pod *v1.Pod) bool {
	return p.shouldCreateShadow(pod) || (p.config.Network.FullMesh && !isMeshNetworkingDisabled(pod))
}

// shadowNodeConfigMapName returns the ConfigMap carrying the compute node name
// for the given shadow.
func shadowNodeConfigMapName(shadowName string) string {
	return shadowName + shadowNodeConfigMapSuffix
}

// resetShadowNodeConfigMap creates, or blanks, the per-shadow node ConfigMap.
// The shadow is rendered before the remote batch system has allocated anything,
// so the ConfigMap has to exist - and be mountable - while still empty. Blanking
// an existing one stops a previous run's node from being tunnelled to.
func (p *Provider) resetShadowNodeConfigMap(ctx context.Context, identity shadowResourceIdentity) error {
	cm := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shadowNodeConfigMapName(identity.Name),
			Namespace: identity.Namespace,
		},
		Data: map[string]string{shadowNodeNameKey: ""},
	}

	_, err := p.clientSet.CoreV1().ConfigMaps(identity.Namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create shadow node configmap %s/%s: %w", identity.Namespace, cm.Name, err)
	}
	if _, err := p.clientSet.CoreV1().ConfigMaps(identity.Namespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("failed to reset shadow node configmap %s/%s: %w", identity.Namespace, cm.Name, err)
	}
	return nil
}

// publishShadowNodeName records the remote compute node a pod was allocated on
// into its shadow's node ConfigMap, so the shadow can direct its tunnel at the
// right host. The ConfigMap is mounted rather than passed as an env var on
// purpose: kubelet refreshes it in place, whereas changing the pod spec would
// restart the shadow and change the pod IP already reported to Kubernetes as the
// offloaded pod's IP.
//
// Repeated calls with an unchanged value are dropped, so the status loop does not
// write on every poll.
func (p *Provider) publishShadowNodeName(ctx context.Context, pod *v1.Pod, nodeName string) {
	if nodeName == "" || !p.hasShadow(pod) {
		return
	}
	if !isValidComputeNodeName(nodeName) {
		log.G(ctx).Errorf(
			"Refusing to publish compute node %q for %s/%s: not a hostname or IP address. The shadow passes this to ssh, so it is rejected rather than escaped.",
			nodeName, pod.Namespace, pod.Name)
		return
	}
	if last, ok := p.shadowNodeNames.Load(string(pod.UID)); ok && last == nodeName {
		return
	}

	identity, err := computeShadowResourceIdentity(pod)
	if err != nil {
		log.G(ctx).Warningf("Failed to compute shadow resource identity for %s/%s: %v", pod.Namespace, pod.Name, err)
		return
	}

	patch, err := json.Marshal(map[string]map[string]string{"data": {shadowNodeNameKey: nodeName}})
	if err != nil {
		log.G(ctx).Warningf("Failed to marshal shadow node patch for %s/%s: %v", pod.Namespace, pod.Name, err)
		return
	}

	name := shadowNodeConfigMapName(identity.Name)
	_, err = p.clientSet.CoreV1().ConfigMaps(identity.Namespace).Patch(
		ctx, name, k8stypes.StrategicMergePatchType, patch, metav1.PatchOptions{},
	)
	if err != nil {
		log.G(ctx).Warningf("Failed to publish compute node %q to shadow configmap %s/%s: %v", nodeName, identity.Namespace, name, err)
		return
	}

	p.shadowNodeNames.Store(string(pod.UID), nodeName)
	log.G(ctx).Infof("Published compute node %q for shadow %s/%s", nodeName, identity.Namespace, identity.Name)
}

// forgetShadowNodeName drops the cached node name for a pod that is going away,
// so a pod recreated under a new UID starts from a clean slate.
func (p *Provider) forgetShadowNodeName(pod *v1.Pod) {
	p.shadowNodeNames.Delete(string(pod.UID))
}
