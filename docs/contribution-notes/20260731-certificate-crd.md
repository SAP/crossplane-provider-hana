# Certificate CRD for SAP HANA Crossplane Provider

- Status: approved
- Date: 2026-07-31
- Tags: Certificate, hana, crossplane-provider, TLS, PKI

Technical Story:
SAP HANA Cloud supports TLS-based trust through certificates stored in the database. Teams provisioning HANA instances need a declarative way to import CA or server certificates into HANA as part of their infrastructure-as-code workflows — for example, to enable SSL/TLS verification or to support X.509 authentication.

## Context and Problem Statement

Today, certificates must be imported into HANA manually or via the imperative `mcp-create-hana-certificates` script. This creates a gap between the declarative Crossplane lifecycle (provision, configure, decommission) and the manual steps needed to establish certificate trust. We want a Crossplane-native way to manage certificates so that the full HANA setup can be expressed declaratively.

## Goals

Provide a new Kubernetes CRD (`Certificate`) to declaratively import PEM-encoded certificates into SAP HANA Cloud.

- Read the PEM certificate from a Kubernetes Secret.
- Support certificate chains (multiple certificates in a single PEM file).
- Name each imported certificate deterministically, derived from the x509 certificate's serial number and `NotBefore` timestamp, following the existing HANA naming convention.
- Import all certificates in a chain atomically within a single database transaction.
- Expose the IDs and names of all imported certificates in the resource status.
- Integrate with the standard Crossplane managed-resource lifecycle (Observe / Create / Delete).

## Decision Drivers

- Certificates are a prerequisite for secure HANA connectivity and should be provisioned alongside the instance in the same declarative pipeline.
- Storing the PEM content in a Kubernetes Secret keeps credentials out of the CRD spec and allows standard Kubernetes RBAC and secret-rotation workflows to apply.
- HANA's `CREATE CERTIFICATE` SQL statement operates on one certificate at a time, so chain support must be handled by the provider by splitting the PEM chain before issuing SQL.
- Certificate names must be deterministic and unique — derived from the certificate content itself — to match the convention already established by the existing tooling and to be referenceable in `PersonalSecurityEnvironment` `certificateRefs` without manual bookkeeping.
- All certificates in a chain must be imported atomically so partial failures cannot leave HANA in an inconsistent state.

## Decision Outcome

Extend the existing `crossplane-provider-hana` with a new `Certificate` managed resource in the `admin.hana.sap.crossplane.io` API group.

### CRD Details

| Field | Value |
|---|---|
| API Group | `admin.hana.sap.crossplane.io` |
| Kind | `Certificate` |
| Short name | `cert` |
| Scope | Cluster |
| Version | `v1alpha1` |
| Categories | `crossplane`, `managed`, `hana` |

### Implementation Details

#### New Crossplane Resource Type

```go
type CertificateParameters struct {
    // Name is the base name used to derive the HANA certificate name.
    // The final HANA name follows the pattern:
    //   <SANITIZED_BASE>_CRT_SRV_CERTIFICATE_<SERIAL>_<DDMMYYYYHHMMSS>
    Name string `json:"name"`

    // CertificateSecretRef references the Kubernetes Secret containing the
    // PEM-encoded certificate or certificate chain.
    CertificateSecretRef *xpv1.SecretKeySelector `json:"certificateSecretRef"`
}

type CertificateObservation struct {
    // Certificates lists the certificates that have been imported into HANA,
    // each with its HANA-assigned ID and name.
    Certificates []ImportedCertificate `json:"certificates,omitempty"`
}

type ImportedCertificate struct {
    ID   *int   `json:"id,omitempty"`
    Name string `json:"name,omitempty"`
}
```

#### Certificate Naming Convention

Each certificate is named by combining the user-supplied base name with metadata extracted from the x509 certificate itself:

```
<SANITIZED_BASE>_CRT_SRV_CERTIFICATE_<SERIAL>_<DDMMYYYYHHMMSS>
```

- `SANITIZED_BASE` — the `name` field uppercased with non-alphanumeric characters replaced by `_`
- `SERIAL` — the certificate's serial number (decimal)
- `DDMMYYYYHHMMSS` — the certificate's `NotBefore` timestamp in UTC

For example, a base name of `aws-cf-del101` produces:
```
AWS_CF_DEL101_CRT_SRV_CERTIFICATE_334204436771835260918629968473131177511_16072026074032
```

This matches the convention already used by the imperative `mcp-create-hana-certificates` tooling, making the two approaches interoperable.

#### Example Resource Configuration

