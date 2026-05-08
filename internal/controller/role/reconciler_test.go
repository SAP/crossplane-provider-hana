/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package role

import (
	"context"
	"testing"

	"errors"
	"fmt"

	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/role"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	apisv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/v1alpha1"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/logging"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/pkg/resource"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MockLogger is a mock implementation of logging.Logger
type MockLogger struct{}

// Debug logs debug messages.
func (l *MockLogger) Debug(_ string, _ ...any) {}

// Info logs info messages.
func (l *MockLogger) Info(_ string, _ ...any) {}

// WithValues returns a logger with the specified key-value pairs.
func (l *MockLogger) WithValues(_ ...any) logging.Logger { return l }

type mockClient struct {
	MockRead             func(ctx context.Context, parameters *v1alpha1.RoleParameters) (observed *v1alpha1.RoleObservation, err error)
	MockCreate           func(ctx context.Context, parameters *v1alpha1.RoleParameters) error
	MockDelete           func(ctx context.Context, parameters *v1alpha1.RoleParameters) error
	MockUpdateLdapGroups func(ctx context.Context, parameters *v1alpha1.RoleParameters, groupsToAdd, groupsToRemove []string) error
	MockUpdatePrivileges func(ctx context.Context, parameters *v1alpha1.RoleParameters, privilegesToGrant, privilegesToRevoke []string) error
}

func (m mockClient) Read(ctx context.Context, parameters *v1alpha1.RoleParameters) (observed *v1alpha1.RoleObservation, err error) {
	return m.MockRead(ctx, parameters)
}

func (m mockClient) Create(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
	return m.MockCreate(ctx, parameters)
}

func (m mockClient) Delete(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
	return m.MockDelete(ctx, parameters)
}

func (m mockClient) UpdateLdapGroups(ctx context.Context, parameters *v1alpha1.RoleParameters, groupsToAdd, groupsToRemove []string) error {
	if m.MockUpdateLdapGroups != nil {
		return m.MockUpdateLdapGroups(ctx, parameters, groupsToAdd, groupsToRemove)
	}
	return nil
}

func (m mockClient) UpdatePrivileges(ctx context.Context, parameters *v1alpha1.RoleParameters, privilegesToGrant, privilegesToRevoke []string) error {
	if m.MockUpdatePrivileges != nil {
		return m.MockUpdatePrivileges(ctx, parameters, privilegesToGrant, privilegesToRevoke)
	}
	return nil
}

func TestConnect(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		kube      client.Client
		usage     resource.Tracker
		newClient func(db xsql.DB, username string) role.Client
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
		"ErrNotRole": {
			reason: "An error should be returned if the managed resource is not a *Role",
			args: args{
				mg: nil,
			},
			want: errors.New(errNotRole),
		},
		"ErrTrackProviderConfigUsage": {
			reason: "An error should be returned if we can't track our ProviderConfig usage",
			fields: fields{
				usage: resource.TrackerFn(func(ctx context.Context, mg resource.Managed) error { return errBoom }),
			},
			args: args{
				mg: &v1alpha1.Role{},
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
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
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
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
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
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
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
		client role.RoleClient
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
		"ErrNotRole": {
			reason: "An error should be returned if the managed resource is not a *Role",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotRole),
			},
		},
		"ErrObserve": {
			reason: "Any errors encountered while observing the role should be returned",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.RoleParameters) (observed *v1alpha1.RoleObservation, err error) {
						return nil, errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName: "DEMO_ROLE",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errSelectRole, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully observe a role",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.RoleParameters) (observed *v1alpha1.RoleObservation, err error) {
						return &v1alpha1.RoleObservation{
							RoleName: "",
							Schema:   "",
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName: "DEMO_ROLE",
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
		client role.RoleClient
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
		"ErrNotRole": {
			reason: "An error should be returned if the managed resource is not a *Role",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotRole),
			},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the role should be returned",
			fields: fields{
				client: mockClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName: "DEMO_ROLE",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errCreateRole, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create a role",
			fields: fields{
				client: mockClient{
					MockCreate: func(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName: "DEMO_ROLE",
						},
					},
				},
			},
			want: want{
				err: nil,
				c:   managed.ExternalCreation{ConnectionDetails: managed.ConnectionDetails{}},
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
		client role.RoleClient
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
		"ErrNotRole": {
			reason: "An error should be returned if the managed resource is not a *Role",
			args: args{
				mg: nil,
			},
			want: want{
				err: errors.New(errNotRole),
			},
		},
		"ErrDelete": {
			reason: "Any errors encountered while deleting the role should be returned",
			fields: fields{
				client: mockClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
						return errBoom
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName: "DEMO_ROLE",
						},
					},
				},
			},
			want: want{
				err: fmt.Errorf(errDropRole, errBoom),
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a role",
			fields: fields{
				client: mockClient{
					MockDelete: func(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
						return nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName: "DEMO_ROLE",
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

func TestBuildDesiredParameters(t *testing.T) {
	cases := map[string]struct {
		reason string
		cr     *v1alpha1.Role
		want   *v1alpha1.RoleParameters
	}{
		"PreservesLowercaseRoleName": {
			reason: "Lowercase role name should be preserved without conversion to uppercase",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "my_lowercase_role",
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "my_lowercase_role",
			},
		},
		"PreservesMixedCaseRoleName": {
			reason: "Mixed case role name should be preserved exactly as specified",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "MyMixedCaseRole",
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "MyMixedCaseRole",
			},
		},
		"PreservesUppercaseRoleName": {
			reason: "Uppercase role name should remain uppercase",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "UPPERCASE_ROLE",
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "UPPERCASE_ROLE",
			},
		},
		"PreservesLowercaseSchema": {
			reason: "Lowercase schema name should be preserved without conversion to uppercase",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "test_role",
						Schema:   "my_schema",
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "test_role",
				Schema:   "my_schema",
			},
		},
		"PreservesCaseSensitiveLdapGroups": {
			reason: "LDAP Distinguished Names are case-sensitive and should be preserved exactly",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "test_role",
						LdapGroups: []string{
							"cn=Securities_DBA,OU=Application,OU=Groups,ou=DatabaseAdmins,cn=Users,o=largebank.com",
							"CN=Admins,DC=example,DC=com",
						},
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "test_role",
				LdapGroups: []string{
					"cn=Securities_DBA,OU=Application,OU=Groups,ou=DatabaseAdmins,cn=Users,o=largebank.com",
					"CN=Admins,DC=example,DC=com",
				},
			},
		},
		"PreservesCaseSensitivePrivileges": {
			reason: "Privilege strings containing schema/object names should preserve their case",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "test_role",
						Privileges: []string{
							`SELECT ON SCHEMA "mySchema"`,
							`INSERT ON "MySchema"."MyTable"`,
							"CREATE ANY",
						},
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "test_role",
				Privileges: []string{
					`SELECT ON SCHEMA "mySchema"`,
					`INSERT ON "MySchema"."MyTable"`,
					"CREATE ANY",
				},
			},
		},
		"PreservesAllFieldsWithMixedCase": {
			reason: "All fields should preserve their original case in a complete role specification",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName:         "MyRole",
						Schema:           "MySchema",
						Privileges:       []string{`SELECT ON SCHEMA "testSchema"`},
						LdapGroups:       []string{"cn=TestGroup,ou=Groups,dc=example,dc=com"},
						NoGrantToCreator: true,
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName:         "MyRole",
				Schema:           "MySchema",
				Privileges:       []string{`SELECT ON SCHEMA "testSchema"`},
				LdapGroups:       []string{"cn=TestGroup,ou=Groups,dc=example,dc=com"},
				NoGrantToCreator: true,
			},
		},
		"PreservesRoleNameWithSpecialCharacters": {
			reason: "Role name with special characters like colons should be preserved exactly",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName:   "data::access_g",
						Privileges: []string{"ACCESS_TEST WITH ADMIN OPTION"},
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName:   "data::access_g",
				Privileges: []string{"ACCESS_TEST WITH ADMIN OPTION"},
			},
		},
		"PreservesRoleNameWithDotsAndColons": {
			reason: "Role name with dots and colons (namespace-style) should be preserved exactly",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "sap.hana::data_reader",
						Schema:   "my_schema",
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "sap.hana::data_reader",
				Schema:   "my_schema",
			},
		},
		"PreservesRoleNameWithUnderscoresAndNumbers": {
			reason: "Role name with underscores and numbers should be preserved exactly",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName: "role_123_test",
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName: "role_123_test",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := buildDesiredParameters(tc.cr)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\nbuildDesiredParameters(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}
