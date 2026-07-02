package jwtprovider

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

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }

// providerRow builds a MockDB whose QueryRowContext returns one row shaped like
// SYS.JWT_PROVIDERS. The inner QueryRowContext is a fresh in-memory driver
// call, not a propagation of the outer ctx (hence the contextcheck opt-out).
//
//nolint:contextcheck
func providerRow(issuer, claim string, caseSensitive any, priority int) fake.MockDB {
	return fake.MockDB{
		MockQueryRowContext: func(ctx context.Context, query string, args ...any) *sql.Row {
			db, mock, _ := sqlmock.New()
			mock.ExpectQuery("SELECT").WillReturnRows(
				sqlmock.NewRows([]string{"ISSUER_NAME", "EXTERNAL_IDENTITY_CLAIM", "IS_CASE_SENSITIVE", "PRIORITY"}).
					AddRow(issuer, claim, caseSensitive, priority))
			return db.QueryRowContext(context.Background(), "SELECT")
		},
	}
}

// providerRowErr builds a MockDB whose QueryRowContext returns an error.
//
//nolint:contextcheck // sqlmock helper; see providerRow.
func providerRowErr(err error) fake.MockDB {
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
		want    *v1alpha1.JWTProviderObservation
		wantErr error
	}{
		"NotFound": {
			reason: "SYS.JWT_PROVIDERS with no matching row → nil observation",
			db:     providerRowErr(sql.ErrNoRows),
			want:   nil,
		},
		"ErrRead": {
			reason:  "Driver error on the providers query propagates wrapped",
			db:      providerRowErr(errBoom),
			wantErr: fmt.Errorf("failed to read JWT provider: %w", errBoom),
		},
		"CaseSensitive": {
			// IS_CASE_SENSITIVE=TRUE maps to CaseInsensitiveIdentity=false;
			// the reverse (false→true) is symmetric and covered by Update tests.
			reason: "IS_CASE_SENSITIVE=TRUE observes CaseInsensitiveIdentity=false",
			db:     providerRow("https://issuer.example/v2.0", "sub", true, 100),
			want: &v1alpha1.JWTProviderObservation{
				Name:                    strPtr("P"),
				Issuer:                  strPtr("https://issuer.example/v2.0"),
				ExternalIdentityClaim:   strPtr("sub"),
				CaseInsensitiveIdentity: boolPtr(false),
				Priority:                intPtr(100),
			},
		},
		"CaseSensitiveNull": {
			// Defensive: NULL BOOLEAN leaves the pointer nil; the reconciler
			// must tolerate this because Update skips the case-sensitivity
			// branch when the observation is nil.
			reason: "IS_CASE_SENSITIVE=NULL leaves CaseInsensitiveIdentity unset",
			db:     providerRow("https://issuer.example/v2.0", "sub", nil, 100),
			want: &v1alpha1.JWTProviderObservation{
				Name:                  strPtr("P"),
				Issuer:                strPtr("https://issuer.example/v2.0"),
				ExternalIdentityClaim: strPtr("sub"),
				Priority:              intPtr(100),
			},
		},
		"WithClaims": {
			reason: "SYS.JWT_PROVIDER_CLAIMS rows populate ApplicationUserClaim and ClaimFilters; the mirror row for EXTERNAL IDENTITY is filtered out",
			db: func() fake.MockDB {
				m := providerRow("https://issuer.example/v2.0", "sub", true, 100)
				m.MockQueryContext = func(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
					return fake.MockRowsToSQLRows(
						sqlmock.NewRows([]string{"CLAIM", "OPERATION", "VALUE"}).
							AddRow("sub", "AS EXTERNAL IDENTITY", nil).
							AddRow("azp", "AS APPLICATION USER", nil).
							AddRow("aud", "HAS MEMBER", "audience-a").
							AddRow("aud", "HAS MEMBER", "audience-b"),
					), nil
				}
				return m
			}(),
			want: &v1alpha1.JWTProviderObservation{
				Name:                    strPtr("P"),
				Issuer:                  strPtr("https://issuer.example/v2.0"),
				ExternalIdentityClaim:   strPtr("sub"),
				CaseInsensitiveIdentity: boolPtr(false),
				Priority:                intPtr(100),
				ApplicationUserClaim:    "azp",
				ClaimFilters: []v1alpha1.JWTClaimFilter{
					{Claim: "aud", Value: "audience-a"},
					{Claim: "aud", Value: "audience-b"},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: tc.db}
			got, err := c.Read(context.Background(), &v1alpha1.JWTProviderParameters{Name: "P"})
			if diff := cmp.Diff(tc.wantErr, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nRead(...): -want error, +got error:\n%s", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("\n%s\nRead(...): -want, +got:\n%s", tc.reason, diff)
			}
		})
	}
}

