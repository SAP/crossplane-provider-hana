/*
Copyright 2026 SAP SE.
*/

package kymainstancemapping

import (
	"context"
	"errors"
	"fmt"

	servicescloudsapv1 "github.com/SAP/sap-btp-service-operator/api/v1"
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud/instancemapping"
	"github.com/SAP/crossplane-provider-hana/internal/clients/remotecluster"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotKymaInstanceMapping = "managed resource is not a KymaInstanceMapping custom resource"
	errTrackPCUsage           = "cannot track ProviderConfig usage: %w"
	errGetPC                  = "cannot get ProviderConfig: %w"
	errGetKubeconfigSecret    = "cannot get kubeconfig secret: %w"
	errMissingKubeconfigKey   = "kubeconfig key %q not found in secret"
	errCreateRemoteClient     = "cannot create remote cluster client: %w"
	errGetServiceInstance     = "cannot get ServiceInstance from remote cluster: %w"
	errInstanceNotReady       = "ServiceInstance on remote cluster is not ready"
	errMissingInstanceID      = "ServiceInstance on remote cluster has no instanceID"
	errGetServiceBinding      = "cannot get ServiceBinding from remote cluster: %w"
	errGetAdminSecret         = "cannot get admin API credentials secret from remote cluster: %w"
	errMissingAdminAPIData    = "admin API credentials secret missing required keys"
	errParseAdminAPI          = "cannot parse admin API credentials: %w"
	errGetConfigMap           = "cannot get ConfigMap from remote cluster: %w"
	errClusterIDNotFound      = "CLUSTER_ID not found in ConfigMap"
	errExtractKymaData        = "cannot extract data from Kyma cluster: %w"
	errConnectHANACloud       = "cannot connect to HANA Cloud API: %w"
	errListMappings           = "cannot list instance mappings: %w"
	errCreateMapping          = "cannot create instance mapping: %w"
	errDeleteMapping          = "cannot delete instance mapping: %w"
)

// Setup adds a controller that reconciles KymaInstanceMapping managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.KymaInstanceMappingGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	log := o.Logger.WithValues("controller", name)
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.KymaInstanceMappingGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:  mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			log:   log,
		}),
		managed.WithLogger(log),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.KymaInstanceMapping{}).
		Complete(r)
}

// A connector is expected to produce an ExternalClient when its Connect method is called.
type connector struct {
	kube  client.Client
	usage resource.Tracker
	log   logging.Logger
}

// kymaExtractedData holds all data extracted from the remote Kyma cluster
type kymaExtractedData struct {
	serviceInstanceID    string
	clusterID            string
	serviceInstanceName  string
	serviceInstanceReady bool
	adminAPICredentials  hanacloud.AdminAPICredentials
}

// Connect establishes connections to either the local or remote Kyma cluster and HANA Cloud API.
// It follows this flow:
// 1. Track ProviderConfig usage
// 2. Determine cluster client (local or remote based on KymaConnectionRef)
//   - If KymaConnectionRef is nil, use local cluster (c.kube)
//   - If KymaConnectionRef is provided, read kubeconfig and create remote client
//
// 3. Extract all needed data from cluster (ServiceInstance, ServiceBinding, ConfigMap)
// 4. Create a new HANA Cloud client and connect using extracted credentials
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.KymaInstanceMapping)
	if !ok {
		return nil, errors.New(errNotKymaInstanceMapping)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, fmt.Errorf(errTrackPCUsage, err)
	}

	// Determine which cluster client to use
	var clusterClient client.Client
	var extractErr error

	if cr.Spec.ForProvider.KymaConnectionRef == nil {
		// Use local cluster
		clusterClient = c.kube
		c.log.Info("Using local cluster for KymaInstanceMapping", "mapping", cr.Name)
	} else {
		// Read kubeconfig secret from management cluster
		kubeconfigData, err := c.getKubeconfigData(ctx, cr)
		if err != nil {
			return nil, err
		}

		// Create remote cluster client
		clusterClient, extractErr = remotecluster.CreateRemoteClient(ctx, kubeconfigData)
		if extractErr != nil {
			return nil, fmt.Errorf(errCreateRemoteClient, extractErr)
		}

		c.log.Info("Connected to remote Kyma cluster", "mapping", cr.Name)
	}

	// Extract all data from cluster (local or remote)
	kymaData, extractErr := extractKymaData(ctx, clusterClient, cr)
	if extractErr != nil {
		return nil, fmt.Errorf(errExtractKymaData, extractErr)
	}

	// Create a new HANA Cloud client for this reconcile
	cloudClient := hanacloud.New(c.log.WithValues("mapping", cr.Name))

	// Connect to HANA Cloud API using extracted credentials
	if err := cloudClient.Connect(ctx, kymaData.adminAPICredentials); err != nil {
		return nil, fmt.Errorf(errConnectHANACloud, err)
	}

	c.log.Info("Connected to HANA Cloud Admin API", "mapping", cr.Name)

	// Update CR status with extracted Kyma data
	cr.Status.AtProvider.Kyma = &v1alpha1.KymaClusterObservation{
		ServiceInstanceID:    kymaData.serviceInstanceID,
		ClusterID:            kymaData.clusterID,
		ServiceInstanceName:  kymaData.serviceInstanceName,
		ServiceInstanceReady: kymaData.serviceInstanceReady,
	}

	return &external{
		managementClient: c.kube,
		clusterClient:    clusterClient,
		client:           cloudClient.InstanceMapping(),
		kymaData:         kymaData,
		log:              c.log,
	}, nil
}

