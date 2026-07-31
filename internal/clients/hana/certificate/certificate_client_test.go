package certificate

import (
	"context"
	"database/sql"
	"encoding/pem"
	stderrors "errors"
	"fmt"
	"testing"

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

	singlePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("cert-one")})
	chainPEM := append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("cert-one")}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("cert-two")})...,
	)

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
			reason: "Non-PEM input should return an error before any SQL is executed",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errors.New("should not be called")
					},
				},
			},
			args: args{
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: []byte("not valid pem"),
			},
			// splitPEMChain uses stdlib errors.New so we match with stdlib here
			want: want{err: stderrors.New("failed to decode PEM certificate")},
		},
		"ErrExecSingleCert": {
			reason: "A SQL error while importing a single certificate should be returned",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, errBoom
					},
				},
			},
			args: args{
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: singlePEM,
			},
			want: want{err: errBoom},
		},
		"SuccessSingleCert": {
			reason: "A single PEM certificate should execute exactly one CREATE CERTIFICATE statement",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: singlePEM,
			},
			want: want{err: nil},
		},
		"ErrExecSecondCertInChain": {
			reason: "A SQL error on the second certificate of a chain should be returned",
			fields: fields{
				db: func() fake.MockDB {
					call := 0
					return fake.MockDB{
						MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
							call++
							if call == 2 {
								return nil, errBoom
							}
							return nil, nil
						},
					}
				}(),
			},
			args: args{
				parameters:     &adminv1alpha1.CertificateParameters{Name: "my-ca"},
				certificatePEM: chainPEM,
			},
			want: want{err: errBoom},
		},
		"SuccessChain": {
			reason: "A two-certificate PEM chain should execute two CREATE CERTIFICATE statements without error",
			fields: fields{
				db: fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, nil
					},
				},
			},
			args: args{
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
