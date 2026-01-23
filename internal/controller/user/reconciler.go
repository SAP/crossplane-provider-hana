package user

import (
	"context"
	"errors"
	"fmt"
	"slices"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/user"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/crossplane/crossplane-runtime/pkg/controller"
	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
)

const (
	errNotUser                 = "managed resource is not a User custom resource"
	errTrackPCUsage            = "cannot track ProviderConfig usage: %w"
	errGetPC                   = "cannot get ProviderConfig: %w"
	errNoSecretRef             = "ProviderConfig does not reference a credentials Secret"
	errGetPasswordSecretFailed = "cannot get password secret: %w"
	errGetSecret               = "cannot get credentials Secret: %w"
	errKeyNotFound             = "key %s not found in secret %s/%s"

	errSelectUser       = "cannot select user: %w"
	errCreateUser       = "cannot create user: %w"
	errUpdateUser       = "cannot update user: %w"
	errDropUser         = "cannot drop user: %w"
	errFilterPrivileges = "cannot filter privileges: %w"

	msgNotValidSecret = "Object is not a valid secret"
	msgListFailed     = "Failed to list users"
)

// Setup adds a controller that reconciles User managed resources.
func Setup(mgr ctrl.Manager, o controller.Options, db xsql.DB) error {
	name := managed.ControllerName(v1alpha1.UserGroupKind)

	log := o.Logger.WithValues("controller", name)
	t := resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ProviderConfigUsage{})
	r := managed.NewReconciler(mgr,
		resource.ManagedKind(v1alpha1.UserGroupVersionKind),
		managed.WithExternalConnecter(&connector{
			kube:      mgr.GetClient(),
			usage:     t,
			newClient: user.New,
			log:       log,
			db:        db,
		}),
		managed.WithLogger(log),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))))

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		For(&v1alpha1.User{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
				return generateReconcileRequestsFromSecret(ctx, obj, mgr.GetClient(), log)
			})),
		).
		Complete(r)
}

func generateReconcileRequestsFromSecret(ctx context.Context, obj client.Object, kube client.Client, log logging.Logger) []reconcile.Request {
	log.Debug("Enqueueing requests from secret")
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		log.Debug(msgNotValidSecret)
		return []reconcile.Request{}
	}

	users := &v1alpha1.UserList{}
	if err := kube.List(ctx, users); err != nil {
		log.Debug(msgListFailed, "error", err)
		return []reconcile.Request{}
	}

	requests := []reconcile.Request{}
	for _, user := range users.Items {
		if secretRef := user.Spec.ForProvider.Authentication.Password.PasswordSecretRef; secretRef != nil &&
			secretRef.Namespace == secret.GetNamespace() &&
			secretRef.Name == secret.GetName() {
			log.Info("Secret for user changed", "user", user.GetName(), "secret", secret.GetName())
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: user.Name,
				},
			},
			)
		}
	}

	return requests
}

// A connector is expected to produce an ExternalClient when its Connect method
// is called.
type connector struct {
	db        xsql.DB
	kube      client.Client
	usage     resource.Tracker
	newClient func(xsql.DB, string) user.Client
	log       logging.Logger
}

// Connect typically produces an ExternalClient by:
// 1. Tracking that the managed resource is using a ProviderConfig.
// 2. Getting the managed resource's ProviderConfig.
// 3. Getting the credentials specified by the ProviderConfig.
// 4. Using the credentials to form a client.
func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.User)
	if !ok {
		return nil, errors.New(errNotUser)
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

	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		return nil, fmt.Errorf(errGetSecret, err)
	}

	c.log.Debug("Connecting to user resource", "name", cr.Name)

	username := string(secret.Data[xpv1.ResourceCredentialsSecretUserKey])

	if err := c.db.Connect(ctx, secret.Data); err != nil {
		return nil, fmt.Errorf("cannot connect to HANA DB: %w", err)
	}

	return &external{
		client: c.newClient(c.db, username),
		kube:   c.kube,
		log:    c.log,
	}, nil
}

