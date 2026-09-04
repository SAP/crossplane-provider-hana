package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana/privilege"
)

var testTime = metav1.Now()

// nolint: contextcheck
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
						return db.QueryRowContext(context.Background(), "SELECT")
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
						return db.QueryRowContext(context.Background(), "SELECT")
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
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED", "IS_PASSWORD_ENABLED", "IS_CLIENT_CONNECT_ENABLED"}).
							AddRow("TEST_USER", "TEST_GROUP", testTime.Time, testTime.Time, false, false, true, true)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
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
					Authentication: v1alpha1.Authentication{
						Password: &v1alpha1.Password{
							PasswordSecretRef: &xpv1.SecretKeySelector{},
						},
					},
				},
				password: "test-password",
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       new("TEST_USER"),
					RestrictedUser:                 new(false),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     map[string]string{"LOCALE": "en_US", "TIME ZONE": "UTC"},
					Usergroup:                      new("TEST_GROUP"),
					PasswordUpToDate:               new(true),
					IsPasswordLifetimeCheckEnabled: new(false),
					IsPasswordEnabled:              new(true),
					IsJWTEnabled:                   new(false),
					IsClientConnectEnabled:         new(true),
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
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED", "IS_PASSWORD_ENABLED", "IS_CLIENT_CONNECT_ENABLED"}).
							AddRow("POWER_USER", "", testTime.Time, testTime.Time, false, false, true, true)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
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
					Authentication: v1alpha1.Authentication{
						Password: &v1alpha1.Password{
							PasswordSecretRef: &xpv1.SecretKeySelector{},
						},
					},
				},
				password: "test-password",
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       new("POWER_USER"),
					RestrictedUser:                 new(false),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     make(map[string]string),
					Usergroup:                      new(""),
					PasswordUpToDate:               new(true),
					IsPasswordLifetimeCheckEnabled: new(false),
					IsPasswordEnabled:              new(true),
					IsJWTEnabled:                   new(false),
					IsClientConnectEnabled:         new(true),
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
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED", "IS_PASSWORD_ENABLED", "IS_CLIENT_CONNECT_ENABLED"}).
							AddRow("RESTRICTED_USER", "", testTime.Time, testTime.Time, true, false, true, true)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
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
					Authentication: v1alpha1.Authentication{
						Password: &v1alpha1.Password{
							PasswordSecretRef: &xpv1.SecretKeySelector{},
						},
					},
				},
				password: "test-password",
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       new("RESTRICTED_USER"),
					RestrictedUser:                 new(true),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     make(map[string]string),
					Usergroup:                      new(""),
					PasswordUpToDate:               new(true),
					IsPasswordLifetimeCheckEnabled: new(false),
					IsPasswordEnabled:              new(true),
					IsJWTEnabled:                   new(false),
					IsClientConnectEnabled:         new(true),
				},
				err: nil,
			},
		},
		"SuccessWithX509Providers": {
			reason: "Should successfully read user with X509 authentication providers",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED", "IS_PASSWORD_ENABLED", "IS_CLIENT_CONNECT_ENABLED"}).
							AddRow("X509_USER", "X509_GROUP", testTime.Time, testTime.Time, false, true, false, true)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Check if this is an X509 providers query
						if strings.Contains(query, "X509_USER_MAPPINGS") {
							return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"X509_PROVIDER_NAME", "SUBJECT_NAME"}).
								AddRow("TEST_PROVIDER", "CN=John Doe,O=Acme Corp").
								AddRow("BACKUP_PROVIDER", sql.NullString{})), nil
						}
						// Other queries return empty results
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// No password validation needed for X509-only user
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "X509_USER",
					Authentication: v1alpha1.Authentication{
						X509Providers: []v1alpha1.X509UserMapping{
							{
								X509ProviderRef: v1alpha1.X509ProviderRef{Name: "TEST_PROVIDER"},
								SubjectName:     "CN=John Doe,O=Acme Corp",
							},
							{
								X509ProviderRef: v1alpha1.X509ProviderRef{Name: "BACKUP_PROVIDER"},
								SubjectName:     "ANY",
							},
						},
					},
				},
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       new("X509_USER"),
					RestrictedUser:                 new(false),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     make(map[string]string),
					Usergroup:                      new("X509_GROUP"),
					PasswordUpToDate:               nil,
					IsPasswordLifetimeCheckEnabled: new(true),
					IsPasswordEnabled:              new(false),
					IsJWTEnabled:                   new(false),
					IsClientConnectEnabled:         new(true),
					X509Providers: []v1alpha1.X509UserMapping{
						{
							X509ProviderRef: v1alpha1.X509ProviderRef{Name: "TEST_PROVIDER"},
							SubjectName:     "CN=John Doe,O=Acme Corp",
						},
						{
							X509ProviderRef: v1alpha1.X509ProviderRef{Name: "BACKUP_PROVIDER"},
							SubjectName:     "ANY",
						},
					},
				},
				err: nil,
			},
		},
		"SuccessHybridAuthentication": {
			reason: "Should successfully read user with both password and X509 authentication",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED", "IS_PASSWORD_ENABLED", "IS_CLIENT_CONNECT_ENABLED"}).
							AddRow("HYBRID_USER", "HYBRID_GROUP", testTime.Time, testTime.Time, false, true, true, true)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Check if this is an X509 providers query
						if strings.Contains(query, "X509_USER_MAPPINGS") {
							return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"X509_PROVIDER_NAME", "SUBJECT_NAME"}).
								AddRow("MAIN_PROVIDER", "CN=Hybrid User,O=Company")), nil
						}
						// Other queries return empty results
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
					Username: "HYBRID_USER",
					Authentication: v1alpha1.Authentication{
						Password: &v1alpha1.Password{
							PasswordSecretRef: &xpv1.SecretKeySelector{},
						},
						X509Providers: []v1alpha1.X509UserMapping{
							{
								X509ProviderRef: v1alpha1.X509ProviderRef{Name: "MAIN_PROVIDER"},
								SubjectName:     "CN=Hybrid User,O=Company",
							},
						},
					},
				},
				password: "hybrid-password",
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       new("HYBRID_USER"),
					RestrictedUser:                 new(false),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     make(map[string]string),
					Usergroup:                      new("HYBRID_GROUP"),
					PasswordUpToDate:               new(true),
					IsPasswordLifetimeCheckEnabled: new(true),
					IsPasswordEnabled:              new(true),
					IsJWTEnabled:                   new(false),
					IsClientConnectEnabled:         new(true),
					X509Providers: []v1alpha1.X509UserMapping{
						{
							X509ProviderRef: v1alpha1.X509ProviderRef{Name: "MAIN_PROVIDER"},
							SubjectName:     "CN=Hybrid User,O=Company",
						},
					},
				},
				err: nil,
			},
		},
		"ErrX509ProvidersQuery": {
			reason: "Should return error when X509 providers query fails",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"USER_NAME", "USERGROUP_NAME", "CREATE_TIME", "LAST_PASSWORD_CHANGE_TIME", "IS_RESTRICTED", "IS_PASSWORD_LIFETIME_CHECK_ENABLED", "IS_PASSWORD_ENABLED", "IS_CLIENT_CONNECT_ENABLED"}).
							AddRow("ERROR_USER", "", testTime.Time, testTime.Time, false, false, true, true)
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Check if this is an X509 providers query
						if strings.Contains(query, "X509_USER_MAPPINGS") {
							return nil, errBoom
						}
						// Other queries return empty results
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.UserParameters{
					Username: "ERROR_USER",
				},
			},
			want: want{
				observed: &v1alpha1.UserObservation{
					Username:                       new("ERROR_USER"),
					RestrictedUser:                 new(false),
					LastPasswordChangeTime:         testTime,
					CreatedAt:                      testTime,
					Privileges:                     make([]string, 0),
					Roles:                          make([]string, 0),
					Parameters:                     make(map[string]string),
					Usergroup:                      new(""),
					PasswordUpToDate:               new(false),
					IsPasswordLifetimeCheckEnabled: new(false),
					IsPasswordEnabled:              new(true),
					IsClientConnectEnabled:         new(true),
				},
				err: fmt.Errorf("failed to query x509 providers: %w", errBoom),
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
		password   string
		providers  []ResolvedUserMapping
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
			err := c.Create(tc.args.ctx, tc.args.parameters, tc.args.password, tc.args.providers, nil)
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

func TestUpdateX509Providers(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx      context.Context
		username string
		toAdd    []ResolvedUserMapping
		toRemove []ResolvedUserMapping
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
		"ErrAddProviders": {
			reason: "Any errors encountered while adding X509 providers should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "ADD IDENTITY") {
							return nil, errBoom
						}
						return nil, nil
					},
				},
			},
			args: args{
				username: "TEST_USER",
				toAdd: []ResolvedUserMapping{
					{Name: "TEST_PROVIDER", SubjectName: "CN=Test User"},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"ErrRemoveProviders": {
			reason: "Any errors encountered while removing X509 providers should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "DROP IDENTITY") {
							return nil, errBoom
						}
						return nil, nil
					},
				},
			},
			args: args{
				username: "TEST_USER",
				toRemove: []ResolvedUserMapping{
					{Name: "OLD_PROVIDER", SubjectName: "CN=Old User"},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"SuccessAddSingleProvider": {
			reason: "Should successfully add a single X509 provider",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER TEST_USER ADD IDENTITY 'CN=Test User,O=Acme Corp' FOR X509 PROVIDER TEST_PROVIDER"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				username: "TEST_USER",
				toAdd: []ResolvedUserMapping{
					{Name: "TEST_PROVIDER", SubjectName: "CN=Test User,O=Acme Corp"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessRemoveSingleProvider": {
			reason: "Should successfully remove a single X509 provider",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER TEST_USER DROP IDENTITY 'CN=Old User' FOR X509 PROVIDER OLD_PROVIDER"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				username: "TEST_USER",
				toRemove: []ResolvedUserMapping{
					{Name: "OLD_PROVIDER", SubjectName: "CN=Old User"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessAddMultipleProviders": {
			reason: "Should successfully add multiple X509 providers",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// Expect two separate queries for multiple providers
						if strings.Contains(query, "MAIN_PROVIDER") || strings.Contains(query, "BACKUP_PROVIDER") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				username: "MULTI_USER",
				toAdd: []ResolvedUserMapping{
					{Name: "MAIN_PROVIDER", SubjectName: "CN=Main User"},
					{Name: "BACKUP_PROVIDER", SubjectName: "ANY"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessRemoveMultipleProviders": {
			reason: "Should successfully remove multiple X509 providers",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// Expect two separate queries for multiple providers
						if strings.Contains(query, "OLD_MAIN") || strings.Contains(query, "OLD_BACKUP") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				username: "MULTI_USER",
				toRemove: []ResolvedUserMapping{
					{Name: "OLD_MAIN", SubjectName: "CN=Old Main"},
					{Name: "OLD_BACKUP", SubjectName: "CN=Old Backup"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessAddAndRemove": {
			reason: "Should successfully add and remove X509 providers in the same operation",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						// Should handle both ADD and DROP operations
						if strings.Contains(query, "ADD IDENTITY") || strings.Contains(query, "DROP IDENTITY") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				username: "COMPLEX_USER",
				toAdd: []ResolvedUserMapping{
					{Name: "NEW_PROVIDER", SubjectName: "CN=New User"},
				},
				toRemove: []ResolvedUserMapping{
					{Name: "OLD_PROVIDER", SubjectName: "CN=Old User"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessNoChanges": {
			reason: "Should successfully handle case when no changes are needed",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, fmt.Errorf("no queries should be executed when no changes are needed")
					},
				},
			},
			args: args{
				username: "UNCHANGED_USER",
				toAdd:    []ResolvedUserMapping{},
				toRemove: []ResolvedUserMapping{},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessWithAnySubject": {
			reason: "Should successfully handle providers with ANY subject name",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER ANY_USER ADD IDENTITY 'ANY' FOR X509 PROVIDER ANY_PROVIDER"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				username: "ANY_USER",
				toAdd: []ResolvedUserMapping{
					{Name: "ANY_PROVIDER", SubjectName: "ANY"},
				},
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.UpdateX509Providers(tc.args.ctx, tc.args.username, tc.args.toAdd, tc.args.toRemove)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nc.UpdateX509Providers(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestTogglePasswordAuthentication(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx               context.Context
		username          string
		isPasswordEnabled bool
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
		"ErrTogglePassword": {
			reason: "Any errors encountered while toggling password authentication should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				username:          "TEST_USER",
				isPasswordEnabled: true,
			},
			want: want{
				err: fmt.Errorf("failed to enable/disable password: %w", errBoom),
			},
		},
		"SuccessDisablePassword": {
			reason: "Should successfully disable password authentication when isPasswordEnabled is true",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER TEST_USER DISABLE PASSWORD"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				username:          "TEST_USER",
				isPasswordEnabled: true,
			},
			want: want{
				err: nil,
			},
		},
		"SuccessEnablePassword": {
			reason: "Should successfully enable password authentication when isPasswordEnabled is false",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER TEST_USER ENABLE PASSWORD"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				username:          "TEST_USER",
				isPasswordEnabled: false,
			},
			want: want{
				err: nil,
			},
		},
		"SuccessComplexUsername": {
			reason: "Should successfully handle users with complex usernames",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER USER COMPLEX_USER_NAME DISABLE PASSWORD"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				username:          "COMPLEX_USER_NAME",
				isPasswordEnabled: true,
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.TogglePasswordAuthentication(tc.args.ctx, tc.args.username, tc.args.isPasswordEnabled)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nc.TogglePasswordAuthentication(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

// jwtCaptureExec collects every ExecContext query so the JWT-oriented tests
// can assert on the exact DDL emitted. Same pattern as publickey and
// jwtprovider client tests.
type jwtCaptureExec struct {
	queries []string
}

func (c *jwtCaptureExec) mock() fake.MockDB {
	return fake.MockDB{
		MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			c.queries = append(c.queries, query)
			return nil, nil
		},
	}
}

// TestUpdateJWTProviders covers the ADD/DROP IDENTITY DDL emitted when JWT
// identity mappings drift on a user. Regression guard for the JWT-SSO flow.
// nolint: contextcheck
func TestUpdateJWTProviders(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason   string
		username string
		toAdd    []ResolvedJWTUserMapping
		toRemove []ResolvedJWTUserMapping
		driver   error
		wantSQL  []string
		unwant   []string
		wantErr  string
	}{
		"NoOp": {
			reason:   "Empty add/remove lists emit no DDL",
			username: "DEMO_USER",
			unwant:   []string{"ALTER USER"},
		},
		"AddOne": {
			reason:   "Single ADD IDENTITY ... FOR JWT PROVIDER emitted per resolved mapping",
			username: "DEMO_USER",
			toAdd: []ResolvedJWTUserMapping{
				{Name: "IAS_JWT", ExternalIdentity: "user@example.com"},
			},
			wantSQL: []string{"ALTER USER DEMO_USER ADD IDENTITY 'user@example.com' FOR JWT PROVIDER IAS_JWT"},
			unwant:  []string{"DROP IDENTITY"},
		},
		"RemoveOne": {
			reason:   "Single DROP IDENTITY ... FOR JWT PROVIDER emitted per resolved mapping",
			username: "DEMO_USER",
			toRemove: []ResolvedJWTUserMapping{
				{Name: "IAS_JWT", ExternalIdentity: "user@example.com"},
			},
			wantSQL: []string{"ALTER USER DEMO_USER DROP IDENTITY 'user@example.com' FOR JWT PROVIDER IAS_JWT"},
			unwant:  []string{"ADD IDENTITY"},
		},
		"AddAndRemove": {
			// A value-change on the same provider round-trips as remove + add;
			// asserts the emit order is "removes first, then adds" to match the
			// implementation loop.
			reason:   "Mixed add/remove emits DROP before ADD",
			username: "DEMO_USER",
			toRemove: []ResolvedJWTUserMapping{
				{Name: "IAS_JWT", ExternalIdentity: "old@example.com"},
			},
			toAdd: []ResolvedJWTUserMapping{
				{Name: "IAS_JWT", ExternalIdentity: "new@example.com"},
			},
			wantSQL: []string{
				"ALTER USER DEMO_USER ADD IDENTITY 'new@example.com' FOR JWT PROVIDER IAS_JWT",
				"ALTER USER DEMO_USER DROP IDENTITY 'old@example.com' FOR JWT PROVIDER IAS_JWT",
			},
		},
		"DriverErrorAdd": {
			reason:   "Driver failure on ADD IDENTITY propagates wrapped with ErrUpdateUserJWTProviders",
			username: "DEMO_USER",
			toAdd: []ResolvedJWTUserMapping{
				{Name: "IAS_JWT", ExternalIdentity: "user@example.com"},
			},
			driver:  errBoom,
			wantErr: "cannot update user JWT providers",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var db fake.MockDB
			var cap jwtCaptureExec
			if tc.driver != nil {
				db = fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, tc.driver
					},
				}
			} else {
				db = cap.mock()
			}
			c := Client{DB: db}
			err := c.UpdateJWTProviders(context.Background(), tc.username, tc.toAdd, tc.toRemove)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("\n%s\nUpdateJWTProviders(...): want error containing %q, got: %v", tc.reason, tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.reason, err)
			}
			joined := strings.Join(cap.queries, "\n---\n")
			for _, w := range tc.wantSQL {
				if !strings.Contains(joined, w) {
					t.Errorf("\n%s\nUpdateJWTProviders(...): missing SQL substring %q:\n%s", tc.reason, w, joined)
				}
			}
			for _, u := range tc.unwant {
				if strings.Contains(joined, u) {
					t.Errorf("\n%s\nUpdateJWTProviders(...): forbidden SQL substring %q:\n%s", tc.reason, u, joined)
				}
			}
		})
	}
}

// TestToggleClientConnect covers the ENABLE/DISABLE CLIENT CONNECT DDL. Rest-
// ricted users need this enabled before any external authentication (password,
// X.509, JWT) succeeds — see user_client.go:585.
// nolint: contextcheck
func TestToggleClientConnect(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason  string
		enable  bool
		driver  error
		wantSQL string
		wantErr string
	}{
		"Enable":  {reason: "enable=true emits ENABLE CLIENT CONNECT", enable: true, wantSQL: "ALTER USER U ENABLE CLIENT CONNECT"},
		"Disable": {reason: "enable=false emits DISABLE CLIENT CONNECT", enable: false, wantSQL: "ALTER USER U DISABLE CLIENT CONNECT"},
		"DriverError": {
			reason:  "Driver failure wraps with ErrUpdateUserClientConnect",
			enable:  true,
			driver:  errBoom,
			wantErr: "cannot toggle client connect",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var db fake.MockDB
			var cap jwtCaptureExec
			if tc.driver != nil {
				db = fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, tc.driver
					},
				}
			} else {
				db = cap.mock()
			}
			c := Client{DB: db}
			err := c.ToggleClientConnect(context.Background(), "U", tc.enable)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("\n%s\nToggleClientConnect(...): want error containing %q, got: %v", tc.reason, tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.reason, err)
			}
			if !strings.Contains(strings.Join(cap.queries, "\n"), tc.wantSQL) {
				t.Errorf("\n%s\nToggleClientConnect(...): missing SQL %q, got: %v", tc.reason, tc.wantSQL, cap.queries)
			}
		})
	}
}

// TestToggleJWTAuthentication covers the ENABLE/DISABLE JWT DDL. Users must
// have this enabled before ADD IDENTITY ... FOR JWT PROVIDER succeeds — see
// user_client.go:358.
// nolint: contextcheck
func TestToggleJWTAuthentication(t *testing.T) {
	cases := map[string]struct {
		reason  string
		enable  bool
		wantSQL string
	}{
		"Enable":  {reason: "enable=true emits ENABLE JWT", enable: true, wantSQL: "ALTER USER U ENABLE JWT"},
		"Disable": {reason: "enable=false emits DISABLE JWT", enable: false, wantSQL: "ALTER USER U DISABLE JWT"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var cap jwtCaptureExec
			c := Client{DB: cap.mock()}
			if err := c.ToggleJWTAuthentication(context.Background(), "U", tc.enable); err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.reason, err)
			}
			if !strings.Contains(strings.Join(cap.queries, "\n"), tc.wantSQL) {
				t.Errorf("\n%s\nToggleJWTAuthentication(...): missing SQL %q, got: %v", tc.reason, tc.wantSQL, cap.queries)
			}
		})
	}
}

