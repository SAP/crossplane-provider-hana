/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package user

import (
	"context"
	"errors"
	"fmt"
	"testing"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/user"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"
)

const demoUser = "DEMO_USER"

// MockLogger is a mock implementation of logging.Logger
type MockLogger struct {
	msgs          []string
	keysAndValues []any
}

// Debug logs debug messages.
func (l *MockLogger) Debug(msg string, keysAndValues ...any) {
	l.msgs = append(l.msgs, msg)
	l.keysAndValues = append(l.keysAndValues, keysAndValues...)
}

// Info logs info messages.
func (l *MockLogger) Info(msg string, keysAndValues ...any) {
	l.msgs = append(l.msgs, msg)
	l.keysAndValues = append(l.keysAndValues, keysAndValues...)
}

// WithValues returns a logger with the specified key-value pairs.
func (l *MockLogger) WithValues(_ ...any) logging.Logger { return l }

// mockUserClient implements the user.Client struct methods for testing
type mockUserClient struct {
	MockRead                   func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error)
	MockCreate                 func(ctx context.Context, parameters *v1alpha1.UserParameters, password string, providers []user.ResolvedUserMapping, jwtProviders []user.ResolvedJWTUserMapping) error
	MockDelete                 func(ctx context.Context, parameters *v1alpha1.UserParameters) error
	MockFormatPrivilegeStrings func(privilegeStrings []string) ([]string, error)
}

// Implement the methods that user.Client struct has
func (m mockUserClient) Read(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
	if m.MockRead != nil {
		return m.MockRead(ctx, parameters, password)
	}
	return &v1alpha1.UserObservation{}, nil
}

func (m mockUserClient) Create(ctx context.Context, parameters *v1alpha1.UserParameters, password string, providers []user.ResolvedUserMapping, jwtProviders []user.ResolvedJWTUserMapping) error {
	if m.MockCreate != nil {
		return m.MockCreate(ctx, parameters, password, providers, jwtProviders)
	}
	return nil
}

func (m mockUserClient) Delete(ctx context.Context, parameters *v1alpha1.UserParameters) error {
	if m.MockDelete != nil {
		return m.MockDelete(ctx, parameters)
	}
	return nil
}

func (m mockUserClient) UpdatePrivileges(ctx context.Context, grantee string, toGrant, toRevoke []string) error {
	return nil
}

func (m mockUserClient) UpdateParameters(ctx context.Context, username string, parametersToSet, parametersToClear map[string]string) error {
	return nil
}

func (m mockUserClient) UpdateUsergroup(ctx context.Context, username, usergroup string) error {
	return nil
}

func (m mockUserClient) UpdatePassword(ctx context.Context, username, password string, forceFirstPasswordChange bool) error {
	return nil
}

func (m mockUserClient) UpdateRoles(ctx context.Context, grantee string, toGrant, toRevoke []string) error {
	return nil
}

func (m mockUserClient) UpdatePasswordLifetimeCheck(ctx context.Context, username string, isPasswordLifetimeCheckEnabled bool) error {
	return nil
}

func (m mockUserClient) UpdateX509Providers(ctx context.Context, username string, toAdd, toRemove []user.ResolvedUserMapping) error {
	return nil
}

func (m mockUserClient) UpdateJWTProviders(ctx context.Context, username string, toAdd, toRemove []user.ResolvedJWTUserMapping) error {
	return nil
}

func (m mockUserClient) ToggleJWTAuthentication(ctx context.Context, username string, enable bool) error {
	return nil
}

func (m mockUserClient) ToggleClientConnect(ctx context.Context, username string, enable bool) error {
	return nil
}

func (m mockUserClient) TogglePasswordAuthentication(ctx context.Context, username string, isPasswordEnabled bool) error {
	return nil
}

func (m mockUserClient) GetDefaultSchema() string {
	return "DEFAULT_SCHEMA" // Default schema for testing
}

