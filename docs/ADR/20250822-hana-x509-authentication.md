# X.509 Certificate-Based User Authentication for SAP HANA Crossplane Provider

- Status: implemented
- Date: 2025-08-22

Technical Story:
As a platform operator using the SAP HANA Crossplane provider, I want to be able to configure SAP HANA X.509 providers, PSEs (Personal Security Environments), and certificate management through Crossplane resources to enable certificate-based user authentication on the HANA side.

This will allow users to authenticate to HANA using X.509 certificates and enable certificate rotation without downtime.

## Context and Problem Statement

The current SAP HANA Crossplane provider lacks support for managing HANA-side X.509 configuration resources that are required for certificate-based user authentication.

### Missing HANA-side X.509 Configuration Resources

The provider currently lacks support for managing the following SAP HANA X.509 configuration objects:

- **X509 PROVIDER**: Defines certificate issuers and matching rules for user authentication
- **PSE (Personal Security Environment)**: Manages certificate collections and trust stores  
- **User Certificate Mappings**: Maps X.509 certificate identities to HANA users

These resources must be configured on the HANA side before users can authenticate using X.509 certificates.

### Current Use Case Requirements

- Certificate rotation must be seamless and without downtime (generate new certificate before current expires)
- Rotation needs to be performed without the creation of additional users
- HANA-side X.509 configuration must be managed through Crossplane resources
- The solution must support enterprise PKI infrastructure
- Users need to authenticate using certificates instead of passwords

Currently, these X.509 configurations must be managed manually through SQL commands, which doesn't align with the infrastructure-as-code approach using Crossplane.

## Decision Drivers

- **Infrastructure as Code**: Enable declarative management of HANA X.509 configuration through Crossplane
- **Enterprise readiness**: Support for enterprise PKI infrastructure and compliance requirements
- **Automation**: Enable automated certificate lifecycle management on the HANA side
- **Certificate rotation**: Support seamless certificate rotation without user recreation
- **SAP HANA compatibility**: Leverage native SAP HANA X.509 authentication capabilities

## Considered Options