// An ExternalClient observes, then either creates, updates, or deletes an
// external resource to ensure it reflects the managed resource's desired state.
type external struct {
	client user.UserClient
	kube   client.Client
	log    logging.Logger
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.User)
	if !ok {
		c.log.Debug("Managed resource is not a User custom resource", "resource", mg)
		return managed.ExternalObservation{}, errors.New(errNotUser)
	}

	c.log.Debug("Observing user resource", "name", cr.Name)

	parameters := cr.Spec.ForProvider

	defaultPrivilege := fmt.Sprintf("CREATE ANY ON SCHEMA %s", parameters.Username)
	if cr.Spec.PrivilegeManagementPolicy == "strict" &&
		!parameters.RestrictedUser && !slices.Contains(parameters.Privileges, defaultPrivilege) {
		// Append default Privilege
		parameters.Privileges = append(parameters.Privileges, defaultPrivilege)
	}

	// Append default Role
	if !parameters.RestrictedUser && !slices.Contains(parameters.Roles, "PUBLIC") {
		parameters.Roles = append(parameters.Roles, "PUBLIC")
	}

	var err error
	parameters.Privileges, err = privilege.FormatPrivilegeStrings(parameters.Privileges, c.client.GetDefaultSchema())

	if err != nil {
		c.log.Debug("Error converting privileges", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf("cannot convert privileges: %w", err)
	}

	password, err := c.getPassword(ctx, cr)
	if err != nil {
		c.log.Debug("Error getting password for user", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errGetPasswordSecretFailed, err)
	}

	observed, err := c.client.Read(ctx, &parameters, password)

	// Track if we have authentication errors that should set unavailable status
	var authError error
	if err != nil {
		// Handle specific user authentication errors
		if errors.Is(err, user.ErrValidityPeriod) {
			c.log.Info("User validity period error", "name", cr.Name, "error", err)
			authError = err
		} else if errors.Is(err, user.ErrUserDeactivated) {
			c.log.Info("User deactivated error", "name", cr.Name, "error", err)
			authError = err
		} else if errors.Is(err, user.ErrUserLocked) {
			c.log.Info("User locked error", "name", cr.Name, "error", err)
			authError = err
		} else {
			c.log.Debug("Error observing user", "name", cr.Name, "error", err)
			return managed.ExternalObservation{}, fmt.Errorf(errSelectUser, err)
		}
	}

	if observed.Username == nil || *observed.Username != parameters.Username {
		c.log.Info("User does not exist", "name", cr.Name, "username", parameters.Username)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	observed, err = privilege.FilterManagedPrivileges(observed, parameters.Privileges, cr.Status.AtProvider.Privileges, cr.Spec.PrivilegeManagementPolicy, c.client.GetDefaultSchema())

	if err != nil {
		c.log.Debug("Error filtering managed privileges", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errFilterPrivileges, err)
	}

	cr.Status.AtProvider = *observed

	// Set condition based on authentication errors or normal availability
	if authError != nil {
		cr.SetConditions(xpv1.Unavailable().WithMessage(authError.Error()))
	} else {
		cr.SetConditions(xpv1.Available())
	}

	isUpToDate := upToDate(observed, &parameters)

	c.log.Info("Observed user resource",
		"name", cr.Name,
		"username", parameters.Username,
		"upToDate", isUpToDate)

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: isUpToDate,
	}, nil
}

func upToDate(observed *v1alpha1.UserObservation, desired *v1alpha1.UserParameters) bool {
	return observed.PasswordUpToDate != nil &&
		*observed.PasswordUpToDate &&
		observed.Usergroup != nil &&
		*observed.Usergroup == desired.Usergroup &&
		observed.IsPasswordLifetimeCheckEnabled != nil &&
		*observed.IsPasswordLifetimeCheckEnabled == desired.IsPasswordLifetimeCheckEnabled &&
		equalParameterMap(observed.Parameters, desired.Parameters) &&
		equalArrays(observed.Privileges, desired.Privileges) &&
		equalArrays(observed.Roles, desired.Roles)
}

