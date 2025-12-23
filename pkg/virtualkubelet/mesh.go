package virtualkubelet

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/containerd/containerd/log"
	"golang.org/x/crypto/curve25519"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

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

func computeWstunnelResourceNamesForSameNamespace(podName, podNamespace string) (resourceBaseName, namespace string) {
	// Sanitize namespace and pod name for DNS compliance
	sanitizedNamespace := sanitizeDNSName(podNamespace)
	sanitizedPodName := sanitizeDNSName(podName)

	// Use the original namespace
	namespace = sanitizedNamespace

	// Create a unique resource name to avoid conflicts in the same namespace
	// Add "wstunnel-" prefix to distinguish shadow pod resources
	resourceBaseName = "wstunnel-" + sanitizedPodName + "-" + sanitizedNamespace

	// Ensure resourceBaseName doesn't exceed 63 characters
	if len(resourceBaseName) > 63 {
		// Truncate while keeping some of both names
		maxPodNameLen := 28
		maxNsLen := 28
		if len(sanitizedPodName) > maxPodNameLen {
			sanitizedPodName = sanitizedPodName[:maxPodNameLen]
		}
		if len(sanitizedNamespace) > maxNsLen {
			sanitizedNamespace = sanitizedNamespace[:maxNsLen]
		}
		resourceBaseName = "wstunnel-" + sanitizedPodName + "-" + sanitizedNamespace
		resourceBaseName = strings.TrimRight(resourceBaseName, "-")
	}

	// Additional check for total length after combining with namespace
	ingressFirstLabel := fmt.Sprintf("%s-%s", resourceBaseName, namespace)
	if len(ingressFirstLabel) > 63 {
		// If combined length exceeds 63, we need to truncate
		maxNameLen := 31
		maxNsLen := 31

		truncatedName := resourceBaseName
		if len(truncatedName) > maxNameLen {
			truncatedName = truncatedName[:maxNameLen]
			truncatedName = strings.TrimRight(truncatedName, "-")
		}

		truncatedNs := namespace
		if len(truncatedNs) > maxNsLen {
			truncatedNs = truncatedNs[:maxNsLen]
			truncatedNs = strings.TrimRight(truncatedNs, "-")
		}

		resourceBaseName = truncatedName
		namespace = truncatedNs
	}

	return resourceBaseName, namespace
}

func computeWstunnelResourceNames(podName, podNamespace string) (resourceBaseName, wstunnelNamespace string) {
	// Sanitize namespace and pod name for DNS compliance
	sanitizedNamespace := sanitizeDNSName(podNamespace)
	sanitizedPodName := sanitizeDNSName(podName)

	wstunnelNamespace = sanitizedNamespace + "-wstunnel"
	// Ensure wstunnelNamespace is valid (max 63 chars for namespace)
	if len(wstunnelNamespace) > 63 {
		wstunnelNamespace = sanitizedNamespace[:min(54, len(sanitizedNamespace))] + "-wstunnel"
	}

	resourceBaseName = sanitizedPodName + "-" + sanitizedNamespace
	// Ensure resourceBaseName doesn't exceed 63 characters
	if len(resourceBaseName) > 63 {
		// Truncate while keeping some of both names
		maxPodNameLen := 31
		maxNsLen := 31
		if len(sanitizedPodName) > maxPodNameLen {
			sanitizedPodName = sanitizedPodName[:maxPodNameLen]
		}
		if len(sanitizedNamespace) > maxNsLen {
			sanitizedNamespace = sanitizedNamespace[:maxNsLen]
		}
		resourceBaseName = sanitizedPodName + "-" + sanitizedNamespace
		resourceBaseName = strings.TrimRight(resourceBaseName, "-")
	}

	ingressFirstLabel := fmt.Sprintf("%s-%s", resourceBaseName, wstunnelNamespace)
	if len(ingressFirstLabel) > 63 {
		// If combined length exceeds 63, we need to truncate
		// Strategy: keep both parts but truncate proportionally
		maxNameLen := 31
		maxNsLen := 31

		truncatedName := resourceBaseName
		if len(truncatedName) > maxNameLen {
			truncatedName = truncatedName[:maxNameLen]
			truncatedName = strings.TrimRight(truncatedName, "-")
		}

		truncatedNs := wstunnelNamespace
		if len(truncatedNs) > maxNsLen {
			truncatedNs = truncatedNs[:maxNsLen]
			truncatedNs = strings.TrimRight(truncatedNs, "-")
		}

		resourceBaseName = truncatedName
		wstunnelNamespace = truncatedNs
	}

	return resourceBaseName, wstunnelNamespace
}

func generateWGKeypair() (string, string, error) {
	// 32 random bytes -> clamp per X25519 rules -> public = X25519(priv, basepoint)
	privRaw := make([]byte, 32)
	if _, err := rand.Read(privRaw); err != nil {
		return "", "", fmt.Errorf("rand: %w", err)
	}
	// clamp private (per RFC7748)
	privRaw[0] &= 248
	privRaw[31] &= 127
	privRaw[31] |= 64

	pubRaw, err := curve25519.X25519(privRaw, curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("x25519: %w", err)
	}
	priv := base64.StdEncoding.EncodeToString(privRaw)
	pub := base64.StdEncoding.EncodeToString(pubRaw)
	return priv, pub, nil
}

