package auditpolicy

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	"github.com/SAP/crossplane-provider-hana/internal/clients/hana"
	"github.com/SAP/crossplane-provider-hana/internal/clients/xsql"
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

	for policyActionRows.Next() {
		var policyName string
		var eventStatus string
		var eventAction sql.NullString
		var eventLevel string
		var retentionPeriod sql.NullInt64
		var isActive string
		err = policyActionRows.Scan(&policyName, &eventStatus, &eventAction, &eventLevel, &retentionPeriod, &isActive)
		if err != nil {
			return nil, err
		}
		observed.PolicyName = policyName
		observed.AuditStatus = strings.TrimSuffix(eventStatus, " EVENTS")
		if eventAction.Valid {
			observed.AuditActions = append(observed.AuditActions, eventAction.String)
		}
		observed.AuditLevel = eventLevel
		if retentionPeriod.Valid {
			rp := int(retentionPeriod.Int64)
			observed.AuditTrailRetention = &rp
		}
		if isActive == "TRUE" {
			observed.Enabled = new(true)
		} else {
			observed.Enabled = new(false)
		}
	}

	if err = policyActionRows.Err(); err != nil {
		return nil, err
	}

	return observed, nil
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
	query += fmt.Sprintf(" LEVEL %s TRAIL TYPE TABLE RETENTION %d", parameters.AuditLevel, *parameters.AuditTrailRetention)

	return query
}

func getSelectSql() string {
	return "SELECT AUDIT_POLICY_NAME, EVENT_STATUS, EVENT_ACTION, EVENT_LEVEL, RETENTION_PERIOD, IS_AUDIT_POLICY_ACTIVE FROM AUDIT_POLICIES WHERE AUDIT_POLICY_NAME = ?"
}

func prepareEnableDisablePolicySql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf("ALTER AUDIT POLICY %s %s", parameters.PolicyName, map[bool]string{true: "ENABLE", false: "DISABLE"}[*parameters.Enabled])
}

func prepareUpdateRetentionDaysSql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf("ALTER AUDIT POLICY %s SET RETENTION %d", parameters.PolicyName, *parameters.AuditTrailRetention)
}

func prepareDeleteSql(parameters *v1alpha1.AuditPolicyParameters) string {
	return fmt.Sprintf("DROP AUDIT POLICY %s", parameters.PolicyName)
}
