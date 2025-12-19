package privilege

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Helper functions for creating pointers
func stringPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}

func TestPrivilegeClient_Grant(t *testing.T) {
	errBoom := errors.New("boom")
	cases := map[string]struct {
		reason  string
		db      fake.MockDB
		input   []string
		wantErr error
	}{
		"GrantError": {
			reason: "Should return error when database execution fails",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, errBoom },
			},
			input:   []string{"SELECT"},
			wantErr: errBoom,
		},
		"GrantSuccess": {
			reason: "Should successfully grant single privilege",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{"SELECT"},
			wantErr: nil,
		},
		"GrantMultiplePrivileges": {
			reason: "Should successfully grant multiple privileges",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{"SELECT", "INSERT", "UPDATE"},
			wantErr: nil,
		},
		"GrantMixedPrivilegeTypes": {
			reason: "Should successfully grant mixed privilege types (system, schema, object)",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{"SELECT", "SELECT ON SCHEMA myschema", "SELECT ON mytable", "LINKED DATABASE ON REMOTE SOURCE myremotesys", "USERGROUP OPERATOR ON USERGROUP mygroup"},
			wantErr: nil,
		},
		"GrantEmptyList": {
			reason: "Should handle empty privilege list gracefully",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{},
			wantErr: nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &PrivilegeClient{DB: tc.db}
			err := c.GrantPrivileges(context.Background(), "defaultschema", "USER1", tc.input)
			if diff := cmp.Diff(tc.wantErr, err, cmp.Comparer(func(x, y error) bool {
				return (x == nil && y == nil) || (x != nil && y != nil)
			})); diff != "" {
				t.Errorf("\n%s\nGrant() error diff: %s", tc.reason, diff)
			}
		})
	}
}

func TestPrivilegeClient_Revoke(t *testing.T) {
	errBoom := errors.New("boom")
	cases := map[string]struct {
		reason  string
		db      fake.MockDB
		input   []string
		wantErr error
	}{
		"RevokeError": {
			reason: "Should return error when database execution fails",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, errBoom },
			},
			input:   []string{"SELECT"},
			wantErr: errBoom,
		},
		"RevokeSuccess": {
			reason: "Should successfully revoke single privilege",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{"SELECT"},
			wantErr: nil,
		},
		"RevokeMultiplePrivileges": {
			reason: "Should successfully revoke multiple privileges",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{"SELECT", "INSERT", "UPDATE"},
			wantErr: nil,
		},
		"RevokeMixedPrivilegeTypes": {
			reason: "Should successfully revoke mixed privilege types (system, schema, object)",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{"SELECT", "SELECT ON SCHEMA myschema", "SELECT ON mytable", "LINKED DATABASE ON REMOTE SOURCE myremotesys", "USERGROUP OPERATOR ON USERGROUP mygroup"},
			wantErr: nil,
		},
		"RevokeEmptyList": {
			reason: "Should handle empty privilege list gracefully",
			db: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) { return nil, nil },
			},
			input:   []string{},
			wantErr: nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &PrivilegeClient{DB: tc.db}
			err := c.RevokePrivileges(context.Background(), "defaultschema", "USER1", tc.input)
			if diff := cmp.Diff(tc.wantErr, err, cmp.Comparer(func(x, y error) bool {
				return (x == nil && y == nil) || (x != nil && y != nil)
			})); diff != "" {
				t.Errorf("\n%s\nRevoke() error diff: %s", tc.reason, diff)
			}
		})
	}
}