// deriveWGPublicKey takes a base64 private key and returns base64 public key
func deriveWGPublicKey(privB64 string) (string, error) {
	privRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privB64))
	if err != nil {
		return "", fmt.Errorf("decode priv: %w", err)
	}
	if len(privRaw) != 32 {
		return "", fmt.Errorf("invalid private key length: %d", len(privRaw))
	}
	// Ensure clamping (ok to re-clamp)
	privRaw[0] &= 248
	privRaw[31] &= 127
	privRaw[31] |= 64

	pubRaw, err := curve25519.X25519(privRaw, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("x25519: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pubRaw), nil
}

// addWstunnelClientAnnotation adds the wstunnel client command annotation to the original pod
func (p *Provider) addWstunnelClientAnnotation(ctx context.Context, pod *v1.Pod, td *WstunnelTemplateData) error {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	// Construct and sanitize the full ingress endpoint
	ingressEndpoint := fmt.Sprintf("%s-%s.%s", td.Name, td.Namespace, td.WildcardDNS)
	ingressEndpoint = sanitizeFullDNSName(ingressEndpoint)
	log.G(ctx).Infof("Sanitized ingress endpoint: %s", ingressEndpoint)
	if td.WildcardDNS == "" {
		ingressEndpoint = td.Name
	}

	// Check if FullMesh mode is enabled
	if p.config.Network.FullMesh {
		log.G(ctx).Infof("FullMesh mode enabled, generating pre-exec script for pod %s/%s", pod.Namespace, pod.Name)

		// Generate full mesh script
		script, err := p.generateFullMeshScript(ctx, td, ingressEndpoint, string(pod.UID))
		if err != nil {
			return fmt.Errorf("failed to generate full mesh script: %w", err)
		}
		pod.Annotations["slurm-job.vk.io/pre-exec"] = script + pod.Annotations["slurm-job.vk.io/pre-exec"]
		log.G(ctx).Infof("Added full mesh pre-exec script to pod %s/%s", pod.Namespace, pod.Name)

		clientPriv := "<CLIENT_PRIVATE_KEY>"
		if strings.TrimSpace(td.ClientPrivateKey) != "" {
			clientPriv = td.ClientPrivateKey
		}
		serverPub, err := deriveWGPublicKey(td.WGPrivateKey)
		if err != nil {
			log.G(ctx).Errorf("Failed to derive server public key: %v", err)
			serverPub = "<SERVER_PUBLIC_KEY>"
		}

		wgSnippet := fmt.Sprintf(`
[Interface]
Address = 10.7.0.2/32
PrivateKey = %s
DNS = 1.1.1.1
MTU = %d

[Peer]
PublicKey = %s
AllowedIPs = 10.7.0.1/32, 10.0.0.0/8
Endpoint = 127.0.0.1:51821
PersistentKeepalive = %d
		`, clientPriv, td.WGMTU, serverPub, td.KeepaliveSecs)

		pod.Annotations["interlink.eu/wireguard-client-snippet"] = wgSnippet

	} else {
		var rOptions []string
		for _, port := range td.ExposedPorts {
			if strings.ToUpper(port.Protocol) == "UDP" {
				continue
			}
			rOptions = append(rOptions, fmt.Sprintf("-R tcp://0.0.0.0:%d:localhost:%d", port.Port, port.Port))
		}

		wstunnelCommandTemplate := p.config.Network.WstunnelCommand
		if wstunnelCommandTemplate == "" {
			wstunnelCommandTemplate = DefaultWstunnelCommand
		}

		log.G(ctx).Infof("Default ws tunnel command is: %s", wstunnelCommandTemplate)

		mainCmd := fmt.Sprintf(
			wstunnelCommandTemplate,
			td.RandomPassword,
			strings.Join(rOptions, " "),
			ingressEndpoint,
		)

		pod.Annotations["interlink.eu/wstunnel-client-commands"] = mainCmd

	}

	// Patch the pod on Kubernetes
	patchData := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": pod.Annotations,
		},
	}

	patchBytes, err := json.Marshal(patchData)
	if err != nil {
		log.G(ctx).Errorf("Failed to marshal patch data: %v", err)
		return err
	}

	_, err = p.clientSet.CoreV1().Pods(pod.Namespace).Patch(
		ctx,
		pod.Name,
		k8stypes.StrategicMergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if err != nil {
		log.G(ctx).Errorf("Failed to patch pod annotations on Kubernetes: %v", err)
	} else {
		log.G(ctx).Infof("Successfully patched pod annotations on Kubernetes for %s/%s", pod.Namespace, pod.Name)
	}

	return nil
}

