/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package auditpolicy

import (
	"context"
	"strings"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/auditpolicy"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotAuditPolicy = "managed resource is not a AuditPolicy custom resource"
	errTrackPCUsage   = "cannot track ProviderConfig usage"
	errGetSecret      = "cannot get credentials Secret"
	errNoSecretRef    = "ProviderConfig does not reference a credentials Secret"
	errGetPC          = "cannot get ProviderConfig"
	errGetCreds       = "cannot get credentials"
	errSelectPolicy   = "cannot select audit policy"
	errCreatePolicy   = "cannot create audit policy"
	errNewClient      = "cannot create new Service"
	errUpdatePolicy   = "cannot update audit policy"
	errDropPolicy     = "cannot drop audit policy"
	errDbFail         = "cannot connect to HANA db"
)

// A NoOpService does nothing.
type NoOpService struct{}

// Setup adds a controller that reconciles AuditPolicy managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	name := managed.ControllerName(v1alpha1.AuditPolicyGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	log := o.Logger.WithValues("controller", name)
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.AuditPolicyGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClient: auditpolicy.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		For(&v1alpha1.AuditPolicy{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB) auditpolicy.Client
	log       logging.Logger
	db        xsql.DB
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.AuditPolicy)
	if !ok {
		return nil, errors.New(errNotAuditPolicy)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	ref := pc.Spec.Credentials.ConnectionSecretRef
	if ref == nil {
		return nil, errors.New(errNoSecretRef)
	}

	s := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, s); err != nil {
		return nil, errors.Wrap(err, errGetSecret)
	}

	c.log.Info("Connecting to auditpolicy resource", "name", cr.Name)

	if err := c.db.Connect(ctx, s.Data); err != nil {
		c.log.Info("Error connecting to hana in auditpolicy", "name", cr.Name, "error", err)
		return nil, errors.Wrap(err, errDbFail)
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
	client auditpolicy.AuditPolicyClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.AuditPolicy)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotAuditPolicy)
	}

	// These fmt statements should be removed in the real implementation.

	c.log.Info("Observing auditpolicy resource", "name", cr.Name)

	parameters, err := c.buildDesiredParameters(cr)
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, "error building desired parameters for observation")
	}

	observed, err := c.client.Read(ctx, parameters)
	if err != nil {
		c.log.Info("Error observing auditpolicy", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, errors.Wrap(err, errSelectPolicy)
	}
	observed.AuditActions, err = auditpolicy.OptimizeAuditActions(observed.AuditActions)
	if err != nil {
		c.log.Info("Error optimizing audit actions", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, err
	}

	if err != nil {
		c.log.Info("Error observing auditpolicy", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, errors.Wrap(err, errSelectPolicy)
	}

	if observed == nil || observed.PolicyName == "" || observed.PolicyName != parameters.PolicyName {
		c.log.Info("AuditPolicy does not exist", "name", cr.Name, "policyName", parameters.PolicyName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.Status.AtProvider.PolicyName = observed.PolicyName
	cr.Status.AtProvider.AuditStatus = observed.AuditStatus
	cr.Status.AtProvider.AuditLevel = observed.AuditLevel
	cr.Status.AtProvider.AuditTrailRetention = observed.AuditTrailRetention
	cr.Status.AtProvider.Enabled = observed.Enabled
	cr.Status.AtProvider.AuditActions = observed.AuditActions

	cr.SetConditions(xpv1.Available())

	isUpToDate := upToDate(observed, parameters)
	c.log.Info("Observed auditpolicy resource",
		"name", cr.Name,
		"auditPolicy", parameters.PolicyName,
		"upToDate", isUpToDate)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isUpToDate,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.AuditPolicy)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotAuditPolicy)
	}

	c.log.Info("Creating auditPolicy resource", "name", cr.Name, "policyName", cr.Spec.ForProvider.PolicyName)

	parameters, err := c.buildDesiredParameters(cr)
	if err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, "error building desired parameters for creation")
	}

	c.log.Info("Creating auditPolicy with parameters",
		"policyName", parameters.PolicyName,
		"AuditActions", parameters.AuditActions,
		"AuditStatus", parameters.AuditStatus,
		"auditLevel", parameters.AuditLevel,
		"AuditTrailRetention", parameters.AuditTrailRetention,
		"enabled", parameters.Enabled)

	cr.SetConditions(xpv1.Creating())

	err = c.client.Create(ctx, parameters)

	if err != nil {
		c.log.Info("Error creating auditpolicy", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, errors.Wrap(err, errCreatePolicy)
	}

	c.log.Info("Successfully created auditPolicy resource", "name", cr.Name, "policyName", parameters.PolicyName)
	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.AuditPolicy)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotAuditPolicy)
	}
	c.log.Info("Updating audit policy resource", "name", cr.Name, "policyName", cr.Spec.ForProvider.PolicyName)

	observed, _ := c.buildObservedParameters(cr)
	desired, err := c.buildDesiredParameters(cr)
	if err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, "error building desired parameters")
	}

	// if audit actions, status or level differ, we need to drop and recreate the policy
	if needsRecreation(observed, desired) {
		c.log.Debug("Audit policy differ and will be recreated",
			"name", cr.Name,
			"policyName", desired.PolicyName,
			"observedActions", observed.AuditActions,
			"desiredActions", desired.AuditActions,
			"observedStatus", observed.AuditStatus,
			"desiredStatus", desired.AuditStatus,
			"observedLevel", observed.AuditLevel,
			"desiredLevel", desired.AuditLevel)
		err := c.client.RecreatePolicy(ctx, desired)
		if err != nil {
			c.log.Info("Error updating audit policy", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, errors.Wrap(err, errUpdatePolicy)
		}
		cr.Status.AtProvider.AuditActions = desired.AuditActions
		cr.Status.AtProvider.AuditStatus = desired.AuditStatus
		cr.Status.AtProvider.AuditLevel = desired.AuditLevel
		c.log.Info("Recreated audit policy to update actions/status/level", "name", cr.Name, "policyName", desired.PolicyName)
	} else {
		// if only retention or enabled differ, we can update those without recreating the policy
		// if the policy was just recreated, we don't need to update those again
		if *observed.AuditTrailRetention != *desired.AuditTrailRetention {
			c.log.Info("Audit policy retention differ and will be updated",
				"name", cr.Name,
				"policyName", desired.PolicyName,
				"observedRetention", observed.AuditTrailRetention,
				"desiredRetention", desired.AuditTrailRetention)
			err := c.client.UpdateRetentionDays(ctx, desired)
			if err != nil {
				c.log.Info("Error updating audit policy", "name", cr.Name, "error", err)
				return managed.ExternalUpdate{}, errors.Wrap(err, errUpdatePolicy)
			}
			cr.Status.AtProvider.AuditTrailRetention = desired.AuditTrailRetention
			cr.Status.AtProvider.Enabled = desired.Enabled
			c.log.Info("Updated audit policy retention days", "name", cr.Name, "policyName", desired.PolicyName)
		}

		if *observed.Enabled != *desired.Enabled {
			c.log.Info("Audit policy active state differ and will be updated",
				"name", cr.Name,
				"policyName", desired.PolicyName,
				"observedEnabled", observed.Enabled,
				"desiredEnabled", desired.Enabled)
			err := c.client.UpdateEnablePolicy(ctx, desired)
			if err != nil {
				c.log.Info("Error updating audit policy", "name", cr.Name, "error", err)
				return managed.ExternalUpdate{}, errors.Wrap(err, errUpdatePolicy)
			}
			cr.Status.AtProvider.AuditTrailRetention = desired.AuditTrailRetention
			cr.Status.AtProvider.Enabled = desired.Enabled
			c.log.Info("Updated audit policy enable/disable state", "name", cr.Name, "policyName", desired.PolicyName)
		}
	}

	c.log.Info("Successfully updated audit policy", "name", cr.Name, "policyName", desired.PolicyName)
	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.AuditPolicy)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotAuditPolicy)
	}

	c.log.Info("Deleting auditpolicy resource", "name", cr.Name, "schemaName", cr.Spec.ForProvider.PolicyName)

	parameters, err := c.buildDesiredParameters(cr)
	if err != nil {
		return managed.ExternalDelete{}, errors.Wrap(err, "error building desired parameters for deletion")
	}

	cr.SetConditions(xpv1.Deleting())

	err = c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Info("Error deleting auditpolicy", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, errors.Wrap(err, errDropPolicy)
	}

	c.log.Info("Successfully deleted auditpolicy resource", "name", cr.Name, "auditPolicy", parameters.PolicyName)
	return managed.ExternalDelete{}, err
}