func TestPrivilegeClient_QueryPrivileges(t *testing.T) {
	cases := map[string]struct {
		reason   string
		mockRows *sqlmock.Rows
		mockErr  error
		want     []string
		wantErr  bool
	}{
		"NoRows": {
			reason:   "Should return empty slice when user has no privileges",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME"}),
			want:     []string{},
			wantErr:  false,
		},
		"SystemPrivileges": {
			reason: "Should correctly format system privileges",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME"}).
				AddRow("SYSTEMPRIVILEGE", "SELECT", sql.NullString{Valid: false}, sql.NullString{Valid: false}).
				AddRow("SYSTEMPRIVILEGE", "INSERT", sql.NullString{Valid: false}, sql.NullString{Valid: false}),
			want:    []string{"SELECT", "INSERT"},
			wantErr: false,
		},
		"ObjectPrivileges": {
			reason: "Should correctly format object privileges",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME"}).
				AddRow("TABLE", "SELECT", sql.NullString{String: "SCHEMA1", Valid: true}, sql.NullString{String: "OBJ1", Valid: true}).
				AddRow("TABLE", "UPDATE", sql.NullString{String: "SCHEMA2", Valid: true}, sql.NullString{String: "OBJ2", Valid: true}).
				AddRow("USERGROUP", "OPERATOR", sql.NullString{Valid: false}, sql.NullString{String: "mygroup", Valid: true}),
			want:    []string{"SELECT ON SCHEMA1.OBJ1", "UPDATE ON SCHEMA2.OBJ2", "USERGROUP OPERATOR ON USERGROUP mygroup"},
			wantErr: false,
		},
		"SchemaPrivileges": {
			reason: "Should correctly format schema privileges",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME"}).
				AddRow("SCHEMA", "SELECT", sql.NullString{String: "SCHEMA1", Valid: true}, sql.NullString{Valid: false}).
				AddRow("SCHEMA", "INSERT", sql.NullString{String: "SCHEMA2", Valid: true}, sql.NullString{Valid: false}),
			want:    []string{"SELECT ON SCHEMA SCHEMA1", "INSERT ON SCHEMA SCHEMA2"},
			wantErr: false,
		},
		"SourcePrivileges": {
			reason: "Should correctly format source privileges",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME"}).
				AddRow("SOURCE", "LINKED DATABASE", sql.NullString{Valid: false}, sql.NullString{String: "myremotesys", Valid: true}),
			want:    []string{"LINKED DATABASE ON REMOTE SOURCE myremotesys"},
			wantErr: false,
		},
		"MixedPrivileges": {
			reason: "Should correctly handle mixed privilege types",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME"}).
				AddRow("SYSTEMPRIVILEGE", "SELECT", sql.NullString{Valid: false}, sql.NullString{Valid: false}).
				AddRow("SCHEMA", "INSERT", sql.NullString{String: "SCHEMA1", Valid: true}, sql.NullString{Valid: false}).
				AddRow("TABLE", "UPDATE", sql.NullString{String: "SCHEMA2", Valid: true}, sql.NullString{String: "OBJ1", Valid: true}).
				AddRow("SOURCE", "LINKED DATABASE", sql.NullString{Valid: false}, sql.NullString{String: "myremotesys", Valid: true}).
				AddRow("USERGROUP", "OPERATOR", sql.NullString{Valid: false}, sql.NullString{String: "mygroup", Valid: true}),
			want:    []string{"INSERT ON SCHEMA SCHEMA1", "UPDATE ON SCHEMA2.OBJ1", "SELECT", "LINKED DATABASE ON REMOTE SOURCE myremotesys", "USERGROUP OPERATOR ON USERGROUP mygroup"},
			wantErr: false,
		},
		"QueryError": {
			reason:   "Should return error when database query fails",
			mockRows: nil,
			mockErr:  errors.New("boom"),
			want:     []string{},
			wantErr:  true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			db := fake.MockDB{
				MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
					if tc.mockErr != nil {
						return nil, tc.mockErr
					}
					return fake.MockRowsToSQLRows(tc.mockRows), nil
				},
			}
			c := &PrivilegeClient{DB: db}
			got, err := c.QueryPrivileges(context.Background(), "USER1", GranteeTypeUser)
			if (err != nil) != tc.wantErr {
				t.Fatalf("\n%s\nQueryPrivileges() error = %v, wantErr %v", tc.reason, err, tc.wantErr)
			}
			if !cmp.Equal(tc.want, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })) {
				t.Errorf("\n%s\nQueryPrivileges() got = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

func TestPrivilegeClient_QueryRoles(t *testing.T) {
	cases := map[string]struct {
		reason   string
		mockRows *sqlmock.Rows
		mockErr  error
		want     []string
		wantErr  bool
	}{
		"NoRows": {
			reason:   "Should return empty slice when user has no roles",
			mockRows: sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME"}),
			want:     []string{},
			wantErr:  false,
		},
		"SchemaQualifiedRoles": {
			reason: "Should correctly format schema-qualified roles",
			mockRows: sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME"}).
				AddRow(sql.NullString{String: "SCHEMA1", Valid: true}, "ROLE1").
				AddRow(sql.NullString{String: "SCHEMA2", Valid: true}, "ROLE2"),
			want:    []string{"SCHEMA1.ROLE1", "SCHEMA2.ROLE2"},
			wantErr: false,
		},
		"UnqualifiedRoles": {
			reason: "Should correctly format unqualified roles",
			mockRows: sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME"}).
				AddRow(sql.NullString{Valid: false}, "ROLE1").
				AddRow(sql.NullString{Valid: false}, "ROLE2"),
			want:    []string{"ROLE1", "ROLE2"},
			wantErr: false,
		},
		"MixedRoles": {
			reason: "Should correctly handle mix of qualified and unqualified roles",
			mockRows: sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME"}).
				AddRow(sql.NullString{String: "SCHEMA1", Valid: true}, "ROLE1").
				AddRow(sql.NullString{Valid: false}, "ROLE2").
				AddRow(sql.NullString{String: "SCHEMA3", Valid: true}, "ROLE3"),
			want:    []string{"SCHEMA1.ROLE1", "ROLE2", "SCHEMA3.ROLE3"},
			wantErr: false,
		},
		"QueryError": {
			reason:   "Should return error when database query fails",
			mockRows: nil,
			mockErr:  errors.New("boom"),
			want:     []string{},
			wantErr:  true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			db := fake.MockDB{
				MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
					if tc.mockErr != nil {
						return nil, tc.mockErr
					}
					return fake.MockRowsToSQLRows(tc.mockRows), nil
				},
			}
			c := &PrivilegeClient{DB: db}
			got, err := c.QueryRoles(context.Background(), "USER1", GranteeTypeUser)
			if (err != nil) != tc.wantErr {
				t.Fatalf("\n%s\nQueryRoles() error = %v, wantErr %v", tc.reason, err, tc.wantErr)
			}
			if !cmp.Equal(tc.want, got) {
				t.Errorf("\n%s\nQueryRoles() got = %v, want %v", tc.reason, got, tc.want)
			}
		})
	}
}

