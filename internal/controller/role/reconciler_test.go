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
	"github.com/google/go-cmp/cmp/cmpopts"
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
	MockUpdateRoles      func(ctx context.Context, parameters *v1alpha1.RoleParameters, rolesToGrant, rolesToRevoke []string) error
	MockUpdateRolegroup  func(ctx context.Context, parameters *v1alpha1.RoleParameters) error
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

func (m mockClient) UpdateRoles(ctx context.Context, parameters *v1alpha1.RoleParameters, rolesToGrant, rolesToRevoke []string) error {
	if m.MockUpdateRoles != nil {
		return m.MockUpdateRoles(ctx, parameters, rolesToGrant, rolesToRevoke)
	}
	return nil
}

func (m mockClient) UpdateRolegroup(ctx context.Context, parameters *v1alpha1.RoleParameters) error {
	if m.MockUpdateRolegroup != nil {
		return m.MockUpdateRolegroup(ctx, parameters)
	}
	return nil
}

func (m mockClient) GetDefaultSchema() string {
	return "ADMIN"
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
		// atProvider, when non-nil, is compared against cr.Status.AtProvider
		// after Observe to verify status is populated from the observed (DB)
		// state rather than from spec.
		atProvider *v1alpha1.RoleObservation
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
		"SuccessPopulatesAtProviderFromObserved": {
			reason: "status.atProvider must be populated from the observed (DB) state, not from spec",
			fields: fields{
				client: mockClient{
					// Read returns the real observed state: the role exists,
					// has one privilege, and one role granted to it. Note the
					// spec (below) lists NO privileges/roles — this asserts that
					// Observe writes observed values, never spec values.
					MockRead: func(ctx context.Context, parameters *v1alpha1.RoleParameters) (observed *v1alpha1.RoleObservation, err error) {
						return &v1alpha1.RoleObservation{
							RoleName:   "DEMO_ROLE",
							Schema:     "MY_SCHEMA",
							Privileges: []string{"CREATE ANY"},
							Roles:      []string{`"CONTAINER"."ns::reader"`},
							Rolegroup:  "MY_ROLEGROUP",
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
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: false,
				},
				atProvider: &v1alpha1.RoleObservation{
					RoleName:   "DEMO_ROLE",
					Schema:     "MY_SCHEMA",
					Privileges: []string{"CREATE ANY"},
					Roles:      []string{`"CONTAINER"."ns::reader"`},
					Rolegroup:  "MY_ROLEGROUP",
				},
			},
		},
		"PrivilegeRoleOverlapReturnsErrorAndSkipsUpdate": {
			// When a user places a HANA role name under spec.forProvider.privileges,
			// HANA records it in GRANTED_ROLES, not GRANTED_PRIVILEGES. The desired
			// privileges entry can never match the observed (empty) privileges, causing
			// an infinite Update loop. Observe must detect the overlap and return an
			// error so crossplane-runtime records ReconcileError, emits a Warning
			// event, and skips the Update. (Setting the condition inline and returning
			// nil does not work: the runtime overwrites Synced with ReconcileSuccess on
			// a nil-error, up-to-date Observe and emits no event.)
			reason: "Role name in privileges that appears in observed roles must return an error and skip Update",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.RoleParameters) (observed *v1alpha1.RoleObservation, err error) {
						return &v1alpha1.RoleObservation{
							RoleName:   "DEMO_ROLE",
							Privileges: []string{},
							Roles:      []string{`"DUMMY_SYSTEM_ROLE_A"`, `"DUMMY_SYSTEM_ROLE_B"`},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName:   "DEMO_ROLE",
							Privileges: []string{"DUMMY_SYSTEM_ROLE_A"},
							Roles:      []string{"DUMMY_SYSTEM_ROLE_B"},
						},
					},
				},
			},
			want: want{
				err: errors.New("privileges contains role name(s) that must be moved to spec.forProvider.roles: [DUMMY_SYSTEM_ROLE_A]"),
				c:   managed.ExternalObservation{},
			},
		},
		"UpToDateWhenSpecUnquotedMatchesQuotedObservation": {
			// Regression lock for the grant-thrash bug: the catalog read returns
			// canonical, quoted identifiers ("MY_ROLE"), but a user writes the spec
			// unquoted (MY_ROLE). Observe normalizes the desired side (as the user
			// controller does) so the two compare equal and the resource reports
			// up-to-date instead of re-granting every reconcile forever.
			reason: "Unquoted spec privileges/roles must compare equal to the quoted observed values (no infinite reconcile)",
			fields: fields{
				client: mockClient{
					MockRead: func(ctx context.Context, parameters *v1alpha1.RoleParameters) (observed *v1alpha1.RoleObservation, err error) {
						return &v1alpha1.RoleObservation{
							RoleName:   "DEMO_ROLE",
							Schema:     "",
							Privileges: []string{"CREATE ANY"},
							// QueryRoles always emits the quoted canonical form.
							Roles: []string{`"MY_ROLE"`, `"CONTAINER"."ns::reader" WITH ADMIN OPTION`},
						}, nil
					},
				},
				log: &MockLogger{},
			},
			args: args{
				mg: &v1alpha1.Role{
					Spec: v1alpha1.RoleSpec{
						ForProvider: v1alpha1.RoleParameters{
							RoleName:   "DEMO_ROLE",
							Privileges: []string{"CREATE ANY"},
							// Spec is written unquoted, as a user naturally would.
							Roles: []string{`MY_ROLE`, `"CONTAINER"."ns::reader" WITH ADMIN OPTION`},
						},
					},
				},
			},
			want: want{
				err: nil,
				c: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
				},
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
			if tc.want.atProvider != nil {
				cr, _ := tc.args.mg.(*v1alpha1.Role)
				if diff := cmp.Diff(*tc.want.atProvider, cr.Status.AtProvider); diff != "" {
					t.Errorf("\n%s\ne.Observe(...) status.atProvider: -want, +got:\n%s\n", tc.reason, diff)
				}
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
		"AllFields": {
			reason: "All ForProvider fields should be copied verbatim, preserving case and special characters",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName:         "sap.hana::MixedCase_Role",
						Schema:           "mySchema",
						Privileges:       []string{`SELECT ON SCHEMA "testSchema"`, "CREATE ANY"},
						LdapGroups:       []string{"cn=Securities_DBA,OU=Groups,dc=example,dc=com"},
						NoGrantToCreator: true,
						Rolegroup:        "MY_ROLEGROUP",
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName:         "sap.hana::MixedCase_Role",
				Schema:           "mySchema",
				Privileges:       []string{`SELECT ON SCHEMA "testSchema"`, "CREATE ANY"},
				LdapGroups:       []string{"cn=Securities_DBA,OU=Groups,dc=example,dc=com"},
				NoGrantToCreator: true,
				Rolegroup:        "MY_ROLEGROUP",
			},
		},
		"RolesNormalizedToCanonicalQuotedForm": {
			// buildDesiredParameters is a verbatim copy — normalization now happens
			// in Observe/Update (mirroring the user controller), so this asserts the
			// factory copies roles unchanged. The quoted-vs-unquoted comparison is
			// locked by TestObserve's UpToDate case below.
			reason: "buildDesiredParameters copies all fields verbatim, including roles",
			cr: &v1alpha1.Role{
				Spec: v1alpha1.RoleSpec{
					ForProvider: v1alpha1.RoleParameters{
						RoleName:   "DEMO_ROLE",
						Privileges: []string{"CREATE ANY"},
						Roles: []string{
							"MY_ROLE",
							`"CONTAINER"."ns::reader" WITH ADMIN OPTION`,
						},
					},
				},
			},
			want: &v1alpha1.RoleParameters{
				RoleName:   "DEMO_ROLE",
				Privileges: []string{"CREATE ANY"},
				Roles: []string{
					"MY_ROLE",
					`"CONTAINER"."ns::reader" WITH ADMIN OPTION`,
				},
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

// TestUpdate verifies that Update computes its grant/revoke diffs from the
// observed state stored in cr.Status.AtProvider (which Observe populates from
// the DB) versus the desired spec, and that Update does NOT overwrite
// status.atProvider from spec. This pins the fix for the status-pollution bug:
// previously Create/Update stamped spec values into atProvider, which made the
// Update diff compare spec-against-spec and hid real drift.
func TestUpdate(t *testing.T) {
	sortStrings := cmpopts.SortSlices(func(a, b string) bool { return a < b })

	// A role whose observed state (from the DB, via Observe) differs from the
	// desired spec: observed has role "OLD_ROLE" and privilege "OLD_PRIV"; the
	// spec wants role "NEW_ROLE" and privilege "NEW_PRIV". Update must therefore
	// grant the NEW_* entries and revoke the OLD_* entries.
	observed := v1alpha1.RoleObservation{
		RoleName:   "DEMO_ROLE",
		Privileges: []string{"OLD_PRIV"},
		Roles:      []string{`"OLD_ROLE"`},
	}
	cr := &v1alpha1.Role{
		Spec: v1alpha1.RoleSpec{
			ForProvider: v1alpha1.RoleParameters{
				RoleName:   "DEMO_ROLE",
				Privileges: []string{"NEW_PRIV"},
				Roles:      []string{`"NEW_ROLE"`},
			},
		},
	}
	cr.Status.AtProvider = observed

	var gotPrivGrant, gotPrivRevoke, gotRoleGrant, gotRoleRevoke []string
	mc := mockClient{
		MockUpdatePrivileges: func(ctx context.Context, parameters *v1alpha1.RoleParameters, grant, revoke []string) error {
			gotPrivGrant, gotPrivRevoke = grant, revoke
			return nil
		},
		MockUpdateRoles: func(ctx context.Context, parameters *v1alpha1.RoleParameters, grant, revoke []string) error {
			gotRoleGrant, gotRoleRevoke = grant, revoke
			return nil
		},
	}

	e := external{client: mc, log: &MockLogger{}}
	if _, err := e.Update(context.Background(), cr); err != nil {
		t.Fatalf("Update(...) unexpected error: %v", err)
	}

	if diff := cmp.Diff([]string{"NEW_PRIV"}, gotPrivGrant, sortStrings); diff != "" {
		t.Errorf("privileges to grant: -want, +got:\n%s", diff)
	}
	if diff := cmp.Diff([]string{"OLD_PRIV"}, gotPrivRevoke, sortStrings); diff != "" {
		t.Errorf("privileges to revoke: -want, +got:\n%s", diff)
	}
	if diff := cmp.Diff([]string{`"NEW_ROLE"`}, gotRoleGrant, sortStrings); diff != "" {
		t.Errorf("roles to grant: -want, +got:\n%s", diff)
	}
	if diff := cmp.Diff([]string{`"OLD_ROLE"`}, gotRoleRevoke, sortStrings); diff != "" {
		t.Errorf("roles to revoke: -want, +got:\n%s", diff)
	}

	// Update must NOT overwrite status.atProvider from spec — it stays as the
	// observed state until the next Observe re-reads the DB.
	if diff := cmp.Diff(observed, cr.Status.AtProvider); diff != "" {
		t.Errorf("Update(...) must not mutate status.atProvider from spec: -want, +got:\n%s", diff)
	}
}