// getKubeconfigData reads the kubeconfig from the secret on the management cluster.
// Returns nil if KymaConnectionRef is not specified (indicating local cluster usage).
func (c *connector) getKubeconfigData(ctx context.Context, cr *v1alpha1.KymaInstanceMapping) ([]byte, error) {
	// Check if KymaConnectionRef is specified
	if cr.Spec.ForProvider.KymaConnectionRef == nil {
		return nil, nil
	}

	ref := cr.Spec.ForProvider.KymaConnectionRef.SecretRef
	key := cr.Spec.ForProvider.KymaConnectionRef.KubeconfigKey
	if key == "" {
		key = "kubeconfig"
	}

	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{
		Namespace: ref.Namespace,
		Name:      ref.Name,
	}, secret); err != nil {
		return nil, fmt.Errorf(errGetKubeconfigSecret, err)
	}

	kubeconfigData, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf(errMissingKubeconfigKey, key)
	}

	return kubeconfigData, nil
}

// extractKymaData fetches and extracts all required data from the remote Kyma cluster
func extractKymaData(ctx context.Context, remoteClient client.Client, cr *v1alpha1.KymaInstanceMapping) (*kymaExtractedData, error) {
	data := &kymaExtractedData{}

	// 1. Get ServiceInstance to extract instanceID
	serviceInstance := &servicescloudsapv1.ServiceInstance{}
	if err := remoteClient.Get(ctx, types.NamespacedName{
		Name:      cr.Spec.ForProvider.ServiceInstanceRef.Name,
		Namespace: cr.Spec.ForProvider.ServiceInstanceRef.Namespace,
	}, serviceInstance); err != nil {
		return nil, fmt.Errorf(errGetServiceInstance, err)
	}

	// Check if ServiceInstance is ready
	data.serviceInstanceReady = isServiceInstanceReady(serviceInstance)
	data.serviceInstanceName = serviceInstance.Name

	if serviceInstance.Status.InstanceID == "" {
		return nil, errors.New(errMissingInstanceID)
	}
	data.serviceInstanceID = serviceInstance.Status.InstanceID

	// 2. Get ServiceBinding to find credentials secret
	serviceBinding := &servicescloudsapv1.ServiceBinding{}
	if err := remoteClient.Get(ctx, types.NamespacedName{
		Name:      cr.Spec.ForProvider.AdminBindingRef.Name,
		Namespace: cr.Spec.ForProvider.AdminBindingRef.Namespace,
	}, serviceBinding); err != nil {
		return nil, fmt.Errorf(errGetServiceBinding, err)
	}

	// 3. Get admin API credentials secret
	adminSecret := &corev1.Secret{}
	if err := remoteClient.Get(ctx, types.NamespacedName{
		Name:      serviceBinding.Spec.SecretName,
		Namespace: serviceBinding.Namespace,
	}, adminSecret); err != nil {
		return nil, fmt.Errorf(errGetAdminSecret, err)
	}

	// Parse admin API credentials
	creds, err := parseAdminAPICredentials(adminSecret.Data)
	if err != nil {
		return nil, fmt.Errorf(errParseAdminAPI, err)
	}
	data.adminAPICredentials = creds

	// 4. Get ConfigMap to extract CLUSTER_ID
	cmRef := cr.Spec.ForProvider.ClusterIDConfigMapRef
	if cmRef == nil {
		// Default to BTP operator ConfigMap
		cmRef = &v1alpha1.ResourceReference{
			Namespace: "kyma-system",
			Name:      "sap-btp-operator-config",
		}
	}

	configMap := &corev1.ConfigMap{}
	if err := remoteClient.Get(ctx, types.NamespacedName{
		Name:      cmRef.Name,
		Namespace: cmRef.Namespace,
	}, configMap); err != nil {
		return nil, fmt.Errorf(errGetConfigMap, err)
	}

	clusterID, ok := configMap.Data["CLUSTER_ID"]
	if !ok {
		return nil, errors.New(errClusterIDNotFound)
	}
	data.clusterID = clusterID

	return data, nil
}