func TestConnect(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		kube      client.Client
		usage     resource.Tracker
		newClient func(xsql.DB, string) user.Client
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   error
	}{
		"ErrNotUser": {
			reason: "An error should be returned if the managed resource is not a *User",
			args: args{
				mg: nil,
			},
			want: errors.New(errNotUser),
		},
		"ErrTrackProviderConfigUsage": {
			reason: "An error should be returned if we can't track our ProviderConfig usage",
			fields: fields{
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return errBoom }),
			},
			args: args{
				mg: &v1alpha1.User{},
			},
			want: fmt.Errorf(errTrackPCUsage, errBoom),
		},
		"ErrGetProviderConfig": {
			reason: "An error should be returned if we can't get our ProviderConfig",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(errBoom),
				},
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: fmt.Errorf(errGetPC, errBoom),
		},
		"ErrMissingConnectionSecret": {
			reason: "An error should be returned if our ProviderConfig doesn't specify a connection secret",
			fields: fields{
				kube: &test.MockClient{
					// We call get to populate the Database struct, then again
					// to populate the (empty) ProviderConfig struct, resulting
					// in a ProviderConfig with a nil connection secret.
					MockGet: test.NewMockGetFn(nil),
				},
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: errors.New(errNoSecretRef),
		},
		"ErrGetConnectionSecret": {
			reason: "An error should be returned if we can't get our ProviderConfig's connection secret",
			fields: fields{
				kube: &test.MockClient{
					MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
						switch o := obj.(type) {
						case *apisv1alpha1.ProviderConfig:
							o.Spec.Credentials.ConnectionSecretRef = &xpv1.SecretReference{}
						case *corev1.Secret:
							return errBoom
						}
						return nil
					}),
				},
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return nil }),
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ResourceSpec: xpv1.ResourceSpec{
							ProviderConfigReference: &xpv1.Reference{},
						},
					},
				},
			},
			want: fmt.Errorf(errGetSecret, errBoom),
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &connector{kube: tc.fields.kube, usage: tc.fields.usage, newClient: tc.fields.newClient}
			_, err := e.Connect(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Connect(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestObserve(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client user.UserClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		c   managed.ExternalObservation
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotUser": {
			reason: "An error should be returned if the managed resource is not a *User",
			fields: fields{
				log: &MockLogger{},
			},
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotUser),
			},
		},
		"ErrObserve": {
			reason: "Any errors encountered while observing the User should be returned",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return nil, errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username: " ",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errSelectUser, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully observe a User",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username:                       new(demoUser),
							Privileges:                     []string{privilege.GetDefaultPrivilege("DEMO_USER")},
							Roles:                          []string{`"PUBLIC"`},
							Usergroup:                      new("DEFAULT"),
							PasswordUpToDate:               nil, // No password authentication
							IsPasswordLifetimeCheckEnabled: new(true),
							Parameters:                     make(map[string]string),
							X509Providers:                  []v1alpha1.X509UserMapping{},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       demoUser,
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: true,
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
				err: nil,
			},
		},
		"SuccessWithStrictPrivilegePolicy": {
			reason: "Should successfully observe user with strict privilege policy and handle default privileges",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username:                       new(demoUser),
							Privileges:                     []string{"SELECT", "INSERT", privilege.GetDefaultPrivilege("DEMO_USER")},
							Roles:                          []string{`"PUBLIC"`},
							Usergroup:                      new("DEFAULT"),
							PasswordUpToDate:               nil, // No password authentication
							IsPasswordLifetimeCheckEnabled: new(true),
							Parameters:                     make(map[string]string),
							X509Providers:                  []v1alpha1.X509UserMapping{},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       demoUser,
							Privileges:                     []string{"SELECT", "INSERT"},
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: true,
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
				err: nil,
			},
		},
		"SuccessWithLaxPrivilegePolicy": {
			reason: "Should successfully observe user with lax privilege policy and ignore other privileges",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username:                       new(demoUser),
							Privileges:                     []string{"SELECT", "INSERT", "DELETE", "UPDATE"},
							Roles:                          []string{`"PUBLIC"`},
							Usergroup:                      new("DEFAULT"),
							PasswordUpToDate:               nil, // No password authentication
							IsPasswordLifetimeCheckEnabled: new(true),
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:   demoUser,
							Privileges: []string{"SELECT", "INSERT"},

							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: true,
						},
						PrivilegeManagementPolicy: "lax",
					},
					Status: v1alpha1.UserStatus{
						AtProvider: v1alpha1.UserObservation{
							Privileges: []string{"SELECT", "INSERT"},
						},
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
				err: nil,
			},
		},
		"ErrFilterPrivileges": {
			reason: "Should return error when privilege filtering fails",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username:   new(demoUser),
							Privileges: []string{"SELECT", "INSERT"},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:   demoUser,
							Privileges: []string{"SELECT", "INSERT"},
						},
						PrivilegeManagementPolicy: "invalid",
					},
				},
			},
			want: want{
				err: fmt.Errorf(errFilterPrivileges, fmt.Errorf(privilege.ErrUnknownPrivilegeManagementPolicy, "invalid")),
			},
		},
		"UserNotExists": {
			reason: "Should return ResourceExists false when user doesn't exist",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username: new("DIFFERENT_USER"),
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username: demoUser,
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists: false,
				},
				err: nil,
			},
		},
		"StrictToLaxPolicyTransition": {
			reason: "Should handle transition from strict to lax policy correctly, preventing default privileges from becoming managed",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username:                       new(demoUser),
							Privileges:                     []string{privilege.GetDefaultPrivilege("DEFAULT_SCHEMA"), "SELECT", "INSERT", "UPDATE"},
							Roles:                          []string{`"PUBLIC"`},
							Usergroup:                      new("DEFAULT"),
							PasswordUpToDate:               nil, // No password authentication
							IsPasswordLifetimeCheckEnabled: new(true),
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       demoUser,
							Privileges:                     []string{"SELECT", "INSERT", "UPDATE"},
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: true,
						},
						PrivilegeManagementPolicy: "lax",
					},
					Status: v1alpha1.UserStatus{
						AtProvider: v1alpha1.UserObservation{
							// Previous state when it was in strict mode
							Privileges: []string{privilege.GetDefaultPrivilege("DEMO_USER"), "SELECT", "INSERT", "UPDATE"},
						},
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
				err: nil,
			},
		},
		"DuplicatePrivilegesHandling": {
			reason: "Should handle duplicate privileges correctly and not cause resource to be out of date",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username:                       new(demoUser),
							Privileges:                     []string{privilege.GetDefaultPrivilege("DEMO_USER"), "SELECT", "INSERT", "UPDATE"},
							Roles:                          []string{`"PUBLIC"`},
							Usergroup:                      new("DEFAULT"),
							PasswordUpToDate:               nil, // No password authentication
							IsPasswordLifetimeCheckEnabled: new(true),
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       demoUser,
							Privileges:                     []string{"SELECT", "INSERT", "SELECT", "UPDATE"},
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: true,
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
				err: nil,
			},
		},
		"PasswordLifetimeCheckMismatch": {
			reason: "Should detect when password lifetime check setting is out of date",
			fields: fields{
				client: mockUserClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string) (observed *v1alpha1.UserObservation, err error) {
						return &v1alpha1.UserObservation{
							Username:                       new(demoUser),
							Privileges:                     []string{"CREATE ANY"},
							Roles:                          []string{`"PUBLIC"`},
							Usergroup:                      new("DEFAULT"),
							PasswordUpToDate:               new(true),
							IsPasswordLifetimeCheckEnabled: new(false), // Different from desired
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username:                       demoUser,
							Usergroup:                      "DEFAULT",
							IsPasswordLifetimeCheckEnabled: true, // Desired state
						},
						PrivilegeManagementPolicy: "strict",
					},
				},
			},
			want: want{
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: false, // Should be out of date
				},
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			got, err := e.Observe(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.c, got); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client user.UserClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		c   managed.ExternalCreation
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotUser": {
			reason: "An error should be returned if the managed resource is not a *User",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotUser),
			},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the User should be returned",
			fields: fields{
				client: mockUserClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string, providers []user.ResolvedUserMapping, jwtProviders []user.ResolvedJWTUserMapping) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username: demoUser,
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errCreateUser, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a User",
			fields: fields{
				client: mockUserClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.UserParameters, password string, providers []user.ResolvedUserMapping, jwtProviders []user.ResolvedJWTUserMapping) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username: demoUser,
						},
					},
				},
			},
			want: want{
				err: nil,
				c: managed.ExternalCreation{ConnectionDetails: managed.ConnectionDetails{
					"password": {},
					"user":     []byte(demoUser),
				}},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			got, err := e.Create(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Create(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.c, got); diff != "" {
				t.Errorf("\n%s\ne.Create(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		client user.UserClient
		log    logging.Logger
	}

	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		err error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrNotUser": {
			reason: "An error should be returned if the managed resource is not a *User",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotUser),
			},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the User should be returned",
			fields: fields{
				client: mockUserClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.UserParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username: demoUser,
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errDropUser, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a User",
			fields: fields{
				client: mockUserClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.UserParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.User{
					Spec: v1alpha1.UserSpec{
						ForProvider: v1alpha1.UserParameters{
							Username: demoUser,
						},
					},
				},
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := external{client: tc.fields.client, log: tc.fields.log}
			_, err := e.Delete(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Delete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestGenerateReconcileRequestsFromSecret(t *testing.T) {
	user1 := &v1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testUserName1",
			Namespace: "testUserNamespace1",
		},
		Spec: v1alpha1.UserSpec{
			ForProvider: v1alpha1.UserParameters{
				Authentication: v1alpha1.Authentication{
					Password: &v1alpha1.Password{
						PasswordSecretRef: &xpv1.SecretKeySelector{
							SecretReference: xpv1.SecretReference{
								Namespace: "testSecretNamespace1",
								Name:      "testSecretName1",
							},
						},
					},
				},
			},
		},
	}
	user2 := &v1alpha1.User{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testUserName2",
			Namespace: "testUserNamespace2",
		},
		Spec: v1alpha1.UserSpec{
			ForProvider: v1alpha1.UserParameters{
				Authentication: v1alpha1.Authentication{
					Password: &v1alpha1.Password{
						PasswordSecretRef: &xpv1.SecretKeySelector{
							SecretReference: xpv1.SecretReference{
								Namespace: "testSecretNamespace2",
								Name:      "testSecretName2",
							},
						},
					},
				},
			},
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testSecretName1",
			Namespace: "testSecretNamespace1",
		},
	}

	errBoom := errors.New("boom")

	type args struct {
		ctx  context.Context
		kube client.Client
		log  logging.Logger
		obj  client.Object
	}

	type want struct {
		request []reconcile.Request
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
		logMsg string
	}{
		"ErrNotSecret": {
			reason: "An empty Request should be returned if the resource is not a *Secret",
			args: args{
				kube: &test.MockClient{},
				log:  &MockLogger{},
				obj:  nil,
			},
			want: want{
				request: []reconcile.Request{},
			},
			logMsg: msgNotValidSecret,
		},
		"ErrListUsers": {
			reason: "An empty Request should be returned if we can't list the Users",
			args: args{
				kube: &test.MockClient{
					MockList: test.NewMockListFn(errBoom),
				},
				log: &MockLogger{},
				obj: secret,
			},
			want: want{
				request: []reconcile.Request{},
			},
			logMsg: msgListFailed,
		},
		"EmptyUserList": {
			reason: "An empty list of Users should return an empty request",
			args: args{
				kube: &test.MockClient{
					MockList: test.NewMockListFn(nil, func(obj client.ObjectList) error {
						return nil
					}),
				},
				log: &MockLogger{},
				obj: secret,
			},
			want: want{
				request: []reconcile.Request{},
			},
		},
		"OneUser": {
			reason: "A single User should return a request for that User",
			args: args{
				kube: &test.MockClient{
					MockList: test.NewMockListFn(nil, func(obj client.ObjectList) error {
						users := obj.(*v1alpha1.UserList)
						users.Items = append(users.Items, *user1, *user2)
						return nil
					}),
				},
				log: &MockLogger{},
				obj: secret,
			},
			want: want{
				request: []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Name: "testUserName1",
						},
					},
				},
			},
		},
		"WrongUser": {
			reason: "A User with a different secret name should not return a request",
			args: args{
				kube: &test.MockClient{
					MockList: test.NewMockListFn(nil, func(obj client.ObjectList) error {
						users := obj.(*v1alpha1.UserList)
						users.Items = append(users.Items, *user2)
						return nil
					}),
				},
				log: &MockLogger{},
				obj: secret,
			},
			want: want{
				request: []reconcile.Request{},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := generateReconcileRequestsFromSecret(tc.args.ctx, tc.args.obj, tc.args.kube, tc.args.log)
			if diff := cmp.Diff(tc.want.request, got); diff != "" {
				t.Errorf("\n%s\ne.GenerateReconcileRequestsFromSecret(...): -want, +got:\n%s\n", tc.reason, diff)
			}
			if tc.logMsg != "" {
				msgs := tc.args.log.(*MockLogger).msgs
				if len(msgs) == 0 {
					t.Errorf("\n%s\ne.GenerateReconcileRequestsFromSecret(...): expected error message: %s, got none", tc.reason, tc.logMsg)
				} else if gotMsg := msgs[len(msgs)-1]; gotMsg != tc.logMsg {
					t.Errorf("\n%s\ne.GenerateReconcileRequestsFromSecret(...): -want error message, +got error message:\n-%s\n+%s\n", tc.reason, tc.logMsg, gotMsg)
				}
			}
		})
	}
}