```yaml
apiVersion: admin.hana.sap.crossplane.io/v1alpha1
kind: Certificate
metadata:
  name: my-ca-cert
spec:
  forProvider:
    name: my-ca
    certificateSecretRef:
      namespace: crossplane-system
      name: my-ca-cert-secret
      key: ca.crt
  providerConfigRef:
    name: hana-provider-config
```

The Secret referenced by `certificateSecretRef` must contain a PEM-encoded certificate or certificate chain at the specified key:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-ca-cert-secret
  namespace: crossplane-system
type: Opaque
data:
  ca.crt: <base64-encoded PEM>
```

#### PEM Chain Handling

If the Secret key contains a PEM chain (multiple `-----BEGIN CERTIFICATE-----` blocks), the provider splits it into individual certificates before importing. Each certificate gets its own HANA name derived from its own serial and `NotBefore` values.

Validation rules for the PEM data:

- Every PEM block must be of type `CERTIFICATE`; any other block type (e.g., `PRIVATE KEY`) causes the reconcile to fail.
- At least one certificate must be present; an empty or whitespace-only value causes the reconcile to fail.

#### SQL Command Mapping

##### Observe

```sql
SELECT CERTIFICATE_ID, CERTIFICATE_NAME
FROM CERTIFICATES
WHERE CERTIFICATE_NAME LIKE '<SANITIZED_BASE>_CRT_SRV_CERTIFICATE_%'
ORDER BY CERTIFICATE_NAME
```

The LIKE pattern matches all certificates belonging to this managed resource by their sanitized base name prefix. If the query returns no rows the resource is considered absent and Crossplane will trigger a Create.

##### Create

All statements are executed within a single database transaction. If any statement fails the entire transaction is rolled back, preventing partial imports:

```sql
BEGIN;
CREATE CERTIFICATE "<BASE>_CRT_SRV_CERTIFICATE_<SERIAL1>_<TIMESTAMP1>" FROM '<PEM block>';
CREATE CERTIFICATE "<BASE>_CRT_SRV_CERTIFICATE_<SERIAL2>_<TIMESTAMP2>" FROM '<PEM block>';
-- ... one per certificate in the chain
COMMIT;
```

Both the certificate name (double-quoted identifier) and the PEM content (single-quoted string literal) are sanitized before interpolation to prevent SQL injection.

##### Delete

The provider reads the current certificate names from HANA first (since derived names are not stored in spec), then drops each one inside a single transaction:

```sql
BEGIN;
DROP CERTIFICATE "<BASE>_CRT_SRV_CERTIFICATE_<SERIAL1>_<TIMESTAMP1>";
DROP CERTIFICATE "<BASE>_CRT_SRV_CERTIFICATE_<SERIAL2>_<TIMESTAMP2>";
-- ... one per imported certificate
COMMIT;
```

If the certificate is still referenced by a `PersonalSecurityEnvironment`, HANA will reject the `DROP` statement, the transaction will be rolled back, and the reconcile will fail with an error. The PSE `certificateRefs` must be updated to remove the reference before deleting the `Certificate` CR.

##### Update

Not yet implemented. Because HANA does not support `ALTER CERTIFICATE`, updates will require a drop-and-recreate cycle in a future iteration.

#### Status

After a successful reconcile the resource status reflects the imported certificates:

```yaml
status:
  conditions:
    - type: Ready
      status: "True"
    - type: Synced
      status: "True"
  atProvider:
    certificates:
      - id: 171175
        name: MY_CA_CRT_SRV_CERTIFICATE_334204436771835260918629968473131177511_16072026074032
      - id: 171176
        name: MY_CA_CRT_SRV_CERTIFICATE_334204436771835260918629968473131177512_16072026074032
      - id: 171177
        name: MY_CA_CRT_SRV_CERTIFICATE_334204436771835260918629968473131177513_16072026074032
```

#### SQL Injection Prevention

`CREATE CERTIFICATE` is a DDL statement — HANA does not support bind parameters (`?`) for it. Both interpolated values are sanitized before use:

- **Certificate name** (identifier) — double-quoted and escaped via `EscapeDoubleQuotes` (any `"` inside the name becomes `""`)
- **PEM content** (string literal) — single-quoted and escaped via `EscapeSingleQuotes` (any `'` inside the PEM becomes `''`)

## References

### SAP HANA Documentation

- [SAP HANA Certificate Store](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-security-guide/certificate-management?locale=en-US)
- [CREATE CERTIFICATE Statement](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/create-certificate-statement-system-management?locale=en-US)
- [DROP CERTIFICATE Statement](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/drop-certificate-statement-system-management?locale=en-US)
- [CERTIFICATES System View](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/certificates-system-view-system-management?locale=en-US)
