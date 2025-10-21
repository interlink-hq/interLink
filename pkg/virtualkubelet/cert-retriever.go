package virtualkubelet

import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"time"

	"github.com/containerd/containerd/log"
	certificates "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/certificate"
	// k8s.io/kubernetes/pkg/apis/certificates"
)

type Crtretriever func(*tls.ClientHelloInfo) (*tls.Certificate, error)

// getNodeSignerName returns a unique signer name for the given node
func getNodeSignerName(nodeName string) string {
	return fmt.Sprintf("interlink-project.dev/node-%s", nodeName)
}

// cleanupOldCSRs removes old or pending CSRs for this node to prevent accumulation
func cleanupOldCSRs(ctx context.Context, kubeClient kubernetes.Interface, nodeName string) error {
	nodeSignerName := getNodeSignerName(nodeName)

	// List all CSRs
	csrList, err := kubeClient.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.G(ctx).Warningf("Failed to list CSRs for cleanup: %v", err)
		return err
	}

	deletedCount := 0
	for _, csr := range csrList.Items {
		// Check if this CSR belongs to our node by matching the node-specific signer name
		if csr.Spec.SignerName != nodeSignerName {
			continue
		}

		// Delete old CSRs for this node
		reason := "virtual node CSR cleanup"

		log.G(ctx).Infof("Deleting old CSR %s for node %s (reason: %s)", csr.Name, nodeName, reason)
		err := kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, csr.Name, metav1.DeleteOptions{})
		if err != nil {
			log.G(ctx).Warningf("Failed to delete CSR %s: %v", csr.Name, err)
		} else {
			deletedCount++
		}
	}
	if deletedCount > 0 {
		log.G(ctx).Infof("Cleaned up %d old CSR(s) for node %s", deletedCount, nodeName)
	}

	return nil
}

// NewCertificateManager creates a certificate manager for the kubelet when retrieving a server certificate, or returns an error.
// This function is inspired by Liqo implementation:
// https://github.com/liqotech/liqo/blob/master/cmd/virtual-kubelet/root/http.go#L149
// The signer parameter is kept for backward compatibility but is now ignored in favor of node-specific signers.
func NewCertificateRetriever(kubeClient kubernetes.Interface, _ /* signer */, nodeName string, nodeIP net.IP) (Crtretriever, error) {
	const (
		vkCertsPath   = "/tmp/certs"
		vkCertsPrefix = "virtual-kubelet"
	)

	// Use node-specific signer name instead of the passed signer parameter
	nodeSignerName := getNodeSignerName(nodeName)

	// Clean up old CSRs before creating a new certificate manager
	ctx := context.Background()
	if err := cleanupOldCSRs(ctx, kubeClient, nodeName); err != nil {
		log.G(ctx).Warningf("CSR cleanup had errors but continuing: %v", err)
	}

	certificateStore, err := certificate.NewFileStore(vkCertsPrefix, vkCertsPath, vkCertsPath, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize server certificate store: %w", err)
	}

	getTemplate := func() *x509.CertificateRequest {
		return &x509.CertificateRequest{
			Subject: pkix.Name{
				CommonName:   fmt.Sprintf("system:node:%s", nodeName),
				Organization: []string{"system:nodes"},
			},
			IPAddresses: []net.IP{nodeIP},
		}
	}

	mgr, err := certificate.NewManager(&certificate.Config{
		ClientsetFn: func(_ *tls.Certificate) (kubernetes.Interface, error) {
			return kubeClient, nil
		},
		GetTemplate: getTemplate,
		SignerName:  nodeSignerName,
		Usages: []certificates.KeyUsage{
			// https://tools.ietf.org/html/rfc5280#section-4.2.1.3
			//
			// Digital signature allows the certificate to be used to verify
			// digital signatures used during TLS negotiation.
			certificates.UsageDigitalSignature,
			// KeyEncipherment allows the cert/key pair to be used to encrypt
			// keys, including the symmetric keys negotiated during TLS setup
			// and used for data transfer.
			certificates.UsageKeyEncipherment,
			// ServerAuth allows the cert to be used by a TLS server to
			// authenticate itself to a TLS client.
			certificates.UsageServerAuth,
		},
		CertificateStore: certificateStore,
		Name:             nodeName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize server certificate manager: %w", err)
	}

	mgr.Start()

	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert := mgr.Current()
		if cert == nil {
			return nil, fmt.Errorf("no serving certificate available")
		}
		return cert, nil
	}, nil
}

// newSelfSignedCertificateRetriever creates a new retriever for self-signed certificates.
func NewSelfSignedCertificateRetriever(nodeName string, nodeIP net.IP) Crtretriever {
	creator := func() (*tls.Certificate, time.Time, error) {
		expiration := time.Now().AddDate(1, 0, 0) // 1 year

		// Generate a new private key.
		publicKey, privateKey, err := ed25519.GenerateKey(cryptorand.Reader)
		if err != nil {
			return nil, expiration, fmt.Errorf("failed to generate a key pair: %w", err)
		}

		keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
		if err != nil {
			return nil, expiration, fmt.Errorf("failed to marshal the private key: %w", err)
		}

		// Generate the corresponding certificate.
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName:   fmt.Sprintf("system:node:%s", nodeName),
				Organization: []string{"intertwin.eu"},
			},
			IPAddresses:  []net.IP{nodeIP},
			SerialNumber: big.NewInt(rand.Int63()), //nolint:gosec // A weak random generator is sufficient.
			NotBefore:    time.Now(),
			NotAfter:     expiration,
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		}

		certBytes, err := x509.CreateCertificate(cryptorand.Reader, cert, cert, publicKey, privateKey)
		if err != nil {
			return nil, expiration, fmt.Errorf("failed to create the self-signed certificate: %w", err)
		}

		// Encode the resulting certificate and private key as a single object.
		output, err := tls.X509KeyPair(
			pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes}),
			pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}))
		if err != nil {
			return nil, expiration, fmt.Errorf("failed to create the X509 key pair: %w", err)
		}

		return &output, expiration, nil
	}

	// Cache the last generated cert, until it is not expired.
	var cert *tls.Certificate
	var expiration time.Time
	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		if cert == nil || expiration.Before(time.Now().AddDate(0, 0, 1)) {
			var err error
			cert, expiration, err = creator()
			if err != nil {
				return nil, err
			}
		}
		return cert, nil
	}
}
