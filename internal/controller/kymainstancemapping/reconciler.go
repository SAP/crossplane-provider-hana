/*
Copyright 2026 SAP SE.
*/

package kymainstancemapping

import (
	"context"
	"encoding/json"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud"
	"github.com/SAP/crossplane-provider-hana/internal/clients/remotecluster"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotKymaInstanceMapping = "managed resource is not a KymaInstanceMapping custom resource"
	errTrackPCUsage           = "cannot track ProviderConfig usage: %w"
	errGetKubeconfigSecret    = "cannot get kubeconfig secret: %w"
	errMissingKubeconfigKey   = "kubeconfig key %q not found in secret"
	errCreateRemoteClient     = "cannot create remote cluster client: %w"
	errGetServiceInstance     = "cannot get ServiceInstance from remote cluster: %w"
	errMissingInstanceID      = "ServiceInstance on remote cluster has no instanceID"
	errGetServiceBinding      = "cannot get ServiceBinding from remote cluster: %w"
	errGetAdminSecret         = "cannot get admin API credentials secret from remote cluster: %w"
	errMissingAdminAPIData    = "admin API credentials secret missing required keys"
	errParseAdminAPI          = "cannot parse admin API credentials: %w"
	errGetConfigMap           = "cannot get ConfigMap from remote cluster: %w"
	errClusterIDNotFound      = "CLUSTER_ID not found in ConfigMap"
	errExtractKymaData        = "cannot extract data from Kyma cluster: %w"
	errCreateCredentialsSecret = "cannot create credentials secret: %w"
	errCreateInstanceMapping   = "cannot create InstanceMapping: %w"
	errGetInstanceMapping      = "cannot get InstanceMapping: %w"
	errUpdateCredentialsSecret = "cannot update credentials secret: %w"

	// Resource naming suffixes
	credentialsSecretSuffix = "-admin-creds"
	instanceMappingSuffix   = "-mapping"

	// Default namespace for child resources
	defaultCredentialsNamespace = "crossplane-system"

	// Key for credentials in the secret
	credentialsKey = "credentials"
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
		Owns(&v1alpha1.InstanceMapping{}).
		Owns(&corev1.Secret{}).
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

// Connect establishes connections to either the local or remote Kyma cluster.
// It extracts all needed data from cluster (ServiceInstance, ServiceBinding, ConfigMap)
// but does NOT connect to HANA Cloud API - that's done by the child InstanceMapping.
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
		kymaData:         kymaData,
		log:              c.log,
	}, nil
}

// getKubeconfigData reads the kubeconfig from the secret on the management cluster.
func (c *connector) getKubeconfigData(ctx context.Context, cr *v1alpha1.KymaInstanceMapping) ([]byte, error) {
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
	urlBytes, ok := data["baseurl"]
	if !ok {
		return hanacloud.AdminAPICredentials{}, errors.New(errMissingAdminAPIData)
	}
	uaaBytes, ok := data["uaa"]
	if !ok {
		return hanacloud.AdminAPICredentials{}, errors.New(errMissingAdminAPIData)
	}

	combinedJSON := fmt.Sprintf(`{"baseurl":"%s","uaa":%s}`, string(urlBytes), string(uaaBytes))

	creds, err := hanacloud.ParseAdminAPICredentials([]byte(combinedJSON))
	if err != nil {
		return hanacloud.AdminAPICredentials{}, err
	}

	return creds, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	managementClient client.Client
	clusterClient    client.Client
	kymaData         *kymaExtractedData
	log              logging.Logger
}

func (e *external) Disconnect(_ context.Context) error {
	return nil
}

// getCredentialsNamespace returns the namespace for child resources
func getCredentialsNamespace(cr *v1alpha1.KymaInstanceMapping) string {
	if cr.Spec.ForProvider.CredentialsSecretNamespace != "" {
		return cr.Spec.ForProvider.CredentialsSecretNamespace
	}
	return defaultCredentialsNamespace
}

