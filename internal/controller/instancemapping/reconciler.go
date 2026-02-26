/*
Copyright 2026 SAP SE.
*/

package instancemapping

import (
	"context"
	"errors"
	"fmt"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
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
	"github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud"
	imclient "github.com/SAP/crossplane-provider-hana/internal/clients/hanacloud/instancemapping"
)

const (
	errNotInstanceMapping    = "managed resource is not an InstanceMapping custom resource"
	errGetCredentialsSecret  = "cannot get admin credentials secret: %w"
	errMissingCredentialsKey = "credentials key %q not found in secret"
	errParseCredentials      = "cannot parse admin API credentials: %w"
	errConnectHANACloud      = "cannot connect to HANA Cloud API: %w"
	errListMappings          = "cannot list instance mappings: %w"
	errCreateMapping         = "cannot create instance mapping: %w"
	errDeleteMapping         = "cannot delete instance mapping: %w"
)

// Setup adds a controller that reconciles InstanceMapping managed resources.
func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.InstanceMappingGroupKind)

	log := o.Logger.WithValues("controller", name)
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.InstanceMappingGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube: mgr.GetClient(),
			log:  log,
		}),
		managed.WithLogger(log),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.InstanceMapping{}).
		Complete(r)
}

// connector produces an ExternalClient when its Connect method is called.
type connector struct {
	kube client.Client
	log  logging.Logger
}

// Connect establishes a connection to the HANA Cloud Admin API using credentials
// from the referenced Secret.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.InstanceMapping)
	if !ok {
		return nil, errors.New(errNotInstanceMapping)
	}

	// Get credentials from the referenced secret
	secretRef := cr.Spec.ForProvider.AdminCredentialsSecretRef
	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{
		Namespace: secretRef.Namespace,
		Name:      secretRef.Name,
	}, secret); err != nil {
		return nil, fmt.Errorf(errGetCredentialsSecret, err)
	}

	credentialsJSON, ok := secret.Data[secretRef.Key]
	if !ok {
		return nil, fmt.Errorf(errMissingCredentialsKey, secretRef.Key)
	}

	// Parse credentials
	creds, err := hanacloud.ParseAdminAPICredentials(credentialsJSON)
	if err != nil {
		return nil, fmt.Errorf(errParseCredentials, err)
	}

	// Create HANA Cloud client
	cloudClient := hanacloud.New(c.log.WithValues("instancemapping", cr.Name))
	if err := cloudClient.Connect(ctx, creds); err != nil {
		return nil, fmt.Errorf(errConnectHANACloud, err)
	}

	c.log.Info("Connected to HANA Cloud Admin API", "instancemapping", cr.Name)

	return &external{
		client: cloudClient.InstanceMapping(),
		log:    c.log,
	}, nil
}

// external observes, creates, updates, or deletes an external resource.
type external struct {
	client imclient.Client
	log    logging.Logger
}

func (e *external) Disconnect(_ context.Context) error {
	return nil
}

func (e *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.InstanceMapping)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotInstanceMapping)
	}

	params := cr.Spec.ForProvider

	e.log.Info("Observing instance mapping",
		"name", cr.Name,
		"serviceInstanceID", params.ServiceInstanceID,
		"primaryID", params.PrimaryID,
		"secondaryID", params.SecondaryID)

	mappings, err := e.client.List(ctx, params.ServiceInstanceID)
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf(errListMappings, err)
	}

	// Look for our specific mapping
	for _, mapping := range mappings {
		if mapping.PrimaryID == params.PrimaryID && stringPtrEqual(mapping.SecondaryID, params.SecondaryID) {
			cr.Status.AtProvider.MappingExists = true
			cr.Status.AtProvider.LastSyncTime = &metav1.Time{Time: metav1.Now().Time}
			cr.SetConditions(xpv1.Available())

			e.log.Debug("Instance mapping found",
				"serviceInstanceID", params.ServiceInstanceID,
				"primaryID", mapping.PrimaryID,
				"secondaryID", mapping.SecondaryID)

			return managed.ExternalObservation{
				ResourceExists:   true,
				ResourceUpToDate: true,
			}, nil
		}
	}

	cr.Status.AtProvider.MappingExists = false

	e.log.Debug("Instance mapping not found",
		"serviceInstanceID", params.ServiceInstanceID,
		"primaryID", params.PrimaryID,
		"secondaryID", params.SecondaryID)

	return managed.ExternalObservation{ResourceExists: false}, nil
}

func (e *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.InstanceMapping)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotInstanceMapping)
	}

	params := cr.Spec.ForProvider

	e.log.Info("Creating instance mapping",
		"name", cr.Name,
		"serviceInstanceID", params.ServiceInstanceID,
		"platform", params.Platform,
		"primaryID", params.PrimaryID,
		"secondaryID", params.SecondaryID)

	req := imclient.CreateMappingRequest{
		Platform:    params.Platform,
		PrimaryID:   params.PrimaryID,
		SecondaryID: params.SecondaryID,
		IsDefault:   params.IsDefault,
	}

	if err := e.client.Create(ctx, params.ServiceInstanceID, req); err != nil {
		return managed.ExternalCreation{}, fmt.Errorf(errCreateMapping, err)
	}

	cr.SetConditions(xpv1.Creating())
	return managed.ExternalCreation{}, nil
}

func (e *external) Update(_ context.Context, _ resource.Managed) (managed.ExternalUpdate, error) {
	// Instance mappings are immutable - no update needed
	return managed.ExternalUpdate{}, nil
}

func (e *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.InstanceMapping)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotInstanceMapping)
	}

	params := cr.Spec.ForProvider
	secondaryID := ""
	if params.SecondaryID != nil {
		secondaryID = *params.SecondaryID
	}

	e.log.Info("Deleting instance mapping",
		"name", cr.Name,
		"serviceInstanceID", params.ServiceInstanceID,
		"primaryID", params.PrimaryID,
		"secondaryID", secondaryID)

	if err := e.client.Delete(ctx, params.ServiceInstanceID, params.PrimaryID, secondaryID); err != nil {
		return managed.ExternalDelete{}, fmt.Errorf(errDeleteMapping, err)
	}

	cr.SetConditions(xpv1.Deleting())
	return managed.ExternalDelete{}, nil
}

// stringPtrEqual compares two optional string pointers for equality.
func stringPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
