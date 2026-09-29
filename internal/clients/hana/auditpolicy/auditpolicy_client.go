package auditpolicy

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
	"github.com/SAP/crossplane-provider-hana/internal/utils"
)

type AuditPolicyClient interface {
	hana.QueryClient[v1alpha1.AuditPolicyParameters, v1alpha1.AuditPolicyObservation]
	RecreatePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	UpdateRetentionDays(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
	UpdateEnablePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error
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

// Read checks the state of the audit policy
func (c Client) Read(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) (*v1alpha1.AuditPolicyObservation, error) {

	observed := &v1alpha1.AuditPolicyObservation{}

	query := getSelectSql()
	policyActionRows, err := c.QueryContext(ctx, query, parameters.PolicyName)
	if err != nil {
		return nil, err
	}
	defer policyActionRows.Close() //nolint:errcheck

	// Principals and actions can appear across multiple rows (HANA returns one
	// row per principal, repeating the action), so de-duplicate both while
	// preserving first-seen order.
	seenActions := make(map[string]struct{})
	seenPrincipals := make(map[v1alpha1.AuditPrincipal]struct{})

	for policyActionRows.Next() {
		if err = scanPolicyRow(policyActionRows, observed, seenActions, seenPrincipals); err != nil {
			return nil, err
		}
	}

	if err = policyActionRows.Err(); err != nil {
		return nil, err
	}

	return observed, nil
}

// scanPolicyRow scans a single row from the AUDIT_POLICIES query and merges it
// into observed, de-duplicating actions and principals via the provided sets.
func scanPolicyRow(rows *sql.Rows, observed *v1alpha1.AuditPolicyObservation, seenActions map[string]struct{}, seenPrincipals map[v1alpha1.AuditPrincipal]struct{}) error {
	var policyName string
	var eventStatus string
	var eventAction sql.NullString
	var eventLevel string
	var retentionPeriod sql.NullInt64
	var isActive string
	var principalName sql.NullString
	var exceptPrincipalName sql.NullString
	var principalType sql.NullString
	if err := rows.Scan(&policyName, &eventStatus, &eventAction, &eventLevel, &retentionPeriod, &isActive, &principalName, &exceptPrincipalName, &principalType); err != nil {
		return err
	}

	observed.PolicyName = policyName
	observed.AuditStatus = strings.TrimSuffix(eventStatus, " EVENTS")
	if eventAction.Valid {
		if _, ok := seenActions[eventAction.String]; !ok {
			seenActions[eventAction.String] = struct{}{}
			observed.AuditActions = append(observed.AuditActions, eventAction.String)
		}
	}
	observed.AuditLevel = eventLevel
	if retentionPeriod.Valid {
		rp := int(retentionPeriod.Int64)
		observed.AuditTrailRetention = &rp
	}
	enabled := isActive == "TRUE"
	observed.Enabled = &enabled

	mergePrincipal(observed, seenPrincipals, principalName, exceptPrincipalName, principalType)
	return nil
}

// mergePrincipal parses the principal clause of a single row and appends the
// resolved principal to observed (de-duplicated via seenPrincipals).
//
// HANA's AUDIT_POLICIES view populates PRINCIPAL_NAME for a plain
// "FOR PRINCIPALS ..." clause and EXCEPT_PRINCIPAL_NAME for an
// "EXCEPT FOR PRINCIPALS ..." clause. PRINCIPAL_NAME/EXCEPT_PRINCIPAL_NAME cover
// both USER and USERGROUP principals, so we rely on them (and PRINCIPAL_TYPE)
// rather than the legacy USER_NAME/EXCEPT_USERNAME columns.
func mergePrincipal(observed *v1alpha1.AuditPolicyObservation, seenPrincipals map[v1alpha1.AuditPrincipal]struct{}, principalName, exceptPrincipalName, principalType sql.NullString) {
	var resolvedName string
	switch {
	case principalName.Valid && principalName.String != "":
		// "FOR PRINCIPALS ..." -> ExceptPrincipals stays false.
		resolvedName = principalName.String
	case exceptPrincipalName.Valid && exceptPrincipalName.String != "":
		// "EXCEPT FOR PRINCIPALS ..."
		observed.ExceptPrincipals = true
		resolvedName = exceptPrincipalName.String
	default:
		return
	}

	principal := v1alpha1.AuditPrincipal{Type: principalType.String, Name: resolvedName}
	if _, ok := seenPrincipals[principal]; !ok {
		seenPrincipals[principal] = struct{}{}
		observed.AuditPrincipals = append(observed.AuditPrincipals, principal)
	}
}

// Create a new audit policy
func (c Client) Create(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	query := prepareCreateSql(parameters)

	_, err := c.ExecContext(ctx, query)

	if err != nil {
		return err
	}

	if parameters.Enabled != nil && *parameters.Enabled {
		enableQuery := prepareEnableDisablePolicySql(parameters)
		_, err = c.ExecContext(ctx, enableQuery)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c Client) RecreatePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {
	// Drop and recreate the policy to change actions/status/level
	err := c.Delete(ctx, parameters)
	if err != nil {
		return err
	}
	err = c.Create(ctx, parameters)
	if err != nil {
		return err
	}

	return nil
}

func (c Client) UpdateRetentionDays(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	query := prepareUpdateRetentionDaysSql(parameters)
	_, err := c.ExecContext(ctx, query)
	if err != nil {
		return err
	}

	return nil
}

func (c Client) UpdateEnablePolicy(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	query := prepareEnableDisablePolicySql(parameters)
	_, err := c.ExecContext(ctx, query)
	if err != nil {
		return err
	}

	return nil
}

// Delete an existing audit policy
func (c Client) Delete(ctx context.Context, parameters *v1alpha1.AuditPolicyParameters) error {

	query := prepareDeleteSql(parameters)

	_, err := c.ExecContext(ctx, query)

	if err != nil {
		return err
	}

	return nil
}

func prepareCreateSql(parameters *v1alpha1.AuditPolicyParameters) string {
	query := fmt.Sprintf("CREATE AUDIT POLICY %s AUDITING %s", parameters.PolicyName, parameters.AuditStatus)

	if len(parameters.AuditActions) > 0 {
		for _, action := range parameters.AuditActions {
			query += fmt.Sprintf(" %s,", action)
		}
	}
	query = strings.TrimSuffix(query, ",")

	query += preparePrincipalsClause(parameters)

	query += fmt.Sprintf(" LEVEL %s TRAIL TYPE TABLE RETENTION %d", parameters.AuditLevel, *parameters.AuditTrailRetention)

	return query
}

// preparePrincipalsClause builds the optional principal clause of a
// CREATE AUDIT POLICY statement. It renders an ordered, mixed list of users and
// user groups as "FOR PRINCIPALS USER <name>, USERGROUP <name>, ...". When
// ExceptPrincipals is set, the clause is rendered as "EXCEPT FOR PRINCIPALS ..."
// instead. An empty string is returned when no principals are configured.
func preparePrincipalsClause(parameters *v1alpha1.AuditPolicyParameters) string {
	if len(parameters.AuditPrincipals) == 0 {
		return ""
	}

	principals := make([]string, 0, len(parameters.AuditPrincipals))
	for _, principal := range parameters.AuditPrincipals {
		// HANA's CREATE AUDIT POLICY principal list expects unquoted
		// identifiers (e.g. "USER USER1, USERGROUP TECHNICAL_USER_GROUP").
		// Double-quoting the name triggers a "SQL syntax error near \"" (257).
		// Principal names are validated by the CRD (no quotes, spaces or other
		// special characters) and are upper-cased upstream, so it is safe to
		// render them unquoted. The type is a validated enum (USER/USERGROUP).
		principals = append(principals, fmt.Sprintf("%s %s", principal.Type, principal.Name))
	}

	clause := "FOR PRINCIPALS"
	if parameters.ExceptPrincipals {
		clause = "EXCEPT FOR PRINCIPALS"
	}

	return fmt.Sprintf(" %s %s", clause, strings.Join(principals, ", "))
}

func getSelectSql() string {
	return "SELECT AUDIT_POLICY_NAME, EVENT_STATUS, EVENT_ACTION, EVENT_LEVEL, RETENTION_PERIOD, IS_AUDIT_POLICY_ACTIVE, PRINCIPAL_NAME, EXCEPT_PRINCIPAL_NAME, PRINCIPAL_TYPE FROM AUDIT_POLICIES WHERE AUDIT_POLICY_NAME = ?"
}

func prepareEnableDisablePolicySql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf(`ALTER AUDIT POLICY "%s" %s`, utils.EscapeDoubleQuotes(parameters.PolicyName), map[bool]string{true: "ENABLE", false: "DISABLE"}[*parameters.Enabled])
}

func prepareUpdateRetentionDaysSql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf(`ALTER AUDIT POLICY "%s" SET RETENTION %d`, utils.EscapeDoubleQuotes(parameters.PolicyName), *parameters.AuditTrailRetention)
}

func prepareDeleteSql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf(`DROP AUDIT POLICY "%s"`, utils.EscapeDoubleQuotes(parameters.PolicyName))
}
