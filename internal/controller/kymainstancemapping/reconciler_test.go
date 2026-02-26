/*
Copyright 2026 SAP SE.
*/

package kymainstancemapping

import (
	"context"
	"encoding/json"
	"testing"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud"
)

// stringPtr returns a pointer to the given string value
func stringPtr(s string) *string {
	return &s
}

// validKubeconfig returns a minimal valid kubeconfig for testing
func validKubeconfig() string {
	return `
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
`
}

func TestConnector_Connect(t *testing.T) {
	tests := []struct {
		name    string
		objects []client.Object
		cr      *v1alpha1.KymaInstanceMapping
		wantErr bool
		errMsg  string
	}{
		{
			name: "successfully connects with valid resources",
			cr: &v1alpha1.KymaInstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mapping",
				},
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ResourceSpec: xpv1.ResourceSpec{
						ProviderConfigReference: &xpv1.Reference{Name: "default"},
					},
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						KymaConnectionRef: &v1alpha1.KymaConnectionReference{
							SecretRef: v1alpha1.SecretReference{
								Name:      "kyma-kubeconfig",
								Namespace: "default",
							},
							KubeconfigKey: "kubeconfig",
						},
						ServiceInstanceRef: v1alpha1.ResourceReference{
							Name:      "hana-instance",
							Namespace: "default",
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      "admin-binding",
							Namespace: "default",
						},
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			objects: []client.Object{
				&apisv1alpha1.ProviderConfig{
					ObjectMeta: metav1.ObjectMeta{Name: "default"},
					Spec: apisv1alpha1.ProviderConfigSpec{
						Credentials: apisv1alpha1.ProviderCredentials{
							Source: xpv1.CredentialsSourceSecret,
							ConnectionSecretRef: &xpv1.SecretReference{
								Name:      "provider-creds",
								Namespace: "default",
							},
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kyma-kubeconfig",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"kubeconfig": []byte(validKubeconfig()),
					},
				},
				// Note: We can't actually test remote cluster access in unit tests
				// The remote client creation will fail, so we expect an error
			},
			wantErr: true,
			errMsg:  "cannot extract data from Kyma cluster",
		},
		{
			name: "fails when kubeconfig secret not found",
			cr: &v1alpha1.KymaInstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mapping",
				},
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ResourceSpec: xpv1.ResourceSpec{
						ProviderConfigReference: &xpv1.Reference{Name: "default"},
					},
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						KymaConnectionRef: &v1alpha1.KymaConnectionReference{
							SecretRef: v1alpha1.SecretReference{
								Name:      "missing-kubeconfig",
								Namespace: "default",
							},
						},
						ServiceInstanceRef: v1alpha1.ResourceReference{
							Name:      "hana-instance",
							Namespace: "default",
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      "admin-binding",
							Namespace: "default",
						},
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			objects: []client.Object{
				&apisv1alpha1.ProviderConfig{
					ObjectMeta: metav1.ObjectMeta{Name: "default"},
				},
			},
			wantErr: true,
			errMsg:  "cannot get kubeconfig secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = apisv1alpha1.SchemeBuilder.AddToScheme(scheme)
			_ = v1alpha1.SchemeBuilder.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			_ = servicescloudsapv1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			c := &connector{
				kube:  fakeClient,
				usage: &mockTracker{},
				log:   logging.NewNopLogger(),
			}

			_, err := c.Connect(context.Background(), tt.cr)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Connect() expected error but got none")
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("Connect() error = %v, want error containing %q", err, tt.errMsg)
				}
				return
			}

			if err != nil {
				t.Errorf("Connect() unexpected error = %v", err)
			}
		})
	}
}

// mockTracker is a mock implementation of resource.Tracker
type mockTracker struct{}

