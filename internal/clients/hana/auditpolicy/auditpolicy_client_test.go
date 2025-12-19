package auditpolicy

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.tools.sap/cloud-orchestration/crossplane-provider-hana/internal/clients/fake"
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
