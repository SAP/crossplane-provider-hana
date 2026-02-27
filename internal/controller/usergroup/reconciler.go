/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package usergroup

import (
	"context"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/usergroup"
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
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotUsergroup = "managed resource is not a usergroup custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage: %w"
	errGetPC        = "cannot get ProviderConfig: %w"
	errNoSecretRef  = "ProviderConfig does not reference a credentials Secret"
	errGetSecret    = "cannot get credentials Secret: %w"

	errSelectUsergroup = "cannot select usergroup: %w"
	errCreateUsergroup = "cannot create usergroup: %w"
	errUpdateUsergroup = "cannot update usergroup: %w"
	errDropUsergroup   = "cannot drop usergroup: %w"
)

// Setup adds a controller that reconciles usergroup managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	name := managed.ControllerName(v1alpha1.UsergroupGroupKind)

	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.UsergroupGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: usergroup.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(log),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name)))),
		features.ConfigureBetaManagementPolicies(o))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.Usergroup{}).
		Complete(r)
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(xsql.DB) usergroup.Client
	log       logging.Logger
	db        xsql.DB
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Usergroup)
	if !ok {
		return nil, errors.New(errNotUsergroup)
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

	c.log.Info("Connecting to usergroup resource", "name", cr.Name)

	if err := c.db.Connect(ctx, s.Data); err != nil {
		return nil, fmt.Errorf("cannot connect to HANA DB: %w", err)
	}

	return &external{
		client: c.newClient(c.db),
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
	client usergroup.UsergroupClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Usergroup)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotUsergroup)
	}

	c.log.Info("Observing usergroup resource", "name", cr.Name)

	parameters := &v1alpha1.UsergroupParameters{
		UsergroupName:    cr.Spec.ForProvider.UsergroupName,
		DisableUserAdmin: cr.Spec.ForProvider.DisableUserAdmin,
		Parameters:       cr.Spec.ForProvider.Parameters,
	}

	observed, err := c.client.Read(ctx, parameters)

	if err != nil {
		c.log.Info("Error observing usergroup", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errSelectUsergroup, err)
	}

	if observed.UsergroupName != parameters.UsergroupName {
		c.log.Info("Usergroup does not exist", "name", cr.Name, "usergroupName", parameters.UsergroupName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.Status.AtProvider.UsergroupName = observed.UsergroupName
	cr.Status.AtProvider.DisableUserAdmin = observed.DisableUserAdmin
	cr.Status.AtProvider.Parameters = observed.Parameters

	cr.SetConditions(xpv1.Available())

	isUpToDate := upToDate(observed, parameters)
	c.log.Info("Observed usergroup resource",
		"name", cr.Name,
		"usergroupName", parameters.UsergroupName,
		"upToDate", isUpToDate)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isUpToDate,
	}, nil

}

func upToDate(observed *v1alpha1.UsergroupObservation, desired *v1alpha1.UsergroupParameters) bool {
	if observed.DisableUserAdmin != desired.DisableUserAdmin {
		return false
	}
	if !parametersConfigured(observed.Parameters, desired.Parameters) {
		return false
	}
	return true
}

