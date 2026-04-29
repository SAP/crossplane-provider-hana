/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package rolegroup

import (
	"context"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/rolegroup"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"

	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
)

const (
	errNotRolegroup = "managed resource is not a rolegroup custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage: %w"
	errGetPC        = "cannot get ProviderConfig: %w"
	errNoSecretRef  = "ProviderConfig does not reference a credentials Secret"
	errGetSecret    = "cannot get credentials Secret: %w"

	errSelectRolegroup = "cannot select rolegroup: %w"
	errCreateRolegroup = "cannot create rolegroup: %w"
	errUpdateRolegroup = "cannot update rolegroup: %w"
	errDropRolegroup   = "cannot drop rolegroup: %w"
)

// Setup adds a controller that reconciles rolegroup managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.Connector) error {
	name := managed.ControllerName(v1alpha1.RolegroupGroupKind)

	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.RolegroupGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: rolegroup.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(log),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.Rolegroup{}).
		Complete(r)
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(xsql.DB) rolegroup.Client
	log       logging.Logger
	db        xsql.Connector
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Rolegroup)
	if !ok {
		return nil, errors.New(errNotRolegroup)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, fmt.Errorf(errTrackPCUsage, err)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, fmt.Errorf(errGetPC, err)
	}

	ref := pc.Spec.Credentials.ConnectionSecretRef
	if ref == nil {
		return nil, errors.New(errNoSecretRef)
	}

	s := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, s); err != nil {
		return nil, fmt.Errorf(errGetSecret, err)
	}

	c.log.Info("Connecting to rolegroup resource", "name", cr.Name)

	conn, err := c.db.Connect(ctx, s.Data)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to HANA DB: %w", err)
	}

	return &external{
		client: c.newClient(conn),
		kube:   c.kube,
		log:    c.log,
	}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	client rolegroup.RolegroupClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Rolegroup)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotRolegroup)
	}

	c.log.Info("Observing rolegroup resource", "name", cr.Name)

	parameters := buildDesiredParameters(cr)

	observed, err := c.client.Read(ctx, parameters)

	if err != nil {
		c.log.Info("Error observing rolegroup", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errSelectRolegroup, err)
	}

	if observed.RolegroupName != parameters.RolegroupName {
		c.log.Info("Rolegroup does not exist", "name", cr.Name, "rolegroupName", parameters.RolegroupName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.Status.AtProvider.RolegroupName = observed.RolegroupName
	cr.Status.AtProvider.DisableRoleAdmin = observed.DisableRoleAdmin

	cr.SetConditions(xpv1.Available())

	isUpToDate := upToDate(observed, parameters)
	c.log.Info("Observed rolegroup resource",
		"name", cr.Name,
		"rolegroupName", parameters.RolegroupName,
		"upToDate", isUpToDate)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isUpToDate,
	}, nil

}

func upToDate(observed *v1alpha1.RolegroupObservation, desired *v1alpha1.RolegroupParameters) bool {
	return observed.DisableRoleAdmin == desired.DisableRoleAdmin
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Rolegroup)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRolegroup)
	}

	c.log.Info("Creating rolegroup resource", "name", cr.Name, "rolegroupName", cr.Spec.ForProvider.RolegroupName)

	cr.SetConditions(xpv1.Creating())

	parameters := buildDesiredParameters(cr)

	c.log.Info("Creating rolegroup with parameters",
		"rolegroupName", parameters.RolegroupName,
		"disableRoleAdmin", parameters.DisableRoleAdmin,
		"noGrantToCreator", parameters.NoGrantToCreator,
		"forGrantsOnTenantObjects", parameters.ForGrantsOnTenantObjects)

	err := c.client.Create(ctx, parameters)

	if err != nil {
		c.log.Info("Error creating rolegroup", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateRolegroup, err)
	}

	cr.Status.AtProvider.RolegroupName = parameters.RolegroupName
	cr.Status.AtProvider.DisableRoleAdmin = parameters.DisableRoleAdmin

	c.log.Info("Successfully created rolegroup resource", "name", cr.Name, "rolegroupName", parameters.RolegroupName)

	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Rolegroup)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotRolegroup)
	}

	c.log.Info("Updating rolegroup resource", "name", cr.Name, "rolegroupName", cr.Spec.ForProvider.RolegroupName)

	parameters := buildDesiredParameters(cr)
	// rolegroup.Client has additional functions not defined in global interface
	rgClient, _ := c.client.(rolegroup.Client)
	if cr.Status.AtProvider.DisableRoleAdmin != parameters.DisableRoleAdmin {
		c.log.Info("Updating DisableRoleAdmin setting",
			"name", cr.Name,
			"rolegroupName", parameters.RolegroupName,
			"current", cr.Status.AtProvider.DisableRoleAdmin,
			"desired", parameters.DisableRoleAdmin)

		err := rgClient.UpdateDisableRoleAdmin(ctx, parameters)
		if err != nil {
			c.log.Info("Error updating DisableRoleAdmin", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateRolegroup, err)
		}
		cr.Status.AtProvider.DisableRoleAdmin = parameters.DisableRoleAdmin
		c.log.Info("Updated DisableRoleAdmin setting", "name", cr.Name, "value", parameters.DisableRoleAdmin)
	}

	c.log.Info("Successfully updated rolegroup resource", "name", cr.Name, "rolegroupName", parameters.RolegroupName)
	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Rolegroup)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotRolegroup)
	}

	c.log.Info("Deleting rolegroup resource", "name", cr.Name, "rolegroupName", cr.Spec.ForProvider.RolegroupName)

	parameters := &v1alpha1.RolegroupParameters{
		RolegroupName: cr.Spec.ForProvider.RolegroupName,
	}

	cr.SetConditions(xpv1.Deleting())

	err := c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Info("Error deleting rolegroup", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf(errDropRolegroup, err)
	}

	c.log.Info("Successfully deleted rolegroup resource", "name", cr.Name, "rolegroupName", parameters.RolegroupName)
	return managed.ExternalDelete{}, err
}

func buildDesiredParameters(cr *v1alpha1.Rolegroup) *v1alpha1.RolegroupParameters {
	return &v1alpha1.RolegroupParameters{
		RolegroupName:            cr.Spec.ForProvider.RolegroupName,
		DisableRoleAdmin:         cr.Spec.ForProvider.DisableRoleAdmin,
		NoGrantToCreator:         cr.Spec.ForProvider.NoGrantToCreator,
		ForGrantsOnTenantObjects: cr.Spec.ForProvider.ForGrantsOnTenantObjects,
	}
}
