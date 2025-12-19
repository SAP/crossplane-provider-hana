package personalsecurityenvironment

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

// PersonalSecurityEnvironmentClient defines the interface for PSE client operations
type PersonalSecurityEnvironmentClient interface {
	Read(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error)
	Create(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters, providerName string) error
	Delete(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) error
	Update(ctx context.Context, pseName string, toAdd, toRemove []v1alpha1.CertificateRef, providerName string) error
}

const errQueryRow = "error querying row: %w"

// Client struct holds the connection to the db
type Client struct {
	xsql.DB
}

// New creates a new db client
func New(db xsql.DB) Client {
	return Client{
		DB: db,
	}
}

func (c Client) Read(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
	observed := &v1alpha1.PersonalSecurityEnvironmentObservation{}

	pseCh := make(chan error, 1)
	go c.selectPSE(ctx, parameters.Name, observed, pseCh)

	certCh := make(chan error, 1)
	go c.selectPSECertificates(ctx, parameters.Name, observed, certCh)

	purposeCh := make(chan error, 1)
	go c.selectPSEPurpose(ctx, parameters.Name, observed, purposeCh)

	if err := <-pseCh; xsql.IsNoRows(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	if err := <-certCh; err != nil {
		return nil, err
	}

	if err := <-purposeCh; err != nil {
		return nil, err
	}

	return observed, nil
}

func (c Client) Create(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters, providerName string) error {
	createQuery := fmt.Sprintf("CREATE PSE %s", parameters.Name)
	if _, err := c.ExecContext(ctx, createQuery); err != nil {
		return err
	}

	var chs []chan error

	if providerName != "" {
		ch := make(chan error, 1)
		chs = append(chs, ch)
		go c.setPSEPurpose(ctx, parameters.Name, providerName, ch)
	}

	ch := make(chan error, 1)
	chs = append(chs, ch)
	go c.updateCertificatesForPSE(ctx, true, parameters.Name, parameters.CertificateRefs, ch)

	for _, ch := range chs {
		if err := <-ch; err != nil {
			return err
		}
	}

	return nil
}

func (c Client) Update(ctx context.Context, pseName string, toAdd, toRemove []v1alpha1.CertificateRef, providerName string) error {

	var chs []chan error

	if providerName != "" {
		ch := make(chan error, 1)
		chs = append(chs, ch)
		go c.setPSEPurpose(ctx, pseName, providerName, ch)
	}

	chAdd := make(chan error, 1)
	chs = append(chs, chAdd)
	go c.updateCertificatesForPSE(ctx, true, pseName, toAdd, chAdd)

	chRemove := make(chan error, 1)
	chs = append(chs, chRemove)
	go c.updateCertificatesForPSE(ctx, false, pseName, toRemove, chRemove)

	for _, ch := range chs {
		if err := <-ch; err != nil {
			return err
		}
	}

	return nil
}

func (c Client) Delete(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) error {
	query := fmt.Sprintf("DROP PSE %s", parameters.Name)

	if _, err := c.ExecContext(ctx, query); err != nil {
		return err
	}

	return nil
}

func (c Client) setPSEPurpose(ctx context.Context, identifier string, providerName string, ch chan error) {
	if providerName == "" {
		ch <- errors.New("provider name is empty")
		return
	}

	setPurposeQuery := fmt.Sprintf(
		"SET PSE %s PURPOSE X509 FOR PROVIDER %s",
		identifier,
		providerName,
	)
	_, err := c.ExecContext(ctx, setPurposeQuery)
	ch <- err
}

func (c Client) updateCertificatesForPSE(ctx context.Context, add bool, pseName string, certRefs []v1alpha1.CertificateRef, ch chan error) {
	var query string

	if add {
		query = "ALTER PSE %s ADD CERTIFICATE %s"
	} else {
		query = "ALTER PSE %s DROP CERTIFICATE %s"
	}

	var certNames, certIDs []string
	for _, certRef := range certRefs {
		switch {
		case certRef.ID != nil:
			certIDs = append(certIDs, strconv.Itoa(*certRef.ID))
		case certRef.Name != nil:
			certNames = append(certNames, *certRef.Name)
		default:
			ch <- errors.New("failed to add certificate: certificate reference must have either id or name set")
			return
		}
	}

	var queries []string
	if len(certIDs) > 0 {
		queries = append(queries, fmt.Sprintf(query, pseName, strings.Join(certIDs, ", ")))
	}
	if len(certNames) > 0 {
		queries = append(queries, fmt.Sprintf(query, pseName, `"`+strings.Join(certNames, `", "`)+`"`))
	}

	for _, q := range queries {
		if _, err := c.ExecContext(ctx, q); err != nil {
			ch <- fmt.Errorf("failed to update certificates: %w", err)
			return
		}
	}

	ch <- nil
}

func (c Client) selectPSE(ctx context.Context, identifier string, observed *v1alpha1.PersonalSecurityEnvironmentObservation, ch chan error) {
	selectQuery := "SELECT NAME FROM PSES WHERE NAME = ? AND PURPOSE = 'X509'"

	if err := c.QueryRowContext(ctx, selectQuery, identifier).Scan(&observed.Name); err != nil {
		ch <- fmt.Errorf(errQueryRow, err)
		return
	}
	ch <- nil
}

func (c Client) selectPSECertificates(ctx context.Context, identifier string, observed *v1alpha1.PersonalSecurityEnvironmentObservation, ch chan error) {
	selectCertQuery := "SELECT CERTIFICATE_ID, CERTIFICATE_NAME FROM PSE_CERTIFICATES WHERE PSE_NAME = ?"
	rows, err := c.QueryContext(ctx, selectCertQuery, identifier)
	if err != nil {
		ch <- err
		return
	}
	defer rows.Close() //nolint:errcheck

	for rows.Next() {
		var certID int
		var certName string
		if err := rows.Scan(&certID, &certName); err != nil {
			ch <- err
			return
		}
		observed.CertificateRefs = append(observed.CertificateRefs, v1alpha1.CertificateRef{
			Name: &certName,
			ID:   &certID,
		})
	}

	if err := rows.Err(); err != nil {
		ch <- err
		return
	}

	ch <- nil
}

func (c Client) selectPSEPurpose(ctx context.Context, identifier string, observed *v1alpha1.PersonalSecurityEnvironmentObservation, ch chan error) {
	psePurposeQuery := "SELECT PURPOSE_OBJECT FROM PSE_PURPOSE_OBJECTS WHERE PSE_NAME = ? AND PURPOSE = 'X509'"
	if err := c.QueryRowContext(ctx, psePurposeQuery, identifier).Scan(&observed.X509ProviderName); xsql.IsNoRows(err) {
		// No provider set
		observed.X509ProviderName = ""
		ch <- nil
		return
	} else if err != nil {
		ch <- fmt.Errorf(errQueryRow, err)
		return
	}
	ch <- nil
}
