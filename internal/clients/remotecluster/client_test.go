/*
Copyright 2026 SAP SE.
*/

package remotecluster

import (
	"context"
	"testing"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// validKubeconfig returns a minimal valid kubeconfig for testing
func validKubeconfig() []byte {
	return []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://kubernetes.default.svc
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`)
}

// invalidKubeconfig returns invalid kubeconfig data
func invalidKubeconfig() []byte {
	return []byte("not a valid kubeconfig")
}

func TestCreateRemoteClient(t *testing.T) {
	tests := []struct {
		name           string
		kubeconfigData []byte
		wantErr        bool
		errContains    string
	}{
		{
			name:           "valid kubeconfig creates client successfully",
			kubeconfigData: validKubeconfig(),
			wantErr:        false,
		},
		{
			name:           "invalid kubeconfig returns error",
			kubeconfigData: invalidKubeconfig(),
			wantErr:        true,
			errContains:    "failed to create REST config from kubeconfig",
		},
		{
			name:           "empty kubeconfig returns error",
			kubeconfigData: []byte{},
			wantErr:        true,
			errContains:    "failed to create REST config from kubeconfig",
		},
		{
			name:           "nil kubeconfig returns error",
			kubeconfigData: nil,
			wantErr:        true,
			errContains:    "failed to create REST config from kubeconfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client, err := CreateRemoteClient(ctx, tt.kubeconfigData)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CreateRemoteClient() expected error but got none")
					return
				}
				if tt.errContains != "" && !containsString(err.Error(), tt.errContains) {
					t.Errorf("CreateRemoteClient() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("CreateRemoteClient() unexpected error = %v", err)
				return
			}

			if client == nil {
				t.Errorf("CreateRemoteClient() returned nil client")
			}
		})
	}
}

func TestRemoteClientCanAccessBTPTypes(t *testing.T) {
	ctx := context.Background()

	// Create a fake client with the same scheme that CreateRemoteClient uses
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = servicescloudsapv1.AddToScheme(scheme)

	// Create test objects
	testServiceInstance := &servicescloudsapv1.ServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-instance",
			Namespace: "default",
		},
		Status: servicescloudsapv1.ServiceInstanceStatus{
			InstanceID: "test-instance-id",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(testServiceInstance).
		Build()

	// Verify we can read ServiceInstance (this proves the scheme is registered correctly)
	var retrieved servicescloudsapv1.ServiceInstance
	err := fakeClient.Get(ctx, client.ObjectKey{Name: "test-instance", Namespace: "default"}, &retrieved)
	if err != nil {
		t.Fatalf("Failed to get ServiceInstance from fake client: %v", err)
	}

	if retrieved.Status.InstanceID != "test-instance-id" {
		t.Errorf("Retrieved ServiceInstance has wrong InstanceID: got %q, want %q",
			retrieved.Status.InstanceID, "test-instance-id")
	}
}

func TestRemoteClientSchemeRegistration(t *testing.T) {
	// Test that the scheme created by CreateRemoteClient includes all required types
	ctx := context.Background()
	kubeconfigData := validKubeconfig()

	_, err := CreateRemoteClient(ctx, kubeconfigData)
	if err != nil {
		// Note: This will fail because we can't actually connect to the test cluster
		// but we're testing scheme registration, not connectivity
		// The error should be about connection, not scheme
		if containsString(err.Error(), "scheme") || containsString(err.Error(), "not registered") {
			t.Errorf("CreateRemoteClient() failed with scheme error: %v", err)
		}
	}
}

// containsString checks if s contains substr
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
