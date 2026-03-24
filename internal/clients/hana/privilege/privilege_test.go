package privilege

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
)

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
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME", "IS_GRANTABLE"}),
			want:     []string{},
			wantErr:  false,
		},
		"SystemPrivileges": {
			reason: "Should correctly format system privileges and include admin option when grantable",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME", "IS_GRANTABLE"}).
				AddRow("SYSTEMPRIVILEGE", "SELECT", sql.NullString{Valid: false}, sql.NullString{Valid: false}, true).
				AddRow("SYSTEMPRIVILEGE", "INSERT", sql.NullString{Valid: false}, sql.NullString{Valid: false}, false),
			want:    []string{"SELECT WITH ADMIN OPTION", "INSERT"},
			wantErr: false,
		},
		"ObjectPrivileges": {
			reason: "Should correctly format object privileges and include grant option when grantable",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME", "IS_GRANTABLE"}).
				AddRow("TABLE", "SELECT", sql.NullString{String: "SCHEMA1", Valid: true}, sql.NullString{String: "OBJ1", Valid: true}, true).
				AddRow("TABLE", "UPDATE", sql.NullString{String: "SCHEMA2", Valid: true}, sql.NullString{String: "OBJ2", Valid: true}, false).
				AddRow("USERGROUP", "OPERATOR", sql.NullString{Valid: false}, sql.NullString{String: "mygroup", Valid: true}, true),
			want:    []string{"SELECT ON \"SCHEMA1\".\"OBJ1\" WITH GRANT OPTION", "UPDATE ON \"SCHEMA2\".\"OBJ2\"", "USERGROUP OPERATOR ON USERGROUP \"mygroup\" WITH GRANT OPTION"},
			wantErr: false,
		},
		"SchemaAndSourcePrivileges": {
			reason: "Should correctly format schema and source privileges with grant options",
			mockRows: sqlmock.NewRows([]string{"OBJECT_TYPE", "PRIVILEGE", "SCHEMA_NAME", "OBJECT_NAME", "IS_GRANTABLE"}).
				AddRow("SCHEMA", "SELECT", sql.NullString{String: "SCHEMA1", Valid: true}, sql.NullString{Valid: false}, true).
				AddRow("SOURCE", "LINKED DATABASE", sql.NullString{Valid: false}, sql.NullString{String: "myremotesys", Valid: true}, false),
			want:    []string{"SELECT ON SCHEMA \"SCHEMA1\" WITH GRANT OPTION", "LINKED DATABASE ON REMOTE SOURCE \"myremotesys\""},
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
			mockRows: sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME", "IS_GRANTABLE"}),
			want:     []string{},
			wantErr:  false,
		},
		"SchemaQualifiedRoles": {
			reason: "Should correctly format schema-qualified roles and admin option",
			mockRows: sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME", "IS_GRANTABLE"}).
				AddRow(sql.NullString{String: "SCHEMA1", Valid: true}, "ROLE1", true).
				AddRow(sql.NullString{String: "SCHEMA2", Valid: true}, "ROLE2", false),
			want:    []string{"SCHEMA1.ROLE1 WITH ADMIN OPTION", "SCHEMA2.ROLE2"},
			wantErr: false,
		},
		"UnqualifiedRoles": {
			reason: "Should correctly format unqualified roles and admin option",
			mockRows: sqlmock.NewRows([]string{"ROLE_SCHEMA_NAME", "ROLE_NAME", "IS_GRANTABLE"}).
				AddRow(sql.NullString{Valid: false}, "ROLE1", true).
				AddRow(sql.NullString{Valid: false}, "ROLE2", false),
			want:    []string{"ROLE1 WITH ADMIN OPTION", "ROLE2"},
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
			if !cmp.Equal(tc.want, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })) {
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
			name: "MultiWordGrantableSystemPrivilege",
			in:   "CREATE CLIENTSIDE ENCRYPTION KEYPAIR WITH ADMIN OPTION",
			want: Privilege{Type: SystemPrivilegeType, Name: "CREATE CLIENTSIDE ENCRYPTION KEYPAIR", IsGrantable: true},
			ok:   true,
		},
		{
			name: "MultiWordGrantableSystemWithWrongSuffix",
			in:   "CREATE CLIENTSIDE ENCRYPTION KEYPAIR WITH GRANT OPTION",
			want: Privilege{},
			ok:   false,
		},
		{
			name: "SchemaPrivilege",
			in:   "SELECT ON SCHEMA myschema",
			want: Privilege{Type: SchemaPrivilegeType, Name: "SELECT", Identifier: "myschema"},
			ok:   true,
		},
		{
			name: "GrantableSchemaPrivilege",
			in:   "SELECT ON SCHEMA myschema with grant option",
			want: Privilege{Type: SchemaPrivilegeType, Name: "SELECT", Identifier: "myschema", IsGrantable: true},
			ok:   true,
		},
		{
			name: "GrantableSchemaPrivilegeWithWrongSuffix",
			in:   "SELECT ON SCHEMA myschema with admin option",
			want: Privilege{},
			ok:   false,
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
			want: Privilege{Type: ObjectPrivilegeType, Name: "SELECT", Identifier: "defaultschema", SubIdentifier: "myobj"},
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
		// PSE privilege tests
		{
			name: "PSEPrivilege",
			in:   "REFERENCES ON PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY"},
			ok:   true,
		},
		{
			name: "PSEPrivilegeWithGrantOption",
			in:   "REFERENCES ON PSE my_pse WITH GRANT OPTION",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "PSE my_pse", IsGrantable: true},
			ok:   true,
		},
		{
			name: "PSEPrivilegeQuotedName",
			in:   "REFERENCES ON PSE \"my-pse-with-dashes\"",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "PSE my-pse-with-dashes"},
			ok:   true,
		},
		{
			name: "PSEPrivilegeCaseInsensitive",
			in:   "references on pse TestPSE",
			want: Privilege{Type: ObjectPrivilegeType, Name: "references", Identifier: "PSE TestPSE"},
			ok:   true,
		},
		// JWT PROVIDER privilege tests
		{
			name: "JWTProviderPrivilege",
			in:   "REFERENCES ON JWT PROVIDER my_jwt_provider",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "JWT PROVIDER my_jwt_provider"},
			ok:   true,
		},
		{
			name: "JWTProviderPrivilegeWithGrantOption",
			in:   "REFERENCES ON JWT PROVIDER jwt_test WITH GRANT OPTION",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "JWT PROVIDER jwt_test", IsGrantable: true},
			ok:   true,
		},
		{
			name: "JWTProviderPrivilegeCaseInsensitive",
			in:   "references on jwt provider MyJWTProvider",
			want: Privilege{Type: ObjectPrivilegeType, Name: "references", Identifier: "JWT PROVIDER MyJWTProvider"},
			ok:   true,
		},
		// SAML PROVIDER privilege tests
		{
			name: "SAMLProviderPrivilege",
			in:   "REFERENCES ON SAML PROVIDER my_saml_provider",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "SAML PROVIDER my_saml_provider"},
			ok:   true,
		},
		{
			name: "SAMLProviderPrivilegeWithGrantOption",
			in:   "REFERENCES ON SAML PROVIDER saml_test WITH GRANT OPTION",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "SAML PROVIDER saml_test", IsGrantable: true},
			ok:   true,
		},
		// X509 PROVIDER privilege tests
		{
			name: "X509ProviderPrivilege",
			in:   "REFERENCES ON X509 PROVIDER my_x509_provider",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "X509 PROVIDER my_x509_provider"},
			ok:   true,
		},
		{
			name: "X509ProviderPrivilegeWithGrantOption",
			in:   "REFERENCES ON X509 PROVIDER x509_test WITH GRANT OPTION",
			want: Privilege{Type: ObjectPrivilegeType, Name: "REFERENCES", Identifier: "X509 PROVIDER x509_test", IsGrantable: true},
			ok:   true,
		},
		{
			name: "X509ProviderPrivilegeCaseInsensitive",
			in:   "references on x509 provider MyX509Provider",
			want: Privilege{Type: ObjectPrivilegeType, Name: "references", Identifier: "X509 PROVIDER MyX509Provider"},
			ok:   true,
		},
		// Test different privilege types on PSE
		{
			name: "PSEPrivilegeSelect",
			in:   "SELECT ON PSE test_pse",
			want: Privilege{Type: ObjectPrivilegeType, Name: "SELECT", Identifier: "PSE test_pse"},
			ok:   true,
		},
		{
			name: "PSEPrivilegeInsert",
			in:   "INSERT ON PSE test_pse",
			want: Privilege{Type: ObjectPrivilegeType, Name: "INSERT", Identifier: "PSE test_pse"},
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
		regexp.MustCompile(`SELECT ON SCHEMA "myschema"`),
		regexp.MustCompile(`USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek`),
		regexp.MustCompile(`LINKED DATABASE ON REMOTE SOURCE "myremotesys"`),
		regexp.MustCompile(`USERGROUP OPERATOR ON USERGROUP "mygroup"`),
		regexp.MustCompile(`STRUCTURED PRIVILEGE mystruct`),
	}
	for _, pattern := range expectPatterns {
		found := false
		for _, g := range got {
			if pattern.MatchString(g.Body) {
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
		regexp.MustCompile(`SELECT ON SCHEMA "myschema"`),
		regexp.MustCompile(`USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek`),
		regexp.MustCompile(`LINKED DATABASE ON REMOTE SOURCE "myremotesys"`),
		regexp.MustCompile(`USERGROUP OPERATOR ON USERGROUP "mygroup"`),
		regexp.MustCompile(`STRUCTURED PRIVILEGE mystruct`),
	}
	for _, pattern := range expectPatterns {
		found := false
		for _, g := range got {
			if pattern.MatchString(g.Body) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("groupPrivilegesByTypeAndIdentifier() missing expected pattern : %v, got: %v", pattern, got)
		}
	}
}

func Test_groupPrivilegesByTypeAndIdentifier_GrantableSplit(t *testing.T) {
	privs := []Privilege{
		{Type: ObjectPrivilegeType, Name: "SELECT", Identifier: "S1", SubIdentifier: "T1", IsGrantable: true},
		{Type: ObjectPrivilegeType, Name: "INSERT", Identifier: "S1", SubIdentifier: "T1", IsGrantable: true},
		{Type: ObjectPrivilegeType, Name: "UPDATE", Identifier: "S1", SubIdentifier: "T1", IsGrantable: false},
		{Type: SchemaPrivilegeType, Name: "SELECT", Identifier: "S1", IsGrantable: false},
		{Type: SchemaPrivilegeType, Name: "INSERT", Identifier: "S1", IsGrantable: true},
	}
	got := groupPrivilegesByTypeAndIdentifier(privs)

	// Expect two groups for S1.T1: one grantable (SELECT, INSERT), one not (UPDATE)
	var objGrantable, objNonGrantable *PrivilegeGroup
	var schemaGrantable, schemaNonGrantable *PrivilegeGroup
	for i := range got {
		g := got[i]
		if g.Type == ObjectPrivilegeType && regexp.MustCompile(`ON "S1"\."T1"`).MatchString(g.Body) {
			if g.IsGrantable {
				objGrantable = &g
			} else {
				objNonGrantable = &g
			}
		}
		if g.Type == SchemaPrivilegeType && regexp.MustCompile(`ON SCHEMA "S1"`).MatchString(g.Body) {
			if g.IsGrantable {
				schemaGrantable = &g
			} else {
				schemaNonGrantable = &g
			}
		}
	}
	if objGrantable == nil || !regexp.MustCompile(`SELECT, INSERT|INSERT, SELECT`).MatchString(objGrantable.Body) || !objGrantable.IsGrantable {
		t.Errorf("expected grantable group for S1.T1 with SELECT and INSERT, got: %#v", objGrantable)
	}
	if objNonGrantable == nil || !regexp.MustCompile(`UPDATE`).MatchString(objNonGrantable.Body) || objNonGrantable.IsGrantable {
		t.Errorf("expected non-grantable group for S1.T1 with UPDATE, got: %#v", objNonGrantable)
	}
	if schemaGrantable == nil || !regexp.MustCompile(`INSERT ON SCHEMA "S1"`).MatchString(schemaGrantable.Body) || !schemaGrantable.IsGrantable {
		t.Errorf("expected grantable schema group for S1 with INSERT, got: %#v", schemaGrantable)
	}
	if schemaNonGrantable == nil || !regexp.MustCompile(`SELECT ON SCHEMA "S1"`).MatchString(schemaNonGrantable.Body) || schemaNonGrantable.IsGrantable {
		t.Errorf("expected non-grantable schema group for S1 with SELECT, got: %#v", schemaNonGrantable)
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
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "strict",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithSpecPrivileges": {
			reason: "Lax policy should filter to only spec privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{GetDefaultPrivilege("test_user"), "SELECT", "INSERT", "UPDATE", "DELETE"},
				},
				specPrivileges: []string{"INSERT", "SELECT"},
				prevPrivileges: []string{},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"INSERT", "SELECT"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithPrevPrivileges": {
			reason: "Lax policy should include previous privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE", "DELETE"},
				},
				specPrivileges: []string{"UPDATE", "SELECT"},
				prevPrivileges: []string{"SELECT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"SELECT", "UPDATE"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithOverlappingPrivileges": {
			reason: "Lax policy should handle overlapping spec and prev privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT", "UPDATE", "DELETE"},
				},
				specPrivileges: []string{"SELECT", "INSERT"},
				prevPrivileges: []string{"INSERT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"SELECT", "INSERT", "UPDATE"},
				},
				err: nil,
			},
		},
		"LaxPolicyWithNoManagedPrivileges": {
			reason: "Lax policy should return empty privileges when none are managed",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"DELETE", "TRUNCATE", "ALTER"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{"INSERT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{},
				},
				err: nil,
			},
		},
		"LaxPolicyWithEmptyObservedPrivileges": {
			reason: "Lax policy should handle empty observed privileges",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{},
				},
				specPrivileges: []string{"CREATE ANY", "SELECT"},
				prevPrivileges: []string{"INSERT", "UPDATE"},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{},
				},
				err: nil,
			},
		},
		"LaxPolicyWithEmptySpecAndPrevPrivileges": {
			reason: "Lax policy should return empty privileges when spec and prev are empty",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT", "INSERT"},
				},
				specPrivileges: []string{},
				prevPrivileges: []string{},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{},
				},
				err: nil,
			},
		},
		"UnknownPolicy": {
			reason: "Unknown policy should return an error",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "unknown",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				err: fmt.Errorf(ErrUnknownPrivilegeManagementPolicy, "unknown"),
			},
		},
		"EmptyPolicy": {
			reason: "Empty policy should return an error",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{"CREATE ANY", "SELECT"},
				},
				err: fmt.Errorf(ErrUnknownPrivilegeManagementPolicy, ""),
			},
		},
		"LaxPolicyPreservesOtherFields": {
			reason: "Lax policy should preserve other fields in UserObservation",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:               new("test_user"),
					RestrictedUser:         new(true),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"CREATE ANY", "SELECT", "INSERT", "DELETE"},
					Roles:                  []string{"PUBLIC", "ADMIN"},
					Parameters:             map[string]string{"param1": "value1"},
					Usergroup:              new("TEST_GROUP"),
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{},
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:               new("test_user"),
					RestrictedUser:         new(true),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"SELECT"},
					Roles:                  []string{"PUBLIC", "ADMIN"},
					Parameters:             map[string]string{"param1": "value1"},
					Usergroup:              new("TEST_GROUP"),
				},
				err: nil,
			},
		},
		"StrictPolicyPreservesOtherFields": {
			reason: "Strict policy should preserve other fields in UserObservation",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:               new("test_user"),
					RestrictedUser:         new(false),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"CREATE ANY", "SELECT", "INSERT", "DELETE"},
					Roles:                  []string{"PUBLIC"},
					Parameters:             map[string]string{"param1": "value1", "param2": "value2"},
					Usergroup:              new("DEFAULT"),
				},
				specPrivileges: []string{"SELECT"},
				prevPrivileges: []string{"INSERT"},
				policy:         "strict",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:               new("test_user"),
					RestrictedUser:         new(false),
					LastPasswordChangeTime: testTime,
					CreatedAt:              testTime,
					Privileges:             []string{"CREATE ANY", "SELECT", "INSERT", "DELETE"},
					Roles:                  []string{"PUBLIC"},
					Parameters:             map[string]string{"param1": "value1", "param2": "value2"},
					Usergroup:              new("DEFAULT"),
				},
				err: nil,
			},
		},
		"LaxPolicyStrictToLaxTransition": {
			reason: "When transitioning from strict to lax policy, default privileges should not become managed",
			args: args{
				observed: &v1alpha1.UserObservation{
					Username:   new("test_user"),
					Privileges: []string{GetDefaultPrivilege("test_user"), "SELECT", "INSERT", "UPDATE"},
				},
				specPrivileges: []string{"SELECT", "INSERT"},
				prevPrivileges: []string{GetDefaultPrivilege("test_user"), "SELECT", "INSERT", "UPDATE"}, // Previous state from strict mode
				policy:         "lax",
			},
			want: want{
				result: &v1alpha1.UserObservation{
					Username:   new("test_user"),
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

func TestFormatPrivilegeStrings_PSEAndProviderPrivileges(t *testing.T) {
	testCases := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			name: "PSE privileges",
			input: []string{
				"REFERENCES ON PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
				"REFERENCES ON PSE my_pse WITH GRANT OPTION",
				"SELECT ON PSE test_pse",
			},
			expected: []string{
				"REFERENCES ON PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
				"REFERENCES ON PSE my_pse WITH GRANT OPTION",
				"SELECT ON PSE test_pse",
			},
		},
		{
			name: "JWT PROVIDER privileges",
			input: []string{
				"REFERENCES ON JWT PROVIDER my_jwt_provider",
				"REFERENCES ON JWT PROVIDER jwt_test WITH GRANT OPTION",
			},
			expected: []string{
				"REFERENCES ON JWT PROVIDER my_jwt_provider",
				"REFERENCES ON JWT PROVIDER jwt_test WITH GRANT OPTION",
			},
		},
		{
			name: "SAML PROVIDER privileges",
			input: []string{
				"REFERENCES ON SAML PROVIDER my_saml_provider",
				"REFERENCES ON SAML PROVIDER saml_test WITH GRANT OPTION",
			},
			expected: []string{
				"REFERENCES ON SAML PROVIDER my_saml_provider",
				"REFERENCES ON SAML PROVIDER saml_test WITH GRANT OPTION",
			},
		},
		{
			name: "X509 PROVIDER privileges",
			input: []string{
				"REFERENCES ON X509 PROVIDER my_x509_provider",
				"REFERENCES ON X509 PROVIDER x509_test WITH GRANT OPTION",
			},
			expected: []string{
				"REFERENCES ON X509 PROVIDER my_x509_provider",
				"REFERENCES ON X509 PROVIDER x509_test WITH GRANT OPTION",
			},
		},
		{
			name: "Mixed PSE and provider privileges",
			input: []string{
				"REFERENCES ON PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
				"REFERENCES ON JWT PROVIDER jwt_prov",
				"REFERENCES ON SAML PROVIDER saml_prov",
				"REFERENCES ON X509 PROVIDER x509_prov",
			},
			expected: []string{
				"REFERENCES ON PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
				"REFERENCES ON JWT PROVIDER jwt_prov",
				"REFERENCES ON SAML PROVIDER saml_prov",
				"REFERENCES ON X509 PROVIDER x509_prov",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FormatPrivilegeStrings(tc.input, "defaultuser")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !cmp.Equal(tc.expected, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })) {
				t.Errorf("FormatPrivilegeStrings() got = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestFormatPrivilegeStrings_WithGrantableOptions(t *testing.T) {
	in := []string{
		"SELECT ON SCHEMA myschema WITH GRANT OPTION",
		"INSERT ON myobj WITH GRANT OPTION",
		"CREATE SCHEMA WITH ADMIN OPTION",
		"STRUCTURED PRIVILEGE mystruct WITH GRANT OPTION",
		"USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek WITH GRANT OPTION",
		"USERGROUP OPERATOR ON USERGROUP mygroup WITH GRANT OPTION",
		"ROLE ADMIN WITH ADMIN OPTION",
	}
	got, err := FormatPrivilegeStrings(in, "S1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		`SELECT ON SCHEMA "myschema" WITH GRANT OPTION`,
		`INSERT ON "S1"."myobj" WITH GRANT OPTION`,
		"CREATE SCHEMA WITH ADMIN OPTION",
		"STRUCTURED PRIVILEGE mystruct WITH GRANT OPTION",
		"USAGE ON CLIENTSIDE ENCRYPTION COLUMN KEY my_cek WITH GRANT OPTION",
		`USERGROUP OPERATOR ON USERGROUP "mygroup" WITH GRANT OPTION`,
		"ROLE ADMIN WITH ADMIN OPTION",
	}
	if !cmp.Equal(want, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })) {
		t.Errorf("FormatPrivilegeStrings() got = %v, want %v", got, want)
	}
}

func TestParseRoleString_WithOptions(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      Role
		wantError bool
	}{
		{
			name: "PlainRole",
			in:   "ROLE1",
			want: Role{Name: "ROLE1", IsGrantable: false},
		},
		{
			name: "RoleWithAdminOption",
			in:   "ROLE1 WITH ADMIN OPTION",
			want: Role{Name: "ROLE1", IsGrantable: true},
		},
		{
			name:      "RoleWithGrantOptionShouldError",
			in:        "ROLE1 WITH GRANT OPTION",
			want:      Role{},
			wantError: true,
		},
		{
			name: "SchemaQualifiedRoleWithAdmin",
			in:   "MYSCHEMA.ROLE1 WITH ADMIN OPTION",
			want: Role{Name: "MYSCHEMA.ROLE1", IsGrantable: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRoleString(tc.in)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil for input %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseRoleString(%q) got %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatPrivilegeStrings_WithQuoteTrimming(t *testing.T) {
	cases := []struct {
		name     string
		input    []string
		expected []string
		wantErr  bool
	}{
		{
			name: "BasicQuotedPrivileges",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA"`,
				`"SELECT ON SCHEMA OLD_SCHEMA"`,
			},
			expected: []string{
				`INSERT ON SCHEMA "NEW_SCHEMA"`,
				`SELECT ON SCHEMA "OLD_SCHEMA"`,
			},
			wantErr: false,
		},
		{
			name: "QuotedPrivilegesWithInnerQuotes",
			input: []string{
				`"INSERT ON SCHEMA MY_SCHEMA"`,
				`"USERGROUP OPERATOR ON USERGROUP DEFAULT"`,
			},
			expected: []string{
				`INSERT ON SCHEMA "MY_SCHEMA"`,
				`USERGROUP OPERATOR ON USERGROUP "DEFAULT"`,
			},
			wantErr: false,
		},
		{
			name: "MixedQuotedAndUnquoted",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA"`,
				`SELECT ON SCHEMA OLD_SCHEMA`,
				`"USERGROUP OPERATOR ON USERGROUP DEFAULT"`,
			},
			expected: []string{
				`INSERT ON SCHEMA "NEW_SCHEMA"`,
				`SELECT ON SCHEMA "OLD_SCHEMA"`,
				`USERGROUP OPERATOR ON USERGROUP "DEFAULT"`,
			},
			wantErr: false,
		},
		{
			name: "QuotedWithGrantOptions",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA WITH GRANT OPTION"`,
				`"CREATE SCHEMA WITH ADMIN OPTION"`,
			},
			expected: []string{
				`INSERT ON SCHEMA "NEW_SCHEMA" WITH GRANT OPTION`,
				`CREATE SCHEMA WITH ADMIN OPTION`,
			},
			wantErr: false,
		},
		{
			name: "UserSpecificPrivileges",
			input: []string{
				`"INSERT ON SCHEMA MY_SCHEMA"`,
				`"INSERT ON NEW_TABLE"`,
				`"USERGROUP OPERATOR ON USERGROUP DEFAULT"`,
			},
			expected: []string{
				`INSERT ON SCHEMA "MY_SCHEMA"`,
				`INSERT ON "testuser"."NEW_TABLE"`,
				`USERGROUP OPERATOR ON USERGROUP "DEFAULT"`,
			},
			wantErr: false,
		},
		{
			name: "UserOriginalExample",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA"`,
				`"INSERT ON NEW_TABLE"`,
				`"USERGROUP OPERATOR ON USERGROUP DEFAULT"`,
			},
			expected: []string{
				`INSERT ON SCHEMA "NEW_SCHEMA"`,
				`INSERT ON "testuser"."NEW_TABLE"`,
				`USERGROUP OPERATOR ON USERGROUP "DEFAULT"`,
			},
			wantErr: false,
		},
		{
			name: "UserActualScenario",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA"`,
				`"INSERT ON NEW_SCHEMA.NEW_TABLE"`,
				`"USERGROUP OPERATOR ON USERGROUP DEFAULT"`,
			},
			expected: []string{
				`INSERT ON SCHEMA "NEW_SCHEMA"`,
				`INSERT ON "NEW_SCHEMA"."NEW_TABLE"`,
				`USERGROUP OPERATOR ON USERGROUP "DEFAULT"`,
			},
			wantErr: false,
		},
		{
			name: "EmptyQuotes",
			input: []string{
				`""`,
			},
			expected: []string{},
			wantErr:  true, // Empty privilege should cause parse error
		},
		{
			name: "UnclosedQuotes",
			input: []string{
				`"INSERT ON SCHEMA NEW_SCHEMA`,
			},
			expected: []string{},
			wantErr:  true, // Should fail to parse unclosed quote
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FormatPrivilegeStringsWithPreprocessing(tc.input, "testuser")

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %v, got nil", tc.input)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for input %v: %v", tc.input, err)
			}

			// Sort both slices for comparison since order doesn't matter
			if !cmp.Equal(tc.expected, got, cmpopts.SortSlices(func(a, b string) bool { return a < b })) {
				t.Errorf("FormatPrivilegeStringsWithPreprocessing(%v) got = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}

func TestHandlePrivilegeRows_PSEAndProviderPrivileges(t *testing.T) {
	testCases := []struct {
		name        string
		objectType  string
		privilege   string
		schemaName  sql.NullString
		objectName  sql.NullString
		isGrantable bool
		expected    Privilege
	}{
		{
			name:        "PSE privilege",
			objectType:  "PSE",
			privilege:   "REFERENCES",
			schemaName:  sql.NullString{String: "", Valid: false},
			objectName:  sql.NullString{String: "_SAP_DB_ACCESS_PSE_CLIENT_IDENTITY", Valid: true},
			isGrantable: false,
			expected: Privilege{
				Type:        ObjectPrivilegeType,
				Name:        "REFERENCES",
				Identifier:  "PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
				IsGrantable: false,
			},
		},
		{
			name:        "PSE privilege with grant option",
			objectType:  "PSE",
			privilege:   "REFERENCES",
			schemaName:  sql.NullString{String: "", Valid: false},
			objectName:  sql.NullString{String: "my_pse", Valid: true},
			isGrantable: true,
			expected: Privilege{
				Type:        ObjectPrivilegeType,
				Name:        "REFERENCES",
				Identifier:  "PSE my_pse",
				IsGrantable: true,
			},
		},
		{
			name:        "JWT PROVIDER privilege",
			objectType:  "JWT PROVIDER",
			privilege:   "REFERENCES",
			schemaName:  sql.NullString{String: "", Valid: false},
			objectName:  sql.NullString{String: "my_jwt_provider", Valid: true},
			isGrantable: false,
			expected: Privilege{
				Type:        ObjectPrivilegeType,
				Name:        "REFERENCES",
				Identifier:  "JWT PROVIDER my_jwt_provider",
				IsGrantable: false,
			},
		},
		{
			name:        "SAML PROVIDER privilege with grant option",
			objectType:  "SAML PROVIDER",
			privilege:   "REFERENCES",
			schemaName:  sql.NullString{String: "", Valid: false},
			objectName:  sql.NullString{String: "saml_test", Valid: true},
			isGrantable: true,
			expected: Privilege{
				Type:        ObjectPrivilegeType,
				Name:        "REFERENCES",
				Identifier:  "SAML PROVIDER saml_test",
				IsGrantable: true,
			},
		},
		{
			name:        "X509 PROVIDER privilege",
			objectType:  "X509 PROVIDER",
			privilege:   "REFERENCES",
			schemaName:  sql.NullString{String: "", Valid: false},
			objectName:  sql.NullString{String: "x509_provider", Valid: true},
			isGrantable: false,
			expected: Privilege{
				Type:        ObjectPrivilegeType,
				Name:        "REFERENCES",
				Identifier:  "X509 PROVIDER x509_provider",
				IsGrantable: false,
			},
		},
		{
			name:        "Regular system privilege for comparison",
			objectType:  "SYSTEMPRIVILEGE",
			privilege:   "CREATE SCHEMA",
			schemaName:  sql.NullString{String: "", Valid: false},
			objectName:  sql.NullString{String: "", Valid: false},
			isGrantable: true,
			expected: Privilege{
				Type:        SystemPrivilegeType,
				Name:        "CREATE SCHEMA",
				IsGrantable: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create mock SQL rows
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("failed to create mock DB: %v", err)
			}
			defer db.Close()

			rows := sqlmock.NewRows([]string{"object_type", "privilege", "schema_name", "object_name", "is_grantable"}).
				AddRow(tc.objectType, tc.privilege, tc.schemaName, tc.objectName, tc.isGrantable)

			mock.ExpectQuery("SELECT").WillReturnRows(rows)

			sqlRows, err := db.QueryContext(context.Background(), "SELECT object_type, privilege, schema_name, object_name, is_grantable FROM dummy")
			if err != nil {
				t.Fatalf("failed to create SQL rows: %v", err)
			}
			defer sqlRows.Close()

			// Move to first row
			if !sqlRows.Next() {
				t.Fatal("no rows returned")
			}

			// Test the function
			got, err := handlePrivilegeRows(sqlRows)
			if err != nil {
				t.Fatalf("handlePrivilegeRows() error = %v", err)
			}

			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("handlePrivilegeRows() got = %+v, want %+v", got, tc.expected)
			}
		})
	}
}

func TestFormatSpecialObjectPrivilege(t *testing.T) {
	testCases := []struct {
		name       string
		privilege  string
		identifier string
		expected   string
	}{
		{
			name:       "PSE privilege",
			privilege:  "REFERENCES",
			identifier: "PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
			expected:   "REFERENCES ON PSE _SAP_DB_ACCESS_PSE_CLIENT_IDENTITY",
		},
		{
			name:       "JWT PROVIDER privilege",
			privilege:  "REFERENCES",
			identifier: "JWT PROVIDER my_jwt_provider",
			expected:   "REFERENCES ON JWT PROVIDER my_jwt_provider",
		},
		{
			name:       "SAML PROVIDER privilege",
			privilege:  "REFERENCES",
			identifier: "SAML PROVIDER my_saml_provider",
			expected:   "REFERENCES ON SAML PROVIDER my_saml_provider",
		},
		{
			name:       "X509 PROVIDER privilege",
			privilege:  "REFERENCES",
			identifier: "X509 PROVIDER my_x509_provider",
			expected:   "REFERENCES ON X509 PROVIDER my_x509_provider",
		},
		{
			name:       "Regular object privilege",
			privilege:  "SELECT",
			identifier: "my_table",
			expected:   `SELECT ON "my_table"`,
		},
		{
			name:       "Regular object with special chars",
			privilege:  "INSERT",
			identifier: "table-with-dashes",
			expected:   `INSERT ON "table-with-dashes"`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatSpecialObjectPrivilege(tc.privilege, tc.identifier)
			if got != tc.expected {
				t.Errorf("formatSpecialObjectPrivilege(%q, %q) = %q, want %q", tc.privilege, tc.identifier, got, tc.expected)
			}
		})
	}
}

