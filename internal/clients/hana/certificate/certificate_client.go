package certificate

import (
	"context"
	"fmt"

	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
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
) (*adminv1alpha1.CertificateObservation, error) {
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
		parameters.Name+"-%",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query certificates: %w", err)
	}
	defer rows.Close()
	observed := &adminv1alpha1.CertificateObservation{}
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
	for i, cert := range certificates {
		certName := fmt.Sprintf("%s-%d", parameters.Name, i+1)
		query := fmt.Sprintf(
			"CREATE CERTIFICATE %s FROM '%s'",
			certName,
			string(cert),
		)
		if _, err := c.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func (c Client) Delete(
	ctx context.Context,
	parameters *adminv1alpha1.CertificateParameters,
) error {
	return nil
}
