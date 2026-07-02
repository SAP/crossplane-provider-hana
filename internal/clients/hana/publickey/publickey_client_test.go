package publickey

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
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

func strPtr(s string) *string { return &s }

// samplePEM builds a fresh, valid PKIX RSA public-key PEM block.
func samplePEM(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("x509.MarshalPKIXPublicKey: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// samplePrivatePEM returns a PEM block with the wrong Type; used to prove
// that Create rejects private keys and certificates.
func samplePrivatePEM(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(priv)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// captureExec records every ExecContext query so we can assert on the emitted
// DDL. Matches the pattern used by jwtprovider_client_test.go.
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

// keyRow builds a MockDB whose QueryRowContext returns a single SYS.PUBLIC_KEYS
// row. Pass a *string for comment to model a NULL column vs a real value.
// The inner QueryRowContext is a fresh in-memory driver call, not a
// propagation of the outer ctx (hence the contextcheck opt-out).
//
//nolint:contextcheck
func keyRow(name, algo, fp string, comment any) fake.MockDB {
	return fake.MockDB{
		MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
			db, mock, _ := sqlmock.New()
			mock.ExpectQuery("SELECT").WillReturnRows(
				sqlmock.NewRows([]string{"PUBLIC_KEY_NAME", "ALGORITHM", "FINGERPRINT", "COMMENT"}).
					AddRow(name, algo, fp, comment))
			return db.QueryRowContext(context.Background(), "SELECT")
		},
	}
}

//nolint:contextcheck // sqlmock helper; see keyRow.
func keyRowErr(err error) fake.MockDB {
	return fake.MockDB{
		MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
			db, mock, _ := sqlmock.New()
			mock.ExpectQuery("SELECT").WillReturnError(err)
			return db.QueryRowContext(context.Background(), "SELECT")
		},
	}
}

// nolint: contextcheck
func TestRead(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason  string
		db      fake.MockDB
		want    *v1alpha1.PublicKeyObservation
		wantErr error
	}{
		"NotFound": {
			reason: "ErrNoRows → nil observation, nil error",
			db:     keyRowErr(sql.ErrNoRows),
			want:   nil,
		},
		"DriverError": {
			reason:  "Non-ErrNoRows driver error propagates wrapped",
			db:      keyRowErr(errBoom),
			wantErr: fmt.Errorf("failed to query public key: %w", errBoom),
		},
		"FullRow": {
			reason: "Non-null COMMENT populates every observation pointer",
			db:     keyRow("IAS_SIGNING_KEY", "RSA", "AB:CD:EF", "IAS tenant signing key"),
			want: &v1alpha1.PublicKeyObservation{
				Name:        strPtr("IAS_SIGNING_KEY"),
				Algorithm:   strPtr("RSA"),
				Fingerprint: strPtr("AB:CD:EF"),
				Comment:     strPtr("IAS tenant signing key"),
			},
		},
		"NullComment": {
			reason: "NULL COMMENT column leaves obs.Comment == nil",
			db:     keyRow("K", "RSA", "F", nil),
			want: &v1alpha1.PublicKeyObservation{
				Name:        strPtr("K"),
				Algorithm:   strPtr("RSA"),
				Fingerprint: strPtr("F"),
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.db}
			got, err := c.Read(context.Background(), &v1alpha1.PublicKeyParameters{Name: "K"})
			if diff := cmp.Diff(tc.wantErr, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nRead(...): -want error, +got error:\n%s", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\nRead(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

// nolint: contextcheck
func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")
	valid := samplePEM(t)
	privatePEM := samplePrivatePEM(t)

	cases := map[string]struct {
		reason  string
		params  *v1alpha1.PublicKeyParameters
		driver  error    // when set, mocked driver returns this on Exec
		wantSQL []string // substrings that must appear
		unwant  []string // substrings that must NOT appear
		wantErr string   // when set, error message must contain this
	}{
		// Validation branches — one representative case per rejection reason.
		"EmptyPEM": {
			reason:  "Whitespace-only PEM rejected before any DDL",
			params:  &v1alpha1.PublicKeyParameters{Name: "K", PEM: "   \n\t"},
			wantErr: "PEM is empty",
		},
		"UnparseablePEM": {
			reason:  "Non-PEM garbage rejected at pem.Decode",
			params:  &v1alpha1.PublicKeyParameters{Name: "K", PEM: "not-a-pem-block-at-all"},
			wantErr: "does not decode as a PEM block",
		},
		"WrongPEMType": {
			reason:  "PRIVATE KEY block rejected — paste-error guard",
			params:  &v1alpha1.PublicKeyParameters{Name: "K", PEM: privatePEM},
			wantErr: "has type",
		},
		"TrailingData": {
			reason:  "Non-empty rest after first PEM block rejected",
			params:  &v1alpha1.PublicKeyParameters{Name: "K", PEM: valid + "trailing garbage\n"},
			wantErr: "trailing data",
		},
		"NotPKIX": {
			reason:  "Well-formed PEM wrapper with body that isn't PKIX SubjectPublicKeyInfo is rejected before it reaches HANA",
			params:  &v1alpha1.PublicKeyParameters{Name: "K", PEM: "-----BEGIN PUBLIC KEY-----\nAA==\n-----END PUBLIC KEY-----\n"},
			wantErr: "does not parse as PKIX",
		},
		"DriverError": {
			reason:  "Driver failure on CREATE PUBLIC KEY propagates wrapped",
			params:  &v1alpha1.PublicKeyParameters{Name: "K", PEM: valid},
			driver:  errBoom,
			wantErr: "failed to create public key",
		},
		"SuccessNoComment": {
			reason:  "Valid PEM without comment emits CREATE PUBLIC KEY and no COMMENT clause",
			params:  &v1alpha1.PublicKeyParameters{Name: "IAS_KEY", PEM: valid},
			wantSQL: []string{"CREATE PUBLIC KEY IAS_KEY FROM '-----BEGIN PUBLIC KEY-----", "-----END PUBLIC KEY-----"},
			unwant:  []string{"COMMENT"},
		},
		"SuccessWithComment": {
			// Single-quote escape check: `tenant's` must become `tenant''s`.
			reason: "Valid PEM with comment appends the COMMENT clause with single quotes doubled",
			params: &v1alpha1.PublicKeyParameters{Name: "IAS_KEY", PEM: valid, Comment: "IAS tenant's signing key"},
			wantSQL: []string{
				"CREATE PUBLIC KEY IAS_KEY FROM '",
				"COMMENT 'IAS tenant''s signing key'",
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var db fake.MockDB
			var cap captureExec
			if tc.driver != nil {
				db = fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, tc.driver
					},
				}
			} else {
				db = cap.mock()
			}

			c := Client{DB: db}
			err := c.Create(context.Background(), tc.params)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("\n%s\nCreate(...): want error containing %q, got: %v", tc.reason, tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Errorf("\n%s\nCreate(...): unexpected error: %v", tc.reason, err)
				return
			}
			joined := strings.Join(cap.queries, "\n---\n")
			for _, want := range tc.wantSQL {
				if !strings.Contains(joined, want) {
					t.Errorf("\n%s\nCreate(...): missing SQL substring %q:\n%s", tc.reason, want, joined)
				}
			}
			for _, no := range tc.unwant {
				if strings.Contains(joined, no) {
					t.Errorf("\n%s\nCreate(...): forbidden SQL substring %q:\n%s", tc.reason, no, joined)
				}
			}
		})
	}
}

// nolint: contextcheck
func TestUpdate(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason   string
		params   *v1alpha1.PublicKeyParameters
		observed *v1alpha1.PublicKeyObservation
		driver   error
		wantSQL  []string
		unwant   []string
		wantErr  string
	}{
		"NilObservation": {
			reason:   "Update with no observation returns a clear error, no panic",
			params:   &v1alpha1.PublicKeyParameters{Name: "K"},
			observed: nil,
			wantErr:  "not found, cannot update",
		},
		"NoDrift": {
			// Equal comment values (both empty, or both the same) must not
			// emit any DDL — reconcile has to be idempotent.
			reason:   "Equal comments (or both empty) emit no DDL",
			params:   &v1alpha1.PublicKeyParameters{Name: "K", Comment: "same"},
			observed: &v1alpha1.PublicKeyObservation{Comment: strPtr("same")},
			unwant:   []string{"COMMENT ON PUBLIC KEY"},
		},
		"CommentDriftEscape": {
			// Covers both the drift path (nil→"team's key") and single-quote
			// escaping in one case; previously two cases, redundant.
			reason:   "Setting a first-time comment with a single quote emits COMMENT ON with quotes doubled",
			params:   &v1alpha1.PublicKeyParameters{Name: "K", Comment: "team's key"},
			observed: &v1alpha1.PublicKeyObservation{},
			wantSQL:  []string{"COMMENT ON PUBLIC KEY K IS 'team''s key'"},
		},
		"DriverError": {
			reason:   "Driver failure on COMMENT ON propagates wrapped",
			params:   &v1alpha1.PublicKeyParameters{Name: "K", Comment: "new"},
			observed: &v1alpha1.PublicKeyObservation{Comment: strPtr("old")},
			driver:   errBoom,
			wantErr:  "failed to update public key comment",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var db fake.MockDB
			var cap captureExec
			if tc.driver != nil {
				db = fake.MockDB{
					MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
						return nil, tc.driver
					},
				}
			} else {
				db = cap.mock()
			}

			c := Client{DB: db}
			err := c.Update(context.Background(), tc.params, tc.observed)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("\n%s\nUpdate(...): want error containing %q, got: %v", tc.reason, tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Errorf("\n%s\nUpdate(...): unexpected error: %v", tc.reason, err)
				return
			}
			joined := strings.Join(cap.queries, "\n---\n")
			for _, want := range tc.wantSQL {
				if !strings.Contains(joined, want) {
					t.Errorf("\n%s\nUpdate(...): missing SQL substring %q:\n%s", tc.reason, want, joined)
				}
			}
			for _, no := range tc.unwant {
				if strings.Contains(joined, no) {
					t.Errorf("\n%s\nUpdate(...): forbidden SQL substring %q:\n%s", tc.reason, no, joined)
				}
			}
		})
	}
}

// nolint: contextcheck
func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason  string
		err     error // driver error to return
		want    error
		wantSQL string
	}{
		"Success":     {reason: "DROP PUBLIC KEY emitted with the name unquoted", wantSQL: "DROP PUBLIC KEY IAS_KEY"},
		"DriverError": {reason: "Driver failure on DROP PUBLIC KEY propagates wrapped", err: errBoom, want: fmt.Errorf("failed to drop public key: %w", errBoom)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var cap captureExec
			db := cap.mock()
			if tc.err != nil {
				db = fake.MockDB{MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					return nil, tc.err
				}}
			}
			c := Client{DB: db}
			err := c.Delete(context.Background(), &v1alpha1.PublicKeyParameters{Name: "IAS_KEY"})
			if diff := cmp.Diff(tc.want, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nDelete(...): -want error, +got error:\n%s", tc.reason, diff)
			}
			if tc.wantSQL != "" {
				joined := strings.Join(cap.queries, "\n---\n")
				if !strings.Contains(joined, tc.wantSQL) {
					t.Errorf("\n%s\nDelete(...): missing SQL substring %q:\n%s", tc.reason, tc.wantSQL, joined)
				}
			}
		})
	}
}
