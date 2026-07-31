# Certificate CRD for SAP HANA Crossplane Provider

- Status: approved
- Date: 2026-07-31
- Tags: Certificate, hana, crossplane-provider, TLS, PKI

Technical Story:
SAP HANA Cloud supports TLS-based trust through certificates stored in the database. Teams provisioning HANA instances need a declarative way to import CA or server certificates into HANA as part of their infrastructure-as-code workflows — for example, to enable SSL/TLS verification or to support X.509 authentication.

## Context and Problem Statement

Today, certificates must be imported into HANA manually or via ad hoc SQL scripts. This creates a gap between the declarative Crossplane lifecycle (provision, configure, decommission) and the manual steps needed to establish certificate trust. We want a Crossplane-native way to manage certificates so that the full HANA setup can be expressed declaratively.

## Goals

Provide a new Kubernetes CRD (`Certificate`) to declaratively import PEM-encoded certificates into SAP HANA Cloud.

- Read the PEM certificate from a Kubernetes Secret.
- Support certificate chains (multiple certificates in a single PEM file).
- Name each imported certificate predictably, using a user-supplied base name plus a numeric suffix.
- Expose the IDs and names of all imported certificates in the resource status.
- Integrate with the standard Crossplane managed-resource lifecycle (Observe / Create / Delete).

## Decision Drivers

- Certificates are a prerequisite for secure HANA connectivity and should be provisioned alongside the instance in the same declarative pipeline.
- Storing the PEM content in a Kubernetes Secret keeps credentials out of the CRD spec and allows standard Kubernetes RBAC and secret-rotation workflows to apply.
- HANA's `CREATE CERTIFICATE` SQL statement operates on one certificate at a time, so chain support must be handled by the provider by splitting the PEM chain before issuing SQL.

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
    // Name is the base name used to identify the certificate(s) in HANA.
    // Each certificate in a chain is stored as "<name>-1", "<name>-2", etc.
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

If the Secret key contains a PEM chain (multiple `-----BEGIN CERTIFICATE-----` blocks), the provider splits it into individual certificates before importing. Each certificate is stored in HANA under a separate name: `<name>-1`, `<name>-2`, etc.

Validation rules for the PEM data:

- Every PEM block must be of type `CERTIFICATE`; any other block type (e.g., `PRIVATE KEY`) causes the reconcile to fail.
- At least one certificate must be present; an empty or whitespace-only value causes the reconcile to fail.

#### SQL Command Mapping

##### Observe

```sql
SELECT CERTIFICATE_ID, CERTIFICATE_NAME
FROM CERTIFICATES
WHERE CERTIFICATE_NAME LIKE '<name>-%'
ORDER BY CERTIFICATE_NAME
```

The `LIKE '<name>-%'` pattern matches all certificates belonging to this managed resource. If the query returns no rows the resource is considered absent and Crossplane will trigger a Create.

##### Create

One statement is executed per certificate in the chain:

```sql
CREATE CERTIFICATE <name>-1 FROM '<PEM block>';
CREATE CERTIFICATE <name>-2 FROM '<PEM block>';
-- ... one per certificate in the chain
```

##### Delete

Currently a no-op (orphan semantics). A future iteration will issue:

```sql
DROP CERTIFICATE <name>-1;
DROP CERTIFICATE <name>-2;
-- ... one per imported certificate
```

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
      - id: 42
        name: my-ca-1
      - id: 43
        name: my-ca-2
```

## References

### SAP HANA Documentation

- [SAP HANA Certificate Store](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-security-guide/certificate-management?locale=en-US)
- [CREATE CERTIFICATE Statement](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/create-certificate-statement-system-management?locale=en-US)
- [DROP CERTIFICATE Statement](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/drop-certificate-statement-system-management?locale=en-US)
- [CERTIFICATES System View](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/certificates-system-view-system-management?locale=en-US)
