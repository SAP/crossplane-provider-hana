/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package role

import (
	"context"
	"reflect"
	"strings"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"

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
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
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
	db        xsql.DB
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

	if err := c.db.Connect(ctx, s.Data); err != nil {
		return nil, fmt.Errorf("cannot connect to HANA DB: %w", err)
	}

	return &external{
		client: c.newClient(c.db, username),
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

	parameters := &v1alpha1.RoleParameters{
		RoleName:   strings.ToUpper(cr.Spec.ForProvider.RoleName),
		Schema:     strings.ToUpper(cr.Spec.ForProvider.Schema),
		Privileges: arrayToUpper(cr.Spec.ForProvider.Privileges),
		LdapGroups: arrayToUpper(cr.Spec.ForProvider.LdapGroups),
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
	cr.Status.AtProvider.LdapGroups = observed.LdapGroups

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
	if !equalArrays(observed.Privileges, desired.Privileges) {
		return false
	}
	if !equalArrays(observed.LdapGroups, desired.LdapGroups) {
		return false
	}
	return true
}

func equalArrays(arr1, arr2 []string) bool {
	if len(arr1) != len(arr2) {
		return false
	}

	set1 := arrayToSet(arr1)
	set2 := arrayToSet(arr2)

	return reflect.DeepEqual(set1, set2)
}

func arrayToSet(arr []string) map[string]bool {
	set := make(map[string]bool)
	for _, item := range arr {
		set[item] = true
	}
	return set
}

func arrayToUpper(arr []string) []string {
	upperArr := make([]string, len(arr))
	for i, item := range arr {
		upperArr[i] = strings.ToUpper(item)
	}
	return upperArr
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.Role)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRole)
	}

	c.log.Info("Creating role resource", "name", cr.Name, "roleName", cr.Spec.ForProvider.RoleName)

	cr.SetConditions(xpv1.Creating())

	parameters := &v1alpha1.RoleParameters{
		RoleName:         cr.Spec.ForProvider.RoleName,
		Schema:           cr.Spec.ForProvider.Schema,
		Privileges:       cr.Spec.ForProvider.Privileges,
		LdapGroups:       cr.Spec.ForProvider.LdapGroups,
		NoGrantToCreator: cr.Spec.ForProvider.NoGrantToCreator,
	}

	c.log.Info("Creating role with parameters",
		"roleName", parameters.RoleName,
		"schema", parameters.Schema,
		"privileges", parameters.Privileges,
		"ldapGroups", parameters.LdapGroups,
		"noGrantToCreator", parameters.NoGrantToCreator)

	err := c.client.Create(ctx, parameters)

	if err != nil {
		c.log.Info("Error creating role", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateRole, err)
	}

	cr.Status.AtProvider.RoleName = parameters.RoleName
	cr.Status.AtProvider.Schema = parameters.Schema
	cr.Status.AtProvider.Privileges = parameters.Privileges
	cr.Status.AtProvider.LdapGroups = parameters.LdapGroups

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

	parameters := &v1alpha1.RoleParameters{
		RoleName:   strings.ToUpper(cr.Spec.ForProvider.RoleName),
		Schema:     strings.ToUpper(cr.Spec.ForProvider.Schema),
		Privileges: arrayToUpper(cr.Spec.ForProvider.Privileges),
		LdapGroups: arrayToUpper(cr.Spec.ForProvider.LdapGroups),
	}

	observedLdapGroups := cr.Status.AtProvider.LdapGroups
	desiredLdapGroups := parameters.LdapGroups

	observedPrivileges := cr.Status.AtProvider.Privileges
	desiredPrivileges := parameters.Privileges

	if !equalArrays(observedLdapGroups, desiredLdapGroups) {
		groupsToAdd := stringArrayDifference(desiredLdapGroups, observedLdapGroups)
		groupsToRemove := stringArrayDifference(observedLdapGroups, desiredLdapGroups)

		c.log.Info("Updating role LDAP groups",
			"name", cr.Name,
			"roleName", parameters.RoleName,
			"groupsToAdd", groupsToAdd,
			"groupsToRemove", groupsToRemove)

		err := c.client.UpdateLdapGroups(ctx, parameters, groupsToAdd, groupsToRemove)
		if err != nil {
			c.log.Info("Error updating role LDAP groups", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateRole, err)
		}
		cr.Status.AtProvider.LdapGroups = parameters.LdapGroups
		c.log.Info("Updated role LDAP groups", "name", cr.Name, "roleName", parameters.RoleName)
	}

	if !equalArrays(observedPrivileges, desiredPrivileges) {
		privilegesToAdd := stringArrayDifference(desiredPrivileges, observedPrivileges)
		privilegesToRemove := stringArrayDifference(observedPrivileges, desiredPrivileges)

		c.log.Info("Updating role privileges",
			"name", cr.Name,
			"roleName", parameters.RoleName,
			"privilegesToAdd", privilegesToAdd,
			"privilegesToRemove", privilegesToRemove)

		err := c.client.UpdatePrivileges(ctx, parameters, privilegesToAdd, privilegesToRemove)
		if err != nil {
			c.log.Info("Error updating role privileges", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateRole, err)
		}
		cr.Status.AtProvider.Privileges = parameters.Privileges
		c.log.Info("Updated role privileges", "name", cr.Name, "roleName", parameters.RoleName)
	}

	c.log.Info("Successfully updated role resource", "name", cr.Name, "roleName", parameters.RoleName)
	return managed.ExternalUpdate{}, nil
}

func stringArrayDifference(arr1, arr2 []string) []string {
	set := make(map[string]bool)

	for _, item := range arr2 {
		set[item] = true
	}

	var difference []string

	for _, item := range arr1 {
		if _, found := set[item]; !found {
			difference = append(difference, item)
		}
	}

	return difference
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.Role)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotRole)
	}

	c.log.Info("Deleting role resource", "name", cr.Name, "roleName", cr.Spec.ForProvider.RoleName)

	parameters := &v1alpha1.RoleParameters{
		RoleName: cr.Spec.ForProvider.RoleName,
		Schema:   cr.Spec.ForProvider.Schema,
	}

	cr.SetConditions(xpv1.Deleting())

	err := c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Info("Error deleting role", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf(errDropRole, err)
	}

	c.log.Info("Successfully deleted role resource", "name", cr.Name, "roleName", parameters.RoleName)
	return managed.ExternalDelete{}, err
}
