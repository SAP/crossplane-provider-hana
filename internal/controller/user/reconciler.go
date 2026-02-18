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
	"github.com/SAP/crossplane-provider-hana/internal/utils"

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
	log.Info("Enqueueing requests from secret")
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		log.Info(msgNotValidSecret)
		return []reconcile.Request{}
	}

	users := &v1alpha1.UserList{}
	if err := kube.List(ctx, users); err != nil {
		log.Info(msgListFailed, "error", err)
		return []reconcile.Request{}
	}

	requests := []reconcile.Request{}
	for _, user := range users.Items {
		if passwordObj := user.Spec.ForProvider.Authentication.Password; passwordObj != nil {
			if secretRef := passwordObj.PasswordSecretRef; secretRef != nil &&
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

	c.log.Info("Connecting to user resource", "name", cr.Name)

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
		c.log.Info("Managed resource is not a User custom resource", "resource", mg)
		return managed.ExternalObservation{}, errors.New(errNotUser)
	}

	c.log.Info("Observing user resource", "name", cr.Name)

	parameters := handleDefaults(cr)

	var err error
	parameters.Privileges, err = privilege.FormatPrivilegeStrings(parameters.Privileges, c.client.GetDefaultSchema())

	if err != nil {
		c.log.Info("Error converting privileges", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf("cannot convert privileges: %w", err)
	}

	password, err := c.getPassword(ctx, cr)
	if err != nil {
		c.log.Info("Error getting password for user", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errGetPasswordSecretFailed, err)
	}

	observed, err := c.client.Read(ctx, parameters, password)

	// Track if we have authentication errors that should set unavailable status
	errIsUnknown, authError := handleAuthError(cr, c.log, err)
	if errIsUnknown {
		return managed.ExternalObservation{}, authError
	}

	if observed.Username == nil || *observed.Username != parameters.Username {
		c.log.Info("User does not exist", "name", cr.Name, "username", parameters.Username)
		return managed.ExternalObservation{ResourceExists: false}, nil
	}

	observed, err = privilege.FilterManagedPrivileges(observed, parameters.Privileges, cr.Status.AtProvider.Privileges, cr.Spec.PrivilegeManagementPolicy, c.client.GetDefaultSchema())

	if err != nil {
		c.log.Info("Error filtering managed privileges", "name", cr.Name, "error", err)
		return managed.ExternalObservation{}, fmt.Errorf(errFilterPrivileges, err)
	}

	cr.Status.AtProvider = *observed

	// Set condition based on authentication errors or normal availability
	if authError != nil {
		cr.SetConditions(xpv1.Unavailable().WithMessage(authError.Error()))
	} else {
		cr.SetConditions(xpv1.Available())
	}

	isUpToDate := upToDate(observed, parameters)

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
	return isPasswordUpToDate(observed, desired) &&
		isX509MappingsUpToDate(observed, desired) &&
		observed.Usergroup != nil &&
		*observed.Usergroup == desired.Usergroup &&
		observed.IsPasswordLifetimeCheckEnabled != nil &&
		*observed.IsPasswordLifetimeCheckEnabled == desired.IsPasswordLifetimeCheckEnabled &&
		utils.MapsEqual(observed.Parameters, desired.Parameters) &&
		utils.ArraysEqual(observed.Privileges, desired.Privileges) &&
		utils.ArraysEqual(observed.Roles, desired.Roles)
}

func isPasswordUpToDate(observed *v1alpha1.UserObservation, desired *v1alpha1.UserParameters) bool {
	if desired.Authentication.Password != nil {
		return observed.PasswordUpToDate != nil && *observed.PasswordUpToDate
	}
	return observed.PasswordUpToDate == nil
}

func isX509MappingsUpToDate(observed *v1alpha1.UserObservation, desired *v1alpha1.UserParameters) bool {
	if desired.Authentication.X509Providers != nil {
		return utils.ArraysEqual(observed.X509Providers, desired.Authentication.X509Providers)
	}
	return len(observed.X509Providers) == 0
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.User)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotUser)
	}

	c.log.Info("Creating user resource", "name", cr.Name, "username", cr.Spec.ForProvider.Username)

	cr.SetConditions(xpv1.Creating())

	parameters := &cr.Spec.ForProvider

	c.log.Info("Creating user with parameters",
		"username", parameters.Username,
		"restrictedUser", parameters.RestrictedUser,
		"usergroup", parameters.Usergroup)

	password, pasErr := c.getPassword(ctx, cr)

	if pasErr != nil {
		c.log.Info("Error getting password for user", "name", cr.Name, "error", pasErr)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateUser, pasErr)
	}

	// Get resolved X509 providers for user creation
	providersToAdd, err := c.ResolveUserMappings(ctx, parameters.Authentication.X509Providers, cr.GetNamespace())
	if err != nil {
		c.log.Info("Error resolving user X.509 providers", "name", cr.Name, "error", err)
		return managed.ExternalCreation{}, fmt.Errorf(errCreateUser, err)
	}

	if err := c.client.Create(ctx, parameters, password, providersToAdd); err != nil {
		c.log.Info("Error creating user", "name", cr.Name, "error", err)
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

	desired := c.buildDesiredParameters(cr)
	observed := c.buildObservedParameters(cr)

	observed, err := privilege.FilterManagedPrivileges(observed, cr.Spec.ForProvider.Privileges, cr.Status.AtProvider.Privileges, cr.Spec.PrivilegeManagementPolicy, c.client.GetDefaultSchema())

	if err != nil {
		c.log.Info("Error filtering managed privileges", "name", cr.Name, "error", err)
		return managed.ExternalUpdate{}, fmt.Errorf(errFilterPrivileges, err)
	}

	if err := c.updatePrivileges(ctx, cr, desired, observed); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if err := c.updateRoles(ctx, cr, desired, observed); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if err := c.updateParameters(ctx, cr, desired, observed); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if err := c.updateUsergroup(ctx, cr, desired, observed); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if err := c.updateX509Providers(ctx, cr, desired, observed); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if err := c.updatePasswordLifetimeCheck(ctx, cr, desired, observed); err != nil {
		return managed.ExternalUpdate{}, err
	}

	if err := c.updatePassword(ctx, cr, desired); err != nil {
		return managed.ExternalUpdate{}, err
	}

	c.log.Info("Successfully updated user resource", "name", cr.Name, "username", desired.Username)
	return managed.ExternalUpdate{}, nil
}

