/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package role

import (
	"context"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/role"

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
	errNotRole      = "managed resource is not a Role custom resource"
	errTrackPCUsage = "cannot track ProviderConfig usage: %w"
	errGetPC        = "cannot get ProviderConfig: %w"
	errNoSecretRef  = "ProviderConfig does not reference a credentials Secret"
	errGetSecret    = "cannot get credentials Secret: %w"

	errSelectRole = "cannot select role: %w"
	errCreateRole = "cannot create role: %w"
	errUpdateRole = "cannot update role: %w"
	errDropRole   = "cannot drop role: %w"
)

// Setup adds a controller that reconciles Role managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.Connector) error {
	name := managed.ControllerName(v1alpha1.RoleGroupKind)

	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.RoleGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: role.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(log),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
		features.ConfigureBetaManagementPolicies(o))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.Role{}).
		Complete(r)
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	kube      client.Client
	usage     resource.Tracker
	newClient func(db xsql.DB, username string) role.Client
	log       logging.Logger
	db        xsql.Connector
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.Role)
	if !ok {
		return nil, errors.New(errNotRole)
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

	c.log.Info("Connecting to role resource", "name", cr.Name)

	username := string(s.Data[xpv1.ResourceCredentialsSecretUserKey])

	conn, err := c.db.Connect(ctx, s.Data)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to HANA DB: %w", err)
	}

	return &external{
		client: c.newClient(conn, username),
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
	client role.RoleClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.Role)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotRole)
	}

	c.log.Info("Observing role resource", "name", cr.Name)

	parameters := buildDesiredParameters(cr)

	// Normalize the desired privileges/roles to the same canonical form the
	// catalog read returns, so upToDate compares like-for-like (see
	// normalizeDesired). Without this an unquoted spec value never matches the
	// quoted observed value and the controller re-grants every reconcile.
	if err := c.normalizeDesired(parameters); err != nil {
		c.log.Info("Error normalizing desired role parameters", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errSelectRole, err)
	}

	observed, err := c.client.Read(ctx, parameters)

	if err != nil {
		c.log.Info("Error observing role", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errSelectRole, err)
	}

	if observed.RoleName != parameters.RoleName {
		c.log.Info("Role does not exist", "name", cr.Name, "roleName", parameters.RoleName)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	cr.Status.AtProvider.RoleName = observed.RoleName
	cr.Status.AtProvider.Schema = observed.Schema
	cr.Status.AtProvider.Privileges = observed.Privileges
	cr.Status.AtProvider.Roles = observed.Roles
	cr.Status.AtProvider.LdapGroups = observed.LdapGroups
	cr.Status.AtProvider.Rolegroup = observed.Rolegroup

	// A role name mistakenly placed under spec.forProvider.privileges can never
	// reconcile: it is granted via GRANT ROLE and read back from GRANTED_ROLES,
	// so it never appears in the observed privileges and the controller re-grants
	// it every reconcile (grant thrash). Return an error rather than setting the
	// condition inline and returning nil: crossplane-runtime overwrites the Synced
	// condition with ReconcileSuccess on a nil-error, up-to-date Observe (and emits
	// no event), so a manually-set ReconcileError would not survive. Returning the
	// error makes the runtime record ReconcileError, emit a Warning event, and skip
	// the Update, surfacing the misconfiguration to the operator.
	if overlap := privilege.FindPrivilegeRoleOverlap(parameters.Privileges, observed.Roles); len(overlap) > 0 {
		c.log.Info("Misconfigured privileges detected", "name", cr.Name, "overlap", overlap)
		return managed.ExternalObservation{}, fmt.Errorf(
			"privileges contains role name(s) that must be moved to spec.forProvider.roles: %v", overlap)
	}

	cr.SetConditions(xpv1.Available())

	isUpToDate := upToDate(observed, parameters)
	c.log.Info("Observed role resource",
		"name", cr.Name,
		"roleName", parameters.RoleName,
		"upToDate", isUpToDate)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isUpToDate,
	}, nil
}

