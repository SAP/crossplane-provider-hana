package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
)

var testTime = metav1.Now()

// Helper functions for creating pointers
func stringPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}

func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.UserParameters
		password   string
	}

	type want struct {
		observed *v1alpha1.UserObservation
		err      error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrRead": {
			reason: "Any errors encountered while reading the user should be returned",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						mock.ExpectQuery("SELECT").WillReturnError(errBoom)
						return db.QueryRow("SELECT")
					},
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "DEMO_USER",
				},
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:   nil,
					Parameters: nil,
				},
				err: errBoom,
			},
		},
		"UserNotFound": {
			reason: "Should handle case when user does not exist (no rows returned)",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						mock.ExpectQuery("SELECT").WillReturnError(sql.ErrNoRows)
						return db.QueryRow("SELECT")
					},
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "NONEXISTENT_USER",
				},
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       nil,
					RestrictedUser:                 nil,
					LastPasswordChangeTime:         metav1.Time{},
					CreatedAt:                      metav1.Time{},
					Privileges:                     nil,
					Roles:                          nil,
					Parameters:                     nil,
					Usergroup:                      nil,
					IsPasswordLifetimeCheckEnabled: nil,
				},
				err: nil,
			},
		},
		"SuccessWithCompleteUserData": {
			reason: "Should successfully read user with complete data including privileges and roles",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED"}).
							AddRow("TEST_USER", "TEST_GROUP", testTime.Time, testTime.Time, false, false)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRow("SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Check if this is a user parameters query (has 3 columns and username arg)
						if len(args) > 0 && args[0] == "TEST_USER" && strings.Contains(query, "USER_PARAMETERS") {
							return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"USER_NAME", "PARAMETER", "VALUE"}).
								AddRow("TEST_USER", "LOCALE", "en_US").
								AddRow("TEST_USER", "TIME ZONE", "UTC")), nil
						}
						// Mock privileges query - needs 4 columns: OBJECT_TYPE, PRIVILEGE, SCHEMA_NAME, OBJECT_NAME
						if strings.Contains(query, "GRANTED_PRIVILEGES") {
							return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME"})), nil
						}
						// Mock roles query - needs 2 columns: ROLE_SCHEMA_NAME, ROLE_NAME
						if strings.Contains(query, "GRANTED_ROLES") {
							return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME"})), nil
						}
						// Default empty result
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// Mock password validation - return nil for success
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "TEST_USER",
				},
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       stringPtr("TEST_USER"),
					RestrictedUser:                 boolPtr(false),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     map[string]string{"LOCALE": "en_US", "TIME ZONE": "UTC"},
					Usergroup:                      stringPtr("TEST_GROUP"),
					PasswordUpToDate:               boolPtr(true),
					IsPasswordLifetimeCheckEnabled: boolPtr(false),
				},
				err: nil,
			},
		},
		"SuccessWithPrivilegesAndRoles": {
			reason: "Should successfully read user with privileges and roles",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED"}).
							AddRow("POWER_USER", "", testTime.Time, testTime.Time, false, false)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRow("SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Return empty rows for parameters, privileges/roles - they'll be mocked by privilegeClient
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// Mock password validation - return nil for success
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "POWER_USER",
				},
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       stringPtr("POWER_USER"),
					RestrictedUser:                 boolPtr(false),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     make(map[string]string),
					Usergroup:                      stringPtr(""),
					PasswordUpToDate:               boolPtr(true),
					IsPasswordLifetimeCheckEnabled: boolPtr(false),
				},
				err: nil,
			},
		},
		"RestrictedUser": {
			reason: "Should correctly handle restricted user flag",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED"}).
							AddRow("RESTRICTED_USER", "", testTime.Time, testTime.Time, true, false)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRow("SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// Mock password validation - return nil for success
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "RESTRICTED_USER",
				},
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       stringPtr("RESTRICTED_USER"),
					RestrictedUser:                 boolPtr(true),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     make(map[string]string),
					Usergroup:                      stringPtr(""),
					PasswordUpToDate:               boolPtr(true),
					IsPasswordLifetimeCheckEnabled: boolPtr(false),
				},
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{
				DB:     tc.fields.db,
				Client: &privilege.PrivilegeClient{DB: tc.fields.db},
			}
			got, err := c.Read(tc.args.ctx, tc.args.parameters, tc.args.password)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.observed, got); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want, +got:\n%s\n", tc.reason, diff)
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
		parameters *v1alpha1.UserParameters
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
			reason: "Any errors encountered while creating the user should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "DEMO_USER",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"BasicUserCreation": {
			reason: "Should successfully create a basic user without additional parameters",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "BASIC_USER",
				},
			},
			want: want{
				err: nil,
			},
		},
		"RestrictedUserCreation": {
			reason: "Should successfully create a restricted user",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username:       "RESTRICTED_USER",
					RestrictedUser: true,
				},
			},
			want: want{
				err: nil,
			},
		},
		"UserWithParameters": {
			reason: "Should successfully create user with custom parameters",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "PARAM_USER",
					Parameters: map[string]string{
						"LOCALE":    "en_US",
						"TIME ZONE": "UTC",
						"CLIENT":    "100",
					},
				},
			},
			want: want{
				err: nil,
			},
		},
		"UserWithUsergroup": {
			reason: "Should successfully create user with usergroup assignment",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username:  "GROUP_USER",
					Usergroup: "ADMIN_GROUP",
				},
			},
			want: want{
				err: nil,
			},
		},
		"UserWithPrivileges": {
			reason: "Should successfully create user and grant privileges",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username:   "PRIV_USER",
					Privileges: []string{"SELECT", "INSERT", "SELECT ON SCHEMA myschema"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"UserWithRoles": {
			reason: "Should successfully create user and assign roles",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "ROLE_USER",
					Roles:    []string{"ADMIN_ROLE", "SCHEMA1.CUSTOM_ROLE"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"ComplexUserCreation": {
			reason: "Should successfully create user with all possible configurations",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username:       "COMPLEX_USER",
					RestrictedUser: false,
					Usergroup:      "POWER_USERS",
					Parameters: map[string]string{
						"LOCALE":                 "de_DE",
						"TIME ZONE":              "Europe/Berlin",
						"STATEMENT MEMORY LIMIT": "1GB",
						"STATEMENT THREAD LIMIT": "10",
					},
					Privileges: []string{"SELECT", "INSERT", "UPDATE", "SELECT ON SCHEMA analytics"},
					Roles:      []string{"DATA_ANALYST", "REPORTING.VIEWER"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"PrivilegeGrantError": {
			reason: "Should return error if privilege granting fails during user creation",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// First call (CREATE USER) succeeds, second call (GRANT) fails
						if query == "CREATE USER PRIV_ERROR_USER" {
							return nil, nil
						}
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username:   "PRIV_ERROR_USER",
					Privileges: []string{"SELECT"},
				},
			},
			want: want{
				err: fmt.Errorf(errGrantPrivileges, errBoom),
			},
		},
		"RoleGrantError": {
			reason: "Should return error if role granting fails during user creation",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// First call (CREATE USER) succeeds, second call (GRANT ROLE) fails
						if query == "CREATE USER ROLE_ERROR_USER" {
							return nil, nil
						}
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "ROLE_ERROR_USER",
					Roles:    []string{"ADMIN_ROLE"},
				},
			},
			want: want{
				err: fmt.Errorf(errGrantRoles, errBoom),
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{
				DB:     tc.fields.db,
				Client: &privilege.PrivilegeClient{DB: tc.fields.db},
			}
			err := c.Create(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nCreate(...): -want error, +got error:\n%s\n", tc.reason, diff)
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
		parameters *v1alpha1.UserParameters
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
			reason: "Any errors encountered while deleting the user should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "DEMO_USER",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"BasicUserDeletion": {
			reason: "Should successfully delete a basic user",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "BASIC_USER",
				},
			},
			want: want{
				err: nil,
			},
		},
		"RestrictedUserDeletion": {
			reason: "Should successfully delete a restricted user",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username:       "RESTRICTED_USER",
					RestrictedUser: true,
				},
			},
			want: want{
				err: nil,
			},
		},
		"UserWithComplexConfiguration": {
			reason: "Should successfully delete user regardless of its configuration complexity",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username:       "COMPLEX_USER",
					RestrictedUser: false,
					Usergroup:      "ADMIN_GROUP",
					Parameters: map[string]string{
						"LOCALE":    "en_US",
						"TIME ZONE": "UTC",
					},
					Privileges: []string{"SELECT", "INSERT", "UPDATE"},
					Roles:      []string{"ADMIN_ROLE", "DATA_ANALYST"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"NonExistentUser": {
			reason: "Should handle deletion of non-existent user gracefully",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// Simulate user not found error (this would typically be a specific DB error)
						return nil, errors.New("user does not exist")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "NONEXISTENT_USER",
				},
			},
			want: want{
				err: errors.New("user does not exist"),
			},
		},
		"DatabaseConnectionError": {
			reason: "Should return error when database connection fails during deletion",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errors.New("database connection lost")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "ANY_USER",
				},
			},
			want: want{
				err: errors.New("database connection lost"),
			},
		},
		"PermissionDeniedError": {
			reason: "Should return error when insufficient permissions to delete user",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errors.New("insufficient privilege: Not authorized")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "PROTECTED_USER",
				},
			},
			want: want{
				err: errors.New("insufficient privilege: Not authorized"),
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{
				DB:     tc.fields.db,
				Client: &privilege.PrivilegeClient{DB: tc.fields.db},
			}
			err := c.Delete(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nDelete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestUpdatePasswordLifetimeCheck(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx                            context.Context
		username                       string
		isPasswordLifetimeCheckEnabled bool
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
		"ErrUpdatePasswordLifetimeCheck": {
			reason: "Any errors encountered while updating password lifetime check should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				username:                       "DEMO_USER",
				isPasswordLifetimeCheckEnabled: true,
			},
			want: want{
				err: fmt.Errorf(ErrUpdateUserPasswordLifetimeCheck, errBoom),
			},
		},
		"SuccessEnable": {
			reason: "No error should be returned when we successfully enable password lifetime check",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER DEMO_USER ENABLE PASSWORD LIFETIME"
						if query != expectedQuery {
							return nil, errors.New("unexpected query")
						}
						return nil, nil
					},
				},
			},
			args: args{
				username:                       "DEMO_USER",
				isPasswordLifetimeCheckEnabled: true,
			},
			want: want{
				err: nil,
			},
		},
		"SuccessDisable": {
			reason: "No error should be returned when we successfully disable password lifetime check",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER DEMO_USER DISABLE PASSWORD LIFETIME"
						if query != expectedQuery {
							return nil, errors.New("unexpected query")
						}
						return nil, nil
					},
				},
			},
			args: args{
				username:                       "DEMO_USER",
				isPasswordLifetimeCheckEnabled: false,
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.UpdatePasswordLifetimeCheck(tc.args.ctx, tc.args.username, tc.args.isPasswordLifetimeCheckEnabled)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nc.UpdatePasswordLifetimeCheck(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
