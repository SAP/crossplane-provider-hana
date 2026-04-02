/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package dbschema

import (
	"context"
	"errors"
	"fmt"
	"strings"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/pkg/connection"
	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/dbschema"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"

	"github.com/SAP/crossplane-provider-hana/apis/schema/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/controller/features"
)

const (
	errNotDbSchema  = "managed resource is not a Dbschema custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage: %w"
	errGetPC        = "cannot get ProviderConfig: %w"
	errGetCreds     = "cannot get credentials: %w"
	errGetSecret    = "cannot get credentials Secret: %w"
	errNoSecretRef  = "ProviderConfig does not reference a credentials Secret"
	errNewClient    = "cannot create new Service: %w"
	errSelectSchema = "cannot select schema: %w"
	errCreateSchema = "cannot create schema: %w"
	errDropSchema   = "cannot drop schema: %w"
)

// A NoOpService does nothing.
type NoOpService struct{}

// Setup adds a controller that reconciles Dbschema managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	name := managed.ControllerName(v1alpha1.DbSchemaGroupKind)

	cps := []managed.ConnectionPublisher{managed.NewAPISecretPublisher(mgr.GetClient(), mgr.GetScheme())}
	if o.Features.Enabled(features.EnableAlphaExternalSecretStores) {
		cps = append(cps, connection.NewDetailsManager(mgr.GetClient(), apisv1alpha1.StoreConfigGroupVersionKind))
	}

	log := o.Logger.WithValues("controller", name)
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.DbSchemaGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{}),
			newClient: dbschema.New,
			log:       log,
			db:        db}),
		managed.WithLogger(log),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		managed.WithConnectionPublishers(cps...),
		features.ConfigureBetaManagementPolicies(o))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.DbSchema{}).
		Complete(r)
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB) dbschema.Client
	log       logging.Logger
	db        xsql.DB
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.DbSchema)
	if !ok {
		return nil, errors.New(errNotDbSchema)
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

	c.log.Info("Connecting to dbschema resource", "name", cr.Name)

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
	client dbschema.DbSchemaClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.DbSchema)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotDbSchema)
	}

	c.log.Info("Observing dbschema resource", "name", cr.Name)

	parameters := &v1alpha1.DbSchemaParameters{
		SchemaName: strings.ToUpper(cr.Spec.ForProvider.SchemaName),
	}

	observed, err := c.client.Read(ctx, parameters)

	if err != nil {
		c.log.Info("Error observing dbschema", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errSelectSchema, err)
	}

	if observed.SchemaName != parameters.SchemaName {
		c.log.Info("DbSchema does not exist", "name", cr.Name, "schemaName", parameters.SchemaName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.SetConditions(xpv1.Available())

	c.log.Info("Observed dbschema resource",
		"name", cr.Name,
		"schemaName", parameters.SchemaName,
		"upToDate", true)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.DbSchema)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotDbSchema)
	}

	c.log.Info("Creating dbschema resource", "name", cr.Name, "schemaName", cr.Spec.ForProvider.SchemaName)

	parameters := &v1alpha1.DbSchemaParameters{
		SchemaName: cr.Spec.ForProvider.SchemaName,
		Owner:      cr.Spec.ForProvider.Owner,
	}

	c.log.Info("Creating dbschema with parameters",
		"schemaName", parameters.SchemaName,
		"owner", parameters.Owner)

	cr.SetConditions(xpv1.Creating())

	err := c.client.Create(ctx, parameters)

	if err != nil {
		c.log.Info("Error creating dbschema", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateSchema, err)
	}

	c.log.Info("Successfully created dbschema resource", "name", cr.Name, "schemaName", parameters.SchemaName)
	return managed.ExternalCreation{}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.DbSchema)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotDbSchema)
	}

	c.log.Info("Updating dbschema resource", "name", cr.Name, "schemaName", cr.Spec.ForProvider.SchemaName)

	// Replace the fmt.Printf with proper logging
	c.log.Info("Update details", "resource", cr)

	c.log.Info("Successfully updated dbschema resource", "name", cr.Name, "schemaName", cr.Spec.ForProvider.SchemaName)
	return managed.ExternalUpdate{
		// Optionally return any details that may be required to connect to the
		// external resource. These will be stored as the connection secret.
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.DbSchema)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotDbSchema)
	}

	c.log.Info("Deleting dbschema resource", "name", cr.Name, "schemaName", cr.Spec.ForProvider.SchemaName)

	parameters := &v1alpha1.DbSchemaParameters{
		SchemaName: cr.Spec.ForProvider.SchemaName,
	}

	cr.SetConditions(xpv1.Deleting())

	err := c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Info("Error deleting dbschema", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf(errDropSchema, err)
	}

	c.log.Info("Successfully deleted dbschema resource", "name", cr.Name, "schemaName", parameters.SchemaName)
	return managed.ExternalDelete{}, err
}
