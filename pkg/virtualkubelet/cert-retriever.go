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
	"sync"
	"time"

	"github.com/containerd/containerd/log"
	certificates "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/certificate"
	// k8s.io/kubernetes/pkg/apis/certificates"
)

type Crtretriever func(*tls.ClientHelloInfo) (*tls.Certificate, error)

// cleanupOldCSRs removes old or pending CSRs for this node to prevent accumulation
func cleanupOldCSRs(ctx context.Context, kubeClient kubernetes.Interface, signerName string, nodeName string) error {
	expectedCommonName := fmt.Sprintf("system:node:%s", nodeName)

	// List all CSRs
	csrList, err := kubeClient.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.G(ctx).Warningf("Failed to list CSRs for cleanup: %v", err)
		return err
	}

	deletedCount := 0
	for _, csr := range csrList.Items {
		// Check if this CSR belongs to our node by matching both signer name and Common Name
		if csr.Spec.SignerName != signerName {
			continue
		}

		// Parse the CSR to extract the Common Name
		block, _ := pem.Decode(csr.Spec.Request)
		if block == nil {
			log.G(ctx).Warningf("Failed to decode CSR %s: invalid PEM block", csr.Name)
			continue
		}

		parsedCSR, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil {
			log.G(ctx).Warningf("Failed to parse CSR %s: %v", csr.Name, err)
			continue
		}

		// Only delete CSRs that match our node's Common Name
		if parsedCSR.Subject.CommonName != expectedCommonName {
			continue
		}

		// Delete old CSRs for this node
		reason := "virtual node CSR cleanup"

		log.G(ctx).Infof("Deleting old CSR %s for node %s (reason: %s)", csr.Name, nodeName, reason)
		err = kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, csr.Name, metav1.DeleteOptions{})
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

// persistentCSRManager manages a single CSR and waits indefinitely for approval
type persistentCSRManager struct {
	kubeClient kubernetes.Interface
	signerName string
	nodeName   string
	nodeIP     net.IP
	certStore  certificate.Store

	mu   sync.RWMutex
	cert *tls.Certificate
	csrName string
}

// createAndSubmitCSR creates a new CSR and submits it to the API server
func (m *persistentCSRManager) createAndSubmitCSR(ctx context.Context) (string, []byte, error) {
	// Generate a new private key
	_, privateKey, err := ed25519.GenerateKey(cryptorand.Reader)
	if err != nil {
		return "", nil, fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Create CSR template
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("system:node:%s", m.nodeName),
			Organization: []string{"system:nodes"},
		},
		IPAddresses: []net.IP{m.nodeIP},
	}

	// Create the CSR
	csrDER, err := x509.CreateCertificateRequest(cryptorand.Reader, template, privateKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	// Marshal private key for storage
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return "", nil, fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})

	// Create CSR object
	csrObj := &certificates.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("vk-%s-", m.nodeName),
		},
		Spec: certificates.CertificateSigningRequestSpec{
			Request:    csrPEM,
			SignerName: m.signerName,
			Usages: []certificates.KeyUsage{
				certificates.UsageDigitalSignature,
				certificates.UsageKeyEncipherment,
				certificates.UsageServerAuth,
			},
		},
	}

	// Submit CSR to API server
	createdCSR, err := m.kubeClient.CertificatesV1().CertificateSigningRequests().Create(ctx, csrObj, metav1.CreateOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	log.G(ctx).Infof("Created CSR %s for node %s (will wait indefinitely for approval)", createdCSR.Name, m.nodeName)
	return createdCSR.Name, keyPEM, nil
}