- [Manual SQL Configuration (Current State)](#manual-sql-configuration-current-state)
- [Crossplane Resource Management](#crossplane-resource-management)
- [External Configuration Management Tool](#external-configuration-management-tool)

## Decision Outcome

Chosen option: "[Crossplane Resource Management](#crossplane-resource-management)", because it provides native integration with the existing Crossplane provider architecture and enables infrastructure-as-code management of HANA X.509 configuration.

### Implementation Details

#### New Crossplane Resource Types

New Crossplane resource types have to be added to manage HANA-side X.509 configuration:

##### X509Provider Resource

```go
type X509ProviderParameters struct {
	Name          string   `json:"name"`
	Issuer        string   `json:"issuer"`
	MatchingRules []string `json:"matchingRules,omitempty"`
	Priority      *int     `json:"priority,omitempty"`
}

type X509ProviderSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       X509ProviderParameters `json:"forProvider"`
}
```

##### PersonalSecurityEnvironment (PSE) Resource

```go
// CertificateRef - must have either ID or Name
type CertificateRef struct {
	ID   *int    `json:"id,omitempty"`
	Name *string `json:"name,omitempty"`
}

// X509ProviderRef - can reference by direct name or Crossplane resource
type X509ProviderRef struct {
	Name        string          `json:"name,omitempty"`
	ProviderRef *xpv1.Reference `json:"providerRef,omitempty"`
}

type PersonalSecurityEnvironmentParameters struct {
	Name            string           `json:"name"`
	X509ProviderRef *X509ProviderRef `json:"x509ProviderRef,omitempty"`
	CertificateRefs []CertificateRef `json:"certificateRefs,omitempty"`
}

type PersonalSecurityEnvironmentSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       PersonalSecurityEnvironmentParameters `json:"forProvider"`
}
```

##### User Resource Extension

Extend the existing User resource to support X.509 authentication:

```go
// X509UserMapping maps a certificate identity to a user
type X509UserMapping struct {
	X509ProviderRef `json:",inline"`
	SubjectName     string `json:"subjectName,omitempty"`
}

// Authentication supports both password and X.509 methods
type Authentication struct {
	Password      *Password         `json:"password,omitempty"`
	X509Providers []X509UserMapping `json:"x509Providers,omitempty"`
}
```

#### Example Resource Configurations

```yaml
apiVersion: admin.hana.orchestrate.cloud.sap/v1alpha1
kind: X509Provider
metadata:
  name: my-x509provider
spec:
  forProvider:
    name: MY_X509_PROVIDER
    issuer: CN=My CA, O=My Org, C=US
    matchingRules:
      - CN=*
  providerConfigRef:
    name: hana-provider-config
---
apiVersion: admin.hana.orchestrate.cloud.sap/v1alpha1
kind: PersonalSecurityEnvironment
metadata:
  name: my-pse
spec:
  forProvider:
    name: MY_PSE
    x509ProviderRef:
      providerRef:
        name: my-x509provider
    certificateRefs:
      - id: 123456
  providerConfigRef:
    name: hana-provider-config
---
apiVersion: admin.hana.orchestrate.cloud.sap/v1alpha1
kind: User
metadata:
  name: my-user
spec:
  forProvider:
    username: MY_USER
    authentication:
      x509Providers:
        - providerRef:
            name: my-x509provider
          subjectName: CN=MyUser,O=My Org,C=US
  providerConfigRef:
    name: hana-provider-config
```

#### SQL Command Mapping

The Crossplane resources execute the following SQL commands on HANA:

##### X509Provider
```sql
CREATE X509 PROVIDER MY_PROVIDER WITH ISSUER 'CN=My CA, O=My Org, C=US';
ALTER X509 PROVIDER MY_PROVIDER SET MATCHING RULES 'CN=*';
```

##### PersonalSecurityEnvironment (PSE)
```sql
CREATE PSE MY_PSE;
ALTER PSE MY_PSE ADD CERTIFICATE 123456;
SET PSE MY_PSE PURPOSE X509 FOR PROVIDER MY_PROVIDER;
```

##### User X.509 Identity
```sql
ALTER USER MY_USER ADD IDENTITY 'CN=MyUser,O=My Org,C=US' FOR X509 PROVIDER MY_PROVIDER;
-- Or use ANY for rule-based matching:
ALTER USER MY_USER ADD IDENTITY 'ANY' FOR X509 PROVIDER MY_PROVIDER;
```

## Pros and Cons of the Options

### Manual SQL Configuration (Current State)

Current approach where X.509 configuration is managed manually through SQL commands.

**Pros**:

- Simple and direct
- Minimal implementation overhead

**Cons**:

- Doesn't align with infrastructure-as-code principles
- Requires manual intervention for configuration changes
- Error-prone and lacks version control
- Doesn't integrate with Crossplane workflows

### Crossplane Resource Management

Add new Crossplane resource types to manage HANA X.509 configuration declaratively.

**Pros**:

- Enables infrastructure-as-code management
- Integrates natively with existing Crossplane provider architecture
- Provides version control and audit trails
- Enables automated certificate lifecycle management
- Supports GitOps workflows

**Cons**:

- Increases implementation complexity
- Requires new CRDs and controllers

### External Configuration Management Tool

Use an external tool (e.g., Ansible, Terraform) to manage HANA X.509 configuration.

**Pros**:

- Leverages existing configuration management expertise
- Can be integrated with other infrastructure management

**Cons**:

- Creates dependencies on external tools
- Doesn't integrate with Crossplane workflows
- Requires additional tooling and complexity
- Breaks the unified management approach of Crossplane

## Security Considerations

1. **Certificate Storage**: Client certificates and private keys will be stored in Kubernetes secrets
2. **Certificate Rotation**: Implement automated certificate rotation mechanisms
3. **Trust Store Management**: Provide mechanisms to update trust stores for CA certificate changes
4. **Temporary Files**: Ensure temporary certificate files are securely created and cleaned up

## References

### SAP HANA Documentation
- [SAP HANA X.509 Certificate-Based User Authentication](https://help.sap.com/docs/SAP_HANA_PLATFORM/b3ee5778bc2e4a089d3299b82ec762a7/2b335f7eec6a450095f110ea961d77cc.html)
- [SAP HANA Client Interface Programming Reference - JDBC Connection Properties](https://help.sap.com/viewer/f1b440ded6144a54ada97ff95dac7adf/latest/en-US/109397c2206a4ab2a5386d494f4cf75e.html)
- [CREATE X509 PROVIDER Statement (Access Control)](https://help.sap.com/docs/hana-cloud-database/sap-hana-cloud-sap-hana-database-sql-reference-guide/create-x509-provider-statement-access-control)
- [Personal Security Environment (PSE) Management](https://help.sap.com/docs/SAP_HANA_PLATFORM/4fe29514fd584807ac9f2a04f6754767/4d80bf63fc374a7f99be94d8ce70a07a.html)
- [SAP Developers Tutorial: Connect to SAP HANA using X.509 certificates](https://developers.sap.com/tutorials/hana-clients-x509.html)
