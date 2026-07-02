package jwtprovider

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

// Operation values used by HANA in SYS.JWT_PROVIDER_CLAIMS.OPERATION.
const (
	OpExternalIdentity = "AS EXTERNAL IDENTITY"
	OpApplicationUser  = "AS APPLICATION USER"
	OpHasMember        = "HAS MEMBER"
)

// JWTProviderClient is the interface satisfied by Client.
type JWTProviderClient interface {
	Read(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error)
	Create(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) error
	Update(ctx context.Context, parameters *v1alpha1.JWTProviderParameters, observation *v1alpha1.JWTProviderObservation) error
	Delete(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) error
}

// Client manages HANA JWT PROVIDER DDL objects.
type Client struct {
	xsql.DB
}

// New creates a JWT provider client.
func New(db xsql.DB) Client {
	return Client{DB: db}
}

// Read inspects SYS.JWT_PROVIDERS and SYS.JWT_PROVIDER_CLAIMS for the named
// provider. Returns nil when the provider does not exist.
//
// indirection without reducing branches.
//
//nolint:gocyclo // Straight-line SQL + column-by-column scan; splitting adds
func (c Client) Read(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) (*v1alpha1.JWTProviderObservation, error) {
	const providerQuery = "SELECT ISSUER_NAME, EXTERNAL_IDENTITY_CLAIM, IS_CASE_SENSITIVE, PRIORITY " +
		"FROM SYS.JWT_PROVIDERS WHERE JWT_PROVIDER_NAME = ?"

	var issuer, externalIdentityClaim string
	var caseSensitive sql.NullBool
	var priority sql.NullInt64

	err := c.QueryRowContext(ctx, providerQuery, parameters.Name).Scan(&issuer, &externalIdentityClaim, &caseSensitive, &priority)
	if xsql.IsNoRows(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to read JWT provider: %w", err)
	}

	name := parameters.Name
	obs := &v1alpha1.JWTProviderObservation{
		Name:                  &name,
		Issuer:                &issuer,
		ExternalIdentityClaim: &externalIdentityClaim,
	}

	// HANA stores IS_CASE_SENSITIVE as a BOOLEAN on SYS.JWT_PROVIDERS; we
	// invert it so the spec field maps to the SQL keyword the operator typed.
	if caseSensitive.Valid {
		ci := !caseSensitive.Bool
		obs.CaseInsensitiveIdentity = &ci
	}
	if priority.Valid {
		p := int(priority.Int64)
		obs.Priority = &p
	}

	const claimsQuery = "SELECT CLAIM, OPERATION, VALUE FROM SYS.JWT_PROVIDER_CLAIMS WHERE JWT_PROVIDER_NAME = ?"
	rows, err := c.QueryContext(ctx, claimsQuery, parameters.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to read JWT provider claims: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	for rows.Next() {
		var claim, operation string
		var value sql.NullString
		if err := rows.Scan(&claim, &operation, &value); err != nil {
			return nil, err
		}
		switch normalizeOp(operation) {
		case OpApplicationUser:
			obs.ApplicationUserClaim = claim
		case OpHasMember:
			if value.Valid {
				obs.ClaimFilters = append(obs.ClaimFilters, v1alpha1.JWTClaimFilter{
					Claim: claim,
					Value: value.String,
				})
			}
		case OpExternalIdentity:
			// already captured via SYS.JWT_PROVIDERS.EXTERNAL_IDENTITY_CLAIM;
			// the row in SYS.JWT_PROVIDER_CLAIMS just mirrors it.
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return obs, nil
}

// Create issues `CREATE JWT PROVIDER ...` and any follow-up `ALTER ... SET
// CLAIM` statements for application-user mapping and HAS MEMBER filters.
func (c Client) Create(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) error {
	claim := parameters.ExternalIdentityClaim
	if claim == "" {
		claim = "sub"
	}

	query := fmt.Sprintf(
		"CREATE JWT PROVIDER %s WITH ISSUER '%s' CLAIM '%s' AS EXTERNAL IDENTITY",
		parameters.Name,
		utils.EscapeSingleQuotes(parameters.Issuer),
		utils.EscapeSingleQuotes(claim),
	)
	if parameters.CaseInsensitiveIdentity {
		query += " CASE INSENSITIVE IDENTITY"
	}
	query += fmt.Sprintf(" PRIORITY %d", parameters.Priority)

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to create JWT provider: %w", err)
	}

	if parameters.ApplicationUserClaim != "" {
		if err := c.setApplicationUserClaim(ctx, parameters.Name, parameters.ApplicationUserClaim); err != nil {
			return err
		}
	}

	for _, f := range parameters.ClaimFilters {
		if err := c.addClaimFilter(ctx, parameters.Name, f); err != nil {
			return err
		}
	}
	return nil
}

// Update reconciles drift between desired and observed state.
//
// the DDL surface and is intentionally flat.
//
//nolint:gocyclo // One branch per JWT provider attribute; the shape mirrors
func (c Client) Update(ctx context.Context, parameters *v1alpha1.JWTProviderParameters, observation *v1alpha1.JWTProviderObservation) error {
	if observation == nil {
		return fmt.Errorf("cannot update non-existent JWT provider %q", parameters.Name)
	}

	if observation.Issuer == nil || parameters.Issuer != *observation.Issuer {
		query := fmt.Sprintf("ALTER JWT PROVIDER %s SET ISSUER '%s'", parameters.Name, utils.EscapeSingleQuotes(parameters.Issuer))
		if _, err := c.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to update issuer: %w", err)
		}
	}

	if observation.Priority == nil || parameters.Priority != *observation.Priority {
		query := fmt.Sprintf("ALTER JWT PROVIDER %s SET PRIORITY %d", parameters.Name, parameters.Priority)
		if _, err := c.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to update priority: %w", err)
		}
	}

	// Case sensitivity. HANA takes no `SET` keyword here: the DDL is
	// `ALTER JWT PROVIDER <n> CASE (IN)SENSITIVE IDENTITY`, matching the
	// CREATE-time clause exactly. Skip reconciliation when the observation
	// has no bool (nil column) since drift is undetectable in that case.
	if observation.CaseInsensitiveIdentity != nil && parameters.CaseInsensitiveIdentity != *observation.CaseInsensitiveIdentity {
		verb := "CASE SENSITIVE IDENTITY"
		if parameters.CaseInsensitiveIdentity {
			verb = "CASE INSENSITIVE IDENTITY"
		}
		query := fmt.Sprintf("ALTER JWT PROVIDER %s %s", parameters.Name, verb)
		if _, err := c.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to update case sensitivity: %w", err)
		}
	}

	// External identity claim. HANA replaces the binding atomically via
	// `SET CLAIM '<new>' AS EXTERNAL IDENTITY`; `UNSET CLAIM '<current>'` on
	// the external identity claim fails with SQL error 4239.
	desiredClaim := parameters.ExternalIdentityClaim
	if desiredClaim == "" {
		desiredClaim = "sub"
	}
	if observation.ExternalIdentityClaim == nil || *observation.ExternalIdentityClaim != desiredClaim {
		query := fmt.Sprintf("ALTER JWT PROVIDER %s SET CLAIM '%s' AS EXTERNAL IDENTITY",
			parameters.Name, utils.EscapeSingleQuotes(desiredClaim))
		if _, err := c.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to update external identity claim: %w", err)
		}
	}

	// Application user claim.
	desiredAppUser := parameters.ApplicationUserClaim
	if desiredAppUser != observation.ApplicationUserClaim {
		if desiredAppUser == "" {
			// HANA syntax to drop the binding: UNSET CLAIM '<claim>'.
			if observation.ApplicationUserClaim != "" {
				query := fmt.Sprintf("ALTER JWT PROVIDER %s UNSET CLAIM '%s'", parameters.Name, utils.EscapeSingleQuotes(observation.ApplicationUserClaim))
				if _, err := c.ExecContext(ctx, query); err != nil {
					return fmt.Errorf("failed to unset application user claim: %w", err)
				}
			}
		} else {
			if err := c.setApplicationUserClaim(ctx, parameters.Name, desiredAppUser); err != nil {
				return err
			}
		}
	}

	// Claim filters.
	toAdd, toRemove := diffClaimFilters(parameters.ClaimFilters, observation.ClaimFilters)
	for _, f := range toRemove {
		query := fmt.Sprintf("ALTER JWT PROVIDER %s UNSET CLAIM '%s'", parameters.Name, utils.EscapeSingleQuotes(f.Claim))
		if _, err := c.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to drop claim filter %q: %w", f.Claim, err)
		}
	}
	for _, f := range toAdd {
		if err := c.addClaimFilter(ctx, parameters.Name, f); err != nil {
			return err
		}
	}

	return nil
}