func (c *external) updatePrivileges(ctx context.Context, cr *v1alpha1.User, desired *v1alpha1.UserParameters, observed *v1alpha1.UserObservation) error {
	// Update privileges if needed
	if isEqual, toGrant, toRevoke := utils.ArraysBothDiff(desired.Privileges, observed.Privileges); !isEqual {
		c.log.Info("Updating user privileges",
			"name", cr.Name,
			"username", desired.Username,
			"toGrant", toGrant,
			"toRevoke", toRevoke)

		err := c.client.UpdatePrivileges(ctx, desired.Username, toGrant, toRevoke)
		if err != nil {
			c.log.Info("Error updating user privileges", "name", cr.Name, "error", err)
			return fmt.Errorf(errUpdateUser, err)
		}

		cr.Status.AtProvider.Privileges = desired.Privileges
		c.log.Info("Updated user privileges", "name", cr.Name, "username", desired.Username)
	}
	return nil
}

func (c *external) updateRoles(ctx context.Context, cr *v1alpha1.User, desired *v1alpha1.UserParameters, observed *v1alpha1.UserObservation) error {
	// Update roles if needed
	if isEqual, toGrant, toRevoke := utils.ArraysBothDiff(desired.Roles, observed.Roles); !isEqual {
		c.log.Info("Updating user roles",
			"name", cr.Name,
			"username", desired.Username,
			"toGrant", toGrant,
			"toRevoke", toRevoke)

		err := c.client.UpdateRoles(ctx, desired.Username, toGrant, toRevoke)
		if err != nil {
			c.log.Info("Error updating user roles", "name", cr.Name, "error", err)
			return fmt.Errorf(errUpdateUser, err)
		}

		cr.Status.AtProvider.Roles = desired.Roles
		c.log.Info("Updated user roles", "name", cr.Name, "username", desired.Username)
	}

	return nil
}

