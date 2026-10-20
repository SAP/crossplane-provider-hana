/*
Copyright 2026 SAP SE.
*/

package kymainstancemapping

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud"
)

const (
	testMappingName      = "test-mapping"
	defaultNamespace     = "default"
	testHANAInstanceName = "hana-instance"
	testAdminBindingName = "admin-binding"
	testInstanceID       = "test-instance-id"
	testClusterID        = "test-cluster-id"
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
					Name: testMappingName,
				},
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ClusterManagedResourceSpec: xpv2.ClusterManagedResourceSpec{
						ProviderConfigReference: &xpv2.Reference{Name: defaultNamespace},
					},
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						KymaConnectionRef: &v1alpha1.KymaConnectionReference{
							SecretRef: v1alpha1.SecretReference{
								Name:      "kyma-kubeconfig",
								Namespace: defaultNamespace,
							},
							KubeconfigKey: "kubeconfig",
						},
						ServiceInstanceRef: v1alpha1.ResourceReference{
							Name:      testHANAInstanceName,
							Namespace: defaultNamespace,
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      testAdminBindingName,
							Namespace: defaultNamespace,
						},
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			objects: []client.Object{
				&apisv1alpha1.ProviderConfig{
					ObjectMeta: metav1.ObjectMeta{Name: defaultNamespace},
					Spec: apisv1alpha1.ProviderConfigSpec{
						Credentials: apisv1alpha1.ProviderCredentials{
							Source: xpv2.CredentialsSourceSecret,
							ConnectionSecretRef: &xpv2.SecretReference{
								Name:      "provider-creds",
								Namespace: defaultNamespace,
							},
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kyma-kubeconfig",
						Namespace: defaultNamespace,
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
					Name: testMappingName,
				},
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ClusterManagedResourceSpec: xpv2.ClusterManagedResourceSpec{
						ProviderConfigReference: &xpv2.Reference{Name: defaultNamespace},
					},
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						KymaConnectionRef: &v1alpha1.KymaConnectionReference{
							SecretRef: v1alpha1.SecretReference{
								Name:      "missing-kubeconfig",
								Namespace: defaultNamespace,
							},
						},
						ServiceInstanceRef: v1alpha1.ResourceReference{
							Name:      testHANAInstanceName,
							Namespace: defaultNamespace,
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      testAdminBindingName,
							Namespace: defaultNamespace,
						},
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			objects: []client.Object{
				&apisv1alpha1.ProviderConfig{
					ObjectMeta: metav1.ObjectMeta{Name: defaultNamespace},
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

			c := &Connector{
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
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
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

// mockTracker is a mock implementation of resource.LegacyTracker.
type mockTracker struct{}

func (m *mockTracker) Track(_ context.Context, _ resource.LegacyManaged) error { //nolint:staticcheck // Legacy cluster-scoped resources are intentionally preserved.
	return nil
}

func TestExternal_Observe(t *testing.T) {
	tests := []struct {
		name       string
		cr         *v1alpha1.KymaInstanceMapping
		existingIM *v1alpha1.InstanceMapping
		want       bool // want ResourceExists
		wantErr    bool
	}{
		{
			name: "child InstanceMapping exists and is ready",
			cr: &v1alpha1.KymaInstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: testMappingName,
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
						ServiceInstanceID: testInstanceID,
						Platform:          "kubernetes",
						PrimaryID:         testClusterID,
						SecondaryID:       stringPtr("target-ns"),
					},
				},
				Status: v1alpha1.InstanceMappingStatus{
					ManagedResourceStatus: xpv2.ManagedResourceStatus{
						ConditionedStatus: xpv2.ConditionedStatus{
							Conditions: []xpv2.Condition{
								{Type: xpv2.TypeReady, Status: corev1.ConditionTrue},
								{Type: xpv2.TypeSynced, Status: corev1.ConditionTrue},
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
					Name: testMappingName,
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

			e := &External{
				managementClient: fakeClient,
				clusterClient:    nil,
				kymaData: &kymaExtractedData{
					serviceInstanceID: testInstanceID,
					clusterID:         testClusterID,
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
					Name: testMappingName,
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

			e := &External{
				managementClient: fakeClient,
				clusterClient:    nil,
				kymaData: &kymaExtractedData{
					serviceInstanceID: testInstanceID,
					clusterID:         testClusterID,
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
			if im.Spec.ForProvider.ServiceInstanceID != testInstanceID {
				t.Errorf("InstanceMapping.ServiceInstanceID = %v, want %v",
					im.Spec.ForProvider.ServiceInstanceID, testInstanceID)
			}
			if im.Spec.ForProvider.PrimaryID != testClusterID {
				t.Errorf("InstanceMapping.PrimaryID = %v, want %v",
					im.Spec.ForProvider.PrimaryID, testClusterID)
			}
		})
	}
}

func TestExtractKymaData(t *testing.T) {
	uaaConfig := map[string]interface{}{
		"url":          "https://uaa.example.com",
		"clientid":     "test-client",
		"clientsecret": "test-secret",
	}
	uaaJSON, err := json.Marshal(uaaConfig)
	if err != nil {
		t.Fatalf("Failed to marshal UAA config: %v", err)
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
						Name:      testHANAInstanceName,
						Namespace: defaultNamespace,
					},
					Status: servicescloudsapv1.ServiceInstanceStatus{
						InstanceID: testInstanceID,
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
						Name:      testAdminBindingName,
						Namespace: defaultNamespace,
					},
					Spec: servicescloudsapv1.ServiceBindingSpec{
						SecretName: "admin-secret",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "admin-secret",
						Namespace: defaultNamespace,
					},
					Data: map[string][]byte{
						"baseurl": []byte("https://hana-cloud-api.example.com"),
						"uaa":     uaaJSON,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sap-btp-operator-config",
						Namespace: "kyma-system",
					},
					Data: map[string]string{
						"CLUSTER_ID": testClusterID,
					},
				},
			},
			cr: &v1alpha1.KymaInstanceMapping{
				Spec: v1alpha1.KymaInstanceMappingSpec{
					ForProvider: v1alpha1.KymaInstanceMappingParameters{
						ServiceInstanceRef: v1alpha1.ResourceReference{
							Name:      testHANAInstanceName,
							Namespace: defaultNamespace,
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      testAdminBindingName,
							Namespace: defaultNamespace,
						},
						TargetNamespace: stringPtr("target-ns"),
					},
				},
			},
			wantData: &kymaExtractedData{
				serviceInstanceID:    testInstanceID,
				clusterID:            testClusterID,
				serviceInstanceName:  testHANAInstanceName,
				serviceInstanceReady: true,
				adminAPICredentials: hanacloud.AdminAPICredentials{
					BaseURL: "https://hana-cloud-api.example.com",
					UAA: hanacloud.UAAConfig{
						URL:          "https://uaa.example.com",
						ClientID:     "test-client",
						ClientSecret: "test-secret",
					},
				},
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
							Namespace: defaultNamespace,
						},
						AdminBindingRef: v1alpha1.ResourceReference{
							Name:      testAdminBindingName,
							Namespace: defaultNamespace,
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
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
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
			// Verify adminAPICredentials
			if data.adminAPICredentials.BaseURL != tt.wantData.adminAPICredentials.BaseURL {
				t.Errorf("adminAPICredentials.BaseURL = %v, want %v", data.adminAPICredentials.BaseURL, tt.wantData.adminAPICredentials.BaseURL)
			}
			if data.adminAPICredentials.UAA.URL != tt.wantData.adminAPICredentials.UAA.URL {
				t.Errorf("adminAPICredentials.UAA.URL = %v, want %v", data.adminAPICredentials.UAA.URL, tt.wantData.adminAPICredentials.UAA.URL)
			}
			if data.adminAPICredentials.UAA.ClientID != tt.wantData.adminAPICredentials.UAA.ClientID {
				t.Errorf("adminAPICredentials.UAA.ClientID = %v, want %v", data.adminAPICredentials.UAA.ClientID, tt.wantData.adminAPICredentials.UAA.ClientID)
			}
			if data.adminAPICredentials.UAA.ClientSecret != tt.wantData.adminAPICredentials.UAA.ClientSecret {
				t.Errorf("adminAPICredentials.UAA.ClientSecret = %v, want %v", data.adminAPICredentials.UAA.ClientSecret, tt.wantData.adminAPICredentials.UAA.ClientSecret)
			}
		})
	}
}