func Test_stringToPrivilege(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Privilege
		ok   bool
	}{
		{
			name: "SystemPrivilege",
			in:   "SELECT",
			want: Privilege{Type: SystemPrivilegeType, Name: "SELECT"},
			ok:   true,
		},
		{
			name: "SchemaPrivilege",
			in:   "SELECT ON SCHEMA myschema",
			want: Privilege{Type: SchemaPrivilegeType, Name: "SELECT", Identifier: "myschema"},
			ok:   true,
		},
		{
			name: "CEKAdminSchemaPrivilege",
			in:   "CLIENTSIDE ENCRYPTION COLUMN KEY ADMIN ON SCHEMA MySchema",
			want: Privilege{Type: SchemaPrivilegeType, Name: "CLIENTSIDE ENCRYPTION COLUMN KEY ADMIN", Identifier: "MySchema"},
			ok:   true,
		},
		{
			name: "SourcePrivilege",
			in:   "SELECT ON REMOTE SOURCE src",
			want: Privilege{Type: SourcePrivilegeType, Name: "SELECT", Identifier: "src"},
			ok:   true,
		},
		{
			name: "LinkedDatabasePrivilege",
			in:   "LINKED DATABASE ON REMOTE SOURCE myremotesys",
			want: Privilege{Type: SourcePrivilegeType, Name: "LINKED DATABASE", Identifier: "myremotesys"},
			ok:   true,
		},
		{
			name: "ObjectPrivilege",
			in:   "SELECT ON myobj",
			want: Privilege{Type: ObjectPrivilegeType, Name: "SELECT", Identifier: "defaultschema.myobj"},
			ok:   true,
		},
		{
			name: "UsergroupOperatorPrivilege",
			in:   "USERGROUP OPERATOR ON USERGROUP mygroup",
			want: Privilege{Type: UserGroupPrivilegeType, Name: "USERGROUP OPERATOR", Identifier: "mygroup"},
			ok:   true,
		},
		{
			name: "ColumnKeyPrivilege",
			in:   "USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek",
			want: Privilege{Type: ColumnKeyPrivilegeType, Name: "USAGE", Identifier: "my_cek"},
			ok:   true,
		},
		{
			name: "WrongColumnKeyPrivilege",
			in:   "TRIGGER ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek",
			want: Privilege{},
			ok:   false,
		},
		{
			name: "StructuredPrivilege",
			in:   "STRUCTURED PRIVILEGE mystruct",
			want: Privilege{Type: StructuredPrivilegeType, Name: "STRUCTURED PRIVILEGE", Identifier: "mystruct"},
			ok:   true,
		},
		{
			name: "EmptyString",
			in:   "",
			want: Privilege{},
			ok:   false,
		},
		{
			name: "CaseInsensitiveSchema",
			in:   "select on schema MySchema",
			want: Privilege{Type: SchemaPrivilegeType, Name: "select", Identifier: "MySchema"},
			ok:   true,
		},
		{
			name: "CaseInsensitiveRemoteSource",
			in:   "INSERT ON remote source MySource",
			want: Privilege{Type: SourcePrivilegeType, Name: "INSERT", Identifier: "MySource"},
			ok:   true,
		},
		{
			name: "ComplexPrivilegeName",
			in:   "CREATE ANY TABLE",
			want: Privilege{Type: SystemPrivilegeType, Name: "CREATE ANY TABLE"},
			ok:   true,
		},
		{
			name: "WhitespaceHandling",
			in:   "  SELECT ON SCHEMA   myschema  ",
			want: Privilege{Type: SchemaPrivilegeType, Name: "SELECT", Identifier: "myschema"},
			ok:   true,
		},
		{
			name: "PrivilegeNameWithTrailingSpace",
			in:   "CREATE ANY TABLE ",
			want: Privilege{Type: SystemPrivilegeType, Name: "CREATE ANY TABLE"},
			ok:   true,
		},
		{
			name: "MultiWordPrivilegeNoTrailingSpace",
			in:   "CREATE ANY TABLE",
			want: Privilege{Type: SystemPrivilegeType, Name: "CREATE ANY TABLE"},
			ok:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePrivilegeString(tc.in, "defaultschema")
			if (err == nil) != tc.ok {
				t.Errorf("parsePrivilegeString(%q) error = %v, want ok %v", tc.in, err, tc.ok)
			}
			if tc.ok && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parsePrivilegeString(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func Test_groupPrivilegesByType(t *testing.T) {
	in := []string{
		"SELECT",
		"INSERT",
		"SELECT ON myobj",
		"INSERT ON myobj",
		"SELECT ON SCHEMA myschema",
		"USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek",
		"LINKED DATABASE ON REMOTE SOURCE myremotesys",
		"USERGROUP OPERATOR ON USERGROUP mygroup",
		"STRUCTURED PRIVILEGE mystruct",
	}
	got, err := groupPrivilegesByType(in, "defaultschema")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should group by type and identifier
	expectPatterns := []*regexp.Regexp{
		regexp.MustCompile(`SELECT, INSERT|INSERT, SELECT`),
		regexp.MustCompile(`SELECT ON SCHEMA myschema`),
		regexp.MustCompile(`USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek`),
		regexp.MustCompile(`LINKED DATABASE ON REMOTE SOURCE myremotesys`),
		regexp.MustCompile(`USERGROUP OPERATOR ON USERGROUP mygroup`),
		regexp.MustCompile(`STRUCTURED PRIVILEGE mystruct`),
	}
	for _, pattern := range expectPatterns {
		found := false
		for _, s := range got {
			if pattern.MatchString(s) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("groupPrivilegesByType() missing expected pattern : %v, got: %v", pattern, got)
		}
	}
}

func Test_groupPrivilegesByTypeAndIdentifier(t *testing.T) {
	privs := []Privilege{
		{Type: SystemPrivilegeType, Name: "SELECT", Identifier: ""},
		{Type: SystemPrivilegeType, Name: "INSERT", Identifier: ""},
		{Type: ObjectPrivilegeType, Name: "SELECT", Identifier: "OBJ1"},
		{Type: ObjectPrivilegeType, Name: "INSERT", Identifier: "OBJ1"},
		{Type: SchemaPrivilegeType, Name: "SELECT", Identifier: "myschema"},
		{Type: SourcePrivilegeType, Name: "LINKED DATABASE", Identifier: "myremotesys"},
		{Type: ColumnKeyPrivilegeType, Name: "USAGE", Identifier: "my_cek"},
		{Type: UserGroupPrivilegeType, Name: "USERGROUP OPERATOR", Identifier: "mygroup"},
		{Type: StructuredPrivilegeType, Name: "STRUCTURED PRIVILEGE", Identifier: "mystruct"},
	}
	got := groupPrivilegesByTypeAndIdentifier(privs)
	expectPatterns := []*regexp.Regexp{
		regexp.MustCompile(`SELECT, INSERT|INSERT, SELECT`),
		regexp.MustCompile(`SELECT ON SCHEMA myschema`),
		regexp.MustCompile(`USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek`),
		regexp.MustCompile(`LINKED DATABASE ON REMOTE SOURCE myremotesys`),
		regexp.MustCompile(`USERGROUP OPERATOR ON USERGROUP mygroup`),
		regexp.MustCompile(`STRUCTURED PRIVILEGE mystruct`),
	}
	for _, pattern := range expectPatterns {
		found := false
		for _, s := range got {
			if pattern.MatchString(s) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("groupPrivilegesByTypeAndIdentifier() missing expected pattern : %v, got: %v", pattern, got)
		}
	}
}

func TestFilterManagedPrivileges(t *testing.T) {
	testTime := metav1.Now()

	type args struct {
		observed       *v1alpha1.UserObservation
		specPrivileges []string
		prevPrivileges []string
		policy         string
	}

	type want struct {
		result *v1alpha1.UserObservation
		err    error
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"StrictPolicy": {
			reason: "Strict policy should return observed privileges unchanged",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "strict",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithSpecPrivileges": {
			reason: "Lax policy should filter to only spec privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY ON SCHEMA test_user", "SELECT", "INSERT", "UPDATE", "DELETE"},
				},
				specPrivileges: []string{"INSERT", "SELECT"},
				prevPrivileges: []string{},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"INSERT", "SELECT"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithPrevPrivileges": {
			reason: "Lax policy should include previous privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE", "DELETE"},
				},
				specPrivileges: []string{"UPDATE", "SELECT"},
				prevPrivileges: []string{"SELECT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"SELECT", "UPDATE"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithOverlappingPrivileges": {
			reason: "Lax policy should handle overlapping spec and prev privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE", "DELETE"},
				},
				specPrivileges: []string{"SELECT", "INSERT"},
				prevPrivileges: []string{"INSERT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"SELECT", "INSERT", "UPDATE"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithNoManagedPrivileges": {
			reason: "Lax policy should return empty privileges when none are managed",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"DELETE", "TRUNCATE", "ALTER"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{"INSERT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{},
				},
				err: nil,
			},
		},
		"LaxPolicyWithEmptyObservedPrivileges": {
			reason: "Lax policy should handle empty observed privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{},
				},
				specPrivileges: []string{"CREATE ANY", "SELECT"},
				prevPrivileges: []string{"INSERT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{},
				},
				err: nil,
			},
		},
		"LaxPolicyWithEmptySpecAndPrevPrivileges": {
			reason: "Lax policy should return empty privileges when spec and prev are empty",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT"},
				},
				specPrivileges: []string{},
				prevPrivileges: []string{},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{},
				},
				err: nil,
			},
		},
		"UnknownPolicy": {
			reason: "Unknown policy should return an error",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "unknown",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				err: fmt.Errorf(ErrUnknownPrivilegeManagementPolicy, "unknown"),
			},
		},
		"EmptyPolicy": {
			reason: "Empty policy should return an error",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				err: fmt.Errorf(ErrUnknownPrivilegeManagementPolicy, ""),
			},
		},
		"LaxPolicyPreservesOtherFields": {
			reason: "Lax policy should preserve other fields in UserObservation",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:               stringPtr("test_user"),
					RestrictedUser:         boolPtr(true),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"CREATE ANY", "SELECT", "INSERT", "DELETE"},
					Roles:                  []string{"PUBLIC", "ADMIN"},
					Parameters:             map[string]string{"param1": "value1"},
					Usergroup:              stringPtr("TEST_GROUP"),
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:               stringPtr("test_user"),
					RestrictedUser:         boolPtr(true),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"SELECT"},
					Roles:                  []string{"PUBLIC", "ADMIN"},
					Parameters:             map[string]string{"param1": "value1"},
					Usergroup:              stringPtr("TEST_GROUP"),
				},
				err: nil,
			},
		},
		"StrictPolicyPreservesOtherFields": {
			reason: "Strict policy should preserve other fields in UserObservation",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:               stringPtr("test_user"),
					RestrictedUser:         boolPtr(false),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"CREATE ANY", "SELECT", "INSERT", "DELETE"},
					Roles:                  []string{"PUBLIC"},
					Parameters:             map[string]string{"param1": "value1", "param2": "value2"},
					Usergroup:              stringPtr("DEFAULT"),
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{"INSERT"},
				policy:         "strict",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:               stringPtr("test_user"),
					RestrictedUser:         boolPtr(false),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"CREATE ANY", "SELECT", "INSERT", "DELETE"},
					Roles:                  []string{"PUBLIC"},
					Parameters:             map[string]string{"param1": "value1", "param2": "value2"},
					Usergroup:              stringPtr("DEFAULT"),
				},
				err: nil,
			},
		},
		"LaxPolicyStrictToLaxTransition": {
			reason: "When transitioning from strict to lax policy, default privileges should not become managed",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"CREATE ANY ON SCHEMA test_user", "SELECT", "INSERT", "UPDATE"},
				},
				specPrivileges: []string{"SELECT", "INSERT"},
				prevPrivileges: []string{"CREATE ANY ON SCHEMA test_user", "SELECT", "INSERT", "UPDATE"}, // Previous state from strict mode
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   stringPtr("test_user"),
					Privileges: []string{"SELECT", "INSERT", "UPDATE"},
				},
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := FilterManagedPrivileges(tc.args.observed, tc.args.specPrivileges, tc.args.prevPrivileges, tc.args.policy, "test_user")

			if tc.want.err != nil {
				if err == nil {
					t.Errorf("\n%s\nFilterManagedPrivileges(...): expected error %v, got nil", tc.reason, tc.want.err)
					return
				}
				if err.Error() != tc.want.err.Error() {
					t.Errorf("\n%s\nFilterManagedPrivileges(...): expected error %v, got %v", tc.reason, tc.want.err, err)
					return
				}
			} else if err != nil {
				t.Errorf("\n%s\nFilterManagedPrivileges(...): unexpected error: %v", tc.reason, err)
				return
			}

			if diff := cmp.Diff(tc.want.result, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("\n%s\nFilterManagedPrivileges(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

func TestFilterManagedPrivilegesNilObservation(t *testing.T) {
	// Test with nil observation - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("FilterManagedPrivileges panicked with nil observation: %v", r)
		}
	}()

	_, err := FilterManagedPrivileges(nil, []string{"CREATE ANY"}, []string{}, "strict", "test_user")
	if err == nil {
		t.Error("Expected error when observation is nil, got nil")
		return
	}

	expectedError := "observed user observation cannot be nil"
	if err.Error() != expectedError {
		t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
	}
}