func (c *external) updateParameters(ctx context.Context, cr *v1alpha1.User, desired *v1alpha1.UserParameters, observed *v1alpha1.UserObservation) error {
	// Update parameters if needed
	if isEqual, parametersToSet, parametersToClear := utils.MapsBothDiff(desired.Parameters, observed.Parameters); !isEqual {
		c.log.Info("Updating user parameters",
			"name", cr.Name,
			"username", desired.Username,
			"parametersToSet", parametersToSet,
			"parametersToClear", parametersToClear)

		err := c.client.UpdateParameters(ctx, desired.Username, parametersToSet, parametersToClear)
		if err != nil {
			c.log.Info("Error updating user parameters", "name", cr.Name, "error", err)
			return fmt.Errorf(errUpdateUser, err)
		}
		cr.Status.AtProvider.Parameters = desired.Parameters
		c.log.Info("Updated user parameters", "name", cr.Name, "username", desired.Username)
	}
	return nil
}

func (c *external) updateUsergroup(ctx context.Context, cr *v1alpha1.User, desired *v1alpha1.UserParameters, observed *v1alpha1.UserObservation) error {
	// Update usergroup if needed
	if observed.Usergroup == nil || *observed.Usergroup != desired.Usergroup {
		c.log.Info("Updating user usergroup",
			"name", cr.Name,
			"username", desired.Username,
			"current", observed.Usergroup,
			"desired", desired.Usergroup)

		err := c.client.UpdateUsergroup(ctx, desired.Username, desired.Usergroup)
		if err != nil {
			c.log.Info("Error updating user usergroup", "name", cr.Name, "error", err)
			return fmt.Errorf(errUpdateUser, err)
		}
		cr.Status.AtProvider.Usergroup = &desired.Usergroup
		c.log.Info("Updated user usergroup", "name", cr.Name, "username", desired.Username)
	}
	return nil
}

func (c *external) updateX509Providers(ctx context.Context, cr *v1alpha1.User, desired *v1alpha1.UserParameters, observed *v1alpha1.UserObservation) error {
	desiredProviders := desired.Authentication.X509Providers
	observedProviders := observed.X509Providers

	isEqual, providerMappingsToAdd, providerMappingsToRemove := utils.ArraysBothDiff(desiredProviders, observedProviders)
	providersToAdd, err := c.ResolveUserMappings(ctx, providerMappingsToAdd, cr.GetNamespace())
	if err != nil {
		c.log.Info("Error resolving user X.509 providers", "name", cr.Name, "error", err)
		return fmt.Errorf(errUpdateUser, err)
	}

	providersToRemove, err := c.ResolveUserMappings(ctx, providerMappingsToRemove, cr.GetNamespace())
	if err != nil {
		c.log.Info("Error resolving user X.509 providers", "name", cr.Name, "error", err)
		return fmt.Errorf(errUpdateUser, err)
	}

	if !isEqual {
		c.log.Info("Updating user X.509 providers",
			"name", cr.Name,
			"username", desired.Username,
			"toAdd", providersToAdd,
			"toRemove", providersToRemove)

		if err := c.client.UpdateX509Providers(ctx, desired.Username, providersToAdd, providersToRemove); err != nil {
			c.log.Info("Error updating user X.509 providers", "name", cr.Name, "error", err)
			return fmt.Errorf(errUpdateUser, err)
		}
		cr.Status.AtProvider.X509Providers = desired.Authentication.X509Providers
		c.log.Info("Updated user X.509 providers", "name", cr.Name, "username", desired.Username)
	}

	return nil
}

