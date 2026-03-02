# Instance Mapping Architecture

- Status: implemented
- Date: 2026-02-18 (updated 2026-03-02)
- Tags: InstanceMapping, KymaInstanceMapping, HANA Cloud, CloudFoundry, Kubernetes

## Context

HANA Cloud supports "instance mapping" - binding a database instance to an environment context (Kubernetes cluster+namespace or CloudFoundry org+space) so applications can auto-discover connection info and create HDI containers.

This provider supports two platforms:
- **Kubernetes/Kyma**: Cluster ID + namespace
- **CloudFoundry**: Organization GUID + space GUID

## Architecture Overview

The architecture uses a two-layer design:

```
┌─────────────────────────────────────────────────────────────────────┐
│                        HANA Cloud Admin API                          │
│         POST /inventory/v2/serviceInstances/{id}/instanceMappings    │
└─────────────────────────────────────────────────────────────────────┘
                                    ▲
                                    │
                    ┌───────────────┴───────────────┐
                    │                               │
         ┌──────────┴──────────┐       ┌───────────┴───────────┐
         │  InstanceMapping    │       │   InstanceMapping     │
         │  platform: k8s      │       │   platform: cf        │
         └──────────┬──────────┘       └───────────┬───────────┘
                    │                              │
                    │ creates                      │ creates (optional)
         ┌──────────┴──────────┐       ┌───────────┴───────────┐
         │ KymaInstanceMapping │       │ KRO + CF Provider     │
         │ (built-in)          │       │ (external composition)│
         └─────────────────────┘       └───────────────────────┘
```

| Layer | CR | Purpose |
|-------|----|---------|
| Low-level | `InstanceMapping` | Direct HANA Cloud Admin API calls. Platform-agnostic. |
| High-level (Kyma) | `KymaInstanceMapping` | Built-in. Extracts data from Kyma cluster, creates InstanceMapping. |
| High-level (CF) | KRO + CF Provider | Optional. Users can compose using external tools (see below). |