func upToDate(observed *v1alpha1.RoleObservation, desired *v1alpha1.RoleParameters) bool {
	if !utils.ArraysEqual(observed.Privileges, desired.Privileges) {
		return false
	}
	if !utils.ArraysEqual(observed.Roles, desired.Roles) {
		return false
	}
	if !utils.ArraysEqual(observed.LdapGroups, desired.LdapGroups) {
		return false
	}
	if observed.Rolegroup != desired.Rolegroup {
		return false
	}
	return true
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Role)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRole)
	}

	c.log.Info("Creating role resource", "name", cr.Name, "roleName", cr.Spec.ForProvider.RoleName)

	cr.SetConditions(xpv1.Creating())

	parameters := buildDesiredParameters(cr)

	c.log.Info("Creating role with parameters",
		"roleName", parameters.RoleName,
		"schema", parameters.Schema,
		"privileges", parameters.Privileges,
		"roles", parameters.Roles,
		"ldapGroups", parameters.LdapGroups,
		"noGrantToCreator", parameters.NoGrantToCreator)

	err := c.client.Create(ctx, parameters)

	if err != nil {
		c.log.Info("Error creating role", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateRole, err)
	}

	// Note: status.atProvider is intentionally NOT written here. crossplane-runtime
	// calls Observe immediately after a successful Create, and Observe populates every
	// atProvider field from the real DB read. Stamping desired (spec) values here would
	// make status reflect intent rather than observed state, masking real drift.
	c.log.Info("Successfully created role resource", "name", cr.Name, "roleName", parameters.RoleName)
	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.Role)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotRole)
	}

	c.log.Info("Updating role resource", "name", cr.Name, "roleName", cr.Spec.ForProvider.RoleName)

	parameters := buildDesiredParameters(cr)

	// Same normalization as Observe, so the desired side matches the observed
	// (catalog-canonical) values stored in status.atProvider and the grant/revoke
	// diffs are computed like-for-like.
	if err := c.normalizeDesired(parameters); err != nil {
		c.log.Info("Error normalizing desired role parameters", "name", cr.Name, "error", err)
		return managed.ExternalUpdate{}, fmt.Errorf(errUpdateRole, err)
	}

	observedLdapGroups := cr.Status.AtProvider.LdapGroups
	observedPrivileges := cr.Status.AtProvider.Privileges
	observedRoles := cr.Status.AtProvider.Roles

	if err := c.applyArrayUpdate(cr, "LDAP groups", parameters.LdapGroups, observedLdapGroups,
		func(toAdd, toRemove []string) error {
			return c.client.UpdateLdapGroups(ctx, parameters, toAdd, toRemove)
		}); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if err := c.applyArrayUpdate(cr, "privileges", parameters.Privileges, observedPrivileges,
		func(toAdd, toRemove []string) error {
			return c.client.UpdatePrivileges(ctx, parameters, toAdd, toRemove)
		}); err != nil {
		return managed.ExternalUpdate{}, err
	}

	// Roles granted TO this role (e.g. HDI container roles like
	// "CONTAINER"."ns::reader") live in the GRANTED_ROLES catalog and are
	// managed via GRANT ROLE / REVOKE ROLE. They are strictly separate from
	// direct privileges (GRANTED_PRIVILEGES) — a role granted to a role
	// never appears in the privileges list — so a dedicated diff is required.
	if err := c.applyArrayUpdate(cr, "roles", parameters.Roles, observedRoles,
		func(toAdd, toRemove []string) error {
			return c.client.UpdateRoles(ctx, parameters, toAdd, toRemove)
		}); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if cr.Status.AtProvider.Rolegroup != parameters.Rolegroup {
		c.log.Info("Updating role rolegroup",
			"name", cr.Name,
			"roleName", parameters.RoleName,
			"from", cr.Status.AtProvider.Rolegroup,
			"to", parameters.Rolegroup)

		err := c.client.UpdateRolegroup(ctx, parameters)
		if err != nil {
			c.log.Info("Error updating role rolegroup", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateRole, err)
		}
		c.log.Info("Updated role rolegroup", "name", cr.Name, "roleName", parameters.RoleName)
	}

	c.log.Info("Successfully updated role resource", "name", cr.Name, "roleName", parameters.RoleName)
	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Role)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotRole)
	}

	c.log.Info("Deleting role resource", "name", cr.Name, "roleName", cr.Spec.ForProvider.RoleName)

	parameters := buildDesiredParameters(cr)

	cr.SetConditions(xpv1.Deleting())

	err := c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Debug("Error deleting role", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf(errDropRole, err)
	}

	c.log.Info("Successfully deleted role resource", "name", cr.Name, "roleName", parameters.RoleName)
	return managed.ExternalDelete{}, err
}

