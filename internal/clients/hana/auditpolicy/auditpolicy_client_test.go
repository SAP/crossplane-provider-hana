package auditpolicy

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
)

func sortedStrings(ss []string) []string {
	out := make([]string, len(ss))
	copy(out, ss)
	sort.Strings(out)
	return out
}

func splitAndSort(s string) []string {
	parts := strings.Split(s, ", ")
	sort.Strings(parts)
	return parts
}

func TestOptimizeAuditActions(t *testing.T) {
	type args struct {
		actionStrings []string
	}
	type want struct {
		result []string
		errMsg string
	}

	cases := map[string]struct {
		reason string
		args   args
		want   want
	}{
		"EmptyInput": {
			reason: "Empty input produces empty output without error",
			args:   args{actionStrings: []string{}},
			want:   want{result: []string{}},
		},
		"SingleAction": {
			reason: "A single action with no principal is returned as-is",
			args:   args{actionStrings: []string{"GRANT"}},
			want:   want{result: []string{"GRANT"}},
		},
		"MultipleActionsGroupedIntoOne": {
			reason: "Multiple no-principal actions across separate strings are merged into a single comma-separated action",
			args:   args{actionStrings: []string{"GRANT", "REVOKE", "SELECT"}},
			want:   want{result: []string{"GRANT, REVOKE, SELECT"}},
		},
		"CommaSeparatedActionsInOneString": {
			reason: "A single string with comma-separated actions is normalized the same as separate strings",
			args:   args{actionStrings: []string{"GRANT,REVOKE"}},
			want:   want{result: []string{"GRANT, REVOKE"}},
		},
		"AllUsersStripped": {
			reason: "FOR PRINCIPALS ALL USERS suffix is removed before processing",
			args:   args{actionStrings: []string{"GRANT FOR PRINCIPALS ALL USERS"}},
			want:   want{result: []string{"GRANT"}},
		},
		"MultipleAllUsersStrippedAndGrouped": {
			reason: "Multiple actions with FOR PRINCIPALS ALL USERS are stripped and merged",
			args:   args{actionStrings: []string{"GRANT FOR PRINCIPALS ALL USERS", "REVOKE FOR PRINCIPALS ALL USERS"}},
			want:   want{result: []string{"GRANT, REVOKE"}},
		},
		"LowercaseActionsNormalized": {
			reason: "Action names are uppercased during grouping",
			args:   args{actionStrings: []string{"grant", "revoke"}},
			want:   want{result: []string{"GRANT, REVOKE"}},
		},
		"DuplicateActionsGrouped": {
			reason: "Duplicate action names across separate strings are both included in the merged result",
			args:   args{actionStrings: []string{"GRANT", "GRANT"}},
			want:   want{result: []string{"GRANT, GRANT"}},
		},
		"NamedUserPrincipalSucceeds": {
			reason: "A single FOR PRINCIPALS USER action produces one group and is returned without error",
			args:   args{actionStrings: []string{"GRANT FOR PRINCIPALS USER ALICE"}},
			want:   want{result: []string{"GRANT FOR PRINCIPALS USER ALICE"}},
		},
		"MixedNoUserAndForPrincipalFails": {
			reason: "Mixing unscoped actions with FOR PRINCIPALS actions creates multiple groups and must error",
			args:   args{actionStrings: []string{"GRANT", "REVOKE FOR PRINCIPALS USER ALICE"}},
			want:   want{errMsg: "only one audit action is supported at the moment"},
		},
		"TwoDistinctForPrincipalGroupsFail": {
			reason: "Actions scoped to two different users form two separate groups and must error",
			args:   args{actionStrings: []string{"GRANT FOR PRINCIPALS USER ALICE", "REVOKE FOR PRINCIPALS USER BOB"}},
			want:   want{errMsg: "only one audit action is supported at the moment"},
		},
		"InvalidPrincipalEntryFails": {
			reason: "A malformed principal entry without USER or USERGROUP prefix returns a parse error",
			args:   args{actionStrings: []string{"GRANT FOR PRINCIPALS BADINPUT"}},
			want:   want{errMsg: "error parsing action string"},
		},
		"ExceptForPrincipalSucceeds": {
			reason: "A single EXCEPT FOR PRINCIPALS action produces one group and is returned without error",
			args:   args{actionStrings: []string{"GRANT EXCEPT FOR PRINCIPALS USER ALICE"}},
			want:   want{result: []string{"GRANT EXCEPT FOR PRINCIPALS USER ALICE"}},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := OptimizeAuditActions(tc.args.actionStrings)

			if tc.want.errMsg != "" {
				if err == nil {
					t.Errorf("\n%s\nOptimizeAuditActions(...): expected error containing %q, got nil", tc.reason, tc.want.errMsg)
					return
				}
				if !strings.Contains(err.Error(), tc.want.errMsg) {
					t.Errorf("\n%s\nOptimizeAuditActions(...): expected error containing %q, got %q", tc.reason, tc.want.errMsg, err.Error())
				}
				return
			}

			normalizedWant := make([][]string, len(tc.want.result))
			for i, s := range tc.want.result {
				normalizedWant[i] = splitAndSort(s)
			}
			normalizedGot := make([][]string, len(got))
			for i, s := range got {
				normalizedGot[i] = splitAndSort(s)
			}
			if diff := cmp.Diff(normalizedWant, normalizedGot); diff != "" {
				t.Errorf("\n%s\nOptimizeAuditActions(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestGroupActions(t *testing.T) {
	cases := map[string]struct {
		reason  string
		actions []parsedAction
		want    []parsedAction
	}{
		"EmptyInput": {
			reason:  "Empty input produces no groups",
			actions: []parsedAction{},
			want:    []parsedAction{},
		},
		"SingleNoUser": {
			reason:  "A single action without principals is placed in the no-user group",
			actions: []parsedAction{{actionNames: []string{"GRANT"}}},
			want:    []parsedAction{{actionNames: []string{"GRANT"}}},
		},
		"MultipleNoUserMerged": {
			reason: "Multiple no-principal actions are merged into one group",
			actions: []parsedAction{
				{actionNames: []string{"GRANT"}},
				{actionNames: []string{"REVOKE"}},
			},
			want: []parsedAction{{actionNames: []string{"GRANT", "REVOKE"}}},
		},
		"ForPrincipalsSameUserGrouped": {
			reason: "Actions sharing the same FOR principal are merged into one group",
			actions: []parsedAction{
				{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
				{actionNames: []string{"REVOKE"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
			},
			want: []parsedAction{
				{actionNames: []string{"GRANT", "REVOKE"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
			},
		},
		"ForPrincipalsDifferentUsersSeparated": {
			reason: "Actions with different FOR principals remain in separate groups",
			actions: []parsedAction{
				{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
				{actionNames: []string{"REVOKE"}, auditFor: []parsedPrincipal{{user: "BOB"}}},
			},
			want: []parsedAction{
				{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
				{actionNames: []string{"REVOKE"}, auditFor: []parsedPrincipal{{user: "BOB"}}},
			},
		},
		"ExceptForPrincipalGrouped": {
			reason: "Actions sharing the same EXCEPT FOR principal are merged into one group",
			actions: []parsedAction{
				{actionNames: []string{"GRANT"}, auditExceptFor: []parsedPrincipal{{user: "ALICE"}}},
				{actionNames: []string{"REVOKE"}, auditExceptFor: []parsedPrincipal{{user: "ALICE"}}},
			},
			want: []parsedAction{
				{actionNames: []string{"GRANT", "REVOKE"}, auditExceptFor: []parsedPrincipal{{user: "ALICE"}}},
			},
		},
		"MixedNoUserAndForPrincipalSeparated": {
			reason: "No-principal actions and FOR principal actions produce separate groups",
			actions: []parsedAction{
				{actionNames: []string{"GRANT"}},
				{actionNames: []string{"REVOKE"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
			},
			want: []parsedAction{
				{actionNames: []string{"GRANT"}},
				{actionNames: []string{"REVOKE"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
			},
		},
		"UsergroupPrincipalGrouped": {
			reason: "Actions sharing the same USERGROUP principal are merged into one group",
			actions: []parsedAction{
				{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{usergroup: "ADMINS"}}},
				{actionNames: []string{"REVOKE"}, auditFor: []parsedPrincipal{{usergroup: "ADMINS"}}},
			},
			want: []parsedAction{
				{actionNames: []string{"GRANT", "REVOKE"}, auditFor: []parsedPrincipal{{usergroup: "ADMINS"}}},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := groupActions(tc.actions)
			if len(got) != len(tc.want) {
				t.Errorf("\n%s\ngroupActions(...): want %d groups, got %d", tc.reason, len(tc.want), len(got))
				return
			}
			for i, wantGroup := range tc.want {
				gotGroup := got[i]
				if diff := cmp.Diff(sortedStrings(wantGroup.actionNames), sortedStrings(gotGroup.actionNames)); diff != "" {
					t.Errorf("\n%s\ngroupActions(...) group[%d] actionNames: -want, +got:\n%s\n", tc.reason, i, diff)
				}
			}
		})
	}
}

func TestParseIntoActions(t *testing.T) {
	cases := map[string]struct {
		reason string
		input  string
		want   []parsedAction
		errMsg string
	}{
		"SingleAction": {
			reason: "A bare action name yields one parsedAction with no principals",
			input:  "GRANT",
			want:   []parsedAction{{actionNames: []string{"GRANT"}}},
		},
		"CommaSeparatedActions": {
			reason: "Comma-separated actions are split into separate parsedActions",
			input:  "GRANT,REVOKE",
			want: []parsedAction{
				{actionNames: []string{"GRANT"}},
				{actionNames: []string{"REVOKE"}},
			},
		},
		"ForPrincipalsUser": {
			reason: "FOR PRINCIPALS USER X is parsed into auditFor with user set",
			input:  "GRANT FOR PRINCIPALS USER ALICE",
			want: []parsedAction{
				{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
			},
		},
		"ExceptForPrincipalsUser": {
			reason: "EXCEPT FOR PRINCIPALS USER X is parsed into auditExceptFor with user set",
			input:  "GRANT EXCEPT FOR PRINCIPALS USER ALICE",
			want: []parsedAction{
				{actionNames: []string{"GRANT"}, auditExceptFor: []parsedPrincipal{{user: "ALICE"}}},
			},
		},
		"ForPrincipalsUsergroup": {
			reason: "FOR PRINCIPALS USERGROUP G is parsed into auditFor with usergroup set",
			input:  "GRANT FOR PRINCIPALS USERGROUP ADMINS",
			want: []parsedAction{
				{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{usergroup: "ADMINS"}}},
			},
		},
		"InvalidPrincipalEntry": {
			reason: "A principal entry without USER or USERGROUP prefix produces an error",
			input:  "GRANT FOR PRINCIPALS BADINPUT",
			errMsg: "invalid principal entry",
		},
		"LowercaseActionNormalized": {
			reason: "Lowercase action names are uppercased",
			input:  "grant",
			want:   []parsedAction{{actionNames: []string{"GRANT"}}},
		},
		"ActionWithWhitespace": {
			reason: "Leading and trailing whitespace around action names is trimmed",
			input:  " GRANT , REVOKE ",
			want: []parsedAction{
				{actionNames: []string{"GRANT"}},
				{actionNames: []string{"REVOKE"}},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parseIntoActions(tc.input)

			if tc.errMsg != "" {
				if err == nil {
					t.Errorf("\n%s\nparseIntoActions(%q): expected error containing %q, got nil", tc.reason, tc.input, tc.errMsg)
					return
				}
				if !strings.Contains(err.Error(), tc.errMsg) {
					t.Errorf("\n%s\nparseIntoActions(%q): expected error containing %q, got %q", tc.reason, tc.input, tc.errMsg, err.Error())
				}
				return
			}

			if diff := cmp.Diff(tc.want, got, cmp.AllowUnexported(parsedAction{}, parsedPrincipal{})); diff != "" {
				t.Errorf("\n%s\nparseIntoActions(%q): -want, +got:\n%s\n", tc.reason, tc.input, diff)
			}
		})
	}
}

func TestStringifyParsedAction(t *testing.T) {
	cases := map[string]struct {
		reason string
		input  parsedAction
		want   string
	}{
		"NoUser": {
			reason: "Action with no principals is serialized as a bare comma-separated list",
			input:  parsedAction{actionNames: []string{"GRANT", "REVOKE"}},
			want:   "GRANT, REVOKE",
		},
		"ForUser": {
			reason: "Action with auditFor user is serialized with FOR PRINCIPALS USER",
			input:  parsedAction{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{user: "ALICE"}}},
			want:   "GRANT FOR PRINCIPALS USER ALICE",
		},
		"ForUsergroup": {
			reason: "Action with auditFor usergroup is serialized with FOR PRINCIPALS USERGROUP",
			input:  parsedAction{actionNames: []string{"GRANT"}, auditFor: []parsedPrincipal{{usergroup: "ADMINS"}}},
			want:   "GRANT FOR PRINCIPALS USERGROUP ADMINS",
		},
		"ExceptForUser": {
			reason: "Action with auditExceptFor user is serialized with EXCEPT FOR PRINCIPALS USER",
			input:  parsedAction{actionNames: []string{"GRANT"}, auditExceptFor: []parsedPrincipal{{user: "ALICE"}}},
			want:   "GRANT EXCEPT FOR PRINCIPALS USER ALICE",
		},
		"MultipleForUsers": {
			reason: "Multiple auditFor users are joined with comma-space",
			input: parsedAction{
				actionNames: []string{"GRANT"},
				auditFor:    []parsedPrincipal{{user: "ALICE"}, {user: "BOB"}},
			},
			want: "GRANT FOR PRINCIPALS USER ALICE, USER BOB",
		},
		"EmptyActionNames": {
			reason: "Empty action name list produces an empty string",
			input:  parsedAction{actionNames: []string{}},
			want:   "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := stringifyParsedAction(tc.input)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\nstringifyParsedAction(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

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
