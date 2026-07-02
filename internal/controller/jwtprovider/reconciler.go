/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package jwtprovider

import (
	"context"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/jwtprovider"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotJWTProvider = "managed resource is not a JWTProvider custom resource"
	errTrackPCUsage   = "cannot track ProviderConfig usage"
	errGetPC          = "cannot get ProviderConfig"
	errNoSecretRef    = "ProviderConfig does not reference a credentials Secret"
	errGetSecret      = "cannot get credentials Secret"
	errDbFail         = "cannot connect to HANA db"
)

// Setup adds a controller that reconciles JWTProvider managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.Connector) error {
	name := managed.ControllerName(adminv1alpha1.JWTProviderGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), v1alpha1.StoreConfigGroupVersionKind))
	}
	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(adminv1alpha1.JWTProviderGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: jwtprovider.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...),
		features.ConfigureBetaManagementPolicies(o))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		For(&adminv1alpha1.JWTProvider{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB) jwtprovider.Client
	log       logging.Logger
	db        xsql.Connector
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*adminv1alpha1.JWTProvider)
	if !ok {
		return nil, errors.New(errNotJWTProvider)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	pc := &v1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, errors.Wrap(err, errGetPC)
	}

	ref := pc.Spec.Credentials.ConnectionSecretRef
	if ref == nil {
		return nil, errors.New(errNoSecretRef)
	}

	s := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, s); err != nil {
		return nil, errors.Wrapf(err, "%s: %s", errGetSecret, ref.Name)
	}

	c.log.Info("Connecting to JWT provider resource", "name", cr.Name)

	conn, err := c.db.Connect(ctx, s.Data)
	if err != nil {
		return nil, errors.Wrap(err, errDbFail)
	}

	return &external{
		client: c.newClient(conn),
		kube:   c.kube,
		log:    c.log,
	}, nil
}

type external struct {
	client jwtprovider.JWTProviderClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Disconnect(ctx context.Context) error { return nil }

// comparison and flattening mirrors the JWTProvider surface.
//
//nolint:gocyclo // Field-by-field drift check; each branch is a single
func isUpToDate(p adminv1alpha1.JWTProviderParameters, o adminv1alpha1.JWTProviderObservation) bool {
	if o.Issuer == nil || p.Issuer != *o.Issuer {
		return false
	}
	desiredClaim := p.ExternalIdentityClaim
	if desiredClaim == "" {
		desiredClaim = "sub"
	}
	if o.ExternalIdentityClaim == nil || *o.ExternalIdentityClaim != desiredClaim {
		return false
	}
	// A nil observation indicates the column was NULL (shouldn't happen on
	// current HANA Cloud, but if it does the client can't reconcile so we
	// don't call it drift).
	if o.CaseInsensitiveIdentity != nil && p.CaseInsensitiveIdentity != *o.CaseInsensitiveIdentity {
		return false
	}
	if p.ApplicationUserClaim != o.ApplicationUserClaim {
		return false
	}
	if o.Priority == nil || p.Priority != *o.Priority {
		return false
	}
	if !equalFilters(p.ClaimFilters, o.ClaimFilters) {
		return false
	}
	return true
}

func equalFilters(a, b []adminv1alpha1.JWTClaimFilter) bool {
	if len(a) != len(b) {
		return false
	}
	in := func(x adminv1alpha1.JWTClaimFilter, s []adminv1alpha1.JWTClaimFilter) bool {
		for _, y := range s {
			if x.Claim == y.Claim && x.Value == y.Value {
				return true
			}
		}
		return false
	}
	for _, x := range a {
		if !in(x, b) {
			return false
		}
	}
	return true
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*adminv1alpha1.JWTProvider)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotJWTProvider)
	}
	c.log.Info("Observing JWT provider resource", "name", cr.Name)

	parameters := cr.Spec.ForProvider
	observed, err := c.client.Read(ctx, &parameters)
	if err != nil {
		return managed.ExternalObservation{}, err
	}
	if observed == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.Status.AtProvider = *observed
	cr.Status.SetConditions(xpv1.Available())

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isUpToDate(parameters, *observed),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*adminv1alpha1.JWTProvider)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotJWTProvider)
	}
	c.log.Info("Creating JWT provider resource", "name", cr.Name)

	parameters := cr.Spec.ForProvider.DeepCopy()
	if err := c.client.Create(ctx, parameters); err != nil {
		return managed.ExternalCreation{}, err
	}

	meta.SetExternalName(cr, parameters.Name)
	return managed.ExternalCreation{ConnectionDetails: managed.ConnectionDetails{}}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*adminv1alpha1.JWTProvider)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotJWTProvider)
	}
	c.log.Info("Updating JWT provider resource", "name", cr.Name)

	parameters := cr.Spec.ForProvider.DeepCopy()
	observation := cr.Status.AtProvider.DeepCopy()
	if err := c.client.Update(ctx, parameters, observation); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{ConnectionDetails: managed.ConnectionDetails{}}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*adminv1alpha1.JWTProvider)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotJWTProvider)
	}
	c.log.Info("Deleting JWT provider", "name", cr.Name)
	cr.SetConditions(xpv1.Deleting())

	parameters := cr.Spec.ForProvider.DeepCopy()
	return managed.ExternalDelete{}, c.client.Delete(ctx, parameters)
}
