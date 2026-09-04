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
	Update(ctx context.Context, pseName string, purpose v1alpha1.PSEPurpose, certsToAdd, certsToRemove []v1alpha1.CertificateRef, keysToAdd, keysToRemove []string, providerName string) error
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

// effectivePurpose returns the purpose to use, defaulting to X509 when unset
// to preserve backward compatibility for existing manifests.
func effectivePurpose(p v1alpha1.PSEPurpose) v1alpha1.PSEPurpose {
	if p == "" {
		return v1alpha1.PSEPurposeX509
	}
	return p
}

func (c Client) Read(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters) (*v1alpha1.PersonalSecurityEnvironmentObservation, error) {
	observed := &v1alpha1.PersonalSecurityEnvironmentObservation{}
	purpose := effectivePurpose(parameters.Purpose)
	observed.Purpose = purpose

	pseCh := make(chan error, 1)
	go c.selectPSE(ctx, parameters.Name, purpose, observed, pseCh)

	certCh := make(chan error, 1)
	keyCh := make(chan error, 1)
	purposeCh := make(chan error, 1)

	if purpose == v1alpha1.PSEPurposeJWT {
		go c.selectPSEPublicKeys(ctx, parameters.Name, observed, keyCh)
		certCh <- nil
	} else {
		go c.selectPSECertificates(ctx, parameters.Name, observed, certCh)
		keyCh <- nil
	}

	go c.selectPSEPurpose(ctx, parameters.Name, purpose, observed, purposeCh)

	if err := <-pseCh; xsql.IsNoRows(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	if err := <-certCh; err != nil {
		return nil, err
	}
	if err := <-keyCh; err != nil {
		return nil, err
	}
	if err := <-purposeCh; err != nil {
		return nil, err
	}

	return observed, nil
}

func (c Client) Create(ctx context.Context, parameters *v1alpha1.PersonalSecurityEnvironmentParameters, providerName string) error {
	purpose := effectivePurpose(parameters.Purpose)

	createQuery := fmt.Sprintf("CREATE PSE %s", parameters.Name)
	if _, err := c.ExecContext(ctx, createQuery); err != nil {
		return err
	}

	var chs []chan error

	if providerName != "" {
		ch := make(chan error, 1)
		chs = append(chs, ch)
		go c.setPSEPurpose(ctx, parameters.Name, purpose, providerName, ch)
	}

	// purpose has been run through effectivePurpose, so it's X509 or JWT.
	switch purpose {
	case v1alpha1.PSEPurposeJWT:
		ch := make(chan error, 1)
		chs = append(chs, ch)
		go c.updatePublicKeysForPSE(ctx, true, parameters.Name, publicKeyNames(parameters.PublicKeyRefs), ch)
	case v1alpha1.PSEPurposeX509:
		ch := make(chan error, 1)
		chs = append(chs, ch)
		go c.updateCertificatesForPSE(ctx, true, parameters.Name, parameters.CertificateRefs, ch)
	}

	for _, ch := range chs {
		if err := <-ch; err != nil {
			return err
		}
	}

	return nil
}

func (c Client) Update(ctx context.Context, pseName string, purpose v1alpha1.PSEPurpose, certsToAdd, certsToRemove []v1alpha1.CertificateRef, keysToAdd, keysToRemove []string, providerName string) error {
	effective := effectivePurpose(purpose)

	var chs []chan error

	if providerName != "" {
		ch := make(chan error, 1)
		chs = append(chs, ch)
		go c.setPSEPurpose(ctx, pseName, effective, providerName, ch)
	}

	// effective has been run through effectivePurpose, so it's X509 or JWT.
	switch effective {
	case v1alpha1.PSEPurposeJWT:
		chAdd := make(chan error, 1)
		chs = append(chs, chAdd)
		go c.updatePublicKeysForPSE(ctx, true, pseName, keysToAdd, chAdd)

		chRemove := make(chan error, 1)
		chs = append(chs, chRemove)
		go c.updatePublicKeysForPSE(ctx, false, pseName, keysToRemove, chRemove)
	case v1alpha1.PSEPurposeX509:
		chAdd := make(chan error, 1)
		chs = append(chs, chAdd)
		go c.updateCertificatesForPSE(ctx, true, pseName, certsToAdd, chAdd)

		chRemove := make(chan error, 1)
		chs = append(chs, chRemove)
		go c.updateCertificatesForPSE(ctx, false, pseName, certsToRemove, chRemove)
	}

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

func (c Client) setPSEPurpose(ctx context.Context, identifier string, purpose v1alpha1.PSEPurpose, providerName string, ch chan error) {
	if providerName == "" {
		ch <- errors.New("provider name is empty")
		return
	}

	setPurposeQuery := fmt.Sprintf(
		"SET PSE %s PURPOSE %s FOR PROVIDER %s",
		identifier,
		string(effectivePurpose(purpose)),
		providerName,
	)
	_, err := c.ExecContext(ctx, setPurposeQuery)
	ch <- err
}

func (c Client) updateCertificatesForPSE(ctx context.Context, add bool, pseName string, certRefs []v1alpha1.CertificateRef, ch chan error) {
	if len(certRefs) == 0 {
		ch <- nil
		return
	}

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

func (c Client) updatePublicKeysForPSE(ctx context.Context, add bool, pseName string, keyNames []string, ch chan error) {
	if len(keyNames) == 0 {
		ch <- nil
		return
	}

	verb := "ADD"
	if !add {
		verb = "DROP"
	}

	// ALTER PSE accepts a single PUBLIC KEY per statement, so we issue one
	// statement per key. Doing it serially keeps error attribution simple.
	for _, name := range keyNames {
		query := fmt.Sprintf("ALTER PSE %s %s PUBLIC KEY %s", pseName, verb, name)
		if _, err := c.ExecContext(ctx, query); err != nil {
			ch <- fmt.Errorf("failed to %s PUBLIC KEY %s on PSE %s: %w", strings.ToLower(verb), name, pseName, err)
			return
		}
	}

	ch <- nil
}

func (c Client) selectPSE(ctx context.Context, identifier string, purpose v1alpha1.PSEPurpose, observed *v1alpha1.PersonalSecurityEnvironmentObservation, ch chan error) {
	selectQuery := "SELECT NAME FROM PSES WHERE NAME = ? AND PURPOSE = ?"

	if err := c.QueryRowContext(ctx, selectQuery, identifier, string(effectivePurpose(purpose))).Scan(&observed.Name); err != nil {
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

func (c Client) selectPSEPublicKeys(ctx context.Context, identifier string, observed *v1alpha1.PersonalSecurityEnvironmentObservation, ch chan error) {
	const query = "SELECT PUBLIC_KEY_NAME FROM SYS.PSE_PUBLIC_KEYS WHERE PSE_NAME = ?"
	rows, err := c.QueryContext(ctx, query, identifier)
	if err != nil {
		ch <- err
		return
	}
	defer rows.Close() //nolint:errcheck

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			ch <- err
			return
		}
		observed.PublicKeys = append(observed.PublicKeys, name)
	}
	if err := rows.Err(); err != nil {
		ch <- err
		return
	}
	ch <- nil
}

func (c Client) selectPSEPurpose(ctx context.Context, identifier string, purpose v1alpha1.PSEPurpose, observed *v1alpha1.PersonalSecurityEnvironmentObservation, ch chan error) {
	psePurposeQuery := "SELECT PURPOSE_OBJECT FROM PSE_PURPOSE_OBJECTS WHERE PSE_NAME = ? AND PURPOSE = ?"
	purposeStr := string(effectivePurpose(purpose))
	var providerName string
	if err := c.QueryRowContext(ctx, psePurposeQuery, identifier, purposeStr).Scan(&providerName); xsql.IsNoRows(err) {
		// No provider set
		ch <- nil
		return
	} else if err != nil {
		ch <- fmt.Errorf(errQueryRow, err)
		return
	}
	switch effectivePurpose(purpose) {
	case v1alpha1.PSEPurposeJWT:
		observed.JWTProviderName = providerName
	case v1alpha1.PSEPurposeX509:
		observed.X509ProviderName = providerName
	}
	ch <- nil
}

func publicKeyNames(refs []v1alpha1.PublicKeyRef) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		if r.Name != "" {
			out = append(out, r.Name)
		}
	}
	return out
}
