package x509provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
)

// X509ProviderClient defines the interface for X509 provider client operations
type X509ProviderClient interface {
	hana.QueryClient[v1alpha1.X509ProviderParameters, v1alpha1.X509ProviderObservation]
	Update(ctx context.Context, parameters *v1alpha1.X509ProviderParameters, observation *v1alpha1.X509ProviderObservation) error
}

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

func (c Client) Read(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) (*v1alpha1.X509ProviderObservation, error) {
	observation := &v1alpha1.X509ProviderObservation{}

	issuerCh := make(chan error, 1)
	go c.readIssuer(ctx, parameters.Name, observation, issuerCh)

	matchingRulesCh := make(chan error, 1)
	go c.readMatchingRules(ctx, parameters.Name, observation, matchingRulesCh)

	if err := <-issuerCh; err != nil {
		return nil, err
	} else if observation.Name == nil || *observation.Name == "" {
		return nil, nil
	}

	observation.Name = &parameters.Name

	if err := <-matchingRulesCh; err != nil {
		return nil, err
	}

	return observation, nil
}

func (c Client) Create(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
	query := fmt.Sprintf(
		"CREATE X509 PROVIDER %s WITH ISSUER '%s'",
		parameters.Name,
		parameters.Issuer,
	)

	_, err := c.ExecContext(ctx, query)

	return err
}

func (c Client) Update(ctx context.Context, parameters *v1alpha1.X509ProviderParameters, observation *v1alpha1.X509ProviderObservation) error {
	issuerCh := make(chan error, 1)
	matchingRulesCh := make(chan error, 1)

	if parameters.Issuer != *observation.Issuer {
		go c.updateIssuer(ctx, parameters.Name, parameters.Issuer, issuerCh)
	} else {
		issuerCh <- nil
	}

	if !slices.Equal(parameters.MatchingRules, observation.MatchingRules) {
		go c.updateMatchingRules(ctx, parameters.Name, parameters.MatchingRules, matchingRulesCh)
	} else {
		matchingRulesCh <- nil
	}

	if err := <-issuerCh; err != nil {
		return err
	}
	observation.Issuer = &parameters.Issuer

	if err := <-matchingRulesCh; err != nil {
		return err
	}
	observation.MatchingRules = parameters.MatchingRules

	return nil
}

func (c Client) Delete(ctx context.Context, parameters *v1alpha1.X509ProviderParameters) error {
	query := fmt.Sprintf("DROP X509 PROVIDER %s", parameters.Name)
	_, err := c.ExecContext(ctx, query)
	return err
}

func (c Client) readIssuer(ctx context.Context, name string, observation *v1alpha1.X509ProviderObservation, ch chan error) {
	query := "SELECT ISSUER_NAME FROM X509_PROVIDERS WHERE X509_PROVIDER_NAME = ?"
	var issuer string
	if err := c.QueryRowContext(ctx, query, name).Scan(&issuer); xsql.IsNoRows(err) {
		ch <- nil
		return
	} else if err != nil {
		ch <- err
		return
	}

	observation.Name = &name
	observation.Issuer = &issuer
	ch <- nil
}

func (c Client) readMatchingRules(ctx context.Context, name string, observation *v1alpha1.X509ProviderObservation, ch chan error) {
	query := "SELECT MATCHING_RULE FROM X509_PROVIDER_RULES WHERE X509_PROVIDER_NAME = ? ORDER BY POSITION ASC"
	rows, err := c.QueryContext(ctx, query, name)
	if err != nil {
		ch <- err
		return
	}
	defer rows.Close() //nolint:errcheck

	var matchingRules []string
	for rows.Next() {
		var rule string
		if err := rows.Scan(&rule); err != nil {
			ch <- err
			return
		}
		matchingRules = append(matchingRules, rule)
	}
	if rows.Err() != nil {
		ch <- rows.Err()
		return
	}
	observation.MatchingRules = matchingRules
	ch <- nil
}

func (c Client) updateIssuer(ctx context.Context, name, issuer string, ch chan error) {
	query := fmt.Sprintf(
		"ALTER X509 PROVIDER %s SET ISSUER '%s'",
		name,
		issuer,
	)
	_, err := c.ExecContext(ctx, query)
	ch <- err
}

func (c Client) updateMatchingRules(ctx context.Context, name string, rules []string, ch chan error) {
	var query string
	if len(rules) == 0 {
		query = fmt.Sprintf("ALTER X509 PROVIDER %s UNSET MATCHING RULES", name)
	} else {
		ruleString := strings.Join(rules, "', '")
		query = fmt.Sprintf("ALTER X509 PROVIDER %s SET MATCHING RULES '%s'", name, ruleString)
	}

	_, err := c.ExecContext(ctx, query)
	ch <- err
}
