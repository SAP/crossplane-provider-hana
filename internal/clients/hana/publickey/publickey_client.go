package publickey

import (
	"bytes"
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

// Client manages HANA `PUBLIC KEY` DDL objects.
type Client struct {
	xsql.DB
}

// New creates a new public key client.
func New(db xsql.DB) Client {
	return Client{DB: db}
}

// PublicKeyClient is the interface satisfied by Client.
type PublicKeyClient interface {
	Read(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error)
	Create(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) error
	Update(ctx context.Context, parameters *v1alpha1.PublicKeyParameters, observation *v1alpha1.PublicKeyObservation) error
	Delete(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) error
}

// Read inspects SYS.PUBLIC_KEYS for the named entry. Returns nil observation
// when the key does not exist.
func (c Client) Read(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) (*v1alpha1.PublicKeyObservation, error) {
	query := "SELECT PUBLIC_KEY_NAME, ALGORITHM, FINGERPRINT, COMMENT FROM SYS.PUBLIC_KEYS WHERE PUBLIC_KEY_NAME = ?"

	var name, algorithm, fingerprint string
	var comment sql.NullString

	err := c.QueryRowContext(ctx, query, parameters.Name).Scan(&name, &algorithm, &fingerprint, &comment)
	if xsql.IsNoRows(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to query public key: %w", err)
	}

	obs := &v1alpha1.PublicKeyObservation{
		Name:        &name,
		Algorithm:   &algorithm,
		Fingerprint: &fingerprint,
	}
	if comment.Valid {
		obs.Comment = &comment.String
	}
	return obs, nil
}

// Create runs `CREATE PUBLIC KEY <name> FROM '<pem>' [COMMENT '<comment>']`.
// The PEM is embedded inline; HANA rejects newlines outside of PEM markers, so
// callers must pass a real PEM-encoded value.
//
// The reason we do not use driver placeholders (`?`) here is that HANA's
// grammar for `CREATE PUBLIC KEY ... FROM '<literal>'` does not accept a
// bound parameter for the PEM slot (same constraint as CREATE USER ...
// PASSWORD).
func (c Client) Create(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) error {
	if strings.TrimSpace(parameters.PEM) == "" {
		return fmt.Errorf("public key PEM is empty")
	}

	// Parse the PEM before inlining it into DDL. `pem.Decode` guarantees the
	// body is base64 and the BEGIN/END markers match, which eliminates the
	// single-quote injection surface for the fmt.Sprintf below (the base64
	// alphabet contains no quote). We also confirm the DER is a PKIX
	// SubjectPublicKeyInfo so operator mistakes (pasting a private key,
	// certificate, or truncated block) fail at the reconciler with a clear
	// error instead of at the driver.
	block, rest := pem.Decode([]byte(parameters.PEM))
	if block == nil {
		return fmt.Errorf("public key PEM does not decode as a PEM block")
	}
	if block.Type != "PUBLIC KEY" {
		return fmt.Errorf("public key PEM has type %q, want %q", block.Type, "PUBLIC KEY")
	}
	if len(bytes.TrimSpace(rest)) != 0 {
		return fmt.Errorf("public key PEM has trailing data after the first block")
	}
	if _, err := x509.ParsePKIXPublicKey(block.Bytes); err != nil {
		return fmt.Errorf("public key PEM does not parse as PKIX: %w", err)
	}

	// Re-encode the block so the SQL literal always sees canonical bytes
	// (no leading whitespace, no headers, LF line endings). Same key,
	// deterministic string.
	canonicalPEM := string(pem.EncodeToMemory(&pem.Block{Type: block.Type, Bytes: block.Bytes}))

	query := fmt.Sprintf("CREATE PUBLIC KEY %s FROM '%s'", parameters.Name, canonicalPEM)
	if parameters.Comment != "" {
		query += fmt.Sprintf(" COMMENT '%s'", utils.EscapeSingleQuotes(parameters.Comment))
	}

	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to create public key: %w", err)
	}
	return nil
}

// Update is best-effort. HANA does not provide an ALTER for the key material
// itself; rotating the PEM requires DROP + CREATE. We only rewrite the COMMENT
// when it diverges (cheap, side-effect-free).
func (c Client) Update(ctx context.Context, parameters *v1alpha1.PublicKeyParameters, observation *v1alpha1.PublicKeyObservation) error {
	if observation == nil {
		return fmt.Errorf("public key %q not found, cannot update", parameters.Name)
	}

	desiredComment := parameters.Comment
	currentComment := ""
	if observation.Comment != nil {
		currentComment = *observation.Comment
	}
	if desiredComment == currentComment {
		return nil
	}

	// COMMENT ON PUBLIC KEY <name> IS '<comment>' is the documented form for
	// rewriting the comment without rotating the key.
	query := fmt.Sprintf("COMMENT ON PUBLIC KEY %s IS '%s'", parameters.Name, utils.EscapeSingleQuotes(desiredComment))
	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to update public key comment: %w", err)
	}
	return nil
}

// Delete drops the public key.
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.PublicKeyParameters) error {
	query := fmt.Sprintf("DROP PUBLIC KEY %s", parameters.Name)
	if _, err := c.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to drop public key: %w", err)
	}
	return nil
}
