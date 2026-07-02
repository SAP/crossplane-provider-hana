/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package publickey

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
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/publickey"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotPublicKey = "managed resource is not a PublicKey custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage"
	errGetPC        = "cannot get ProviderConfig"
	errNoSecretRef  = "ProviderConfig does not reference a credentials Secret"
	errGetSecret    = "cannot get credentials Secret"
	errDbFail       = "cannot connect to HANA db"
)

// Setup adds a controller that reconciles PublicKey managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.Connector) error {
	name := managed.ControllerName(adminv1alpha1.PublicKeyGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), v1alpha1.StoreConfigGroupVersionKind))
	}
	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(adminv1alpha1.PublicKeyGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: publickey.New,
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
		For(&adminv1alpha1.PublicKey{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB) publickey.Client
	log       logging.Logger
	db        xsql.Connector
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*adminv1alpha1.PublicKey)
	if !ok {
		return nil, errors.New(errNotPublicKey)
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

	c.log.Info("Connecting to PublicKey resource", "name", cr.Name)

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
	client publickey.PublicKeyClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Disconnect(ctx context.Context) error { return nil }

func isUpToDate(p adminv1alpha1.PublicKeyParameters, o adminv1alpha1.PublicKeyObservation) bool {
	// PEM is immutable at the CRD level (see PublicKeyParameters.PEM), so the
	// only drift channel here is Comment. HANA has no ALTER for the key
	// bytes; rotation goes through delete + recreate.
	currentComment := ""
	if o.Comment != nil {
		currentComment = *o.Comment
	}
	return p.Comment == currentComment
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*adminv1alpha1.PublicKey)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotPublicKey)
	}
	c.log.Info("Observing PublicKey", "name", cr.Name)

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
	cr, ok := mg.(*adminv1alpha1.PublicKey)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotPublicKey)
	}
	c.log.Info("Creating PublicKey", "name", cr.Name)

	parameters := cr.Spec.ForProvider.DeepCopy()
	if err := c.client.Create(ctx, parameters); err != nil {
		return managed.ExternalCreation{}, err
	}
	meta.SetExternalName(cr, parameters.Name)
	return managed.ExternalCreation{ConnectionDetails: managed.ConnectionDetails{}}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*adminv1alpha1.PublicKey)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotPublicKey)
	}
	c.log.Info("Updating PublicKey", "name", cr.Name)

	parameters := cr.Spec.ForProvider.DeepCopy()
	observation := cr.Status.AtProvider.DeepCopy()
	if err := c.client.Update(ctx, parameters, observation); err != nil {
		return managed.ExternalUpdate{}, err
	}
	return managed.ExternalUpdate{ConnectionDetails: managed.ConnectionDetails{}}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*adminv1alpha1.PublicKey)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotPublicKey)
	}
	c.log.Info("Deleting PublicKey", "name", cr.Name)
	cr.SetConditions(xpv1.Deleting())

	parameters := cr.Spec.ForProvider.DeepCopy()
	return managed.ExternalDelete{}, c.client.Delete(ctx, parameters)
}