// isServiceInstanceReady checks if the ServiceInstance has a Ready condition set to True
func isServiceInstanceReady(si *servicescloudsapv1.ServiceInstance) bool {
	for _, cond := range si.Status.Conditions {
		if cond.Type == "Ready" && cond.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// parseAdminAPICredentials extracts admin API credentials from secret data
func parseAdminAPICredentials(data map[string][]byte) (hanacloud.AdminAPICredentials, error) {
	urlBytes, ok := data["url"]
	if !ok {
		return hanacloud.AdminAPICredentials{}, errors.New(errMissingAdminAPIData)
	}
	uaaBytes, ok := data["uaa"]
	if !ok {
		return hanacloud.AdminAPICredentials{}, errors.New(errMissingAdminAPIData)
	}

	// Combine into format expected by hanacloud.ParseAdminAPICredentials
	// The secret contains: url (string) and uaa (JSON)
	// We need to combine them into a single JSON structure
	combinedJSON := fmt.Sprintf(`{"url":"%s","uaa":%s}`, string(urlBytes), string(uaaBytes))

	// Use the existing parser
	creds, err := hanacloud.ParseAdminAPICredentials([]byte(combinedJSON))
	if err != nil {
		return hanacloud.AdminAPICredentials{}, err
	}

	return creds, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	managementClient client.Client // Client for management cluster
	clusterClient    client.Client // Client for cluster (local or remote)
	client           instancemapping.Client
	kymaData         *kymaExtractedData
	log              logging.Logger
}

func (e *external) Disconnect(ctx context.Context) error {
	return nil
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.KymaInstanceMapping)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotKymaInstanceMapping)
	}

	e.log.Info("Observing instance mapping",
		"name", cr.Name,
		"serviceInstanceID", e.kymaData.serviceInstanceID,
		"namespace", cr.Spec.ForProvider.TargetNamespace,
		"clusterID", e.kymaData.clusterID)

	// List mappings for this service instance
	mappings, err := e.client.List(ctx, e.kymaData.serviceInstanceID)
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf(errListMappings, err)
	}

	// Look for our specific mapping
	for _, mapping := range mappings {
		if mapping.PrimaryID == e.kymaData.clusterID && mapping.SecondaryID == cr.Spec.ForProvider.TargetNamespace {
			// Mapping exists
			e.log.Debug("Instance mapping found",
				"serviceInstanceID", e.kymaData.serviceInstanceID,
				"primaryID", mapping.PrimaryID,
				"secondaryID", mapping.SecondaryID)

			// Update status with mapping details
			cr.Status.AtProvider.Hana = &v1alpha1.HANACloudObservation{
				MappingID: &v1alpha1.MappingID{
					ServiceInstanceID: e.kymaData.serviceInstanceID,
					PrimaryID:         mapping.PrimaryID,
					SecondaryID:       mapping.SecondaryID,
				},
				Ready: true,
			}
			cr.SetConditions(xpv1.Available())

			return managed.ExternalObservation{
				ResourceExists:   true,
				ResourceUpToDate: true,
			}, nil
		}
	}

	// Mapping does not exist
	e.log.Debug("Instance mapping not found",
		"serviceInstanceID", e.kymaData.serviceInstanceID,
		"clusterID", e.kymaData.clusterID,
		"namespace", cr.Spec.ForProvider.TargetNamespace)

	return managed.ExternalObservation{
		ResourceExists: false,
	}, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.KymaInstanceMapping)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotKymaInstanceMapping)
	}

	e.log.Info("Creating instance mapping",
		"name", cr.Name,
		"serviceInstanceID", e.kymaData.serviceInstanceID,
		"namespace", cr.Spec.ForProvider.TargetNamespace,
		"clusterID", e.kymaData.clusterID)

	req := instancemapping.CreateMappingRequest{
		Platform:    "kubernetes",
		PrimaryID:   e.kymaData.clusterID,
		SecondaryID: cr.Spec.ForProvider.TargetNamespace,
		IsDefault:   cr.Spec.ForProvider.IsDefault,
	}

	if err := e.client.Create(ctx, e.kymaData.serviceInstanceID, req); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf(errCreateMapping, err)
	}

	cr.SetConditions(xpv1.Creating())

	return managed.ExternalCreation{}, nil
}

func (e *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	// Instance mappings are immutable - no update needed
	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.KymaInstanceMapping)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotKymaInstanceMapping)
	}

	e.log.Info("Deleting instance mapping",
		"name", cr.Name,
		"serviceInstanceID", e.kymaData.serviceInstanceID,
		"namespace", cr.Spec.ForProvider.TargetNamespace,
		"clusterID", e.kymaData.clusterID)

	if err := e.client.Delete(ctx, e.kymaData.serviceInstanceID, e.kymaData.clusterID, cr.Spec.ForProvider.TargetNamespace); err != nil {
		return managed.ExternalDelete{}, fmt.Errorf(errDeleteMapping, err)
	}

	cr.SetConditions(xpv1.Deleting())

	return managed.ExternalDelete{}, nil
}