// captureExec records every ExecContext query so tests can assert on the DDL
// the client emits.
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

// nolint: contextcheck
func TestCreate(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason  string
		params  *v1alpha1.JWTProviderParameters
		driver  error
		wantSQL []string
		wantErr error
	}{
		"ErrCreate": {
			reason:  "Driver error on CREATE JWT PROVIDER propagates wrapped",
			params:  &v1alpha1.JWTProviderParameters{Name: "P", Issuer: "https://issuer.example", Priority: 100},
			driver:  errBoom,
			wantErr: fmt.Errorf("failed to create JWT provider: %w", errBoom),
		},
		"MinimalCreate": {
			// PRIORITY must be emitted even at the default 100 so the observed
			// row after Create matches the spec (else isUpToDate would flap).
			reason: "CREATE always emits PRIORITY and defaults the claim to 'sub'",
			params: &v1alpha1.JWTProviderParameters{
				Name:     "MY_PROVIDER",
				Issuer:   "https://issuer.example/v2.0",
				Priority: 100,
			},
			wantSQL: []string{
				"CREATE JWT PROVIDER MY_PROVIDER",
				"WITH ISSUER 'https://issuer.example/v2.0'",
				"CLAIM 'sub' AS EXTERNAL IDENTITY",
				"PRIORITY 100",
			},
		},
		"CaseInsensitiveWithCustomClaim": {
			reason: "CaseInsensitiveIdentity=true appends CASE INSENSITIVE IDENTITY; custom claim replaces 'sub'",
			params: &v1alpha1.JWTProviderParameters{
				Name:                    "P",
				Issuer:                  "https://issuer.example",
				ExternalIdentityClaim:   "email",
				CaseInsensitiveIdentity: true,
				Priority:                50,
			},
			wantSQL: []string{
				"CLAIM 'email' AS EXTERNAL IDENTITY",
				"CASE INSENSITIVE IDENTITY",
				"PRIORITY 50",
			},
		},
		"WithApplicationUserAndFilters": {
			reason: "ApplicationUserClaim and ClaimFilters each produce an ALTER statement",
			params: &v1alpha1.JWTProviderParameters{
				Name:                 "P",
				Issuer:               "https://issuer.example",
				Priority:             100,
				ApplicationUserClaim: "azp",
				ClaimFilters:         []v1alpha1.JWTClaimFilter{{Claim: "aud", Value: "audience-a"}},
			},
			wantSQL: []string{
				"CREATE JWT PROVIDER P",
				"ALTER JWT PROVIDER P SET CLAIM 'azp' AS APPLICATION USER",
				"ALTER JWT PROVIDER P SET CLAIM 'aud' HAS MEMBER 'audience-a'",
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
			if diff := cmp.Diff(tc.wantErr, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nCreate(...): -want error, +got error:\n%s", tc.reason, diff)
				return
			}
			if tc.wantErr != nil {
				return
			}
			joined := strings.Join(cap.queries, "\n---\n")
			for _, want := range tc.wantSQL {
				if !strings.Contains(joined, want) {
					t.Errorf("\n%s\nCreate(...): missing SQL substring %q:\n%s", tc.reason, want, joined)
				}
			}
		})
	}
}

// baseObservation is the reference state used by Update tests: matches
// baseParams below, so a bare copy causes zero drift.
func baseParams() *v1alpha1.JWTProviderParameters {
	return &v1alpha1.JWTProviderParameters{
		Name:                  "P",
		Issuer:                "https://issuer.example",
		ExternalIdentityClaim: "sub",
		Priority:              100,
	}
}
func baseObservation() *v1alpha1.JWTProviderObservation {
	return &v1alpha1.JWTProviderObservation{
		Issuer:                strPtr("https://issuer.example"),
		ExternalIdentityClaim: strPtr("sub"),
		Priority:              intPtr(100),
	}
}