// getChildResourceNames returns the names for child Secret and InstanceMapping
func getChildResourceNames(cr *v1alpha1.KymaInstanceMapping) (secretName, imName string) {
	return cr.Name + credentialsSecretSuffix, cr.Name + instanceMappingSuffix
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.KymaInstanceMapping)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotKymaInstanceMapping)
	}

	secretName, imName := getChildResourceNames(cr)
	ns := getCredentialsNamespace(cr)

	e.log.Info("Observing KymaInstanceMapping",
		"name", cr.Name,
		"instanceMappingName", imName,
		"secretName", secretName)

	// Check if child InstanceMapping exists
	im := &v1alpha1.InstanceMapping{}
	err := e.managementClient.Get(ctx, types.NamespacedName{Name: imName}, im)
	if err != nil {
		if apierrors.IsNotFound(err) {
			e.log.Debug("Child InstanceMapping not found", "name", imName)
			return managed.ExternalObservation{ResourceExists: false}, nil
		}
		return managed.ExternalObservation{}, fmt.Errorf(errGetInstanceMapping, err)
	}

	// Update status with child resource references
	cr.Status.AtProvider.ChildResources = &v1alpha1.ChildResourcesReference{
		InstanceMappingName:        imName,
		CredentialsSecretName:      secretName,
		CredentialsSecretNamespace: ns,
		InstanceMappingReady:       isConditionTrue(im.Status.Conditions, xpv1.TypeReady),
		InstanceMappingSynced:      isConditionTrue(im.Status.Conditions, xpv1.TypeSynced),
	}

	// Propagate status from child InstanceMapping
	if im.Status.AtProvider.MappingExists {
		cr.Status.AtProvider.Hana = &v1alpha1.HANACloudObservation{
			MappingID: &v1alpha1.MappingID{
				ServiceInstanceID: im.Spec.ForProvider.ServiceInstanceID,
				PrimaryID:         im.Spec.ForProvider.PrimaryID,
				SecondaryID:       im.Spec.ForProvider.SecondaryID,
			},
			Ready: cr.Status.AtProvider.ChildResources.InstanceMappingReady,
		}
	}

	// Set conditions based on child status
	if cr.Status.AtProvider.ChildResources.InstanceMappingReady {
		cr.SetConditions(xpv1.Available())
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
	}, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.KymaInstanceMapping)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotKymaInstanceMapping)
	}

	secretName, imName := getChildResourceNames(cr)
	ns := getCredentialsNamespace(cr)

	e.log.Info("Creating child resources for KymaInstanceMapping",
		"name", cr.Name,
		"instanceMappingName", imName,
		"secretName", secretName,
		"namespace", ns)

	// Step 1: Create credentials Secret
	credentialsJSON := buildCredentialsJSON(e.kymaData.adminAPICredentials)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         v1alpha1.KymaInstanceMappingGroupVersionKind.GroupVersion().String(),
					Kind:               v1alpha1.KymaInstanceMappingKind,
					Name:               cr.Name,
					UID:                cr.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Data: map[string][]byte{
			credentialsKey: credentialsJSON,
		},
	}

	if err := e.managementClient.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return managed.ExternalCreation{}, fmt.Errorf(errCreateCredentialsSecret, err)
		}
		// Secret exists, update it with fresh credentials
		existingSecret := &corev1.Secret{}
		if err := e.managementClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ns}, existingSecret); err != nil {
			return managed.ExternalCreation{}, fmt.Errorf(errUpdateCredentialsSecret, err)
		}
		existingSecret.Data = secret.Data
		if err := e.managementClient.Update(ctx, existingSecret); err != nil {
			return managed.ExternalCreation{}, fmt.Errorf(errUpdateCredentialsSecret, err)
		}
	}

	// Step 2: Create InstanceMapping CR
	im := &v1alpha1.InstanceMapping{
		ObjectMeta: metav1.ObjectMeta{
			Name: imName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         v1alpha1.KymaInstanceMappingGroupVersionKind.GroupVersion().String(),
					Kind:               v1alpha1.KymaInstanceMappingKind,
					Name:               cr.Name,
					UID:                cr.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: v1alpha1.InstanceMappingSpec{
			ForProvider: v1alpha1.InstanceMappingParameters{
				ServiceInstanceID: e.kymaData.serviceInstanceID,
				Platform:          "kubernetes",
				PrimaryID:         e.kymaData.clusterID,
				SecondaryID:       cr.Spec.ForProvider.TargetNamespace,
				IsDefault:         cr.Spec.ForProvider.IsDefault,
				AdminCredentialsSecretRef: v1alpha1.AdminCredentialsSecretRef{
					Name:      secretName,
					Namespace: ns,
					Key:       credentialsKey,
				},
			},
		},
	}

	if err := e.managementClient.Create(ctx, im); err != nil && !apierrors.IsAlreadyExists(err) {
		return managed.ExternalCreation{}, fmt.Errorf(errCreateInstanceMapping, err)
	}

	// Update status
	cr.Status.AtProvider.ChildResources = &v1alpha1.ChildResourcesReference{
		InstanceMappingName:        imName,
		CredentialsSecretName:      secretName,
		CredentialsSecretNamespace: ns,
	}

	cr.SetConditions(xpv1.Creating())
	return managed.ExternalCreation{}, nil
}

func (e *external) Update(_ context.Context, _ resource.Managed) (managed.ExternalUpdate, error) {
	// KymaInstanceMapping doesn't need update - child InstanceMapping handles it
	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(_ context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.KymaInstanceMapping)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotKymaInstanceMapping)
	}

	e.log.Info("Deleting KymaInstanceMapping - child resources will be garbage collected",
		"name", cr.Name)

	// Owner references will handle cascading delete of Secret and InstanceMapping
	cr.SetConditions(xpv1.Deleting())
	return managed.ExternalDelete{}, nil
}

// buildCredentialsJSON creates the JSON credentials blob for the intermediate secret
func buildCredentialsJSON(creds hanacloud.AdminAPICredentials) []byte {
	data, _ := json.Marshal(creds)
	return data
}

// isConditionTrue checks if a condition of the given type is True
func isConditionTrue(conditions []xpv1.Condition, condType xpv1.ConditionType) bool {
	for _, c := range conditions {
		if c.Type == condType && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