func (m *mockTracker) Track(_ context.Context, _ resource.Managed) error {
	return nil
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestExternal_Observe(t *testing.T) {
	tests := []struct {
		name           string
		cr             *v1alpha1.KymaInstanceMapping
		existingIM     *v1alpha1.InstanceMapping
		want           bool // want ResourceExists
		wantErr        bool
	}{
		{
			name: "child InstanceMapping exists and is ready",
			cr: &v1alpha1.KymaInstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mapping",
					UID:  "test-uid",
				},
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			existingIM: &v1alpha1.InstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mapping-mapping",
				},
				Spec: v1alpha1.InstanceMappingSpec{
					ForProvider: v1alpha1.InstanceMappingParameters{
						ServiceInstanceID: "test-instance-id",
						Platform:          "kubernetes",
						PrimaryID:         "test-cluster-id",
						SecondaryID:       stringPtr("target-ns"),
					},
				},
				Status: v1alpha1.InstanceMappingStatus{
					ResourceStatus: xpv1.ResourceStatus{
						ConditionedStatus: xpv1.ConditionedStatus{
							Conditions: []xpv1.Condition{
								{Type: xpv1.TypeReady, Status: corev1.ConditionTrue},
								{Type: xpv1.TypeSynced, Status: corev1.ConditionTrue},
							},
						},
					},
					AtProvider: v1alpha1.InstanceMappingObservation{
						MappingExists: true,
					},
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "child InstanceMapping does not exist",
			cr: &v1alpha1.KymaInstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mapping",
					UID:  "test-uid",
				},
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			existingIM: nil,
			want:       false,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.SchemeBuilder.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.existingIM != nil {
				builder = builder.WithObjects(tt.existingIM)
			}
			fakeClient := builder.Build()

			e := &external{
				managementClient: fakeClient,
				clusterClient:    nil,
				kymaData: &kymaExtractedData{
					serviceInstanceID: "test-instance-id",
					clusterID:         "test-cluster-id",
				},
				log: logging.NewNopLogger(),
			}

			obs, err := e.Observe(context.Background(), tt.cr)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Observe() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Observe() unexpected error = %v", err)
				return
			}

			if obs.ResourceExists != tt.want {
				t.Errorf("Observe() ResourceExists = %v, want %v", obs.ResourceExists, tt.want)
			}

			// Verify status is updated when InstanceMapping exists
			if tt.existingIM != nil && tt.cr.Status.AtProvider.ChildResources != nil {
				if tt.cr.Status.AtProvider.ChildResources.InstanceMappingName != tt.existingIM.Name {
					t.Errorf("ChildResources.InstanceMappingName = %v, want %v",
						tt.cr.Status.AtProvider.ChildResources.InstanceMappingName, tt.existingIM.Name)
				}
			}
		})
	}
}

func TestExternal_Create(t *testing.T) {
	tests := []struct {
		name    string
		cr      *v1alpha1.KymaInstanceMapping
		wantErr bool
	}{
		{
			name: "successfully creates child resources",
			cr: &v1alpha1.KymaInstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-mapping",
					UID:  "test-uid",
				},
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						TargetNamespace:            stringPtr("target-ns"),
						IsDefault:                  false,
						CredentialsSecretNamespace: "crossplane-system",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = v1alpha1.SchemeBuilder.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			e := &external{
				managementClient: fakeClient,
				clusterClient:    nil,
				kymaData: &kymaExtractedData{
					serviceInstanceID: "test-instance-id",
					clusterID:         "test-cluster-id",
					adminAPICredentials: hanacloud.AdminAPICredentials{
						BaseURL: "api.hana.example.com",
						UAA: hanacloud.UAAConfig{
							URL:          "https://uaa.example.com",
							ClientID:     "test-client",
							ClientSecret: "test-secret",
						},
					},
				},
				log: logging.NewNopLogger(),
			}

			_, err := e.Create(context.Background(), tt.cr)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Create() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Create() unexpected error = %v", err)
				return
			}

			// Verify Secret was created
			secret := &corev1.Secret{}
			err = fakeClient.Get(context.Background(), client.ObjectKey{
				Name:      tt.cr.Name + "-admin-creds",
				Namespace: "crossplane-system",
			}, secret)
			if err != nil {
				t.Errorf("Create() failed to create credentials secret: %v", err)
			}

			// Verify InstanceMapping was created
			im := &v1alpha1.InstanceMapping{}
			err = fakeClient.Get(context.Background(), client.ObjectKey{
				Name: tt.cr.Name + "-mapping",
			}, im)
			if err != nil {
				t.Errorf("Create() failed to create InstanceMapping: %v", err)
			}

			// Verify InstanceMapping spec
			if im.Spec.ForProvider.ServiceInstanceID != "test-instance-id" {
				t.Errorf("InstanceMapping.ServiceInstanceID = %v, want %v",
					im.Spec.ForProvider.ServiceInstanceID, "test-instance-id")
			}
			if im.Spec.ForProvider.PrimaryID != "test-cluster-id" {
				t.Errorf("InstanceMapping.PrimaryID = %v, want %v",
					im.Spec.ForProvider.PrimaryID, "test-cluster-id")
			}
		})
	}
}

