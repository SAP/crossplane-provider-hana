package role

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
)

// TestBuildGranteeLiteral pins down that the grantee passed to the catalog-view
// queries (GRANTED_PRIVILEGES / GRANTED_ROLES) is the UNQUOTED identifier value,
// matching how those views store the GRANTEE column. This is the direct fix for
// the read bug where a quoted grantee ("data::external_access_g") never matched
// the raw column value, so QueryPrivileges/QueryRoles always returned empty and
// the controller re-granted every reconcile.
func TestBuildGranteeLiteral(t *testing.T) {
	cases := map[string]struct {
		schema   string
		roleName string
		want     string
	}{
		"NoSchema":          {schema: "", roleName: "data::external_access_g", want: "data::external_access_g"},
		"NoSchemaSimple":    {schema: "", roleName: "DEMO_ROLE", want: "DEMO_ROLE"},
		"SchemaQualified":   {schema: "MY_CONTAINER", roleName: "ns::reader", want: "MY_CONTAINER.ns::reader"},
		"NoQuotesEverAdded": {schema: "", roleName: `AFL__SYS_AFL_AFLPAL_EXECUTE_WITH_GRANT_OPTION`, want: `AFL__SYS_AFL_AFLPAL_EXECUTE_WITH_GRANT_OPTION`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := buildGranteeLiteral(tc.schema, tc.roleName); got != tc.want {
				t.Errorf("buildGranteeLiteral(%q, %q) = %q, want %q", tc.schema, tc.roleName, got, tc.want)
			}
		})
	}
}

// nolint: contextcheck
func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.RoleParameters
	}

	type want struct {
		observed *v1alpha1.RoleObservation
		err      error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrRead": {
			reason: "Any errors encountered while reading the role should be returned",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						mock.ExpectQuery("SELECT").WillReturnError(errBoom)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName: "DEMO_ROLE",
				},
			},
			want: want{
				observed: &v1alpha1.RoleObservation{
					Schema:   "",
					RoleName: "",
				},
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully read a role",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME", "ROLEGROUP_NAME"}).
							AddRow("", "DEMO_ROLE", nil)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					Schema:   "",
					RoleName: "DEMO_ROLE",
				},
			},
			want: want{
				observed: &v1alpha1.RoleObservation{
					Schema:     "",
					RoleName:   "DEMO_ROLE",
					Privileges: make([]string, 0),
					Roles:      make([]string, 0),
				},
				err: nil,
			},
		},
		"SuccessWithRolegroup": {
			reason: "Role with a rolegroup should be observed correctly",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME", "ROLEGROUP_NAME"}).
							AddRow("", "DEMO_ROLE", "MY_ROLEGROUP")
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					Schema:   "",
					RoleName: "DEMO_ROLE",
				},
			},
			want: want{
				observed: &v1alpha1.RoleObservation{
					Schema:     "",
					RoleName:   "DEMO_ROLE",
					Rolegroup:  "MY_ROLEGROUP",
					Privileges: make([]string, 0),
					Roles:      make([]string, 0),
				},
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db, Client: &privilege.PrivilegeClient{DB: tc.fields.db}, username: "ADMIN"}
			got, err := c.Read(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.observed, got); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