func TestPrivilegesEqualIgnoringGrantable(t *testing.T) {
	cases := []struct {
		name     string
		desired  []string
		observed []string
		want     bool
	}{
		{
			name:     "ExactMatch",
			desired:  []string{"CREATE SCHEMA WITH ADMIN OPTION"},
			observed: []string{"CREATE SCHEMA WITH ADMIN OPTION"},
			want:     true,
		},
		{
			name:     "BothWithout",
			desired:  []string{"CREATE SCHEMA"},
			observed: []string{"CREATE SCHEMA"},
			want:     true,
		},
		{
			name:     "DesiredWithoutObservedWith",
			desired:  []string{"CREATE SCHEMA"},
			observed: []string{"CREATE SCHEMA WITH ADMIN OPTION"},
			want:     true,
		},
		{
			name:     "DesiredWithObservedWithout",
			desired:  []string{"CREATE SCHEMA WITH ADMIN OPTION"},
			observed: []string{"CREATE SCHEMA"},
			want:     true,
		},
		{
			name:     "MissingObserved",
			desired:  []string{"CREATE SCHEMA"},
			observed: []string{},
			want:     false,
		},
		{
			name:     "ExtraObserved",
			desired:  []string{},
			observed: []string{"CREATE SCHEMA"},
			want:     false,
		},
		{
			name:     "MultipleIgnoresGrantable",
			desired:  []string{"CREATE SCHEMA", "SELECT ON SCHEMA myschema"},
			observed: []string{"CREATE SCHEMA WITH ADMIN OPTION", "SELECT ON SCHEMA myschema WITH GRANT OPTION"},
			want:     true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PrivilegesEqualIgnoringGrantable(tc.desired, tc.observed, "defaultschema")
			if err != nil {
				t.Fatalf("PrivilegesEqualIgnoringGrantable() unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("PrivilegesEqualIgnoringGrantable(%v, %v) = %v, want %v", tc.desired, tc.observed, got, tc.want)
			}
		})
	}
}