> **Why no built-in CfInstanceMapping?** Users of the [CloudFoundry Crossplane Provider](https://github.com/SAP/crossplane-provider-cloudfoundry/) can compose resources using [KRO](https://kro.run) to achieve the same result. Building a dedicated CR would duplicate functionality already available through existing tools.

---

## InstanceMapping CR (Low-Level)

The `InstanceMapping` CR directly manages HANA Cloud instance mappings via the Admin API. It's platform-agnostic and can be used for both Kubernetes and CloudFoundry.

### Parameters

| Parameter | Description | Kubernetes | CloudFoundry |
|-----------|-------------|------------|--------------|
| `serviceInstanceID` | HANA Cloud instance GUID | Same | Same |
| `platform` | Target platform | `"kubernetes"` | `"cloudfoundry"` |
| `primaryID` | Primary identifier | Cluster ID | Organization GUID |
| `secondaryID` | Secondary identifier (optional) | Namespace | Space GUID |
| `isDefault` | Default mapping for primaryID | boolean | boolean |
| `adminCredentialsSecretRef` | Secret with Admin API creds | Same | Same |

### Admin Credentials Secret Format

The secret must contain JSON with HANA Cloud Admin API credentials:

```json
{
  "baseurl": "api.hana.cloud...",
  "uaa": {
    "url": "https://...",
    "clientid": "...",
    "clientsecret": "..."
  }
}
```

These credentials come from a service binding with the `admin-api-access` plan.

### Example: Kubernetes Platform

```yaml
apiVersion: inventory.hana.orchestrate.cloud.sap/v1alpha1
kind: InstanceMapping
metadata:
  name: my-k8s-mapping
spec:
  forProvider:
    serviceInstanceID: "abc123-hana-instance-guid"
    platform: "kubernetes"
    primaryID: "cluster-id-from-configmap"
    secondaryID: "my-namespace"
    isDefault: false
    adminCredentialsSecretRef:
      name: hana-admin-creds
      namespace: crossplane-system
      key: credentials
```

### Example: CloudFoundry Platform

```yaml
apiVersion: inventory.hana.orchestrate.cloud.sap/v1alpha1
kind: InstanceMapping
metadata:
  name: my-cf-mapping
spec:
  forProvider:
    serviceInstanceID: "abc123-hana-instance-guid"
    platform: "cloudfoundry"
    primaryID: "cf-org-guid"        # from: cf org <ORG> --guid
    secondaryID: "cf-space-guid"    # from: cf space <SPACE> --guid
    isDefault: false
    adminCredentialsSecretRef:
      name: hana-admin-creds
      namespace: crossplane-system
      key: credentials
```

---

## KymaInstanceMapping CR (High-Level for Kyma)

For Kyma environments, `KymaInstanceMapping` automatically extracts data from Kyma resources and creates an `InstanceMapping`.

### Why KymaInstanceMapping Exists

In Kyma environments:
1. **ServiceInstance** (instance ID) - created by BTP Service Operator, not this provider
2. **ServiceBinding** secret (admin API credentials) - on Kyma cluster
3. **ConfigMap** (cluster ID) - managed by BTP operator

The provider must gather info from resources it didn't create, potentially cross-cluster.

### Data Flow

```
Management Cluster                     Target/Kyma Cluster
┌────────────────────┐                ┌──────────────────────┐
│ KymaInstanceMapping│                │ ServiceInstance      │
│  - kymaConnectionRef [opt]          │   status.instanceID  │───┐
│  - serviceInstanceRef  ─────────────┼──────────────────────┘   │
│  - adminBindingRef     ─────────────┤                          │
│  - targetNamespace     │            │ ServiceBinding           │
└────────────────────┘   │            │   spec.secretName   ─────┼──┐
         │               │            │                          │  │
         │               │            │ Secret                   │  │
         │               │            │   data.credentials  ─────┼──┼─┐
         │               │            │                          │  │ │
         │               └────────────┤ ConfigMap                │  │ │
         │                            │   data.CLUSTER_ID   ─────┼──┼─┼─┐
         │                            └──────────────────────────┘  │ │ │
         │                                                          │ │ │
         └──────────── Controller extracts: ◄───────────────────────┴─┴─┘
                       - serviceInstanceID
                       - adminAPICreds
                       - clusterID (primaryID)
                       - namespace (secondaryID)
                                 │
                                 ▼
                       ┌─────────────────┐
                       │ InstanceMapping │
                       │ (child CR)      │
                       └────────┬────────┘
                                │
                                ▼
                       ┌──────────────────────┐
                       │ HANA Cloud Admin API │
                       └──────────────────────┘
```

### Extraction Steps (Connect Phase)

1. **Get cluster client**: If `kymaConnectionRef` nil → use local client, else create remote client from kubeconfig
2. **Read ServiceInstance**: Extract `status.instanceID` and check `ready` condition
3. **Read ServiceBinding**: Get `spec.secretName`, then read Secret for admin API creds
4. **Read ConfigMap**: Extract `CLUSTER_ID` (default: `kyma-system/sap-btp-operator-config`)
5. **Create InstanceMapping**: With extracted data and `platform: "kubernetes"`

### Example

```yaml
apiVersion: inventory.hana.orchestrate.cloud.sap/v1alpha1
kind: KymaInstanceMapping
metadata:
  name: my-kyma-mapping
spec:
  forProvider:
    # Optional: for remote Kyma cluster
    kymaConnectionRef:
      secretRef:
        name: kyma-kubeconfig
        namespace: crossplane-system
      kubeconfigKey: kubeconfig

    # References to Kyma resources
    serviceInstanceRef:
      name: my-hana-instance
      namespace: default
    adminBindingRef:
      name: my-hana-admin-binding
      namespace: default

    # Target namespace to map
    targetNamespace: my-app-namespace
    isDefault: false
```

---

## CloudFoundry Usage

CloudFoundry users have two options:
1. **Direct**: Create `InstanceMapping` manually with org/space GUIDs
2. **Composed**: Use KRO + CloudFoundry Crossplane Provider for automatic GUID extraction

### Why No Built-in CfInstanceMapping CR?

| Aspect | Kyma | CloudFoundry |
|--------|------|--------------|
| K8s-native resources | Yes (ServiceInstance CR, ConfigMap) | Only with CF Crossplane Provider |
| Built-in high-level CR | `KymaInstanceMapping` | Not needed |
| Composition option | N/A | KRO + CF Provider |

A dedicated `CfInstanceMapping` CR would duplicate functionality already available through the combination of:
- [CloudFoundry Crossplane Provider](https://github.com/SAP/crossplane-provider-cloudfoundry/) - provides `Organization` and `Space` CRs with GUIDs in status
- [KRO](https://kro.run) - composes resources and passes values between them

### Option 1: Direct InstanceMapping (Manual GUIDs)

For users who don't use the CF Crossplane Provider:

1. **Get organization GUID**: `cf org my-org --guid`
2. **Get space GUID**: `cf space my-space --guid`
3. **Get HANA Cloud instance ID**: From service key or BTP cockpit
4. **Get admin credentials**: Create service key with `admin-api-access` plan, copy to K8s Secret
5. **Create InstanceMapping**: With `platform: "cloudfoundry"`

### Option 2: KRO + CloudFoundry Provider (Automatic GUIDs)

For users of the CloudFoundry Crossplane Provider, KRO can compose resources and extract GUIDs automatically.

**ResourceGraphDefinition Example:**

```yaml
apiVersion: kro.run/v1alpha1
kind: ResourceGraphDefinition
metadata:
  name: cf-hana-instance-mapping
spec:
  schema:
    apiVersion: hana.example.com/v1alpha1
    kind: CfHanaMapping
    spec:
      # User-friendly inputs
      organizationRef: string      # Name of CF Provider Organization CR
      spaceRef: string             # Name of CF Provider Space CR
      serviceInstanceID: string
      adminCredentialsSecretRef:
        name: string
        namespace: string
        key: string
      isDefault: boolean | default=false

  # Reference existing CF Provider resources (created independently)
  references:
    - id: org
      resource:
        apiVersion: cloudfoundry.crossplane.io/v1alpha1
        kind: Organization
        name: ${schema.spec.organizationRef}

    - id: space
      resource:
        apiVersion: cloudfoundry.crossplane.io/v1alpha1
        kind: Space
        name: ${schema.spec.spaceRef}

  resources:
    # Create InstanceMapping with GUIDs from referenced CF Provider resources
    - id: instanceMapping
      includeWhen:
        - ${org.status.atProvider.guid != ""}
        - ${space.status.atProvider.guid != ""}
      template:
        apiVersion: inventory.hana.orchestrate.cloud.sap/v1alpha1
        kind: InstanceMapping
        metadata:
          name: ${schema.metadata.name}
        spec:
          forProvider:
            serviceInstanceID: ${schema.spec.serviceInstanceID}
            platform: "cloudfoundry"
            primaryID: ${org.status.atProvider.guid}
            secondaryID: ${space.status.atProvider.guid}
            isDefault: ${schema.spec.isDefault}
            adminCredentialsSecretRef:
              name: ${schema.spec.adminCredentialsSecretRef.name}
              namespace: ${schema.spec.adminCredentialsSecretRef.namespace}
              key: ${schema.spec.adminCredentialsSecretRef.key}
```

**Usage:**

```yaml
apiVersion: hana.example.com/v1alpha1
kind: CfHanaMapping
metadata:
  name: my-cf-hana-mapping
spec:
  organizationRef: my-cf-org        # Existing CF Provider Organization CR
  spaceRef: my-cf-space             # Existing CF Provider Space CR
  serviceInstanceID: "abc123-hana-instance-guid"
  adminCredentialsSecretRef:
    name: hana-admin-creds
    namespace: crossplane-system
    key: credentials
```

> **Note**: The exact status field paths (`status.atProvider.guid`) depend on the CloudFoundry Crossplane Provider's CRD schema. Adjust the CEL expressions accordingly. The `Organization` and `Space` CRs must be created independently using the CF Provider before creating the `CfHanaMapping`.

---

## Key Decisions

### 1. Two-Layer Architecture
**Decision**: Split into low-level `InstanceMapping` and high-level `KymaInstanceMapping`
**Why**: Separation of concerns - API interaction vs data extraction. Enables direct usage for CF.

### 2. No Built-in CfInstanceMapping CR
**Decision**: CF users use `InstanceMapping` directly or compose with KRO + CF Provider
**Why**: The CloudFoundry Crossplane Provider + KRO already provides the composition capability. Building a dedicated CR would duplicate existing functionality without adding value.

### 3. Extract Credentials On-Demand (Kyma)
**Decision**: Read ServiceBinding secret on every reconcile
**Why**: ServiceBinding is source of truth, supports rotation, no credential storage in provider

### 4. Per-Reconcile HANA Cloud Client
**Decision**: New client instance per reconcile
**Why**: Credential isolation between mappings, avoid HTTP client state leakage

### 5. Optional kymaConnectionRef
**Decision**: Make field optional (pointer type)
**Why**: Single-cluster deployments use in-cluster client, no kubeconfig needed

---

## Security

- **Admin credentials never in ProviderConfig**: Only SQL credentials there
- **Never stored in CR status**: Only non-sensitive metadata
- **Extracted on-demand**: Fresh read each reconcile (Kyma)
- **Scoped lifetime**: Client discarded after reconcile
- **User responsibility for CF**: User manages the admin credentials Secret

---

## References

- [HANA Cloud Admin API - Instance Mappings](https://help.sap.com/docs/hana-cloud/sap-hana-cloud-administration-guide/creating-and-managing-instance-mappings-using-rest-api)
- [SAP BTP Service Operator](https://github.com/SAP/sap-btp-service-operator)
- [CloudFoundry Crossplane Provider](https://github.com/SAP/crossplane-provider-cloudfoundry/)
- [KRO - Kubernetes Resource Orchestrator](https://kro.run)
- [CloudFoundry API v3](https://v3-apidocs.cloudfoundry.org/)