func TestExtractKymaData(t *testing.T) {
	adminAPICreds := map[string]interface{}{
		"url": "https://hana-cloud-api.example.com",
		"uaa": map[string]interface{}{
			"clientid":     "test-client",
			"clientsecret": "test-secret",
			"url":          "https://uaa.example.com",
		},
	}
	adminAPIJSON, err := json.Marshal(adminAPICreds)
	if err != nil {
		t.Fatalf("Failed to marshal admin API credentials: %v", err)
	}

	tests := []struct {
		name        string
		objects     []client.Object
		cr          *v1alpha1.KymaInstanceMapping
		wantData    *kymaExtractedData
		wantErr     bool
		errContains string
	}{
		{
			name: "successfully extracts all data",
			objects: []client.Object{
				&servicescloudsapv1.ServiceInstance{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hana-instance",
						Namespace: "default",
					},
					Status: servicescloudsapv1.ServiceInstanceStatus{
						InstanceID: "test-instance-id",
						Conditions: []metav1.Condition{
							{
								Type:   "Ready",
								Status: metav1.ConditionTrue,
							},
						},
					},
				},
				&servicescloudsapv1.ServiceBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-binding",
						Namespace: "default",
					},
					Spec: servicescloudsapv1.ServiceBindingSpec{
						SecretName: "admin-secret",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-secret",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"baseurl": []byte("https://hana-cloud-api.example.com"),
						"uaa":     adminAPIJSON,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sap-btp-operator-config",
						Namespace: "kyma-system",
					},
					Data: map[string]string{
						"CLUSTER_ID": "test-cluster-id",
					},
				},
			},
			cr: &v1alpha1.KymaInstanceMapping{
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						ServiceInstanceRef: v1alpha1.ResourceReference{
							Name:      "hana-instance",
							Namespace: "default",
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      "admin-binding",
							Namespace: "default",
						},
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			wantData: &kymaExtractedData{
				serviceInstanceID:    "test-instance-id",
				clusterID:            "test-cluster-id",
				serviceInstanceName:  "hana-instance",
				serviceInstanceReady: true,
				adminAPICredentials:  hanacloud.AdminAPICredentials{},
			},
			wantErr: false,
		},
		{
			name:    "fails when ServiceInstance not found",
			objects: []client.Object{},
			cr: &v1alpha1.KymaInstanceMapping{
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						ServiceInstanceRef: v1alpha1.ResourceReference{
							Name:      "missing-instance",
							Namespace: "default",
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      "admin-binding",
							Namespace: "default",
						},
					},
				},
			},
			wantErr:     true,
			errContains: "cannot get ServiceInstance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = servicescloudsapv1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.objects...).
				Build()

			data, err := extractKymaData(context.Background(), fakeClient, tt.cr)

			if tt.wantErr {
				if err == nil {
					t.Errorf("extractKymaData() expected error but got none")
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("extractKymaData() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("extractKymaData() unexpected error = %v", err)
				return
			}

			if data == nil {
				t.Fatalf("extractKymaData() returned nil data")
			}

			// Compare basic fields
			if data.serviceInstanceID != tt.wantData.serviceInstanceID {
				t.Errorf("serviceInstanceID = %v, want %v", data.serviceInstanceID, tt.wantData.serviceInstanceID)
			}
			if data.clusterID != tt.wantData.clusterID {
				t.Errorf("clusterID = %v, want %v", data.clusterID, tt.wantData.clusterID)
			}
			if data.serviceInstanceName != tt.wantData.serviceInstanceName {
				t.Errorf("serviceInstanceName = %v, want %v", data.serviceInstanceName, tt.wantData.serviceInstanceName)
			}
			if data.serviceInstanceReady != tt.wantData.serviceInstanceReady {
				t.Errorf("serviceInstanceReady = %v, want %v", data.serviceInstanceReady, tt.wantData.serviceInstanceReady)
			}
		})
	}
}
