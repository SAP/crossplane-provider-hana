package certificate

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/crossplane/crossplane-runtime/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/fake"
)

// nolint: contextcheck
func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	id1, id2 := 1, 2

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx        context.Context
		parameters *adminv1alpha1.CertificateParameters
	}

	type want struct {
		observed *adminv1alpha1.CertificateObservation
		err      error
	}

	cases := map[string]struct {
		reason string
		fields fields
		args   args
		want   want
	}{
		"ErrQuery": {
			reason: "An error from the database query should be returned",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters: &adminv1alpha1.CertificateParameters{Name: "my-ca"},
			},
			want: want{
				observed: nil,
				err:      fmt.Errorf("failed to query certificates: %w", errBoom),
			},
		},
		"NoCertificatesFound": {
			reason: "nil should be returned when no rows match, signalling the resource does not exist",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						rows := sqlmock.NewRows([]string{"CERTIFICATE_ID", "CERTIFICATE_NAME"})
						return fake.MockRowsToSQLRows(rows), nil
					},
				},
			},
			args: args{
				parameters: &adminv1alpha1.CertificateParameters{Name: "my-ca"},
			},
			want: want{
				observed: nil,
				err:      nil,
			},
		},
		"OneCertificate": {
			reason: "A single matching row should be returned as a one-element Certificates list",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						rows := sqlmock.NewRows([]string{"CERTIFICATE_ID", "CERTIFICATE_NAME"}).
							AddRow(1, "my-ca-1")
						return fake.MockRowsToSQLRows(rows), nil
					},
				},
			},
			args: args{
				parameters: &adminv1alpha1.CertificateParameters{Name: "my-ca"},
			},
			want: want{
				observed: &adminv1alpha1.CertificateObservation{
					Certificates: []adminv1alpha1.ImportedCertificate{
						{ID: &id1, Name: "my-ca-1"},
					},
				},
				err: nil,
			},
		},
		"TwoCertificates": {
			reason: "Two matching rows (a chain) should both appear in the Certificates list",
			fields: fields{
				db: fake.MockDB{
					MockQueryContext: func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
						rows := sqlmock.NewRows([]string{"CERTIFICATE_ID", "CERTIFICATE_NAME"}).
							AddRow(1, "my-ca-1").
							AddRow(2, "my-ca-2")
						return fake.MockRowsToSQLRows(rows), nil
					},
				},
			},
			args: args{
				parameters: &adminv1alpha1.CertificateParameters{Name: "my-ca"},
			},
			want: want{
				observed: &adminv1alpha1.CertificateObservation{
					Certificates: []adminv1alpha1.ImportedCertificate{
						{ID: &id1, Name: "my-ca-1"},
						{ID: &id2, Name: "my-ca-2"},
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
				t.Errorf("\n%s\nc.Read(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.observed, got, cmp.Comparer(func(a, b *int) bool {
				if a == nil || b == nil {
					return a == b
				}
				return *a == *b
			})); diff != "" {
				t.Errorf("\n%s\nc.Read(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

// nolint: contextcheck
func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	// Use real x509 PEMs so certName can parse serial and NotBefore.
	serial1 := big.NewInt(1001)
	serial2 := big.NewInt(1002)
	notBefore := time.Date(2026, 7, 16, 7, 40, 32, 0, time.UTC)

	singlePEM := selfSignedPEM(t, serial1, notBefore)
	chainPEM := append(
		selfSignedPEM(t, serial1, notBefore),
		selfSignedPEM(t, serial2, notBefore)...,
	)

	// mockDBWithTx creates a fake.MockDB whose BeginTx returns a *sql.Tx backed
	// by sqlmock, letting the caller set expectations on the transaction.
	mockDBWithTx := func(setupMock func(mock sqlmock.Sqlmock)) fake.MockDB {
		db, mock, _ := sqlmock.New()
		setupMock(mock)
		return fake.MockDB{
			MockBeginTx: func(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
				return db.BeginTx(ctx, opts)
			},
		}
	}

	type fields struct {
		db fake.MockDB
	}

	type args struct {
		ctx            context.Context
		parameters     *adminv1alpha1.CertificateParameters
		certificatePEM []byte
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
		"InvalidPEM": {
			reason: "Non-PEM input should return an error before BeginTx is called",
			fields: fields{db: fake.MockDB{}},
			args: args{
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: []byte("not valid pem"),
			},
			// splitPEMChain uses stdlib errors.New
			want: want{err: stderrors.New("failed to decode PEM certificate")},
		},
		"ErrBeginTx": {
			reason: "An error opening the transaction should be returned immediately",
			fields: fields{
				db: fake.MockDB{
					MockBeginTx: func(_ context.Context, _ *sql.TxOptions) (*sql.Tx, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				ctx:            context.Background(),
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: singlePEM,
			},
			want: want{err: fmt.Errorf("failed to begin transaction: %w", errBoom)},
		},
		"ErrExecSingleCert": {
			reason: "A SQL error on the single CREATE CERTIFICATE should roll back and return an error",
			fields: fields{
				db: mockDBWithTx(func(mock sqlmock.Sqlmock) {
					mock.ExpectBegin()
					mock.ExpectExec("CREATE CERTIFICATE").WillReturnError(errBoom)
					mock.ExpectRollback()
				}),
			},
			args: args{
				ctx:            context.Background(),
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: singlePEM,
			},
			want: want{err: fmt.Errorf("failed to create certificate %q: %w", "MY_CA_CRT_SRV_CERTIFICATE_1001_16072026074032", errBoom)},
		},
		"ErrExecSecondCertInChain": {
			reason: "A SQL error on the second certificate should roll back and return an error",
			fields: fields{
				db: mockDBWithTx(func(mock sqlmock.Sqlmock) {
					mock.ExpectBegin()
					mock.ExpectExec("CREATE CERTIFICATE").WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectExec("CREATE CERTIFICATE").WillReturnError(errBoom)
					mock.ExpectRollback()
				}),
			},
			args: args{
				ctx:            context.Background(),
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: chainPEM,
			},
			want: want{err: fmt.Errorf("failed to create certificate %q: %w", "MY_CA_CRT_SRV_CERTIFICATE_1002_16072026074032", errBoom)},
		},
		"SuccessSingleCert": {
			reason: "A single certificate should execute one CREATE CERTIFICATE and commit",
			fields: fields{
				db: mockDBWithTx(func(mock sqlmock.Sqlmock) {
					mock.ExpectBegin()
					mock.ExpectExec("CREATE CERTIFICATE").WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectCommit()
				}),
			},
			args: args{
				ctx:            context.Background(),
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: singlePEM,
			},
			want: want{err: nil},
		},
		"SuccessChain": {
			reason: "A two-certificate chain should execute two CREATE CERTIFICATE statements and commit",
			fields: fields{
				db: mockDBWithTx(func(mock sqlmock.Sqlmock) {
					mock.ExpectBegin()
					mock.ExpectExec("CREATE CERTIFICATE").WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectExec("CREATE CERTIFICATE").WillReturnResult(sqlmock.NewResult(1, 1))
					mock.ExpectCommit()
				}),
			},
			args: args{
				ctx:            context.Background(),
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: chainPEM,
			},
			want: want{err: nil},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.fields.db}
			err := c.Create(tc.args.ctx, tc.args.parameters, tc.args.certificatePEM)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nc.Create(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
		})
	}
}