func equalArrays(arr1, arr2 []string) bool {
	set1 := arrayToSet(arr1)
	set2 := arrayToSet(arr2)

	if len(set1) != len(set2) {
		return false
	}

	for item := range set1 {
		if _, found := set2[item]; !found {
			return false
		}
	}

	return true
}

func arrayToSet(arr []string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, item := range arr {
		set[item] = struct{}{}
	}
	return set
}

func equalParameterMap(map1, map2 map[string]string) bool {
	if len(map1) != len(map2) {
		return false
	}
	for key, value1 := range map1 {
		value2, ok := map2[key]
		if !ok || value1 != value2 {
			return false
		}
	}
	return true
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.User)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotUser)
	}

	c.log.Info("Creating user resource", "name", cr.Name, "username", cr.Spec.ForProvider.Username)

	cr.SetConditions(xpv1.Creating())

	parameters := &cr.Spec.ForProvider

	c.log.Debug("Creating user with parameters",
		"username", parameters.Username,
		"restrictedUser", parameters.RestrictedUser,
		"forceFirstPasswordChange", parameters.Authentication.Password.ForceFirstPasswordChange,
		"usergroup", parameters.Usergroup)

	password, pasErr := c.getPassword(ctx, cr)

	if pasErr != nil {
		c.log.Debug("Error getting password for user", "name", cr.Name, "error", pasErr)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateUser, pasErr)
	}

	if err := c.client.Create(ctx, parameters, password); err != nil {
		c.log.Debug("Error creating user", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateUser, err)
	}

	c.log.Info("Successfully created user resource", "name", cr.Name, "username", parameters.Username)

	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{
			"user":     []byte(parameters.Username),
			"password": []byte(password),
		},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.User)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotUser)
	}

	c.log.Info("Updating user resource", "name", cr.Name, "username", cr.Spec.ForProvider.Username)

	desired := buildDesiredParameters(cr)
	observed := buildObservedParameters(cr)

	observed, err := privilege.FilterManagedPrivileges(observed, cr.Spec.ForProvider.Privileges, cr.Status.AtProvider.Privileges, cr.Spec.PrivilegeManagementPolicy, c.client.GetDefaultSchema())

	if err != nil {
		c.log.Debug("Error filtering managed privileges", "name", cr.Name, "error", err)
		return managed.ExternalUpdate{}, fmt.Errorf(errFilterPrivileges, err)
	}

	// Update privileges if needed
	if !equalArrays(observed.Privileges, desired.Privileges) {
		toGrant := stringArrayDifference(desired.Privileges, observed.Privileges)
		toRevoke := stringArrayDifference(observed.Privileges, desired.Privileges)

		c.log.Debug("Updating user privileges",
			"name", cr.Name,
			"username", desired.Username,
			"toGrant", toGrant,
			"toRevoke", toRevoke)

		err1 := updatePrivileges(ctx, c.client, desired.Username, desired.Privileges, observed.Privileges)
		if err1 != nil {
			c.log.Debug("Error updating user privileges", "name", cr.Name, "error", err1)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUser, err1)
		}
		cr.Status.AtProvider.Privileges = desired.Privileges
		c.log.Info("Updated user privileges", "name", cr.Name, "username", desired.Username)
	}

	// Update roles if needed
	if !equalArrays(observed.Roles, desired.Roles) {
		toGrant := stringArrayDifference(desired.Roles, observed.Roles)
		toRevoke := stringArrayDifference(observed.Roles, desired.Roles)

		c.log.Debug("Updating user roles",
			"name", cr.Name,
			"username", desired.Username,
			"toGrant", toGrant,
			"toRevoke", toRevoke)

		err2 := updateRoles(ctx, c.client, desired.Username, desired.Roles, observed.Roles)
		if err2 != nil {
			c.log.Debug("Error updating user roles", "name", cr.Name, "error", err2)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUser, err2)
		}
		cr.Status.AtProvider.Roles = desired.Roles
		c.log.Info("Updated user roles", "name", cr.Name, "username", desired.Username)
	}

	// Update parameters if needed
	if !equalParameterMap(observed.Parameters, desired.Parameters) {
		parametersToSet := compareMaps(desired.Parameters, observed.Parameters)
		parametersToClear := compareMaps(observed.Parameters, desired.Parameters)

		c.log.Debug("Updating user parameters",
			"name", cr.Name,
			"username", desired.Username,
			"parametersToSet", parametersToSet,
			"parametersToClear", parametersToClear)

		err := c.client.UpdateParameters(ctx, desired.Username, parametersToSet, parametersToClear)
		if err != nil {
			c.log.Debug("Error updating user parameters", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUser, err)
		}
		cr.Status.AtProvider.Parameters = desired.Parameters
		c.log.Info("Updated user parameters", "name", cr.Name, "username", desired.Username)
	}

	// Update usergroup if needed
	if observed.Usergroup == nil || *observed.Usergroup != desired.Usergroup {
		c.log.Debug("Updating user usergroup",
			"name", cr.Name,
			"username", desired.Username,
			"current", observed.Usergroup,
			"desired", desired.Usergroup)

		err := c.client.UpdateUsergroup(ctx, desired.Username, desired.Usergroup)
		if err != nil {
			c.log.Debug("Error updating user usergroup", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUser, err)
		}
		cr.Status.AtProvider.Usergroup = &desired.Usergroup
		c.log.Info("Updated user usergroup", "name", cr.Name, "username", desired.Username)
	}

	if observed.IsPasswordLifetimeCheckEnabled == nil || *observed.IsPasswordLifetimeCheckEnabled != desired.IsPasswordLifetimeCheckEnabled {
		c.log.Debug("Updating user password lifetime check",
			"name", cr.Name,
			"username", desired.Username,
			"current", observed.IsPasswordLifetimeCheckEnabled,
			"desired", desired.IsPasswordLifetimeCheckEnabled)
		err := c.client.UpdatePasswordLifetimeCheck(ctx, desired.Username, desired.IsPasswordLifetimeCheckEnabled)
		if err != nil {
			c.log.Debug("Error updating user password lifetime check", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUser, err)
		}
		cr.Status.AtProvider.IsPasswordLifetimeCheckEnabled = &desired.IsPasswordLifetimeCheckEnabled
		c.log.Info("Updated user password lifetime check", "name", cr.Name, "username", desired.Username)
	}

	if cr.Status.AtProvider.PasswordUpToDate == nil || !*cr.Status.AtProvider.PasswordUpToDate {
		c.log.Debug("Updating user password", "name", cr.Name, "username", desired.Username)
		password, err := c.getPassword(ctx, cr)
		if err != nil {
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUser, err)
		}
		err = c.client.UpdatePassword(ctx, desired.Username, password, desired.Authentication.Password.ForceFirstPasswordChange)
		if err != nil {
			c.log.Debug("Error updating user password", "name", cr.Name, "error", err)
			return managed.ExternalUpdate{}, fmt.Errorf(errUpdateUser, err)
		}
		upToDate := true
		cr.Status.AtProvider.PasswordUpToDate = &upToDate
		c.log.Info("Updated user password", "name", cr.Name, "username", desired.Username)
	}

	c.log.Info("Successfully updated user resource", "name", cr.Name, "username", desired.Username)
	return managed.ExternalUpdate{}, nil
}