// TestReadPassesUnquotedGrantee is the regression lock for the read bug: Read
// must pass the UNQUOTED role name as the grantee to QueryPrivileges/QueryRoles,
// because the GRANTED_PRIVILEGES / GRANTED_ROLES catalog views store GRANTEE as
// the raw identifier value. A quoted grantee ("data::external_access_g") matches
// no rows, so both queries silently returned empty and the controller re-granted
// the role every reconcile (grant thrash). The role name here contains "::",
// which forces quoting in SQL identifiers — making the quoted-vs-unquoted
// difference unmistakable.
//
// nolint: contextcheck
func TestReadPassesUnquotedGrantee(t *testing.T) {
	cases := map[string]struct {
		schema      string
		roleName    string
		wantGrantee string // expected GRANTEE arg value (the raw column match)
	}{
		"TopLevelRole": {
			schema:      "",
			roleName:    "data::external_access_g",
			wantGrantee: "data::external_access_g",
		},
		"SchemaQualifiedRole": {
			schema:      "MY_CONTAINER",
			roleName:    "ns::reader",
			wantGrantee: "ns::reader", // addGranteeQuery splits schema.name; GRANTEE = name
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var capturedGranteeArgs [][]any
			db := fake.MockDB{
				MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
					// QueryPrivileges and QueryRoles both funnel through here.
					// Capture the args of the grantee-filtered queries.
					if len(args) > 0 {
						capturedGranteeArgs = append(capturedGranteeArgs, args)
					}
					return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
				},
				MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
					db, mock, _ := sqlmock.New()
					rows := sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME", "ROLEGROUP_NAME"}).
						AddRow(tc.schema, tc.roleName, nil)
					mock.ExpectQuery("SELECT").WillReturnRows(rows)
					return db.QueryRowContext(context.Background(), "SELECT")
				},
			}
			c := Client{DB: db, Client: &privilege.PrivilegeClient{DB: db}, username: "ADMIN"}
			if _, err := c.Read(context.Background(), &v1alpha1.RoleParameters{Schema: tc.schema, RoleName: tc.roleName}); err != nil {
				t.Fatalf("Read(...) unexpected error: %v", err)
			}

			// QueryPrivileges and QueryRoles are both grantee-filtered, so we
			// expect at least two captured arg sets, none of which may contain a
			// quoted grantee value.
			if len(capturedGranteeArgs) < 2 {
				t.Fatalf("expected >=2 grantee-filtered queries (privileges + roles), got %d", len(capturedGranteeArgs))
			}
			quoted := fmt.Sprintf(`"%s"`, tc.roleName)
			for _, args := range capturedGranteeArgs {
				foundExpected := false
				for _, a := range args {
					s, ok := a.(string)
					if !ok {
						continue
					}
					if s == quoted {
						t.Errorf("grantee arg is quoted %q; catalog GRANTEE column stores the raw value, so this matches no rows", s)
					}
					if s == tc.wantGrantee {
						foundExpected = true
					}
				}
				if !foundExpected {
					t.Errorf("expected unquoted grantee %q among query args %v", tc.wantGrantee, args)
				}
			}
		})
	}
}

func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.RoleParameters
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
		"ErrDelete": {
			reason: "Any errors encountered while deleting the role should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName: "DEMO_ROLE",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete a role",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName: "DEMO_ROLE",
				},
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db, Client: &privilege.PrivilegeClient{DB: tc.fields.db}, username: "ADMIN"}
			err := c.Delete(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.RoleParameters
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
		"ErrCreate": {
			reason: "Any errors encountered while creating the role should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName: "DEMO_ROLE",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"SuccessWithRolegroup": {
			reason: "Create should include SET ROLEGROUP when rolegroup is specified",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expected := `CREATE ROLE "DEMO_ROLE" SET ROLEGROUP "MY_ROLEGROUP"`
						if query != expected {
							t.Errorf("expected query %q, got %q", expected, query)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName:  "DEMO_ROLE",
					Rolegroup: "MY_ROLEGROUP",
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessWithoutRolegroup": {
			reason: "Create should not include SET ROLEGROUP when rolegroup is empty",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expected := `CREATE ROLE "DEMO_ROLE"`
						if query != expected {
							t.Errorf("expected query %q, got %q", expected, query)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName: "DEMO_ROLE",
				},
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db, Client: &privilege.PrivilegeClient{DB: tc.fields.db}, username: "ADMIN"}
			err := c.Create(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Create(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestUpdateRolegroup(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.RoleParameters
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
		"ErrUpdateRolegroup": {
			reason: "Any errors encountered while updating the rolegroup should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName:  "DEMO_ROLE",
					Rolegroup: "NEW_ROLEGROUP",
				},
			},
			want: want{
				err: fmt.Errorf("failed to update rolegroup: %w", errBoom),
			},
		},
		"SuccessSetRolegroup": {
			reason: "No error should be returned when setting a rolegroup",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expected := `ALTER ROLE "DEMO_ROLE" SET ROLEGROUP "MY_ROLEGROUP"`
						if query != expected {
							t.Errorf("expected query %q, got %q", expected, query)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName:  "DEMO_ROLE",
					Rolegroup: "MY_ROLEGROUP",
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessUnsetRolegroup": {
			reason: "No error should be returned when unsetting a rolegroup",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expected := `ALTER ROLE "DEMO_ROLE" UNSET ROLEGROUP`
						if query != expected {
							t.Errorf("expected query %q, got %q", expected, query)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.RoleParameters{
					RoleName:  "DEMO_ROLE",
					Rolegroup: "",
				},
			},
			want: want{
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db, Client: &privilege.PrivilegeClient{DB: tc.fields.db}, username: "ADMIN"}
			err := c.UpdateRolegroup(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.UpdateRolegroup(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
