package virtualkubelet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
)

// shadowResourcePrefix is prepended to shadow resource names created in the
// offloaded pod's own namespace, so they cannot collide with the pod itself.
const shadowResourcePrefix = "shadow-"

// shadowNamespaceSuffix is appended to the offloaded pod's namespace to build
// the dedicated namespace shadow resources live in by default.
const shadowNamespaceSuffix = "-shadow"

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