// TestResolveJWTProviderNames locks in the fix for the providerRef diff bug.
// Before the fix, spec-side JWTUserMapping entries that used providerRef
// were never resolved before isJWTMappingsUpToDate / updateJWTProviders
// compared them against observed entries from SYS.JWT_USER_MAPPINGS. The
// names never matched, so the controller emitted DROP IDENTITY + ADD IDENTITY
// every reconcile and any auth attempt landing in the DROP-then-ADD window
// failed with HANA A02 "No JWT mapping found".
func TestResolveJWTProviderNames(t *testing.T) {
	const (
		k8sProviderName  = "ias-jwt"
		hanaProviderName = "JWTTEST_IAS_JWT"
		externalIdentity = "user@example.com"
	)

	resolverKube := &test.MockClient{
		MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
			jp, ok := obj.(*v1alpha1.JWTProvider)
			if !ok {
				return fmt.Errorf("unexpected Get type %T", obj)
			}
			jp.Spec.ForProvider.Name = hanaProviderName
			return nil
		}),
	}

	desired := &v1alpha1.UserParameters{
		Authentication: v1alpha1.Authentication{
			JWTProviders: []v1alpha1.JWTUserMapping{{
				JWTProviderRef: v1alpha1.JWTProviderRef{
					ProviderRef: &xpv1.Reference{Name: k8sProviderName},
				},
				ExternalIdentity: externalIdentity,
			}},
		},
	}

	observed := &v1alpha1.UserObservation{
		JWTProviders: []v1alpha1.JWTUserMapping{{
			JWTProviderRef:   v1alpha1.JWTProviderRef{Name: hanaProviderName},
			ExternalIdentity: externalIdentity,
		}},
	}

	if isJWTMappingsUpToDate(observed, desired) {
		t.Fatal("unresolved desired must not match observed; this is the precondition for the bug being fixed")
	}

	c := &external{kube: resolverKube, log: &MockLogger{}}
	if err := c.resolveJWTProviderNames(context.Background(), desired); err != nil {
		t.Fatalf("resolveJWTProviderNames: %v", err)
	}

	if got := desired.Authentication.JWTProviders[0].Name; got != hanaProviderName {
		t.Errorf("desired Name after resolve: want %q, got %q", hanaProviderName, got)
	}

	if !isJWTMappingsUpToDate(observed, desired) {
		t.Errorf("after resolveJWTProviderNames, desired and observed must compare equal")
	}
}

