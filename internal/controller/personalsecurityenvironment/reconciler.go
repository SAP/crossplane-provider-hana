/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package personalsecurityenvironment

import (
	"context"
	"errors"
	"fmt"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
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
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/personalsecurityenvironment"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotPersonalSecurityEnvironment = "managed resource is not a PersonalSecurityEnvironment custom resource"
	errTrackPCUsage                   = "cannot track ProviderConfig usage: %w"
	errGetPC                          = "cannot get ProviderConfig: %w"
	errNoSecretRef                    = "ProviderConfig does not reference a credentials Secret"
	errGetSecret                      = "cannot get credentials Secret: %w"
	errDbFail                         = "cannot connect to HANA db: %w"
)

// Setup adds a controller that reconciles PersonalSecurityEnvironment managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	name := managed.ControllerName(adminv1alpha1.PersonalSecurityEnvironmentGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), v1alpha1.StoreConfigGroupVersionKind))
	}

	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &v1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(adminv1alpha1.PersonalSecurityEnvironmentGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: personalsecurityenvironment.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		For(&adminv1alpha1.PersonalSecurityEnvironment{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB) personalsecurityenvironment.Client
	log       logging.Logger
	db        xsql.DB
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*adminv1alpha1.PersonalSecurityEnvironment)
	if !ok {
		return nil, errors.New(errNotPersonalSecurityEnvironment)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, fmt.Errorf(errTrackPCUsage, err)
	}

	pc := &v1alpha1.ProviderConfig{}
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

	client := c.newClient(c.db)

	c.log.Info("Connecting to personalsecurityenvironment resource", "name", cr.Name)

	if err := c.db.Connect(ctx, s.Data); err != nil {
		return nil, fmt.Errorf(errDbFail, err)
	}

	return &external{
		client: client,
		kube:   c.kube,
		log:    c.log,
	}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	client personalsecurityenvironment.PersonalSecurityEnvironmentClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*adminv1alpha1.PersonalSecurityEnvironment)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotPersonalSecurityEnvironment)
	}

	parameters := cr.Spec.ForProvider.DeepCopy()

	c.log.Info("Observing Personal Security Environment", "name", cr.Name)

	observed, err := c.client.Read(ctx, parameters)
	if err != nil {
		return managed.ExternalObservation{}, err
	}
	if observed == nil {
		return managed.ExternalObservation{
			ResourceExists: false,
		}, nil
	}

	providerName, err := c.getX509ProviderName(ctx, parameters.X509ProviderRef)
	if err != nil {
		return managed.ExternalObservation{}, fmt.Errorf("failed to get provider for pse: %w", err)
	}

	cr.Status.AtProvider = *observed
	cr.Status.SetConditions(xpv1.Available())
	meta.SetExternalName(cr, observed.Name)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isUpToDate(parameters, *observed, providerName),
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*adminv1alpha1.PersonalSecurityEnvironment)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotPersonalSecurityEnvironment)
	}

	c.log.Info("Creating Personal Security Environment", "name", cr.Name)

	parameters := cr.Spec.ForProvider.DeepCopy()

	providerName, err := c.getX509ProviderName(ctx, parameters.X509ProviderRef)
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("failed to get provider for pse: %w", err)
	}

	return managed.ExternalCreation{}, c.client.Create(ctx, parameters, providerName)
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*adminv1alpha1.PersonalSecurityEnvironment)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotPersonalSecurityEnvironment)
	}

	parameters := cr.Spec.ForProvider.DeepCopy()
	observed := cr.Status.AtProvider.DeepCopy()

	c.log.Info("Updating Personal Security Environment", "name", cr.Name)

	toAdd := certListDifference(parameters.CertificateRefs, observed.CertificateRefs)
	toRemove := certListDifference(observed.CertificateRefs, parameters.CertificateRefs)

	providerName, err := c.getX509ProviderName(ctx, parameters.X509ProviderRef)
	if err != nil {
		return managed.ExternalUpdate{}, fmt.Errorf("failed to get provider for pse: %w", err)
	}

	// Avoid setting the provider name if it hasn't changed
	if providerName == cr.Status.AtProvider.X509ProviderName {
		providerName = ""
	}

	if err := c.client.Update(ctx, parameters.Name, toAdd, toRemove, providerName); err != nil {
		return managed.ExternalUpdate{}, err
	}

	cr.Status.AtProvider.CertificateRefs = parameters.CertificateRefs

	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*adminv1alpha1.PersonalSecurityEnvironment)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotPersonalSecurityEnvironment)
	}

	parameters := cr.Spec.ForProvider.DeepCopy()

	c.log.Info("Deleting Personal Security Environment", "name", cr.Name)

	cr.SetConditions(xpv1.Deleting())

	return managed.ExternalDelete{}, c.client.Delete(ctx, parameters)
}

func isUpToDate(p *adminv1alpha1.PersonalSecurityEnvironmentParameters, o adminv1alpha1.PersonalSecurityEnvironmentObservation, providerName string) bool {
	return len(p.CertificateRefs) == len(o.CertificateRefs) &&
		len(certListDifference(p.CertificateRefs, o.CertificateRefs)) == 0 &&
		providerName == o.X509ProviderName &&
		p.Name == o.Name
}

func (c *external) getX509ProviderName(ctx context.Context, ref *adminv1alpha1.X509ProviderRef) (string, error) {
	if ref == nil {
		return "", nil
	}

	switch {
	case ref.ProviderRef != nil:
		provider := adminv1alpha1.X509Provider{}
		if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.ProviderRef.Name}, &provider); err != nil {
			return "", err
		}
		return provider.Spec.ForProvider.Name, nil
	case ref.Name != "":
		return ref.Name, nil
	default:
		return "", errors.New("X509ProviderRef must have either ProviderRef or Name specified")
	}
}

// certListDifference returns the certificates that are in 'a' but not in 'b'
func certListDifference(a, b []adminv1alpha1.CertificateRef) []adminv1alpha1.CertificateRef {
	var diff []adminv1alpha1.CertificateRef
	for _, certA := range a {
		found := false
		for _, certB := range b {
			if certDifferent(certA, certB) {
				found = true
				break
			}
		}
		if !found {
			diff = append(diff, certA)
		}
	}
	return diff
}

func certDifferent(certA, certB adminv1alpha1.CertificateRef) bool {
	return (certA.ID != nil && certB.ID != nil && *certA.ID == *certB.ID) ||
		(certA.Name != nil && certB.Name != nil && *certA.Name != "" && *certA.Name == *certB.Name)
}
