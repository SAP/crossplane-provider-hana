//go:build e2e

/*
Copyright 2026 SAP SE.
*/

package e2e

import (
	"context"
	"testing"

	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	inventoryv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud"
	imclient "github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud/instancemapping"
	"github.com/SAP/crossplane-provider-hana/internal/controller/instancemapping"
	"github.com/SAP/crossplane-provider-hana/internal/controller/kymainstancemapping"
	"github.com/SAP/crossplane-provider-hana/test/e2e/mocks"
)

// mockTracker implements resource.Tracker for testing.
type mockTracker struct{}

func (m *mockTracker) Track(_ context.Context, _ resource.Managed) error {
	return nil
}

func TestKymaInstanceMappingIntegration(t *testing.T) {
	happyPath := features.New("happy-path").
		WithLabel("type", "integration").
		Assess("full flow creates child resources and calls HANA API", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			c := cfg.Client().Resources().GetControllerRuntimeClient()
			logger := logging.NewNopLogger()

			// Setup: Create mock Kyma resources
			resources := createMockKymaResources(t, ctx, c, "test-happy", "test-instance-id-123", "test-cluster-id-456")
			defer cleanupResources(t, ctx, c, resources.ServiceInstance, resources.ServiceBinding, resources.AdminSecret, resources.ConfigMap)

			// Create KymaInstanceMapping CR
			kim := createKymaInstanceMapping(t, ctx, c, "test-happy-kim", resources, "target-namespace")
			defer cleanupResources(t, ctx, c, kim)

			// Create KymaInstanceMapping connector and connect
			kimConnector := kymainstancemapping.NewConnector(c, &mockTracker{}, logger)
			kimExternal, err := kimConnector.Connect(ctx, kim)
			require.NoError(t, err, "KymaInstanceMapping Connect should succeed")

			// Observe should return ResourceExists=false (no child InstanceMapping yet)
			obs, err := kimExternal.Observe(ctx, kim)
			require.NoError(t, err)
			assert.False(t, obs.ResourceExists, "ResourceExists should be false before Create")

			// Create should create Secret and InstanceMapping
			_, err = kimExternal.Create(ctx, kim)
			require.NoError(t, err, "KymaInstanceMapping Create should succeed")

			// Verify child Secret was created
			secret := assertSecretExists(t, ctx, c, "test-happy-kim-admin-creds", crossplaneNS)
			assert.NotEmpty(t, secret.Data["credentials"], "credentials key should exist in secret")

			// Verify child InstanceMapping was created
			im := assertInstanceMappingExists(t, ctx, c, "test-happy-kim-mapping")
			assert.Equal(t, "test-instance-id-123", im.Spec.ForProvider.ServiceInstanceID)
			assert.Equal(t, "kubernetes", im.Spec.ForProvider.Platform)
			assert.Equal(t, "test-cluster-id-456", im.Spec.ForProvider.PrimaryID)
			assert.Equal(t, "target-namespace", *im.Spec.ForProvider.SecondaryID)

			// Now test InstanceMapping reconciler with mock HANA client
			mockClient := mocks.NewMockClient()
			mockClientFactory := func(ctx context.Context, creds hanacloud.AdminAPICredentials, log logging.Logger) (imclient.Client, error) {
				return mockClient, nil
			}

			imConnector := instancemapping.NewConnector(c, logger, mockClientFactory)
			imExternal, err := imConnector.Connect(ctx, im)
			require.NoError(t, err, "InstanceMapping Connect should succeed")

			// Observe should return ResourceExists=false (no mapping in HANA yet)
			imObs, err := imExternal.Observe(ctx, im)
			require.NoError(t, err)
			assert.False(t, imObs.ResourceExists, "ResourceExists should be false before HANA Create")

			// Create should call mock HANA client
			_, err = imExternal.Create(ctx, im)
			require.NoError(t, err, "InstanceMapping Create should succeed")

			// Verify mock client was called correctly
			require.Len(t, mockClient.CreateCalls, 1, "Create should be called once")
			call := mockClient.CreateCalls[0]
			assert.Equal(t, "test-instance-id-123", call.ServiceInstanceID)
			assert.Equal(t, "kubernetes", call.Request.Platform)
			assert.Equal(t, "test-cluster-id-456", call.Request.PrimaryID)
			assert.Equal(t, "target-namespace", *call.Request.SecondaryID)

			// Observe again should return ResourceExists=true
			imObs, err = imExternal.Observe(ctx, im)
			require.NoError(t, err)
			assert.True(t, imObs.ResourceExists, "ResourceExists should be true after Create")
			assert.True(t, imObs.ResourceUpToDate, "ResourceUpToDate should be true")

			// Cleanup child resources
			cleanupResources(t, ctx, c, im, secret)

			return ctx
		}).
		Feature()

	testenv.Test(t, happyPath)
}