// TestResolveJWTProviderNamesExplicitNameWins ensures an explicit Name on
// the mapping is preserved and providerRef is not consulted.
func TestResolveJWTProviderNamesExplicitNameWins(t *testing.T) {
	const hanaName = "DIRECTLY_SET_PROVIDER"

	resolverKube := &test.MockClient{
		MockGet: test.NewMockGetFn(nil, func(obj client.Object) error {
			return fmt.Errorf("kube.Get must not be called when Name is set explicitly")
		}),
	}

	desired := &v1alpha1.UserParameters{
		Authentication: v1alpha1.Authentication{
			JWTProviders: []v1alpha1.JWTUserMapping{{
				JWTProviderRef:   v1alpha1.JWTProviderRef{Name: hanaName},
				ExternalIdentity: "x@y.z",
			}},
		},
	}
	c := &external{kube: resolverKube, log: &MockLogger{}}
	if err := c.resolveJWTProviderNames(context.Background(), desired); err != nil {
		t.Fatalf("resolveJWTProviderNames: %v", err)
	}
	if got := desired.Authentication.JWTProviders[0].Name; got != hanaName {
		t.Errorf("want %q, got %q", hanaName, got)
	}
}

// TestResolveJWTProviderNamesMissingRef ensures we surface a clear error when
// neither Name nor ProviderRef is set, rather than silently producing "".
func TestResolveJWTProviderNamesMissingRef(t *testing.T) {
	desired := &v1alpha1.UserParameters{
		Authentication: v1alpha1.Authentication{
			JWTProviders: []v1alpha1.JWTUserMapping{{
				ExternalIdentity: "x@y.z",
			}},
		},
	}
	c := &external{kube: &test.MockClient{}, log: &MockLogger{}}
	err := c.resolveJWTProviderNames(context.Background(), desired)
	if err == nil {
		t.Fatal("expected error for mapping without Name or ProviderRef")
	}
}