// nolint: contextcheck
func TestUpdate(t *testing.T) {
	// Each case builds params/observed from the base and mutates only the
	// field under test; keeps the intent obvious and each case short.
	cases := map[string]struct {
		reason    string
		mutate    func(p *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation)
		wantSQL   []string
		unwantSQL []string
	}{
		"NoDrift": {
			reason:    "Spec == observation emits no ALTER",
			mutate:    func(p *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation) {},
			unwantSQL: []string{"ALTER JWT PROVIDER"},
		},
		"PriorityDrift": {
			// Priority is a plain int with default 100; drift from 100→50 must
			// emit ALTER. Regression guard: earlier draft gated on *int nil.
			reason:  "Priority difference emits ALTER ... SET PRIORITY",
			mutate:  func(p *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) { p.Priority = 50 },
			wantSQL: []string{"ALTER JWT PROVIDER P SET PRIORITY 50"},
		},
		"IssuerDrift": {
			reason: "Issuer difference emits ALTER ... SET ISSUER",
			mutate: func(p *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) {
				p.Issuer = "https://new-issuer.example"
			},
			wantSQL: []string{"ALTER JWT PROVIDER P SET ISSUER 'https://new-issuer.example'"},
		},
		"ExternalIdentityClaimDrift": {
			// A change to externalIdentityClaim must emit the ALTER; else
			// isUpToDate=false while Update is a no-op and the reconciler loops.
			reason: "ExternalIdentityClaim drift emits SET CLAIM ... AS EXTERNAL IDENTITY",
			mutate: func(p *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) {
				p.ExternalIdentityClaim = "email"
			},
			wantSQL: []string{"ALTER JWT PROVIDER P SET CLAIM 'email' AS EXTERNAL IDENTITY"},
		},
		"ExternalIdentityClaimDefault": {
			// Empty spec compares against the "sub" default so no drift is
			// detected between {"" -> "sub"} on freshly-observed rows.
			reason: "Empty spec claim vs 'sub' observation is a no-op",
			mutate: func(p *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) {
				p.ExternalIdentityClaim = ""
			},
			unwantSQL: []string{"AS EXTERNAL IDENTITY"},
		},
		"CaseInsensitiveDriftEnable": {
			// HANA's ALTER JWT PROVIDER for case sensitivity takes NO `SET`
			// keyword; the DDL is `ALTER ... CASE (IN)SENSITIVE IDENTITY`.
			// Verified on HANA Cloud 4.00.000.00.1782807759 which rejects
			// `SET CASE ...` with SQL error 257.
			reason: "false→true emits CASE INSENSITIVE IDENTITY (no SET keyword)",
			mutate: func(p *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation) {
				p.CaseInsensitiveIdentity = true
				o.CaseInsensitiveIdentity = boolPtr(false)
			},
			wantSQL:   []string{"ALTER JWT PROVIDER P CASE INSENSITIVE IDENTITY"},
			unwantSQL: []string{"SET CASE"},
		},
		"CaseInsensitiveDriftDisable": {
			reason: "true→false emits CASE SENSITIVE IDENTITY",
			mutate: func(p *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation) {
				// p.CaseInsensitiveIdentity stays false (zero value).
				o.CaseInsensitiveIdentity = boolPtr(true)
			},
			wantSQL: []string{"ALTER JWT PROVIDER P CASE SENSITIVE IDENTITY"},
		},
		"CaseInsensitiveNilObservation": {
			// Defensive: nil observation.CaseInsensitiveIdentity means the
			// column was NULL — skip the branch, don't emit a false-positive.
			reason: "Nil observation suppresses the case-sensitivity branch",
			mutate: func(p *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) {
				p.CaseInsensitiveIdentity = true
			},
			unwantSQL: []string{"CASE"},
		},
		"ClaimFilterAdd": {
			// Desired filter that isn't in the observation adds a HAS MEMBER
			// via `SET CLAIM ... HAS MEMBER`. No UNSET is emitted.
			reason: "Adding a claim filter emits SET CLAIM ... HAS MEMBER, no UNSET",
			mutate: func(p *v1alpha1.JWTProviderParameters, _ *v1alpha1.JWTProviderObservation) {
				p.ClaimFilters = []v1alpha1.JWTClaimFilter{
					{Claim: "groups", Value: "00000000-0000-0000-0000-deadbeefcafe"},
				}
			},
			wantSQL: []string{
				"ALTER JWT PROVIDER P SET CLAIM 'groups' HAS MEMBER '00000000-0000-0000-0000-deadbeefcafe'",
			},
			unwantSQL: []string{"UNSET CLAIM"},
		},
		"ClaimFilterRemove": {
			// Observation has a filter the spec no longer wants — UNSET the
			// whole claim name; no SET emitted.
			reason: "Removing a claim filter emits UNSET CLAIM, no SET",
			mutate: func(_ *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation) {
				o.ClaimFilters = []v1alpha1.JWTClaimFilter{
					{Claim: "groups", Value: "00000000-0000-0000-0000-deadbeefcafe"},
				}
			},
			wantSQL:   []string{"ALTER JWT PROVIDER P UNSET CLAIM 'groups'"},
			unwantSQL: []string{"SET CLAIM 'groups' HAS MEMBER"},
		},
		"ClaimFilterValueChange": {
			// One-value-per-claim: replacing the value must UNSET then SET.
			// This is what the JWT-SSO flow relies on when operators rotate
			// SCIM group UUIDs.
			reason: "Changing a claim filter value emits UNSET CLAIM followed by SET CLAIM ... HAS MEMBER with the new value",
			mutate: func(p *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation) {
				p.ClaimFilters = []v1alpha1.JWTClaimFilter{
					{Claim: "groups", Value: "11111111-2222-3333-4444-555555555555"},
				}
				o.ClaimFilters = []v1alpha1.JWTClaimFilter{
					{Claim: "groups", Value: "00000000-0000-0000-0000-deadbeefcafe"},
				}
			},
			wantSQL: []string{
				"ALTER JWT PROVIDER P UNSET CLAIM 'groups'",
				"ALTER JWT PROVIDER P SET CLAIM 'groups' HAS MEMBER '11111111-2222-3333-4444-555555555555'",
			},
		},
		"ClaimFilterNoDrift": {
			// Identical filter set on both sides emits neither UNSET nor SET.
			reason: "Identical claim filters on both sides emit no filter DDL",
			mutate: func(p *v1alpha1.JWTProviderParameters, o *v1alpha1.JWTProviderObservation) {
				f := v1alpha1.JWTClaimFilter{Claim: "aud", Value: "audience-a"}
				p.ClaimFilters = []v1alpha1.JWTClaimFilter{f}
				o.ClaimFilters = []v1alpha1.JWTClaimFilter{f}
			},
			unwantSQL: []string{"UNSET CLAIM", "SET CLAIM 'aud' HAS MEMBER"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, o := baseParams(), baseObservation()
			tc.mutate(p, o)
			var cap captureExec
			c := Client{DB: cap.mock()}
			if err := c.Update(context.Background(), p, o); err != nil {
				t.Fatalf("%s: Update returned unexpected error: %v", tc.reason, err)
			}
			joined := strings.Join(cap.queries, "\n---\n")
			for _, want := range tc.wantSQL {
				if !strings.Contains(joined, want) {
					t.Errorf("\n%s\nUpdate(...): missing SQL substring %q:\n%s", tc.reason, want, joined)
				}
			}
			for _, unwant := range tc.unwantSQL {
				if strings.Contains(joined, unwant) {
					t.Errorf("\n%s\nUpdate(...): forbidden SQL substring %q:\n%s", tc.reason, unwant, joined)
				}
			}
		})
	}
}

// nolint: contextcheck
func TestDelete(t *testing.T) {
	errBoom := errors.New("boom")

	cases := map[string]struct {
		reason string
		err    error // driver error to return
		want   error
	}{
		"Success": {reason: "DROP JWT PROVIDER succeeds silently"},
		"ErrDelete": {
			reason: "Driver error on DROP JWT PROVIDER propagates wrapped",
			err:    errBoom,
			want:   fmt.Errorf("failed to drop JWT provider: %w", errBoom),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := Client{DB: fake.MockDB{
				MockExecContext: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
					return nil, tc.err
				},
			}}
			err := c.Delete(context.Background(), &v1alpha1.JWTProviderParameters{Name: "P"})
			if diff := cmp.Diff(tc.want, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\nDelete(...): -want error, +got error:\n%s", tc.reason, diff)
			}
		})
	}
}
