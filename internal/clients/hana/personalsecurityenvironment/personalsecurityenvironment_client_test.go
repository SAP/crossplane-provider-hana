/*
Copyright 2026 SAP SE.
*/

package personalsecurityenvironment

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"

	"github.com/crossplane/crossplane-runtime/pkg/test"
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
		parameters *v1alpha1.PersonalSecurityEnvironmentParameters
	}

	type want struct {
		observed *v1alpha1.PersonalSecurityEnvironmentObservation
		err      error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrRead": {
			reason: "Any errors encountered while reading the PersonalSecurityEnvironment should be returned",
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
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "test-pse",
				},
			},
			want: want{
				observed: nil,
				err:      fmt.Errorf("error querying row: %w", errBoom),
			},
		},
		"PSENotFound": {
			reason: "Should return nil when PersonalSecurityEnvironment does not exist",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						db, mock, _ := sqlmock.New()
						mock.ExpectQuery("SELECT").WillReturnError(sql.ErrNoRows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "nonexistent-pse",
				},
			},
			want: want{
				observed: nil,
				err:      nil,
			},
		},
		"SuccessWithCertificatesAndPurpose": {
			reason: "Should successfully read PersonalSecurityEnvironment with certificates and purpose",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						// Mock PSE query
						db, mock, _ := sqlmock.New()
						if strings.Contains(query, "PSE_PURPOSE_OBJECTS") {
							rows := sqlmock.NewRows([]string{"PURPOSE_OBJECT"}).AddRow("test-provider")
							mock.ExpectQuery("SELECT").WillReturnRows(rows)
						} else {
							rows := sqlmock.NewRows([]string{"NAME"}).AddRow("test-pse")
							mock.ExpectQuery("SELECT").WillReturnRows(rows)
						}
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Mock certificates query
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"CERTIFICATE_ID", "CERTIFICATE_NAME"}).
							AddRow(1, "cert1").
							AddRow(2, "cert2")), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "test-pse",
				},
			},
			want: want{
				observed: &v1alpha1.PersonalSecurityEnvironmentObservation{
					Name:             "test-pse",
					X509ProviderName: "test-provider",
					CertificateRefs: []v1alpha1.CertificateRef{
						{ID: intPtr(1), Name: stringPtr("cert1")},
						{ID: intPtr(2), Name: stringPtr("cert2")},
					},
				},
				err: nil,
			},
		},
		"SuccessWithoutCertificates": {
			reason: "Should successfully read PersonalSecurityEnvironment without certificates",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						// Mock PSE query and purpose query
						db, mock, _ := sqlmock.New()
						if strings.Contains(query, "PSE_PURPOSE_OBJECTS") {
							rows := sqlmock.NewRows([]string{"PURPOSE_OBJECT"}).AddRow("simple-provider")
							mock.ExpectQuery("SELECT").WillReturnRows(rows)
						} else {
							rows := sqlmock.NewRows([]string{"NAME"}).AddRow("simple-pse")
							mock.ExpectQuery("SELECT").WillReturnRows(rows)
						}
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Mock empty certificates query
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"CERTIFICATE_ID", "CERTIFICATE_NAME"})), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "simple-pse",
				},
			},
			want: want{
				observed: &v1alpha1.PersonalSecurityEnvironmentObservation{
					Name:             "simple-pse",
					X509ProviderName: "simple-provider",
					CertificateRefs:  nil,
				},
				err: nil,
			},
		},
		"SuccessWithoutProvider": {
			reason: "Should successfully read PersonalSecurityEnvironment without X509 provider",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						// Mock PSE query and empty purpose query
						db, mock, _ := sqlmock.New()
						if strings.Contains(query, "PSE_PURPOSE_OBJECTS") {
							mock.ExpectQuery("SELECT").WillReturnError(sql.ErrNoRows)
						} else {
							rows := sqlmock.NewRows([]string{"NAME"}).AddRow("no-provider-pse")
							mock.ExpectQuery("SELECT").WillReturnRows(rows)
						}
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Mock certificates query
						return fake.MockRowsToSQLRows(sqlmock.NewRows([]string{"CERTIFICATE_ID", "CERTIFICATE_NAME"}).
							AddRow(3, "cert3")), nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "no-provider-pse",
				},
			},
			want: want{
				observed: &v1alpha1.PersonalSecurityEnvironmentObservation{
					Name:             "no-provider-pse",
					X509ProviderName: "",
					CertificateRefs: []v1alpha1.CertificateRef{
						{ID: intPtr(3), Name: stringPtr("cert3")},
					},
				},
				err: nil,
			},
		},
		"ErrCertificatesQuery": {
			reason: "Should return error when certificates query fails",
			fields: fields{
				db: fake.MockDB{
					MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
						// Mock successful PSE query
						db, mock, _ := sqlmock.New()
						rows := sqlmock.NewRows([]string{"NAME"}).AddRow("test-pse")
						mock.ExpectQuery("SELECT").WillReturnRows(rows)
						return db.QueryRowContext(context.Background(), "SELECT")
					},
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						// Mock failing certificates query
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "test-pse",
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
		parameters *v1alpha1.PersonalSecurityEnvironmentParameters
		provider   string
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
			reason: "Any errors encountered while creating the PersonalSecurityEnvironment should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "test-pse",
				},
				provider: "test-provider",
			},
			want: want{
				err: errBoom,
			},
		},
		"SuccessBasicPSE": {
			reason: "Should successfully create a basic PersonalSecurityEnvironment",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "CREATE PSE test-pse"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "test-pse",
				},
				provider: "",
			},
			want: want{
				err: nil,
			},
		},
		"SuccessWithProvider": {
			reason: "Should successfully create PersonalSecurityEnvironment with X509 provider",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "CREATE PSE") || strings.Contains(query, "SET PSE") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "provider-pse",
				},
				provider: "test-provider",
			},
			want: want{
				err: nil,
			},
		},
		"SuccessWithCertificates": {
			reason: "Should successfully create PersonalSecurityEnvironment with certificates",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "CREATE PSE") || strings.Contains(query, "ALTER PSE") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "cert-pse",
					CertificateRefs: []v1alpha1.CertificateRef{
						{ID: intPtr(1), Name: stringPtr("cert1")},
						{ID: intPtr(2), Name: stringPtr("cert2")},
					},
				},
				provider: "",
			},
			want: want{
				err: nil,
			},
		},
		"SuccessComplexPSE": {
			reason: "Should successfully create PersonalSecurityEnvironment with provider and certificates",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "CREATE PSE") ||
							strings.Contains(query, "SET PSE") ||
							strings.Contains(query, "ALTER PSE") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "complex-pse",
					CertificateRefs: []v1alpha1.CertificateRef{
						{ID: intPtr(1), Name: stringPtr("cert1")},
					},
				},
				provider: "complex-provider",
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.Create(tc.args.ctx, tc.args.parameters, tc.args.provider)
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
		ctx          context.Context
		pseName      string
		toAdd        []v1alpha1.CertificateRef
		toRemove     []v1alpha1.CertificateRef
		providerName string
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
		"ErrUpdateProvider": {
			reason: "Any errors encountered while updating provider should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "SET PSE") {
							return nil, errBoom
						}
						return nil, nil
					},
				},
			},
			args: args{
				pseName:      "test-pse",
				providerName: "new-provider",
			},
			want: want{
				err: errBoom,
			},
		},
		"ErrUpdateCertificates": {
			reason: "Any errors encountered while updating certificates should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "ALTER PSE") {
							return nil, errBoom
						}
						return nil, nil
					},
				},
			},
			args: args{
				pseName: "test-pse",
				toAdd: []v1alpha1.CertificateRef{
					{ID: intPtr(1), Name: stringPtr("cert1")},
				},
			},
			want: want{
				err: fmt.Errorf("failed to update certificates: %w", errBoom),
			},
		},
		"SuccessAddCertificates": {
			reason: "Should successfully add certificates to PersonalSecurityEnvironment",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "ALTER PSE test-pse ADD CERTIFICATE 1, 2"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				pseName: "test-pse",
				toAdd: []v1alpha1.CertificateRef{
					{ID: intPtr(1)},
					{ID: intPtr(2)},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessRemoveCertificates": {
			reason: "Should successfully remove certificates from PersonalSecurityEnvironment",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := `ALTER PSE test-pse DROP CERTIFICATE "cert1", "cert2"`
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				pseName: "test-pse",
				toRemove: []v1alpha1.CertificateRef{
					{Name: stringPtr("cert1")},
					{Name: stringPtr("cert2")},
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessUpdateProvider": {
			reason: "Should successfully update X509 provider for PersonalSecurityEnvironment",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "SET PSE test-pse PURPOSE X509 FOR PROVIDER new-provider"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				pseName:      "test-pse",
				providerName: "new-provider",
			},
			want: want{
				err: nil,
			},
		},
		"SuccessComplexUpdate": {
			reason: "Should successfully update both provider and certificates",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						if strings.Contains(query, "SET PSE") || strings.Contains(query, "ALTER PSE") {
							return nil, nil
						}
						return nil, fmt.Errorf("unexpected query: %s", query)
					},
				},
			},
			args: args{
				pseName: "complex-pse",
				toAdd: []v1alpha1.CertificateRef{
					{ID: intPtr(1), Name: stringPtr("cert1")},
				},
				toRemove: []v1alpha1.CertificateRef{
					{ID: intPtr(2), Name: stringPtr("cert2")},
				},
				providerName: "updated-provider",
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
				pseName:      "test-pse",
				toAdd:        []v1alpha1.CertificateRef{},
				toRemove:     []v1alpha1.CertificateRef{},
				providerName: "",
			},
			want: want{
				err: nil,
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.Update(tc.args.ctx, tc.args.pseName, tc.args.toAdd, tc.args.toRemove, tc.args.providerName)
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
		parameters *v1alpha1.PersonalSecurityEnvironmentParameters
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
			reason: "Any errors encountered while deleting the PersonalSecurityEnvironment should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "test-pse",
				},
			},
			want: want{
				err: errBoom,
			},
		},
		"Success": {
			reason: "Should successfully delete PersonalSecurityEnvironment",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "DROP PSE test-pse"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "test-pse",
				},
			},
			want: want{
				err: nil,
			},
		},
		"SuccessComplexName": {
			reason: "Should successfully delete PersonalSecurityEnvironment with complex name",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						expectedQuery := "DROP PSE complex-pse-name"
						if query != expectedQuery {
							return nil, fmt.Errorf("unexpected query: got %s, want %s", query, expectedQuery)
						}
						return nil, nil
					},
				},
			},
			args: args{
				parameters: &v1alpha1.PersonalSecurityEnvironmentParameters{
					Name: "complex-pse-name",
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

// Helper functions for testing
func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}
