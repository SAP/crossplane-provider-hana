package certificate

import (
	"fmt"
    "context"
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
	return nil, nil
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