func (c *external) updatePasswordLifetimeCheck(ctx context.Context, cr *v1alpha1.User, desired *v1alpha1.UserParameters, observed *v1alpha1.UserObservation) error {
	if observed.IsPasswordLifetimeCheckEnabled == nil || *observed.IsPasswordLifetimeCheckEnabled != desired.IsPasswordLifetimeCheckEnabled {
		c.log.Info("Updating user password lifetime check",
			"name", cr.Name,
			"username", desired.Username,
			"current", observed.IsPasswordLifetimeCheckEnabled,
			"desired", desired.IsPasswordLifetimeCheckEnabled)
		err := c.client.UpdatePasswordLifetimeCheck(ctx, desired.Username, desired.IsPasswordLifetimeCheckEnabled)
		if err != nil {
			c.log.Info("Error updating user password lifetime check", "name", cr.Name, "error", err)
			return fmt.Errorf(errUpdateUser, err)
		}
		cr.Status.AtProvider.IsPasswordLifetimeCheckEnabled = &desired.IsPasswordLifetimeCheckEnabled
		c.log.Info("Updated user password lifetime check", "name", cr.Name, "username", desired.Username)
	}
	return nil
}

func (c *external) updatePassword(ctx context.Context, cr *v1alpha1.User, desired *v1alpha1.UserParameters) error {
	if cr.Status.AtProvider.PasswordUpToDate != nil && !*cr.Status.AtProvider.PasswordUpToDate {
		if cr.Spec.ForProvider.Authentication.Password == nil || (cr.Status.AtProvider.IsPasswordEnabled != nil && !*cr.Status.AtProvider.IsPasswordEnabled) {
			if err := c.client.TogglePasswordAuthentication(ctx, desired.Username, *cr.Status.AtProvider.IsPasswordEnabled); err != nil {
				c.log.Info("Error disabling password authentication", "name", cr.Name, "error", err)
				return fmt.Errorf(errUpdateUser, err)
			}
		} else {
			c.log.Info("Updating user password", "name", cr.Name, "username", desired.Username)
			password, err := c.getPassword(ctx, cr)
			if err != nil {
				return fmt.Errorf(errUpdateUser, err)
			}
			err = c.client.UpdatePassword(ctx, desired.Username, password, desired.Authentication.Password.ForceFirstPasswordChange)
			if err != nil {
				c.log.Info("Error updating user password", "name", cr.Name, "error", err)
				return fmt.Errorf(errUpdateUser, err)
			}
			upToDate := true
			cr.Status.AtProvider.PasswordUpToDate = &upToDate
			c.log.Info("Updated user password", "name", cr.Name, "username", desired.Username)
		}
	}
	return nil
}

func (c *external) transformParameters(parameters map[string]string) map[string]string {
	// Validate and format parameters
	stringKeys := []string{
		"CLIENT",
		"LOCALE",
		"TIME ZONE",
		"EMAIL ADDRESS",
	}
	integerKeys := []string{
		"STATEMENT MEMORY LIMIT",
		"STATEMENT THREAD LIMIT",
	}

	filteredParameters := make(map[string]string, len(parameters))

	upperParameters := utils.MapKeysToUpper(parameters)
	for key, value := range upperParameters {
		isKnownIntegerKey := slices.Contains(integerKeys, key)
		isKnownStringKey := slices.Contains(stringKeys, key)
		if !isKnownIntegerKey && !isKnownStringKey {
			c.log.Debug("Unknown parameter key, no specific validation applied", "key", key)
		}
		if isKnownIntegerKey {
			// Validate integer
			if _, err := fmt.Sscanf(value, "%d", new(int)); err != nil {
				c.log.Debug("Invalid integer parameter", "key", key, "value", value)
				continue
			}
		}
		filteredParameters[key] = value
	}
	return filteredParameters
}

func (c *external) buildDesiredParameters(cr *v1alpha1.User) *v1alpha1.UserParameters {
	parameters := handleDefaults(cr)
	return parameters
}

