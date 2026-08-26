/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package certificate

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
	"github.com/crossplane/crossplane-runtime/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	certclient "github.com/SAP/crossplane-provider-hana/internal/clients/hana/certificate"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotCertificate = "managed resource is not a Certificate custom resource"
	errTrackPCUsage   = "cannot track ProviderConfig usage"
	errGetPC          = "cannot get ProviderConfig"
	errNoSecretRef    = "ProviderConfig does not reference a credentials Secret"
	errGetSecret      = "cannot get credentials Secret"
	errDbFail         = "cannot connect to HANA db"
	errGetCertSecret  = "cannot get certificate Secret"
	errReadCert       = "cannot read certificate from HANA"
	errCreateCert     = "cannot create certificate in HANA"
	errDeleteCert     = "cannot delete certificate from HANA"
)

// Setup adds a controller that reconciles Certificate managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.Connector) error {
	name := managed.ControllerName(v1alpha1.CertificateGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	log := o.Logger.WithValues("controller", name)
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.CertificateGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClient: certclient.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(log),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...),
		features.ConfigureBetaManagementPolicies(o))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		For(&v1alpha1.Certificate{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB) certclient.Client
	log       logging.Logger
	db        xsql.Connector
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Certificate)
	if !ok {
		return nil, errors.New(errNotCertificate)
	}

	if err := c.usage.Track(ctx, mg); err != nil {
		return nil, fmt.Errorf("%s: %w", errTrackPCUsage, err)
	}

	pc := &apisv1alpha1.ProviderConfig{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: cr.GetProviderConfigReference().Name}, pc); err != nil {
		return nil, fmt.Errorf("%s: %w", errGetPC, err)
	}

	ref := pc.Spec.Credentials.ConnectionSecretRef
	if ref == nil {
		return nil, errors.New(errNoSecretRef)
	}

	s := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, s); err != nil {
		return nil, fmt.Errorf("%s: %w", errGetSecret, err)
	}

	c.log.Info("Connecting to certificate resource", "name", cr.Name)

	conn, err := c.db.Connect(ctx, s.Data)
	if err != nil {
		c.log.Info("Error connecting to HANA for certificate", "name", cr.Name, "error", err)
		return nil, fmt.Errorf("%s: %w", errDbFail, err)
	}

	return &external{
		client: c.newClient(conn),
		kube:   c.kube,
		log:    c.log,
	}, nil
}

func (c *external) Disconnect(_ context.Context) error {
	return nil
}

type external struct {
	client certclient.CertificateClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Certificate)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotCertificate)
	}

	c.log.Info("Observing certificate resource", "name", cr.Name)

	observed, err := c.client.Read(ctx, &cr.Spec.ForProvider)
	if err != nil {
		c.log.Info("Error observing certificate", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf("%s: %w", errReadCert, err)
	}

	if observed == nil {
		c.log.Info("Certificate does not exist in HANA", "name", cr.Name, "certName", cr.Spec.ForProvider.Name)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.Status.AtProvider.Certificates = observed.Certificates
	cr.SetConditions(xpv1.Available())

	c.log.Info("Observed certificate resource", "name", cr.Name, "certName", cr.Spec.ForProvider.Name)

	// Certificates cannot be updated in HANA (only dropped and recreated).
	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Certificate)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotCertificate)
	}

	c.log.Info("Creating certificate resource", "name", cr.Name, "certName", cr.Spec.ForProvider.Name)

	cr.SetConditions(xpv1.Creating())

	certPEM, err := c.getCertificatePEM(ctx, cr.Spec.ForProvider.CertificateSecretRef)
	if err != nil {
		return managed.ExternalCreation{}, fmt.Errorf("%s: %w", errGetCertSecret, err)
	}

	if err := c.client.Create(ctx, &cr.Spec.ForProvider, certPEM); err != nil {
		c.log.Info("Error creating certificate", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf("%s: %w", errCreateCert, err)
	}

	c.log.Info("Successfully created certificate resource", "name", cr.Name, "certName", cr.Spec.ForProvider.Name)
	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	// Certificates cannot be altered in HANA; Observe always returns ResourceUpToDate: true
	// so this path is never reached in normal operation.
	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Certificate)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotCertificate)
	}

	c.log.Info("Deleting certificate resource", "name", cr.Name, "certName", cr.Spec.ForProvider.Name)

	cr.SetConditions(xpv1.Deleting())

	if err := c.client.Delete(ctx, &cr.Spec.ForProvider); err != nil {
		c.log.Info("Error deleting certificate", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf("%s: %w", errDeleteCert, err)
	}

	c.log.Info("Successfully deleted certificate resource", "name", cr.Name, "certName", cr.Spec.ForProvider.Name)
	return managed.ExternalDelete{}, nil
}

func (c *external) getCertificatePEM(ctx context.Context, ref *xpv1.SecretKeySelector) ([]byte, error) {
	if ref == nil {
		return nil, errors.New("certificateSecretRef is required")
	}
	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		return nil, fmt.Errorf("cannot get secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	certPEM, ok := secret.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s does not contain key %q", ref.Namespace, ref.Name, ref.Key)
	}
	return certPEM, nil
}
