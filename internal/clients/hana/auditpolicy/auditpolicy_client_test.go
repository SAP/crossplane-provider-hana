package auditpolicy

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
)

func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.AuditPolicyParameters
	}

	type want struct {
		observed *v1alpha1.AuditPolicyObservation
		err      error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrRead": {
			reason: "Any errors encountered while reading the audit policy should be returned",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
				},
			},
			want: want{
				observed: nil,
				err:      errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully read a role",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditActions:        []string{"GRANT"},
					AuditStatus:         "ALL",
					AuditLevel:          "INFO",
					AuditTrailRetention: func(i int) *int { return &i }(7),
					Enabled:             func(b bool) *bool { return &b }(true),
				},
			},
			want: want{
				observed: &v1alpha1.AuditPolicyObservation{
					PolicyName:          "",
					AuditActions:        nil,
					AuditStatus:         "",
					AuditLevel:          "",
					AuditTrailRetention: nil,
					Enabled:             nil,
				},
				err: nil,
			},
		},
		"ForPrincipals": {
			reason: "A FOR PRINCIPALS policy populates PRINCIPAL_NAME; principals are observed with ExceptPrincipals=false and repeated actions are de-duplicated",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						cols := []string{"AUDIT_POLICY_NAME", "EVENT_STATUS", "EVENT_ACTION", "EVENT_LEVEL", "RETENTION_PERIOD", "IS_AUDIT_POLICY_ACTIVE", "PRINCIPAL_NAME", "EXCEPT_PRINCIPAL_NAME", "PRINCIPAL_TYPE"}
						rows := sqlmock.NewRows(cols).
							AddRow("DEMO_AUDIT_POLICY", "SUCCESSFUL EVENTS", "ACTIONS", "CRITICAL", 7, "TRUE", "MONITORING_ADMIN", nil, "USER").
							AddRow("DEMO_AUDIT_POLICY", "SUCCESSFUL EVENTS", "ACTIONS", "CRITICAL", 7, "TRUE", "TECHNICAL_USER_GROUP", nil, "USERGROUP")
						return fake.MockRowsToSQLRows(rows), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
				},
			},
			want: want{
				observed: &v1alpha1.AuditPolicyObservation{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditActions:        []string{"ACTIONS"},
					AuditStatus:         "SUCCESSFUL",
					AuditLevel:          "CRITICAL",
					AuditTrailRetention: func(i int) *int { return &i }(7),
					Enabled:             func(b bool) *bool { return &b }(true),
					ExceptPrincipals:    false,
					AuditPrincipals: []v1alpha1.AuditPrincipal{
						{Type: "USER", Name: "MONITORING_ADMIN"},
						{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
					},
				},
				err: nil,
			},
		},
		"ExceptForPrincipals": {
			reason: "An EXCEPT FOR PRINCIPALS policy populates EXCEPT_PRINCIPAL_NAME; principals are observed with ExceptPrincipals=true",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						cols := []string{"AUDIT_POLICY_NAME", "EVENT_STATUS", "EVENT_ACTION", "EVENT_LEVEL", "RETENTION_PERIOD", "IS_AUDIT_POLICY_ACTIVE", "PRINCIPAL_NAME", "EXCEPT_PRINCIPAL_NAME", "PRINCIPAL_TYPE"}
						rows := sqlmock.NewRows(cols).
							AddRow("DEMO_AUDIT_POLICY", "SUCCESSFUL EVENTS", "ACTIONS", "CRITICAL", 7, "FALSE", nil, "MONITORING_ADMIN", "USER").
							AddRow("DEMO_AUDIT_POLICY", "SUCCESSFUL EVENTS", "ACTIONS", "CRITICAL", 7, "FALSE", nil, "TECHNICAL_USER_GROUP", "USERGROUP")
						return fake.MockRowsToSQLRows(rows), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
				},
			},
			want: want{
				observed: &v1alpha1.AuditPolicyObservation{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditActions:        []string{"ACTIONS"},
					AuditStatus:         "SUCCESSFUL",
					AuditLevel:          "CRITICAL",
					AuditTrailRetention: func(i int) *int { return &i }(7),
					Enabled:             func(b bool) *bool { return &b }(false),
					ExceptPrincipals:    true,
					AuditPrincipals: []v1alpha1.AuditPrincipal{
						{Type: "USER", Name: "MONITORING_ADMIN"},
						{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
					},
				},
				err: nil,
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
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

func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.AuditPolicyParameters
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
			reason: "Any errors encountered while creating the audit policy should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditTrailRetention: func(i int) *int { return &i }(7),
					Enabled:             func(b bool) *bool { return &b }(true),
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully create an audit policy",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditTrailRetention: func(i int) *int { return &i }(7),
					Enabled:             func(b bool) *bool { return &b }(true),
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
			err := c.Create(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestPrepareCreateSql(t *testing.T) {
	retention := func(i int) *int { return &i }

	type args struct {
		parameters *v1alpha1.AuditPolicyParameters
	}

	cases := map[string]struct {
		reason string
		args   args
		want   string
	}{
		"NoPrincipals": {
			reason: "The statement should not contain a FOR PRINCIPALS clause when no principals are configured",
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditStatus:         "ALL",
					AuditActions:        []string{"GRANT ANY", "REVOKE ANY"},
					AuditLevel:          "INFO",
					AuditTrailRetention: retention(30),
				},
			},
			want: "CREATE AUDIT POLICY DEMO_AUDIT_POLICY AUDITING ALL GRANT ANY, REVOKE ANY LEVEL INFO TRAIL TYPE TABLE RETENTION 30",
		},
		"SingleUser": {
			reason: "The statement should contain a FOR PRINCIPALS USER clause for a single user principal",
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:   "DEMO_AUDIT_POLICY",
					AuditStatus:  "SUCCESSFUL",
					AuditActions: []string{"ACTIONS"},
					AuditPrincipals: []v1alpha1.AuditPrincipal{
						{Type: "USER", Name: "USER_A"},
					},
					AuditLevel:          "INFO",
					AuditTrailRetention: retention(180),
				},
			},
			want: `CREATE AUDIT POLICY DEMO_AUDIT_POLICY AUDITING SUCCESSFUL ACTIONS FOR PRINCIPALS USER USER_A LEVEL INFO TRAIL TYPE TABLE RETENTION 180`,
		},
		"MixedPrincipals": {
			reason: "The statement should render a mixed, ordered list of users and user groups",
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:   "DEMO_AUDIT_POLICY",
					AuditStatus:  "SUCCESSFUL",
					AuditActions: []string{"ACTIONS"},
					AuditPrincipals: []v1alpha1.AuditPrincipal{
						{Type: "USER", Name: "USER_A"},
						{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
					},
					AuditLevel:          "INFO",
					AuditTrailRetention: retention(180),
				},
			},
			want: `CREATE AUDIT POLICY DEMO_AUDIT_POLICY AUDITING SUCCESSFUL ACTIONS FOR PRINCIPALS USER USER_A, USERGROUP TECHNICAL_USER_GROUP LEVEL INFO TRAIL TYPE TABLE RETENTION 180`,
		},
		"UserGroupConnectScenario": {
			reason: "Scenario 1: a successful CONNECT policy restricted to a user group",
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:   "SIGNAVIO_TECHNICAL_USER_CONNECT",
					AuditStatus:  "SUCCESSFUL",
					AuditActions: []string{"CONNECT"},
					AuditPrincipals: []v1alpha1.AuditPrincipal{
						{Type: "USERGROUP", Name: "TECHNICAL_USER_GROUP"},
					},
					AuditLevel:          "INFO",
					AuditTrailRetention: retention(180),
				},
			},
			want: `CREATE AUDIT POLICY SIGNAVIO_TECHNICAL_USER_CONNECT AUDITING SUCCESSFUL CONNECT FOR PRINCIPALS USERGROUP TECHNICAL_USER_GROUP LEVEL INFO TRAIL TYPE TABLE RETENTION 180`,
		},
		"ExceptMixedPrincipalsScenario": {
			reason: "Scenario 2: an EXCEPT FOR PRINCIPALS policy mixing users and user groups in order",
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:   "EXCEPT_PRINCIPALS_AUDIT_POLICY1",
					AuditStatus:  "SUCCESSFUL",
					AuditActions: []string{"ACTIONS"},
					AuditPrincipals: []v1alpha1.AuditPrincipal{
						{Type: "USER", Name: "USER1"},
						{Type: "USERGROUP", Name: "USERGROUP1"},
						{Type: "USER", Name: "USER2"},
						{Type: "USERGROUP", Name: "USERGROUP2"},
					},
					ExceptPrincipals:    true,
					AuditLevel:          "CRITICAL",
					AuditTrailRetention: retention(7),
				},
			},
			want: `CREATE AUDIT POLICY EXCEPT_PRINCIPALS_AUDIT_POLICY1 AUDITING SUCCESSFUL ACTIONS EXCEPT FOR PRINCIPALS USER USER1, USERGROUP USERGROUP1, USER USER2, USERGROUP USERGROUP2 LEVEL CRITICAL TRAIL TYPE TABLE RETENTION 7`,
		},
		"PrincipalNamesNotQuoted": {
			reason: "Principal names must be rendered unquoted; HANA rejects double-quoted identifiers in the principal list",
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:   "DEMO_AUDIT_POLICY",
					AuditStatus:  "SUCCESSFUL",
					AuditActions: []string{"ACTIONS"},
					AuditPrincipals: []v1alpha1.AuditPrincipal{
						{Type: "USER", Name: "MY_USER"},
						{Type: "USERGROUP", Name: "MY_GROUP"},
					},
					AuditLevel:          "INFO",
					AuditTrailRetention: retention(180),
				},
			},
			want: `CREATE AUDIT POLICY DEMO_AUDIT_POLICY AUDITING SUCCESSFUL ACTIONS FOR PRINCIPALS USER MY_USER, USERGROUP MY_GROUP LEVEL INFO TRAIL TYPE TABLE RETENTION 180`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := prepareCreateSql(tc.args.parameters)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\nprepareCreateSql(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestRecreatePolicy(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.AuditPolicyParameters
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
		"ErrDrop": {
			reason: "Any errors encountered while dropping the audit policy should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"ErrCreate": {
			reason: "Any errors encountered while creating the audit policy should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if query == prepareDeleteSql(&v1alpha1.AuditPolicyParameters{PolicyName: "DEMO_AUDIT_POLICY"}) {
							return nil, nil
						}
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditTrailRetention: func(i int) *int { return &i }(7),
					Enabled:             func(b bool) *bool { return &b }(true),
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully recreate an audit policy",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditTrailRetention: func(i int) *int { return &i }(7),
					Enabled:             func(b bool) *bool { return &b }(true),
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
			err := c.RecreatePolicy(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestUpdateRetentionDays(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}
	type args struct {
		ctx        context.Context
		parameters *v1alpha1.AuditPolicyParameters
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
		"ErrUpdate": {
			reason: "Any errors encountered while updating the retention days should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditTrailRetention: func(i int) *int { return &i }(30),
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully update the retention days",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName:          "DEMO_AUDIT_POLICY",
					AuditTrailRetention: func(i int) *int { return &i }(30),
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
			err := c.UpdateRetentionDays(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestUpdateEnablePolicy(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}
	type args struct {
		ctx        context.Context
		parameters *v1alpha1.AuditPolicyParameters
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
		"ErrUpdate": {
			reason: "Any errors encountered while updating the enable status should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
					Enabled:    func(b bool) *bool { return &b }(true),
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully update the enable status",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
					Enabled:    func(b bool) *bool { return &b }(true),
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
			err := c.UpdateEnablePolicy(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
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
		parameters *v1alpha1.AuditPolicyParameters
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
			reason: "Any errors encountered while deleting the audit policy should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "No error should be returned when we successfully delete an audit policy",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.AuditPolicyParameters{
					PolicyName: "DEMO_AUDIT_POLICY",
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
			err := c.Delete(tc.args.ctx, tc.args.parameters)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