func (c *external) buildObservedParameters(cr *v1alpha1.User) *v1alpha1.UserObservation {
	observed := cr.Status.AtProvider.DeepCopy()

	observed.Parameters = c.transformParameters(observed.Parameters)
	return observed
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
		c.log.Info("Error deleting user", "name", cr.Name, "error", err)
		return managed.ExternalDelete{}, fmt.Errorf(errDropUser, err)
	}

	c.log.Info("Successfully deleted user resource", "name", cr.Name, "username", parameters.Username)
	return managed.ExternalDelete{}, err
}

func (c *external) getPassword(ctx context.Context, user *v1alpha1.User) (newPwd string, err error) {
	passwordObj := user.Spec.ForProvider.Authentication.Password
	if passwordObj == nil {
		return "", nil
	}

	if passwordObj.PasswordSecretRef == nil {
		c.log.Info("Warning: PasswordSecretRef is nil, using empty password", "name", user.Name)
		return "", nil
	}
	nn := types.NamespacedName{
		Name:      user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Name,
		Namespace: user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Namespace,
	}
	currentSecret := &corev1.Secret{}
	if err := c.kube.Get(ctx, nn, currentSecret); err != nil {
		c.log.Info("Error getting password secret", "name", nn.Name, "namespace", nn.Namespace, "error", err)
		return "", fmt.Errorf(errGetPasswordSecretFailed, err)
	}
	newPwdBytes, ok := currentSecret.Data[user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Key]
	if !ok {
		c.log.Info("Password key not found in secret", "key", user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Key, "name", nn.Name, "namespace", nn.Namespace)
		return "", fmt.Errorf(errKeyNotFound, user.Spec.ForProvider.Authentication.Password.PasswordSecretRef.Key, nn.Namespace, nn.Name)
	}
	newPwd = string(newPwdBytes)
	c.log.Info("Got password", "name", nn.Name, "namespace", nn.Namespace)
	return newPwd, nil
}

func handleAuthError(cr *v1alpha1.User, log logging.Logger, err error) (bool, error) {
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, user.ErrValidityPeriod):
		log.Info("User validity period error", "name", cr.Name, "error", err)
		return false, err
	case errors.Is(err, user.ErrUserDeactivated):
		log.Info("User deactivated error", "name", cr.Name, "error", err)
		return false, err
	case errors.Is(err, user.ErrUserLocked):
		log.Info("User locked error", "name", cr.Name, "error", err)
		return false, err
	default:
		log.Info("Error observing user", "name", cr.Name, "error", err)
		return true, fmt.Errorf(errSelectUser, err)
	}
}

func handleDefaults(cr *v1alpha1.User) *v1alpha1.UserParameters {
	parameters := cr.Spec.ForProvider.DeepCopy()
	defaultPrivilege := privilege.GetDefaultPrivilege(parameters.Username)

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

func (c *external) ResolveUserMappings(ctx context.Context, mappings []v1alpha1.X509UserMapping, namespace string) ([]user.ResolvedUserMapping, error) {
	resolved := make([]user.ResolvedUserMapping, 0, len(mappings))
	for _, mapping := range mappings {
		var name, subjectName string
		switch {
		case mapping.Name != "":
			name = mapping.Name
		case mapping.ProviderRef != nil:
			x509providerObj := &v1alpha1.X509Provider{}
			if err := c.kube.Get(ctx, types.NamespacedName{Namespace: namespace, Name: mapping.ProviderRef.Name}, x509providerObj); err != nil {
				return nil, fmt.Errorf("cannot resolve X.509 provider reference: %w", err)
			}
			name = x509providerObj.Spec.ForProvider.Name
		default:
			return nil, errors.New("cannot resolve X.509 provider reference: no name or providerRef specified")
		}
		if mapping.SubjectName != "" {
			subjectName = mapping.SubjectName
		} else {
			subjectName = "ANY"
		}
		resolved = append(resolved, user.ResolvedUserMapping{
			Name:        name,
			SubjectName: subjectName,
		})
	}
	return resolved, nil
}