func (p *Provider) generateFullMeshScript(ctx context.Context, td *WstunnelTemplateData, ingressEndpoint string, podUID string) (string, error) {

	serverPub, err := deriveWGPublicKey(td.WGPrivateKey)
	if err != nil {
		return "", fmt.Errorf("failed to derive server public key: %w", err)
	}

	clientPriv := td.ClientPrivateKey
	if clientPriv == "" {
		return "", fmt.Errorf("client private key not generated")
	}

	log.G(ctx).Infof("Generating full mesh script for pod UID %s", podUID)

	// Generate random interface name
	wgInterfaceName := fmt.Sprintf("wg%s", podUID[:13])

	// Set default URLs if not configured
	WSTunnelExecutableURL := p.config.Network.WSTunnelExecutableURL
	if WSTunnelExecutableURL == "" {
		WSTunnelExecutableURL = "https://github.com/interlink-hq/interlink-artifacts/raw/main/wstunnel/v10.4.4/linux-amd64/wstunnel"
	}

	wireguardGoURL := p.config.Network.WireguardGoURL
	if wireguardGoURL == "" {
		wireguardGoURL = "https://github.com/interlink-hq/interlink-artifacts/raw/main/wireguard-go/v0.0.20201118/linux-amd64/wireguard-go"
	}
	wgToolURL := p.config.Network.WgToolURL
	if wgToolURL == "" {
		wgToolURL = "https://github.com/interlink-hq/interlink-artifacts/raw/main/wgtools/v1.0.20210914/linux-amd64/wg"
	}
	slirp4netnsURL := p.config.Network.Slirp4netnsURL
	if slirp4netnsURL == "" {
		slirp4netnsURL = "https://github.com/interlink-hq/interlink-artifacts/raw/main/slirp4netns/v1.2.3/linux-amd64/slirp4netns"
	}

	// Get network CIDRs
	serviceCIDR := p.config.Network.ServiceCIDR
	if serviceCIDR == "" {
		serviceCIDR = "10.105.0.0/16" // default
	}
	podCIDRCluster := p.config.Network.PodCIDRCluster
	if podCIDRCluster == "" {
		podCIDRCluster = "10.244.0.0/16" // default
	}
	dnsServiceIP := p.config.Network.DNSServiceIP
	if dnsServiceIP == "" {
		dnsServiceIP = "10.244.0.99" // default, usually kube-dns
	}

	// Get unshare mode from config
	unshareMode := p.config.Network.UnshareMode
	if unshareMode == "" {
		unshareMode = "auto" // default to auto-detection
	}

	// Generate WireGuard config with dynamic interface name
	wgConfig := fmt.Sprintf(`[Interface]
PrivateKey = %s

[Peer]
PublicKey = %s
AllowedIPs = 10.7.0.1/32,10.0.0.0/8,%s,%s
Endpoint = 127.0.0.1:51821
PersistentKeepalive = %d
`, clientPriv, serverPub, podCIDRCluster, serviceCIDR, td.KeepaliveSecs)

	// Load template content - try custom path first, then fall back to embedded
	var templateContent string

	// Try to load from custom path first
	if p.config.Network.MeshScriptTemplatePath != "" {
		content, err := os.ReadFile(p.config.Network.MeshScriptTemplatePath)
		if err != nil {
			log.G(ctx).Warningf("Failed to read custom mesh script template from %s: %v, using default", p.config.Network.MeshScriptTemplatePath, err)
		} else {
			templateContent = string(content)
			log.G(ctx).Infof("Using custom mesh script template from %s", p.config.Network.MeshScriptTemplatePath)
		}
	}

	// Fall back to embedded template if no custom template was loaded
	if templateContent == "" {
		tmplContent, err := meshScriptTemplate.ReadFile("templates/mesh.sh")
		if err != nil {
			return "", fmt.Errorf("failed to read embedded mesh script template: %w", err)
		}
		templateContent = string(tmplContent)
		log.G(ctx).Info("Using embedded mesh script template")
	}

	// Parse the template
	tmpl, err := template.New("mesh").Parse(templateContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse mesh script template: %w", err)
	}

	// Prepare template data
	data := MeshScriptTemplateData{
		WGInterfaceName:       wgInterfaceName,
		WSTunnelExecutableURL: WSTunnelExecutableURL,
		WireguardGoURL:        wireguardGoURL,
		WgToolURL:             wgToolURL,
		Slirp4netnsURL:        slirp4netnsURL,
		WGConfig:              wgConfig,
		DNSServiceIP:          dnsServiceIP,
		RandomPassword:        td.RandomPassword,
		IngressEndpoint:       ingressEndpoint,
		WGMTU:                 td.WGMTU,
		PodCIDRCluster:        podCIDRCluster,
		ServiceCIDR:           serviceCIDR,
		UnshareMode:           unshareMode,
	}

	// Execute the template
	var scriptBuf bytes.Buffer
	if err := tmpl.Execute(&scriptBuf, data); err != nil {
		return "", fmt.Errorf("failed to execute mesh script template: %w", err)
	}

	return scriptBuf.String(), nil
}
