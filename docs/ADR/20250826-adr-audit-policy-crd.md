# Audit Policy CRD for SAP HANA Crossplane Provider

- Status: approved
- Deciders: Abdul Wahab, Daniel Lou, Denes Csizmadia
- Date: 2025-08-26
- Tags: Audit Policy, hana, crossplane-provider

Technical Story:
Immediately after provisioning a SAP HANA Cloud instance, teams need to establish essential governance controls, including audit policies that capture security-relevant events (for example, authentication, user and privilege changes etc).

## Context and Problem Statement

Today audit policies are created manually or via ad hoc SQL; we want a declarative way to define and enforce these policies through HANA Crossplane provider.

## Goals

Provide a new Kubernetes CRD (AuditPolicy) to declaratively create, update, enable, and delete SAP HANA Cloud audit policies.

Support the initial, most important parameters:

- policyName (string, required)
- auditActions (list[string], required) e.g., GRANT ANY, REVOKE ANY, CREATE USER
- auditStatus (string, optional): ALL | SUCCESSFUL | UNSUCCESSFUL
- auditLevel (string, optional): INFO | WARNING | ALERT | CRITICAL | EMERGENCY
- auditTrailRetention (int, optional)
- enabled (bool, optional)

Make the controller idempotent, drift-aware, and safe to run continuously.
Allow incremental extension in future to support more policy parameters (for example, target object, user, application, connection, etc.).

## Decision Drivers

Declarative way that reconciles its desired state into SAP HANA Cloud via SQL. The controller will:

- Observe current state from SAP HANA system views.
- Create or update the audit policy to match spec.
- Enable or disable the policy as requested (default enabled).
- Set retention days for the corresponding policy.

## Decision Outcome

Use existing hana crossplane provider and extend it with a new CRD

### Implementation Details

#### New Crossplane Resource Type

New Crossplane resource types have to be added to manage audit policies.

##### Audit Policy Resource Type

```go
type AuditPolicyParameters struct {
	PolicyName string `json:"policyName"`
	AuditActions []string `json:"auditActions"`
	AuditStatus string `json:"auditStatus,omitempty"`
	AuditLevel string `json:"auditLevel,omitempty"`
	AuditTrailRetention *int `json:"auditTrailRetention,omitempty"`
	Enabled *bool `json:"enabled,omitempty"`
}
```

##### Default Values

PolicyName and AuditActions are required fields and must not be empty. Rest of the fields are optional. The default values for rest of the fields will be:

- AuditStatus: ALL
- AuditLevel: CRITICAL
- AuditTrailRetention: 7
- Enabled: False

#### Example Resource Configuration

Corresponding resource yaml file should look like:

```yaml
apiVersion: admin.hana.sap.crossplane.io/v1alpha1
kind: AuditPolicy
metadata:
  name: example-auditpolicy
spec:
  forProvider:
    policyName: sap_authorizations
    auditStatus: UNSUCCESSFUL
    auditActions:
      - GRANT ANY
      - REVOKE ANY
    auditLevel: INFO
    auditTrailRetention: 30
    enabled: true
  providerConfigRef:
    name: hana-provider-config
```

#### SQL Command Mapping

The new Crossplane resources will execute the following SQL commands on HANA:

##### Create Audit Policy

```sql
CREATE AUDIT POLICY "sap_authorizations" AUDITING SUCCESSFUL GRANT ANY, REVOKE ANY LEVEL INFO TRAIL TYPE TABLE RETENTION 30;
```

##### Enable/Disable Audit Policy

```sql
ALTER AUDIT POLICY "sap_authorizations" ENABLE;
ALTER AUDIT POLICY "sap_authorizations" DISABLE;
```

##### Get Audit Policy

```sql
SELECT AUDIT_POLICY_NAME, EVENT_STATUS, EVENT_ACTION, EVENT_LEVEL, RETENTION_PERIOD, IS_AUDIT_POLICY_ACTIVE FROM AUDIT_POLICIES WHERE AUDIT_POLICY_NAME = ?
```

The parameter (IS_AUDIT_POLICY_ACTIVE) tells if the policy is active (enabled) or not.

The audit policy is stored in a row-per-action shape. When a policy is created via SQL with multiple auditActions in a comma-separated list, the system materializes one row per action.
All non-action attributes of the policy (for example auditLevel(EVENT_LEVEL), auditTrailRetention(RETENTION_PERIOD), enabled(IS_AUDIT_POLICY_ACTIVE) and auditStatus(EVENT_STATUS)) are typically identical across those rows.
To reconstruct or compare the policy as a single logical entity, concatenate the EVENT_ACTION values from all rows of the policy into an array, and take the remaining attributes from any row (they should all match).

##### Delete Audit Policy

```sql
DROP AUDIT POLICY "sap_authorizations";
```

##### Update Audit Policy

As per [documentation](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/alter-audit-policy-statement-access-control?locale=en-US&q=+alter+audit), the only parameters that can be altered with HANA audit policy are retention days and enable/disable.

```sql
-- alter policy to set retention days to 30
ALTER AUDIT POLICY "sap_authorizations" SET RETENTION 30;

-- alter policy disable
ALTER AUDIT POLICY "sap_authorizations" DISABLE;
```

If any other parameters needs to be updated (other than 2 above), for example auditLevel or auditActions, then **`the policy has to be deleted/dropped first and recreate again`** with the new parameters.

## References

### SAP HANA Documentation

- [SAP HANA Audit Policy](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-security-guide/audit-policies?locale=en-US)
- [SAP HANA Audit Policy Best Practices](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-security-guide/best-practices-and-recommendations-for-creating-audit-policies?locale=en-US)
- [SAP HANA Audit Policy Create Parameters](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/create-audit-policy-statement-access-control?locale=en-US)
- [ALTER HANA Audit Policy](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/alter-audit-policy-statement-access-control?locale=en-US&q=+alter+audit)

### GitHub Issue

- [GitHub Issue/Feature](https://github.com/SAP/crossplane-provider-hana/issues/127)
