/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package x509provider

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

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
)

// Unlike many Kubernetes projects Crossplane does not use third party testing
// libraries, per the common Go test review comments. Crossplane encourages the
// use of table driven unit tests. The tests of the crossplane-runtime project
// are representative of the testing style Crossplane encourages.
//
// https://github.com/golang/go/wiki/TestComments
// https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md#contributing-code

// nolint: contextcheck
func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *v1alpha1.X509ProviderParameters
	}

	type want struct {
		observed *v1alpha1.X509ProviderObservation
		err      error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrRead": {
			reason: "Any errors encountered while reading the X509Provider should be returned",
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
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "test-provider",
				},
			},
			want: want{
				observed: nil,
				err:      errBoom,
			},
		},
		"ProviderNotFound": {
			reason: "Should return nil when X509Provider does not exist",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						mock.ExpectQuery("SELECT").WillReturnError(sql.ErrNoRows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{})), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "nonexistent-provider",
				},
			},
			want: want{
				observed: nil,
				err:      nil,
			},
		},
		"SuccessWithMatchingRules": {
			reason: "Should successfully read X509Provider with matching rules",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						// Mock issuer query
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"ISSUER_NAME"}).
							AddRow("CN=Test CA")
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Mock matching rules query
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"MATCHING_RULE"}).
							AddRow("rule1").
							AddRow("rule2")), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "test-provider",
				},
			},
			want: want{
				observed: &v1alpha1.X509ProviderObservation{
					Name:          new("test-provider"),
					Issuer:        new("CN=Test CA"),
					MatchingRules: []string{"rule1", "rule2"},
				},
				err: nil,
			},
		},
		"SuccessWithoutMatchingRules": {
			reason: "Should successfully read X509Provider without matching rules",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						// Mock issuer query
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"ISSUER_NAME"}).
							AddRow("CN=Simple CA")
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Mock empty matching rules query
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"MATCHING_RULE"})), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "simple-provider",
				},
			},
			want: want{
				observed: &v1alpha1.X509ProviderObservation{
					Name:          new("simple-provider"),
					Issuer:        new("CN=Simple CA"),
					MatchingRules: nil,
				},
				err: nil,
			},
		},
		"ErrMatchingRulesQuery": {
			reason: "Should return error when matching rules query fails",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						// Mock successful issuer query
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"ISSUER_NAME"}).
							AddRow("CN=Test CA")
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Mock failing matching rules query
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "test-provider",
				},
			},
			want: want{
				observed: nil,
				err:      errBoom,
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
		parameters *v1alpha1.X509ProviderParameters
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
			reason: "Any errors encountered while creating the X509Provider should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:   "test-provider",
					Issuer: "CN=Test CA",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"SuccessBasicProvider": {
			reason: "Should successfully create a basic X509Provider",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "CREATE X509 PROVIDER test-provider WITH ISSUER 'CN=Test CA'"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:   "test-provider",
					Issuer: "CN=Test CA",
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessWithSpecialCharacters": {
			reason: "Should successfully create X509Provider with special characters in issuer",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "CREATE X509 PROVIDER complex-provider WITH ISSUER 'CN=Test CA, O=Acme Corp, C=US'"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:   "complex-provider",
					Issuer: "CN=Test CA, O=Acme Corp, C=US",
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
				t.Errorf("\n%s\nc.Create(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestUpdate(t *testing.T) {
	errBoom := errors.New("boom")

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx         context.Context
		parameters  *v1alpha1.X509ProviderParameters
		observation *v1alpha1.X509ProviderObservation
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
		"ErrUpdateIssuer": {
			reason: "Any errors encountered while updating issuer should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "SET ISSUER") {
							return nil, errBoom
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:   "test-provider",
					Issuer: "CN=New CA",
				},
				observation: &v1alpha1.X509ProviderObservation{
					Name:   new("test-provider"),
					Issuer: new("CN=Old CA"),
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"ErrUpdateMatchingRules": {
			reason: "Any errors encountered while updating matching rules should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "SET MATCHING RULES") {
							return nil, errBoom
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:          "test-provider",
					Issuer:        "CN=Test CA",
					MatchingRules: []string{"new-rule"},
				},
				observation: &v1alpha1.X509ProviderObservation{
					Name:          new("test-provider"),
					Issuer:        new("CN=Test CA"),
					MatchingRules: []string{"old-rule"},
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"SuccessUpdateIssuerOnly": {
			reason: "Should successfully update only issuer when matching rules are the same",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER X509 PROVIDER test-provider SET ISSUER 'CN=New CA'"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:          "test-provider",
					Issuer:        "CN=New CA",
					MatchingRules: []string{"rule1", "rule2"},
				},
				observation: &v1alpha1.X509ProviderObservation{
					Name:          new("test-provider"),
					Issuer:        new("CN=Old CA"),
					MatchingRules: []string{"rule1", "rule2"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessUpdateMatchingRulesOnly": {
			reason: "Should successfully update only matching rules when issuer is the same",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER X509 PROVIDER test-provider SET MATCHING RULES 'new-rule1', 'new-rule2'"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:          "test-provider",
					Issuer:        "CN=Test CA",
					MatchingRules: []string{"new-rule1", "new-rule2"},
				},
				observation: &v1alpha1.X509ProviderObservation{
					Name:          new("test-provider"),
					Issuer:        new("CN=Test CA"),
					MatchingRules: []string{"old-rule"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessUpdateBoth": {
			reason: "Should successfully update both issuer and matching rules",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "SET ISSUER") || strings.Contains(query, "SET MATCHING RULES") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:          "test-provider",
					Issuer:        "CN=New CA",
					MatchingRules: []string{"new-rule"},
				},
				observation: &v1alpha1.X509ProviderObservation{
					Name:          new("test-provider"),
					Issuer:        new("CN=Old CA"),
					MatchingRules: []string{"old-rule"},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessUnsetMatchingRules": {
			reason: "Should successfully unset matching rules when parameter is empty",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER X509 PROVIDER test-provider UNSET MATCHING RULES"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name:          "test-provider",
					Issuer:        "CN=Test CA",
					MatchingRules: []string{},
				},
				observation: &v1alpha1.X509ProviderObservation{
					Name:          new("test-provider"),
					Issuer:        new("CN=Test CA"),
					MatchingRules: []string{"rule1"},
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
				parameters: &v1alpha1.X509ProviderParameters{
					Name:          "test-provider",
					Issuer:        "CN=Test CA",
					MatchingRules: []string{"rule1", "rule2"},
				},
				observation: &v1alpha1.X509ProviderObservation{
					Name:          new("test-provider"),
					Issuer:        new("CN=Test CA"),
					MatchingRules: []string{"rule1", "rule2"},
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
			err := c.Update(tc.args.ctx, tc.args.parameters, tc.args.observation)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nc.Update(...): -want error, +got error:\n%s\n", tc.reason, diff)
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
		parameters *v1alpha1.X509ProviderParameters
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
			reason: "Any errors encountered while deleting the X509Provider should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "test-provider",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "Should successfully delete X509Provider",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "DROP X509 PROVIDER test-provider"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "test-provider",
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessComplexName": {
			reason: "Should successfully delete X509Provider with complex name",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "DROP X509 PROVIDER complex-provider-name"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.X509ProviderParameters{
					Name: "complex-provider-name",
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
				t.Errorf("\n%s\nc.Delete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