func parametersConfigured(observed map[string]string, desired map[string]string) bool {
	for key, value := range desired {
		if observed[key] != value {
			return false
		}
	}
	return true
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Usergroup)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotUsergroup)
	}

	c.log.Info("Creating usergroup resource", "name", cr.Name, "usergroupName", cr.Spec.ForProvider.UsergroupName)

	cr.SetConditions(xpv1.Creating())

	parameters := &v1alpha1.UsergroupParameters{
		UsergroupName:      cr.Spec.ForProvider.UsergroupName,
		DisableUserAdmin:   cr.Spec.ForProvider.DisableUserAdmin,
		NoGrantToCreator:   cr.Spec.ForProvider.NoGrantToCreator,
		Parameters:         cr.Spec.ForProvider.Parameters,
		EnableParameterSet: cr.Spec.ForProvider.EnableParameterSet,
	}

	c.log.Info("Creating usergroup with parameters",
		"usergroupName", parameters.UsergroupName,
		"disableUserAdmin", parameters.DisableUserAdmin,
		"noGrantToCreator", parameters.NoGrantToCreator,
		"enableParameterSet", parameters.EnableParameterSet)

	err := c.client.Create(ctx, parameters)

	if err != nil {
		c.log.Info("Error creating usergroup", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateUsergroup, err)
	}

	cr.Status.AtProvider.UsergroupName = parameters.UsergroupName
	cr.Status.AtProvider.DisableUserAdmin = true // This is a weird behavior
	cr.Status.AtProvider.Parameters = parameters.Parameters

	c.log.Info("Successfully created usergroup resource", "name", cr.Name, "usergroupName", parameters.UsergroupName)

	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Usergroup)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotUsergroup)
	}

	c.log.Info("Updating usergroup resource", "name", cr.Name, "usergroupName", cr.Spec.ForProvider.UsergroupName)

	parameters := &v1alpha1.UsergroupParameters{
		UsergroupName:    cr.Spec.ForProvider.UsergroupName,
		DisableUserAdmin: cr.Spec.ForProvider.DisableUserAdmin,
		Parameters:       cr.Spec.ForProvider.Parameters,
	}
	if cr.Status.AtProvider.DisableUserAdmin != parameters.DisableUserAdmin {
		c.log.Info("Updating DisableUserAdmin setting",
			"name", cr.Name,
			"usergroupName", parameters.UsergroupName,
			"current", cr.Status.AtProvider.DisableUserAdmin,
			"desired", parameters.DisableUserAdmin)

		err := c.client.UpdateDisableUserAdmin(ctx, parameters)
		if err != nil {
			c.log.Info("Error updating DisableUserAdmin", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUsergroup, err)
		}
		cr.Status.AtProvider.DisableUserAdmin = parameters.DisableUserAdmin
		c.log.Info("Updated DisableUserAdmin setting", "name", cr.Name, "value", parameters.DisableUserAdmin)
	}

	observedParameters := cr.Status.AtProvider.Parameters
	desiredParameters := parameters.Parameters

	if !parametersConfigured(observedParameters, desiredParameters) {
		parametersToUpdate := changedParameters(observedParameters, desiredParameters)
		c.log.Info("Updating usergroup parameters",
			"name", cr.Name,
			"usergroupName", parameters.UsergroupName,
			"changedParams", parametersToUpdate)

		err := c.client.UpdateParameters(ctx, parameters, parametersToUpdate)
		if err != nil {
			c.log.Info("Error updating parameters", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUsergroup, err)
		}
		cr.Status.AtProvider.Parameters = parameters.Parameters
		c.log.Info("Updated usergroup parameters", "name", cr.Name, "usergroupName", parameters.UsergroupName)
	}

	c.log.Info("Successfully updated usergroup resource", "name", cr.Name, "usergroupName", parameters.UsergroupName)
	return managed.ExternalUpdate{}, nil
}

func changedParameters(observed map[string]string, desired map[string]string) map[string]string {
	changed := make(map[string]string)

	for key, value := range desired {
		if observed[key] != value {
			changed[key] = value
		}
	}

	if len(changed) == 0 {
		return nil
	}
	return changed
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Usergroup)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotUsergroup)
	}

	c.log.Info("Deleting usergroup resource", "name", cr.Name, "usergroupName", cr.Spec.ForProvider.UsergroupName)

	parameters := &v1alpha1.UsergroupParameters{
		UsergroupName: cr.Spec.ForProvider.UsergroupName,
	}

	cr.SetConditions(xpv1.Deleting())

	err := c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Info("Error deleting usergroup", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf(errDropUsergroup, err)
	}

	c.log.Info("Successfully deleted usergroup resource", "name", cr.Name, "usergroupName", parameters.UsergroupName)
	return managed.ExternalDelete{}, err
}