func buildDesiredParameters(cr *v1alpha1.User) *v1alpha1.UserParameters {
	parameters := cr.Spec.ForProvider.DeepCopy()

	defaultPrivilege := fmt.Sprintf("CREATE ANY ON SCHEMA %s", parameters.Username)
	if cr.Spec.PrivilegeManagementPolicy == "strict" &&
		!parameters.RestrictedUser && !slices.Contains(parameters.Privileges, defaultPrivilege) {
		// Append default Privilege
		parameters.Privileges = append(parameters.Privileges, defaultPrivilege)

	}

	// Append default Role
	if !parameters.RestrictedUser && !slices.Contains(parameters.Roles, "PUBLIC") {
		parameters.Roles = append(parameters.Roles, "PUBLIC")
	}
	return parameters
}

func buildObservedParameters(cr *v1alpha1.User) *v1alpha1.UserObservation {
	return cr.Status.AtProvider.DeepCopy()
}

func updatePrivileges(ctx context.Context, client user.UserClient, grantee string, desired, observed []string) error {
	if !equalArrays(observed, desired) {
		toGrant := stringArrayDifference(desired, observed)
		toRevoke := stringArrayDifference(observed, desired)
		err := client.UpdatePrivileges(ctx, grantee, toGrant, toRevoke)
		if err != nil {
			return err
		}
	}
	return nil
}