// Delete drops the JWT provider. Note that any PSE bindings or user identity
// mappings should be removed first by their respective controllers; HANA will
// reject the DROP otherwise.
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.JWTProviderParameters) error {
	query := fmt.Sprintf("DROP JWT PROVIDER %s", parameters.Name)
	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to drop JWT provider: %w", err)
	}
	return nil
}

func (c Client) setApplicationUserClaim(ctx context.Context, providerName, claim string) error {
	query := fmt.Sprintf(
		"ALTER JWT PROVIDER %s SET CLAIM '%s' AS APPLICATION USER",
		providerName,
		utils.EscapeSingleQuotes(claim),
	)
	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to set application user claim: %w", err)
	}
	return nil
}

func (c Client) addClaimFilter(ctx context.Context, providerName string, f v1alpha1.JWTClaimFilter) error {
	query := fmt.Sprintf(
		"ALTER JWT PROVIDER %s SET CLAIM '%s' HAS MEMBER '%s'",
		providerName,
		utils.EscapeSingleQuotes(f.Claim),
		utils.EscapeSingleQuotes(f.Value),
	)
	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to set claim filter %s=%s: %w", f.Claim, f.Value, err)
	}
	return nil
}

func diffClaimFilters(desired, observed []v1alpha1.JWTClaimFilter) (toAdd, toRemove []v1alpha1.JWTClaimFilter) {
	contains := func(s []v1alpha1.JWTClaimFilter, x v1alpha1.JWTClaimFilter) bool {
		for _, y := range s {
			if y.Claim == x.Claim && y.Value == x.Value {
				return true
			}
		}
		return false
	}
	for _, d := range desired {
		if !contains(observed, d) {
			toAdd = append(toAdd, d)
		}
	}
	// Filters are addressed by claim name in UNSET, so we drop any observed
	// claim whose desired value differs (or absent from desired entirely).
	for _, o := range observed {
		if !contains(desired, o) {
			toRemove = append(toRemove, o)
		}
	}
	return toAdd, toRemove
}

func normalizeOp(v string) string {
	return strings.TrimSpace(strings.ToUpper(v))
}
