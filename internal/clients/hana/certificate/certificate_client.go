package certificate

import (
	"context"
	"fmt"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

type CertificateClient interface {
	Read(ctx context.Context, parameters *adminv1alpha1.CertificateParameters) (*adminv1alpha1.CertificateObservation, error)
	Create(ctx context.Context, parameters *adminv1alpha1.CertificateParameters, certificatePEM []byte) error
	Delete(ctx context.Context, parameters *adminv1alpha1.CertificateParameters) error
}

type Client struct {
	xsql.DB
}

func New(db xsql.DB) Client {
	return Client{DB: db}
}

func (c Client) Read(
	ctx context.Context,
	parameters *adminv1alpha1.CertificateParameters,
) (observed *adminv1alpha1.CertificateObservation, err error) {
	query := `
SELECT CERTIFICATE_ID,
       CERTIFICATE_NAME
FROM CERTIFICATES
WHERE CERTIFICATE_NAME LIKE ?
ORDER BY CERTIFICATE_NAME
`
	rows, err := c.QueryContext(
		ctx,
		query,
		sanitizeIdentifier(parameters.Name)+"_CRT_SRV_CERTIFICATE_%",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query certificates: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close certificate rows: %w", closeErr)
		}
	}()
	observed = &adminv1alpha1.CertificateObservation{}
	for rows.Next() {
		var (
			id   int
			name string
		)
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("failed to scan certificate: %w", err)
		}
		observed.Certificates = append(
			observed.Certificates,
			adminv1alpha1.ImportedCertificate{
				ID:   &id,
				Name: name,
			},
		)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading certificate rows: %w", err)
	}
	// No certificates found means the managed resource doesn't exist.
	if len(observed.Certificates) == 0 {
		return nil, nil
	}
	return observed, nil
}

func (c Client) Create(
	ctx context.Context,
	parameters *adminv1alpha1.CertificateParameters,
	certificatePEM []byte,
) error {
	certificates, err := splitPEMChain(certificatePEM)
	if err != nil {
		return err
	}

	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, cert := range certificates {
		name, err := certName(parameters.Name, cert)
		if err != nil {
			return fmt.Errorf("failed to derive certificate name: %w", err)
		}
		// CREATE CERTIFICATE is a DDL statement; HANA does not support bind
		// parameters for it. Both values are sanitized before interpolation:
		// name via EscapeDoubleQuotes (double-quoted identifier) and cert
		// via EscapeSingleQuotes (single-quoted string literal).
		//nolint:gosec // G201: SQL string formatting — values are sanitized above
		query := fmt.Sprintf(
			`CREATE CERTIFICATE "%s" FROM '%s'`,
			utils.EscapeDoubleQuotes(name),
			utils.EscapeSingleQuotes(string(cert)),
		)
		if _, err = tx.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to create certificate %q: %w", name, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit certificate transaction: %w", err)
	}
	return nil
}

func (c Client) Delete(
	ctx context.Context,
	parameters *adminv1alpha1.CertificateParameters,
) error {
	return nil
}