func (c *external) buildObservedParameters(cr *v1alpha1.AuditPolicy) (*v1alpha1.AuditPolicyObservation, error) {
	observed := cr.Status.AtProvider.DeepCopy()
	auditActions, err := auditpolicy.OptimizeAuditActions(observed.AuditActions)
	if err != nil {
		return nil, err
	}
	observed.AuditActions = auditActions
	return observed, nil
}

func (c *external) buildDesiredParameters(cr *v1alpha1.AuditPolicy) (*v1alpha1.AuditPolicyParameters, error) {
	auditActions, err := auditpolicy.OptimizeAuditActions(cr.Spec.ForProvider.AuditActions)
	if err != nil {
		return nil, err
	}

	return &v1alpha1.AuditPolicyParameters{
		PolicyName:          strings.ToUpper(cr.Spec.ForProvider.PolicyName),
		AuditStatus:         strings.ToUpper(cr.Spec.ForProvider.AuditStatus),
		AuditActions:        auditActions,
		AuditLevel:          strings.ToUpper(cr.Spec.ForProvider.AuditLevel),
		AuditTrailRetention: cr.Spec.ForProvider.AuditTrailRetention,
		Enabled:             cr.Spec.ForProvider.Enabled,
	}, nil
}

func needsRecreation(observed *v1alpha1.AuditPolicyObservation, desired *v1alpha1.AuditPolicyParameters) bool {
	return !utils.ArraysEqual(desired.AuditActions, observed.AuditActions) || (observed.AuditStatus != desired.AuditStatus) || (observed.AuditLevel != desired.AuditLevel)
}

func upToDate(observed *v1alpha1.AuditPolicyObservation, desired *v1alpha1.AuditPolicyParameters) bool {
	if observed.PolicyName != desired.PolicyName || observed.AuditStatus != desired.AuditStatus || observed.AuditLevel != desired.AuditLevel {
		return false
	}
	if *observed.AuditTrailRetention != *desired.AuditTrailRetention {
		return false
	}
	if *observed.Enabled != *desired.Enabled {
		return false
	}
	if !utils.ArraysEqual(observed.AuditActions, desired.AuditActions) {
		return false
	}
	return true
}
