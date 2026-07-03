/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
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
					Purpose:          v1alpha1.PSEPurposeX509,
					X509ProviderName: "test-provider",
					CertificateRefs: []v1alpha1.CertificateRef{
						{ID: new(1), Name: new("cert1")},
						{ID: new(2), Name: new("cert2")},
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
					Purpose:          v1alpha1.PSEPurposeX509,
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
					Purpose:          v1alpha1.PSEPurposeX509,
					X509ProviderName: "",
					CertificateRefs: []v1alpha1.CertificateRef{
						{ID: new(3), Name: new("cert3")},
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
						{ID: new(1), Name: new("cert1")},
						{ID: new(2), Name: new("cert2")},
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
						{ID: new(1), Name: new("cert1")},
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
					{ID: new(1), Name: new("cert1")},
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
					{ID: new(1)},
					{ID: new(2)},
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
					{Name: new("cert1")},
					{Name: new("cert2")},
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
					{ID: new(1), Name: new("cert1")},
				},
				toRemove: []v1alpha1.CertificateRef{
					{ID: new(2), Name: new("cert2")},
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
			err := c.Update(tc.args.ctx, tc.args.pseName, v1alpha1.PSEPurposeX509, tc.args.toAdd, tc.args.toRemove, nil, nil, tc.args.providerName)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nc.Update(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}

// captureExec collects every ExecContext query so JWT-purpose tests can assert
// on the exact DDL emitted. Matches the pattern used by
// publickey/jwtprovider client tests.
type captureExec struct {
	queries []string
}

func (c *captureExec) mock() fake.MockDB {
	return fake.MockDB{
		MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			c.queries = append(c.queries, query)
			return nil, nil
		},
	}
}

// TestCreateJWTPurpose covers the PSE-Create branch that fires when Purpose is
// JWT: the DDL must be CREATE PSE + ALTER PSE ... ADD PUBLIC KEY (one per
// key), and when a provider name is supplied the SET PSE ... PURPOSE JWT FOR
// PROVIDER statement must be emitted with PURPOSE JWT (not X509). Everything
// else is covered by the X509 cases in TestCreate above.
// nolint: contextcheck
func TestCreateJWTPurpose(t *testing.T) {
	cases := map[string]struct {
		reason   string
		params   *v1alpha1.PersonalSecurityEnvironmentParameters
		provider string
		wantSQL  []string
		unwant   []string
	}{
		"JWTPurposeNoProvider": {
			// Create-time with an empty provider name skips the SET PSE ...
			// PURPOSE step (the provider hasn't been created yet). Public-key
			// wiring still happens through ALTER PSE ADD PUBLIC KEY.
			reason: "JWT purpose with empty provider emits CREATE PSE + ALTER PSE ADD PUBLIC KEY, no SET PSE",
			params: &v1alpha1.PersonalSecurityEnvironmentParameters{
				Name:    "test-pse",
				Purpose: v1alpha1.PSEPurposeJWT,
				PublicKeyRefs: []v1alpha1.PublicKeyRef{
					{Name: "IAS_SIGNING_KEY"},
				},
			},
			provider: "",
			wantSQL: []string{
				"CREATE PSE test-pse",
				"ALTER PSE test-pse ADD PUBLIC KEY IAS_SIGNING_KEY",
			},
			unwant: []string{"SET PSE"},
		},
		"JWTPurposeWithProvider": {
			// Regression guard: this branch is what the JWT-SSO flow actually
			// uses at Update time. PURPOSE JWT must appear literally so we
			// don't drift to `PURPOSE X509` (which fails on JWT PSEs).
			reason: "JWT purpose with provider name emits SET PSE ... PURPOSE JWT FOR PROVIDER",
			params: &v1alpha1.PersonalSecurityEnvironmentParameters{
				Name:    "jwt-pse",
				Purpose: v1alpha1.PSEPurposeJWT,
				PublicKeyRefs: []v1alpha1.PublicKeyRef{
					{Name: "IAS_SIGNING_KEY"},
				},
			},
			provider: "jwt-provider",
			wantSQL: []string{
				"CREATE PSE jwt-pse",
				"SET PSE jwt-pse PURPOSE JWT FOR PROVIDER jwt-provider",
				"ALTER PSE jwt-pse ADD PUBLIC KEY IAS_SIGNING_KEY",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var cap captureExec
			c := Client{DB: cap.mock()}
			if err := c.Create(context.Background(), tc.params, tc.provider); err != nil {
				t.Fatalf("%s: Create returned unexpected error: %v", tc.reason, err)
			}
			joined := strings.Join(cap.queries, "\n---\n")
			for _, w := range tc.wantSQL {
				if !strings.Contains(joined, w) {
					t.Errorf("\n%s\nCreate(...): missing SQL substring %q:\n%s", tc.reason, w, joined)
				}
			}
			for _, u := range tc.unwant {
				if strings.Contains(joined, u) {
					t.Errorf("\n%s\nCreate(...): forbidden SQL substring %q:\n%s", tc.reason, u, joined)
				}
			}
		})
	}
}

// TestUpdateJWTPurpose covers the PSE-Update branch that fires when Purpose is
// JWT: key add/remove uses ALTER PSE ... ADD/DROP PUBLIC KEY (not CERTIFICATE)
// and the SET PSE ... PURPOSE JWT FOR PROVIDER binding survives the switch.
// nolint: contextcheck
func TestUpdateJWTPurpose(t *testing.T) {
	cases := map[string]struct {
		reason       string
		pseName      string
		keysToAdd    []string
		keysToRemove []string
		providerName string
		wantSQL      []string
		unwant       []string
	}{
		"AddKeys": {
			reason:    "Adding a public key emits ALTER PSE ... ADD PUBLIC KEY",
			pseName:   "test-pse",
			keysToAdd: []string{"KEY_A"},
			wantSQL:   []string{"ALTER PSE test-pse ADD PUBLIC KEY KEY_A"},
			unwant:    []string{"CERTIFICATE"},
		},
		"RemoveKeys": {
			reason:       "Removing a public key emits ALTER PSE ... DROP PUBLIC KEY",
			pseName:      "test-pse",
			keysToRemove: []string{"KEY_A"},
			wantSQL:      []string{"ALTER PSE test-pse DROP PUBLIC KEY KEY_A"},
			unwant:       []string{"CERTIFICATE"},
		},
		"SetProvider": {
			// Regression guard for the drift path exercised by the JWT-SSO
			// e2e: rebind a JWT PSE to a (possibly new) provider name.
			reason:       "Setting a provider name on a JWT PSE emits SET PSE ... PURPOSE JWT FOR PROVIDER",
			pseName:      "test-pse",
			providerName: "new-provider",
			wantSQL:      []string{"SET PSE test-pse PURPOSE JWT FOR PROVIDER new-provider"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var cap captureExec
			c := Client{DB: cap.mock()}
			// certsToAdd/certsToRemove are irrelevant on the JWT branch —
			// pass nil so any accidental CERTIFICATE emission is a bug.
			err := c.Update(context.Background(), tc.pseName, v1alpha1.PSEPurposeJWT, nil, nil, tc.keysToAdd, tc.keysToRemove, tc.providerName)
			if err != nil {
				t.Fatalf("%s: Update returned unexpected error: %v", tc.reason, err)
			}
			joined := strings.Join(cap.queries, "\n---\n")
			for _, w := range tc.wantSQL {
				if !strings.Contains(joined, w) {
					t.Errorf("\n%s\nUpdate(...): missing SQL substring %q:\n%s", tc.reason, w, joined)
				}
			}
			for _, u := range tc.unwant {
				if strings.Contains(joined, u) {
					t.Errorf("\n%s\nUpdate(...): forbidden SQL substring %q:\n%s", tc.reason, u, joined)
				}
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
