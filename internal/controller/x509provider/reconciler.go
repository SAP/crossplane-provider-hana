/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package x509provider

import (
	"context"
	"slices"

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
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/x509provider"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotX509Provider         = "managed resource is not a X509Provider custom resource"
	errTrackPCUsage            = "cannot track ProviderConfig usage"
	errGetPC                   = "cannot get ProviderConfig"
	errGetCreds                = "cannot get credentials"
	errNoSecretRef             = "ProviderConfig does not reference a credentials Secret"
	errGetPasswordSecretFailed = "cannot get password secret: %w"
	errGetSecret               = "cannot get credentials Secret: %w"
	errKeyNotFound             = "key %s not found in secret %s/%s"
	errDbFail                  = "cannot connect to HANA db"
)

// Setup adds a controller that reconciles X509Provider managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	name := managed.ControllerName(adminv1alpha1.X509ProviderGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), v1alpha1.StoreConfigGroupVersionKind))
	}
	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(adminv1alpha1.X509ProviderGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: x509provider.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...)),
		features.ConfigureBetaManagementPolicies(o))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		For(&adminv1alpha1.X509Provider{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB) x509provider.Client
	log       logging.Logger
	db        xsql.DB
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*adminv1alpha1.X509Provider)
	if !ok {
		return nil, errors.New(errNotX509Provider)
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
		return nil, errors.Wrap(err, errGetSecret)
	}

	c.log.Info("Connecting to X509 provider resource", "name", cr.Name)

	if err := c.db.Connect(ctx, s.Data); err != nil {
		return nil, errors.Wrap(err, errDbFail)
	}

	return &external{
		client: c.newClient(c.db),
		kube:   c.kube,
		log:    c.log,
	}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	client x509provider.X509ProviderClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

func isUpToDate(p adminv1alpha1.X509ProviderParameters, o adminv1alpha1.X509ProviderObservation) bool {
	return o.Issuer != nil &&
		p.Issuer == *o.Issuer &&
		slices.Equal(p.MatchingRules, o.MatchingRules)
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*adminv1alpha1.X509Provider)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotX509Provider)
	}

	c.log.Info("Observing X.509 provider resource", "name", cr.Name)

	parameters := cr.Spec.ForProvider

	observed, err := c.client.Read(ctx, &parameters)
	if err != nil {
		return managed.ExternalObservation{}, err
	} else if observed == nil {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.Status.AtProvider = *observed
	cr.Status.SetConditions(xpv1.Available())

	if !isUpToDate(parameters, *observed) {
		return managed.ExternalObservation{
			ResourceExists:   true,
			ResourceUpToDate: false,
		}, nil
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*adminv1alpha1.X509Provider)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotX509Provider)
	}

	c.log.Info("Creating X.509 provider resource", "name", cr.Name)

	parameters := cr.Spec.ForProvider.DeepCopy()

	if err := c.client.Create(ctx, parameters); err != nil {
		return managed.ExternalCreation{}, err
	}

	meta.SetExternalName(cr, cr.Spec.ForProvider.Name)

	return managed.ExternalCreation{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*adminv1alpha1.X509Provider)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotX509Provider)
	}

	parameters := cr.Spec.ForProvider.DeepCopy()
	observation := cr.Status.AtProvider.DeepCopy()

	c.log.Info("Updating X.509 provider resource", "name", cr.Name)

	if err := c.client.Update(ctx, parameters, observation); err != nil {
		return managed.ExternalUpdate{}, err
	}

	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*adminv1alpha1.X509Provider)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotX509Provider)
	}

	parameters := cr.Spec.ForProvider.DeepCopy()

	c.log.Info("Deleting X.509 provider", "name", cr.Name)
	cr.SetConditions(xpv1.Deleting())

	return managed.ExternalDelete{}, c.client.Delete(ctx, parameters)
}