func updateRoles(ctx context.Context, client user.UserClient, grantee string, desired, observed []string) error {
	if !equalArrays(observed, desired) {
		toGrant := stringArrayDifference(desired, observed)
		toRevoke := stringArrayDifference(observed, desired)
		err := client.UpdateRoles(ctx, grantee, toGrant, toRevoke)
		if err != nil {
			return err
		}
	}
	return nil
}

func stringArrayDifference(arr1, arr2 []string) []string {
	set := make(map[string]struct{}, len(arr2))

	for _, item := range arr2 {
		set[item] = struct{}{}
	}

	var difference []string

	for _, item := range arr1 {
		if _, found := set[item]; !found {
			difference = append(difference, item)
		}
	}

	return difference
}

func compareMaps(map1, map2 map[string]string) map[string]string {
	differenceMap := make(map[string]string)

	for key, val1 := range map1 {
		if val2, ok := map2[key]; !ok || val2 != val1 {
			differenceMap[key] = val1
		}
	}

	return differenceMap
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.User)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotUser)
	}

	c.log.Info("Deleting user resource", "name", cr.Name, "username", cr.Spec.ForProvider.Username)

	parameters := &v1alpha1.UserParameters{
		Username: cr.Spec.ForProvider.Username,
	}

	cr.SetConditions(xpv1.Deleting())

	err := c.client.Delete(ctx, parameters)

	if err != nil {
		c.log.Debug("Error deleting user", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf(errDropUser, err)
	}

	c.log.Info("Successfully deleted user resource", "name", cr.Name, "username", parameters.Username)
	return managed.ExternalDelete{}, err
}

func (c *external) getPassword(ctx context.Context, user *v1alpha1.User) (newPwd string, err error) {
	if user.Spec.ForProvider.Authentication.Password.PasswordSecretRef == nil {
		c.log.Info("Warning: PasswordSecretRef is nil, using empty password", "name", user.Name)
		return "", nil
	}
	nn := types.NamespacedName{
		Name:      user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Name,
		Namespace: user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Namespace,
	}
	currentSecret := &corev1.Secret{}
	if err := c.kube.Get(ctx, nn, currentSecret); err != nil {
		c.log.Debug("Error getting password secret", "name", nn.Name, "namespace", nn.Namespace, "error", err)
		return "", fmt.Errorf(errGetPasswordSecretFailed, err)
	}
	newPwdBytes, ok := currentSecret.Data[user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Key]
	if !ok {
		c.log.Debug("Password key not found in secret", "key", user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Key, "name", nn.Name, "namespace", nn.Namespace)
		return "", fmt.Errorf(errKeyNotFound, user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Key, nn.Namespace, nn.Name)
	}
	newPwd = string(newPwdBytes)

	c.log.Debug("Got password", "name", nn.Name, "namespace", nn.Namespace)

	return newPwd, nil
}