func TestKymaInstanceMappingDeleteFlow(t *testing.T) {
	deleteFlow := features.New("delete-flow").
		WithLabel("type", "integration").
		Assess("delete calls HANA API and cleans up", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			c := cfg.Client().Resources().GetControllerRuntimeClient()
			logger := logging.NewNopLogger()

			// Setup: Create resources and go through create flow
			resources := createMockKymaResources(t, ctx, c, "test-delete", "delete-instance-id", "delete-cluster-id")
			defer cleanupResources(t, ctx, c, resources.ServiceInstance, resources.ServiceBinding, resources.AdminSecret, resources.ConfigMap)

			kim := createKymaInstanceMapping(t, ctx, c, "test-delete-kim", resources, "delete-ns")
			defer cleanupResources(t, ctx, c, kim)

			// Create child resources via KymaInstanceMapping
			kimConnector := kymainstancemapping.NewConnector(c, &mockTracker{}, logger)
			kimExternal, err := kimConnector.Connect(ctx, kim)
			require.NoError(t, err)

			_, err = kimExternal.Create(ctx, kim)
			require.NoError(t, err)

			// Get the created InstanceMapping
			im := assertInstanceMappingExists(t, ctx, c, "test-delete-kim-mapping")
			secret := assertSecretExists(t, ctx, c, "test-delete-kim-admin-creds", crossplaneNS)

			// Create the mapping in mock HANA
			mockClient := mocks.NewMockClient()
			mockClientFactory := func(ctx context.Context, creds hanacloud.AdminAPICredentials, log logging.Logger) (imclient.Client, error) {
				return mockClient, nil
			}

			imConnector := instancemapping.NewConnector(c, logger, mockClientFactory)
			imExternal, err := imConnector.Connect(ctx, im)
			require.NoError(t, err)

			_, err = imExternal.Create(ctx, im)
			require.NoError(t, err)

			// Now test Delete
			_, err = imExternal.Delete(ctx, im)
			require.NoError(t, err, "InstanceMapping Delete should succeed")

			// Verify mock client Delete was called
			require.Len(t, mockClient.DeleteCalls, 1, "Delete should be called once")
			deleteCall := mockClient.DeleteCalls[0]
			assert.Equal(t, "delete-instance-id", deleteCall.ServiceInstanceID)
			assert.Equal(t, "delete-cluster-id", deleteCall.PrimaryID)
			assert.Equal(t, "delete-ns", deleteCall.SecondaryID)

			// Cleanup
			cleanupResources(t, ctx, c, im, secret)

			return ctx
		}).
		Feature()

	testenv.Test(t, deleteFlow)
}

func TestKymaInstanceMappingHANAError(t *testing.T) {
	errorHandling := features.New("hana-api-error").
		WithLabel("type", "integration").
		Assess("HANA API error is handled correctly", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			c := cfg.Client().Resources().GetControllerRuntimeClient()
			logger := logging.NewNopLogger()

			// Setup
			resources := createMockKymaResources(t, ctx, c, "test-error", "error-instance-id", "error-cluster-id")
			defer cleanupResources(t, ctx, c, resources.ServiceInstance, resources.ServiceBinding, resources.AdminSecret, resources.ConfigMap)

			kim := createKymaInstanceMapping(t, ctx, c, "test-error-kim", resources, "error-ns")
			defer cleanupResources(t, ctx, c, kim)

			// Create child resources
			kimConnector := kymainstancemapping.NewConnector(c, &mockTracker{}, logger)
			kimExternal, err := kimConnector.Connect(ctx, kim)
			require.NoError(t, err)

			_, err = kimExternal.Create(ctx, kim)
			require.NoError(t, err)

			im := assertInstanceMappingExists(t, ctx, c, "test-error-kim-mapping")
			secret := assertSecretExists(t, ctx, c, "test-error-kim-admin-creds", crossplaneNS)
			defer cleanupResources(t, ctx, c, im, secret)

			// Configure mock to return error
			mockClient := mocks.NewMockClient()
			mockClient.CreateErr = assert.AnError
			mockClientFactory := func(ctx context.Context, creds hanacloud.AdminAPICredentials, log logging.Logger) (imclient.Client, error) {
				return mockClient, nil
			}

			imConnector := instancemapping.NewConnector(c, logger, mockClientFactory)
			imExternal, err := imConnector.Connect(ctx, im)
			require.NoError(t, err)

			// Create should fail with mock error
			_, err = imExternal.Create(ctx, im)
			require.Error(t, err, "Create should fail when HANA API returns error")
			assert.Contains(t, err.Error(), "cannot create instance mapping")

			return ctx
		}).
		Feature()

	testenv.Test(t, errorHandling)
}

func TestKymaInstanceMappingMissingServiceInstance(t *testing.T) {
	missingResource := features.New("missing-service-instance").
		WithLabel("type", "integration").
		Assess("missing ServiceInstance returns clear error", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			c := cfg.Client().Resources().GetControllerRuntimeClient()
			logger := logging.NewNopLogger()

			// Create namespaces
			createNamespaceIfNotExists(t, ctx, c, testNamespace)
			createNamespaceIfNotExists(t, ctx, c, crossplaneNS)

			// Create KymaInstanceMapping referencing non-existent ServiceInstance
			kim := &inventoryv1alpha1.KymaInstanceMapping{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-missing-kim",
				},
				Spec: inventoryv1alpha1.KymaInstanceMappingSpec{
					ForProvider: inventoryv1alpha1.KymaInstanceMappingParameters{
						ServiceInstanceRef: inventoryv1alpha1.ResourceReference{
							Name:      "non-existent-instance",
							Namespace: testNamespace,
						},
						AdminBindingRef: inventoryv1alpha1.ResourceReference{
							Name:      "non-existent-binding",
							Namespace: testNamespace,
						},
						TargetNamespace: ptr.To("target-ns"),
					},
				},
			}
			require.NoError(t, c.Create(ctx, kim))
			defer cleanupResources(t, ctx, c, kim)

			// Connect should fail with clear error
			kimConnector := kymainstancemapping.NewConnector(c, &mockTracker{}, logger)
			_, err := kimConnector.Connect(ctx, kim)
			require.Error(t, err, "Connect should fail when ServiceInstance is missing")
			assert.Contains(t, err.Error(), "cannot get ServiceInstance")

			return ctx
		}).
		Feature()

	testenv.Test(t, missingResource)
}
