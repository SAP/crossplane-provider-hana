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
