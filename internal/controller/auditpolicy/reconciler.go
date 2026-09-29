/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package auditpolicy

import (
	"context"
	"fmt"
	"strings"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/logging"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"

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
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.Connector) error {
	name := managed.ControllerName(v1alpha1.AuditPolicyGroupKind)

	log := o.Logger.WithValues("controller", name)
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.AuditPolicyGroupVersionKind),
		managed.WithExternalConnector(&connector{
			kube:      mgr.GetClient(),
			usage:     resource.NewLegacyProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClient: auditpolicy.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))), //nolint:staticcheck // NewAPIRecorder still requires the old recorder API.
		features.ConfigureBetaManagementPolicies(o))

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
	usage     resource.LegacyTracker
	newClient func(db xsql.DB) auditpolicy.Client
	log       logging.Logger
	db        xsql.Connector
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

	if err := c.usage.Track(ctx, cr); err != nil {
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

	conn, err := c.db.Connect(ctx, s.Data)
	if err != nil {
		c.log.Info("Error connecting to hana in auditpolicy", "name", cr.Name, "error", err)
		return nil, errors.Wrap(err, errDbFail)
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

	parameters := buildDesiredParameters(cr)

	observed, err := c.client.Read(ctx, parameters)

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
	cr.Status.AtProvider.AuditPrincipals = observed.AuditPrincipals
	cr.Status.AtProvider.ExceptPrincipals = observed.ExceptPrincipals

	cr.SetConditions(xpv2.Available())

	isUpToDate, reason := upToDateWithReason(observed, parameters)
	c.log.Info("Observed auditpolicy resource",
		"name", cr.Name,
		"auditPolicy", parameters.PolicyName,
		"upToDate", isUpToDate)
	if !isUpToDate {
		c.log.Info("AuditPolicy is not up to date",
			"name", cr.Name,
			"auditPolicy", parameters.PolicyName,
			"reason", reason)
	}

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

	parameters := buildDesiredParameters(cr)

	c.log.Info("Creating auditPolicy with parameters",
		"policyName", parameters.PolicyName,
		"AuditActions", parameters.AuditActions,
		"AuditStatus", parameters.AuditStatus,
		"auditLevel", parameters.AuditLevel,
		"AuditTrailRetention", parameters.AuditTrailRetention,
		"enabled", parameters.Enabled)

	cr.SetConditions(xpv2.Creating())

	err := c.client.Create(ctx, parameters)

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

	observed := buildObservedParameters(cr)
	desired := buildDesiredParameters(cr)

	// Audit actions, status, level and principals cannot be altered in place, so
	// any drift in them requires dropping and recreating the policy. Observe
	// already logs the specific field that differs (see "AuditPolicy is not up
	// to date"), so we only log the intent here.
	if needsRecreation(observed, desired) {
		c.log.Debug("Audit policy differs and will be recreated", "name", cr.Name, "policyName", desired.PolicyName)
		err := c.client.RecreatePolicy(ctx, desired)
		if err != nil {
			c.log.Info("Error updating audit policy", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, errors.Wrap(err, errUpdatePolicy)
		}
		cr.Status.AtProvider.AuditActions = desired.AuditActions
		cr.Status.AtProvider.AuditStatus = desired.AuditStatus
		cr.Status.AtProvider.AuditLevel = desired.AuditLevel
		cr.Status.AtProvider.AuditPrincipals = desired.AuditPrincipals
		cr.Status.AtProvider.ExceptPrincipals = desired.ExceptPrincipals
		c.log.Info("Recreated audit policy to update actions/status/level/principals", "name", cr.Name, "policyName", desired.PolicyName)
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

	parameters := buildDesiredParameters(cr)

	cr.SetConditions(xpv2.Deleting())

	err := c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Info("Error deleting auditpolicy", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, errors.Wrap(err, errDropPolicy)
	}

	c.log.Info("Successfully deleted auditpolicy resource", "name", cr.Name, "auditPolicy", parameters.PolicyName)
	return managed.ExternalDelete{}, err
}

func buildObservedParameters(cr *v1alpha1.AuditPolicy) *v1alpha1.AuditPolicyObservation {
	observed := cr.Status.AtProvider.DeepCopy()
	return observed
}

func buildDesiredParameters(cr *v1alpha1.AuditPolicy) *v1alpha1.AuditPolicyParameters {
	return &v1alpha1.AuditPolicyParameters{
		PolicyName:          strings.ToUpper(cr.Spec.ForProvider.PolicyName),
		AuditStatus:         strings.ToUpper(cr.Spec.ForProvider.AuditStatus),
		AuditActions:        utils.ArrayToUpper(cr.Spec.ForProvider.AuditActions),
		AuditLevel:          strings.ToUpper(cr.Spec.ForProvider.AuditLevel),
		AuditPrincipals:     principalsToUpper(cr.Spec.ForProvider.AuditPrincipals),
		ExceptPrincipals:    cr.Spec.ForProvider.ExceptPrincipals,
		AuditTrailRetention: cr.Spec.ForProvider.AuditTrailRetention,
		Enabled:             cr.Spec.ForProvider.Enabled,
	}
}

// principalsToUpper upper-cases the type and name of each principal, keeping the
// original ordering. It returns nil when no principals are configured.
func principalsToUpper(principals []v1alpha1.AuditPrincipal) []v1alpha1.AuditPrincipal {
	if len(principals) == 0 {
		return nil
	}
	upper := make([]v1alpha1.AuditPrincipal, len(principals))
	for i, p := range principals {
		upper[i] = v1alpha1.AuditPrincipal{
			Type: strings.ToUpper(p.Type),
			Name: strings.ToUpper(p.Name),
		}
	}
	return upper
}

func needsRecreation(observed *v1alpha1.AuditPolicyObservation, desired *v1alpha1.AuditPolicyParameters) bool {
	return !utils.ArraysEqual(desired.AuditActions, observed.AuditActions) ||
		(observed.AuditStatus != desired.AuditStatus) ||
		(observed.AuditLevel != desired.AuditLevel) ||
		principalsDiffer(observed, desired)
}

// principalsDiffer reports whether the observed principal configuration differs
// from the desired one. The principal clause cannot be altered in place, so any
// difference requires a drop-and-recreate of the audit policy. ExceptPrincipals
// is only meaningful when principals are configured.
func principalsDiffer(observed *v1alpha1.AuditPolicyObservation, desired *v1alpha1.AuditPolicyParameters) bool {
	if !utils.ArraysEqual(observed.AuditPrincipals, desired.AuditPrincipals) {
		return true
	}
	if len(desired.AuditPrincipals) > 0 && observed.ExceptPrincipals != desired.ExceptPrincipals {
		return true
	}
	return false
}

// upToDateWithReason reports whether the observed state matches the desired
// state and, when it does not, a human-readable reason describing the first
// field that differs. The reason is intended for logging so drift is easy to
// diagnose.
func upToDateWithReason(observed *v1alpha1.AuditPolicyObservation, desired *v1alpha1.AuditPolicyParameters) (bool, string) {
	if observed.PolicyName != desired.PolicyName {
		return false, fmt.Sprintf("policyName differs: observed=%q desired=%q", observed.PolicyName, desired.PolicyName)
	}
	if observed.AuditStatus != desired.AuditStatus {
		return false, fmt.Sprintf("auditStatus differs: observed=%q desired=%q", observed.AuditStatus, desired.AuditStatus)
	}
	if observed.AuditLevel != desired.AuditLevel {
		return false, fmt.Sprintf("auditLevel differs: observed=%q desired=%q", observed.AuditLevel, desired.AuditLevel)
	}
	if !equalIntPtr(observed.AuditTrailRetention, desired.AuditTrailRetention) {
		return false, fmt.Sprintf("auditTrailRetention differs: observed=%v desired=%v", derefInt(observed.AuditTrailRetention), derefInt(desired.AuditTrailRetention))
	}
	if !equalBoolPtr(observed.Enabled, desired.Enabled) {
		return false, fmt.Sprintf("enabled differs: observed=%v desired=%v", derefBool(observed.Enabled), derefBool(desired.Enabled))
	}
	if !utils.ArraysEqual(observed.AuditActions, desired.AuditActions) {
		return false, fmt.Sprintf("auditActions differ: observed=%v desired=%v", observed.AuditActions, desired.AuditActions)
	}
	if principalsDiffer(observed, desired) {
		return false, fmt.Sprintf("principals differ: observed=%v exceptObserved=%v desired=%v exceptDesired=%v",
			observed.AuditPrincipals, observed.ExceptPrincipals, desired.AuditPrincipals, desired.ExceptPrincipals)
	}
	return true, ""
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func derefInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func derefBool(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}
