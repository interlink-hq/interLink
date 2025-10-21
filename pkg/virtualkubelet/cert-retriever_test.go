package virtualkubelet

import (
	"context"
	"fmt"
	"testing"
	"time"

	certificates "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCleanupOldCSRs(t *testing.T) {
	ctx := context.Background()
	nodeName := "test-node"
	expectedUsername := fmt.Sprintf("system:node:%s", nodeName)
	nodeSignerName := getNodeSignerName(nodeName)
	otherNodeSignerName := getNodeSignerName("other-node")

	tests := []struct {
		name            string
		existingCSRs    []certificates.CertificateSigningRequest
		expectedDeletes int
		description     string
	}{
		{
			name: "cleanup approved and issued CSR",
			existingCSRs: []certificates.CertificateSigningRequest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-test-1",
						CreationTimestamp: metav1.Now(),
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   expectedUsername,
						SignerName: nodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{
							{
								Type:   certificates.CertificateApproved,
								Status: "True",
							},
						},
						Certificate: []byte("fake-cert"),
					},
				},
			},
			expectedDeletes: 1,
			description:     "Should delete approved and issued CSR",
		},
		{
			name: "cleanup denied CSR",
			existingCSRs: []certificates.CertificateSigningRequest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-test-2",
						CreationTimestamp: metav1.Now(),
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   expectedUsername,
						SignerName: nodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{
							{
								Type:   certificates.CertificateDenied,
								Status: "True",
							},
						},
					},
				},
			},
			expectedDeletes: 1,
			description:     "Should delete denied CSR",
		},
		{
			name: "cleanup stale pending CSR",
			existingCSRs: []certificates.CertificateSigningRequest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-test-3",
						CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   expectedUsername,
						SignerName: nodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{},
					},
				},
			},
			expectedDeletes: 1,
			description:     "Should delete pending CSR older than 5 minutes",
		},
		{
			name: "cleanup all CSRs with node signer",
			existingCSRs: []certificates.CertificateSigningRequest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-test-4",
						CreationTimestamp: metav1.Now(),
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   expectedUsername,
						SignerName: nodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{},
					},
				},
			},
			expectedDeletes: 1,
			description:     "Should delete all CSRs with matching node signer",
		},
		{
			name: "ignore CSRs from other nodes",
			existingCSRs: []certificates.CertificateSigningRequest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-other-node",
						CreationTimestamp: metav1.Now(),
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   "system:node:other-node",
						SignerName: otherNodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{
							{
								Type:   certificates.CertificateApproved,
								Status: "True",
							},
						},
						Certificate: []byte("fake-cert"),
					},
				},
			},
			expectedDeletes: 0,
			description:     "Should not delete CSRs from other nodes",
		},
		{
			name: "cleanup multiple CSRs",
			existingCSRs: []certificates.CertificateSigningRequest{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-test-5",
						CreationTimestamp: metav1.Now(),
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   expectedUsername,
						SignerName: nodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{
							{
								Type:   certificates.CertificateApproved,
								Status: "True",
							},
						},
						Certificate: []byte("fake-cert"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-test-6",
						CreationTimestamp: metav1.Now(),
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   expectedUsername,
						SignerName: nodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{
							{
								Type:   certificates.CertificateDenied,
								Status: "True",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "csr-test-7",
						CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
					},
					Spec: certificates.CertificateSigningRequestSpec{
						Username:   expectedUsername,
						SignerName: nodeSignerName,
					},
					Status: certificates.CertificateSigningRequestStatus{
						Conditions: []certificates.CertificateSigningRequestCondition{},
					},
				},
			},
			expectedDeletes: 3,
			description:     "Should delete multiple old CSRs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with existing CSRs
			kubeClient := fake.NewSimpleClientset()
			for _, csr := range tt.existingCSRs {
				_, err := kubeClient.CertificatesV1().CertificateSigningRequests().Create(ctx, &csr, metav1.CreateOptions{})
				if err != nil {
					t.Fatalf("Failed to create test CSR: %v", err)
				}
			}

			// Run cleanup
			err := cleanupOldCSRs(ctx, kubeClient, nodeName)
			if err != nil {
				t.Errorf("cleanupOldCSRs returned error: %v", err)
			}

			// Verify the expected number of CSRs were deleted
			remainingCSRs, err := kubeClient.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("Failed to list CSRs: %v", err)
			}

			actualDeletes := len(tt.existingCSRs) - len(remainingCSRs.Items)
			if actualDeletes != tt.expectedDeletes {
				t.Errorf("%s: expected %d deletions, got %d", tt.description, tt.expectedDeletes, actualDeletes)
			}
		})
	}
}

func TestCleanupOldCSRs_EmptyList(t *testing.T) {
	ctx := context.Background()
	nodeName := "test-node"
	kubeClient := fake.NewSimpleClientset()

	// Should not error when there are no CSRs
	err := cleanupOldCSRs(ctx, kubeClient, nodeName)
	if err != nil {
		t.Errorf("cleanupOldCSRs should not error with empty CSR list: %v", err)
	}
}