// applyArrayUpdate diffs a desired vs observed string slice and, when they
// differ, invokes apply with the entries to add and remove. It centralizes the
// diff/log/error-wrap boilerplate shared by the LDAP-group, privilege and role
// updates so Update stays flat. A no-op (already equal) returns nil without
// calling apply.
func (c *external) applyArrayUpdate(cr *v1alpha1.Role, kind string, desired, observed []string, apply func(toAdd, toRemove []string) error) error {
	isEqual, toAdd, toRemove := utils.ArraysBothDiff(desired, observed)
	if isEqual {
		return nil
	}
	c.log.Info("Updating role "+kind,
		"name", cr.Name,
		"roleName", cr.Spec.ForProvider.RoleName,
		"toAdd", toAdd,
		"toRemove", toRemove)
	if err := apply(toAdd, toRemove); err != nil {
		c.log.Info("Error updating role "+kind, "name", cr.Name, "error", err)
		return fmt.Errorf(errUpdateRole, err)
	}
	c.log.Info("Updated role "+kind, "name", cr.Name, "roleName", cr.Spec.ForProvider.RoleName)
	return nil
}

// normalizeDesired rewrites the desired privileges/roles into the same
// canonical, quoted form the catalog read returns, so upToDate and the Update
// diff compare like-for-like. QueryRoles/QueryPrivileges emit quoted
// identifiers, whereas a user writes the spec unquoted — without this they
// never compare equal and the controller re-grants every reconcile (grant
// thrash). Mirrors the user controller; only the in-memory comparison copy is
// changed (spec and status are untouched).
func (c *external) normalizeDesired(parameters *v1alpha1.RoleParameters) error {
	var err error
	if parameters.Privileges, err = privilege.FormatPrivilegeStrings(parameters.Privileges, c.client.GetDefaultSchema()); err != nil {
		return fmt.Errorf("cannot convert privileges: %w", err)
	}
	if parameters.Roles, err = privilege.FormatRoleStrings(parameters.Roles); err != nil {
		return fmt.Errorf("cannot convert roles: %w", err)
	}
	return nil
}

// buildDesiredParameters constructs the desired role parameters from the CR spec.
// Note: We preserve the original case for all fields because:
// - RoleName/Schema: HANA uses double-quoted identifiers which preserve case
// - Privileges: May contain schema/object names that are case-sensitive
// - LdapGroups: LDAP Distinguished Names are case-sensitive
func buildDesiredParameters(cr *v1alpha1.Role) *v1alpha1.RoleParameters {
	return &v1alpha1.RoleParameters{
		RoleName:         cr.Spec.ForProvider.RoleName,
		Schema:           cr.Spec.ForProvider.Schema,
		Privileges:       cr.Spec.ForProvider.Privileges,
		Roles:            cr.Spec.ForProvider.Roles,
		LdapGroups:       cr.Spec.ForProvider.LdapGroups,
		NoGrantToCreator: cr.Spec.ForProvider.NoGrantToCreator,
		Rolegroup:        cr.Spec.ForProvider.Rolegroup,
	}
}
