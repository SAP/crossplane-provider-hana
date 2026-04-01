//go:build e2e

/*
Copyright 2026 SAP SE.
*/

package e2e

import (
	"context"
	"encoding/json"
	"testing"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	inventoryv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
)

const (
	testNamespace = "default"
	kymaSystemNS  = "kyma-system"
	crossplaneNS  = "crossplane-system"
)

// createNamespaceIfNotExists creates a namespace if it doesn't exist.
func createNamespaceIfNotExists(t *testing.T, ctx context.Context, c client.Client, name string) {
	t.Helper()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
	err := c.Create(ctx, ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err, "failed to create namespace %s", name)
	}
}

// MockKymaResources holds the mock resources created for testing.
type MockKymaResources struct {
	ServiceInstance *servicescloudsapv1.ServiceInstance
	ServiceBinding  *servicescloudsapv1.ServiceBinding
	AdminSecret     *corev1.Secret
	ConfigMap       *corev1.ConfigMap
}

// createMockKymaResources creates mock BTP Service Operator resources in the cluster.
func createMockKymaResources(t *testing.T, ctx context.Context, c client.Client, name, instanceID, clusterID string) *MockKymaResources {
	t.Helper()
	// Ensure namespaces exist
	createNamespaceIfNotExists(t, ctx, c, testNamespace)
	createNamespaceIfNotExists(t, ctx, c, kymaSystemNS)
	createNamespaceIfNotExists(t, ctx, c, crossplaneNS)

	// 1. Create ServiceInstance
	si := &servicescloudsapv1.ServiceInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-instance",
			Namespace: testNamespace,
		},
		Spec: servicescloudsapv1.ServiceInstanceSpec{
			ServiceOfferingName: "hana-cloud",
			ServicePlanName:     "hana",
		},
	}
	require.NoError(t, c.Create(ctx, si))

	// Update status with instanceID (simulating BTP operator)
	si.Status.InstanceID = instanceID
	si.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
			Reason:             "Provisioned",
		},
	}
	require.NoError(t, c.Status().Update(ctx, si))

	// 2. Create admin credentials secret
	adminCreds := map[string]interface{}{
		"clientid":     "test-client-id",
		"clientsecret": "test-client-secret",
		"url":          "https://uaa.example.com",
	}
	uaaJSON, err := json.Marshal(adminCreds)
	require.NoError(t, err, "failed to marshal admin credentials")

	adminSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-admin-secret",
			Namespace: testNamespace,
		},
		Data: map[string][]byte{
			"baseurl": []byte("api.hana.example.com"),
			"uaa":     uaaJSON,
		},
	}
	require.NoError(t, c.Create(ctx, adminSecret))

	// 3. Create ServiceBinding
	sb := &servicescloudsapv1.ServiceBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-binding",
			Namespace: testNamespace,
		},
		Spec: servicescloudsapv1.ServiceBindingSpec{
			ServiceInstanceName: si.Name,
			SecretName:          adminSecret.Name,
		},
	}
	require.NoError(t, c.Create(ctx, sb))

	// 4. Create ConfigMap with CLUSTER_ID
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sap-btp-operator-config",
			Namespace: kymaSystemNS,
		},
		Data: map[string]string{
			"CLUSTER_ID": clusterID,
		},
	}
	err = c.Create(ctx, cm)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		require.NoError(t, err)
	} else if apierrors.IsAlreadyExists(err) {
		// Update existing ConfigMap
		existing := &corev1.ConfigMap{}
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: cm.Name, Namespace: cm.Namespace}, existing))
		existing.Data = cm.Data
		require.NoError(t, c.Update(ctx, existing))
	}

	return &MockKymaResources{
		ServiceInstance: si,
		ServiceBinding:  sb,
		AdminSecret:     adminSecret,
		ConfigMap:       cm,
	}
}

// createKymaInstanceMapping creates a KymaInstanceMapping CR.
func createKymaInstanceMapping(t *testing.T, ctx context.Context, c client.Client, name string, resources *MockKymaResources, targetNS string) *inventoryv1alpha1.KymaInstanceMapping {
	t.Helper()
	kim := &inventoryv1alpha1.KymaInstanceMapping{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: inventoryv1alpha1.KymaInstanceMappingSpec{
			ForProvider: inventoryv1alpha1.KymaInstanceMappingParameters{
				// No KymaConnectionRef = use local cluster
				ServiceInstanceRef: inventoryv1alpha1.ResourceReference{
					Name:      resources.ServiceInstance.Name,
					Namespace: resources.ServiceInstance.Namespace,
				},
				AdminBindingRef: inventoryv1alpha1.ResourceReference{
					Name:      resources.ServiceBinding.Name,
					Namespace: resources.ServiceBinding.Namespace,
				},
				TargetNamespace:            ptr.To(targetNS),
				CredentialsSecretNamespace: crossplaneNS,
			},
		},
	}
	require.NoError(t, c.Create(ctx, kim))
	return kim
}

// assertSecretExists verifies a secret exists and returns it.
func assertSecretExists(t *testing.T, ctx context.Context, c client.Client, name, namespace string) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret)
	require.NoError(t, err, "secret %s/%s should exist", namespace, name)
	return secret
}

// assertInstanceMappingExists verifies an InstanceMapping exists and returns it.
func assertInstanceMappingExists(t *testing.T, ctx context.Context, c client.Client, name string) *inventoryv1alpha1.InstanceMapping {
	t.Helper()
	im := &inventoryv1alpha1.InstanceMapping{}
	err := c.Get(ctx, client.ObjectKey{Name: name}, im)
	require.NoError(t, err, "InstanceMapping %s should exist", name)
	return im
}

// cleanupResources deletes the given resources, ignoring errors
// (resources may already be deleted by the controller or previous cleanup).
func cleanupResources(t *testing.T, ctx context.Context, c client.Client, objects ...client.Object) {
	t.Helper()
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		_ = c.Delete(ctx, obj)
	}
}