// TestQueryJWTProviders covers the SYS.JWT_USER_MAPPINGS row-parse path fed
// into UserObservation.JWTProviders. Empty result, single mapping, and multi-
// mapping cases; ensures the JWTProviderRef.Name and ExternalIdentity fields
// round-trip.
// nolint: contextcheck
func TestQueryJWTProviders(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason   string
		rows     *sqlmock.Rows
		queryErr error
		want     []v1alpha1.JWTUserMapping
		wantErr  string
	}{
		"None": {
			reason: "Empty result set returns nil slice",
			rows:   sqlmock.NewRows([]string{"JWT_PROVIDER_NAME", "EXTERNAL_IDENTITY"}),
			want:   nil,
		},
		"One": {
			reason: "Single row maps into one JWTUserMapping",
			rows: sqlmock.NewRows([]string{"JWT_PROVIDER_NAME", "EXTERNAL_IDENTITY"}).
				AddRow("IAS_JWT", "user@example.com"),
			want: []v1alpha1.JWTUserMapping{
				{JWTProviderRef: v1alpha1.JWTProviderRef{Name: "IAS_JWT"}, ExternalIdentity: "user@example.com"},
			},
		},
		"Many": {
			reason: "Multiple rows preserve order and both fields",
			rows: sqlmock.NewRows([]string{"JWT_PROVIDER_NAME", "EXTERNAL_IDENTITY"}).
				AddRow("IAS_JWT", "user-a@example.com").
				AddRow("IAS_JWT", "user-b@example.com").
				AddRow("OTHER_JWT", "user-c@example.com"),
			want: []v1alpha1.JWTUserMapping{
				{JWTProviderRef: v1alpha1.JWTProviderRef{Name: "IAS_JWT"}, ExternalIdentity: "user-a@example.com"},
				{JWTProviderRef: v1alpha1.JWTProviderRef{Name: "IAS_JWT"}, ExternalIdentity: "user-b@example.com"},
				{JWTProviderRef: v1alpha1.JWTProviderRef{Name: "OTHER_JWT"}, ExternalIdentity: "user-c@example.com"},
			},
		},
		"QueryError": {
			reason:   "Driver error on the mappings query propagates wrapped with 'failed to query jwt providers'",
			queryErr: errBoom,
			wantErr:  "failed to query jwt providers",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			db := fake.MockDB{
				MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
					if tc.queryErr != nil {
						return nil, tc.queryErr
					}
					return fake.MockRowsToSQLRows(tc.rows), nil
				},
			}
			c := Client{DB: db, Client: &privilege.PrivilegeClient{DB: db}}
			got, err := c.queryJWTProviders(context.Background(), "DEMO_USER")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("\n%s\nqueryJWTProviders(...): want error containing %q, got: %v", tc.reason, tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.reason, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\nqueryJWTProviders(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}