// monitorCSR monitors the CSR and updates the certificate when approved
func (m *persistentCSRManager) monitorCSR(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	var csrName string
	var keyPEM []byte
	var err error

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// If we don't have a CSR yet, create one
			if csrName == "" {
				csrName, keyPEM, err = m.createAndSubmitCSR(ctx)
				if err != nil {
					log.G(ctx).Errorf("Failed to create CSR: %v (will retry)", err)
					csrName = "" // Reset to retry
					continue
				}
				m.mu.Lock()
				m.csrName = csrName
				m.mu.Unlock()
			}

			// Check if CSR is approved
			csr, err := m.kubeClient.CertificatesV1().CertificateSigningRequests().Get(ctx, csrName, metav1.GetOptions{})
			if err != nil {
				log.G(ctx).Warningf("Failed to get CSR %s: %v (will retry)", csrName, err)
				continue
			}

			// Check if CSR was denied
			for _, condition := range csr.Status.Conditions {
				if condition.Type == certificates.CertificateDenied {
					log.G(ctx).Errorf("CSR %s was denied: %s (will create new CSR)", csrName, condition.Message)
					// Delete the denied CSR
					_ = m.kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, csrName, metav1.DeleteOptions{})
					csrName = "" // Reset to create new CSR
					keyPEM = nil
					break
				}
			}

			// Check if certificate is available
			if len(csr.Status.Certificate) > 0 {
				log.G(ctx).Infof("CSR %s approved, certificate received", csrName)

				// Create tls.Certificate
				cert, err := tls.X509KeyPair(csr.Status.Certificate, keyPEM)
				if err != nil {
					log.G(ctx).Errorf("Failed to create X509 key pair: %v", err)
					continue
				}

				// Store certificate and key
				storedCert, err := m.certStore.Update(csr.Status.Certificate, keyPEM)
				if err != nil {
					log.G(ctx).Warningf("Failed to store certificate: %v", err)
				} else {
					// Use the stored certificate if available
					if storedCert != nil {
						cert = *storedCert
					}
				}

				m.mu.Lock()
				m.cert = &cert
				m.mu.Unlock()

				// Parse certificate to check expiration
				x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
				if err == nil {
					log.G(ctx).Infof("Certificate valid until %s", x509Cert.NotAfter.Format(time.RFC3339))

					// Calculate when to create new CSR (80% of lifetime)
					lifetime := x509Cert.NotAfter.Sub(x509Cert.NotBefore)
					renewAt := x509Cert.NotBefore.Add(time.Duration(float64(lifetime) * 0.8))
					timeUntilRenew := time.Until(renewAt)

					if timeUntilRenew > 0 {
						log.G(ctx).Infof("Will create new CSR in %s (at 80%% of certificate lifetime)", timeUntilRenew.Round(time.Minute))
						// Wait until renewal time
						timer := time.NewTimer(timeUntilRenew)
						select {
						case <-ctx.Done():
							timer.Stop()
							return
						case <-timer.C:
							log.G(ctx).Info("Certificate approaching expiration, creating new CSR")
							// Delete old CSR and reset to create new one
							_ = m.kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, csrName, metav1.DeleteOptions{})
							csrName = ""
							keyPEM = nil
						}
					} else {
						// Certificate already near expiration, create new CSR immediately
						log.G(ctx).Warning("Certificate already near expiration, creating new CSR")
						_ = m.kubeClient.CertificatesV1().CertificateSigningRequests().Delete(ctx, csrName, metav1.DeleteOptions{})
						csrName = ""
						keyPEM = nil
					}
				}
			}
		}
	}
}

// NewCertificateRetriever creates a certificate retriever that creates a single CSR and waits
// indefinitely for approval without any timeout. This implementation:
// - Creates ONE CSR on startup
// - Polls every 10 seconds checking if it's been approved (no 15-minute timeout)
// - Only creates a new CSR when the certificate is at 80% of its lifetime (near expiration)
// - Handles denied CSRs by creating a new one
func NewCertificateRetriever(kubeClient kubernetes.Interface, signer string, nodeName string, nodeIP net.IP) (Crtretriever, error) {
	const (
		vkCertsPath   = "/tmp/certs"
		vkCertsPrefix = "virtual-kubelet"
	)

	ctx := context.Background()

	// Clean up old CSRs before creating a new one
	if err := cleanupOldCSRs(ctx, kubeClient, signer, nodeName); err != nil {
		log.G(ctx).Warningf("CSR cleanup had errors but continuing: %v", err)
	}

	// Create certificate store
	certificateStore, err := certificate.NewFileStore(vkCertsPrefix, vkCertsPath, vkCertsPath, "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to initialize certificate store: %w", err)
	}

	// Check if we already have a valid certificate from a previous run
	existingCert, err := certificateStore.Current()
	if err == nil && existingCert != nil && len(existingCert.Certificate) > 0 {
		// Parse to check if still valid
		x509Cert, err := x509.ParseCertificate(existingCert.Certificate[0])
		if err == nil && time.Now().Before(x509Cert.NotAfter) {
			log.G(ctx).Infof("Using existing certificate valid until %s", x509Cert.NotAfter.Format(time.RFC3339))
			// Certificate is still valid, use it
			mgr := &persistentCSRManager{
				kubeClient: kubeClient,
				signerName: signer,
				nodeName:   nodeName,
				nodeIP:     nodeIP,
				certStore:  certificateStore,
				cert:       existingCert,
			}
			// Start monitoring for renewal
			go mgr.monitorCSR(context.Background())
			return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				mgr.mu.RLock()
				defer mgr.mu.RUnlock()
				if mgr.cert == nil {
					return nil, fmt.Errorf("no certificate available yet - CSR pending approval")
				}
				return mgr.cert, nil
			}, nil
		}
	}

	// No existing certificate or it's expired, create new manager
	log.G(ctx).Info("No existing certificate found, will create CSR and wait for approval")
	mgr := &persistentCSRManager{
		kubeClient: kubeClient,
		signerName: signer,
		nodeName:   nodeName,
		nodeIP:     nodeIP,
		certStore:  certificateStore,
	}

	// Start CSR monitoring in background
	go mgr.monitorCSR(context.Background())

	return func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		mgr.mu.RLock()
		defer mgr.mu.RUnlock()
		if mgr.cert == nil {
			return nil, fmt.Errorf("no certificate available yet - CSR pending approval")
		}
		return mgr.cert, nil
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
