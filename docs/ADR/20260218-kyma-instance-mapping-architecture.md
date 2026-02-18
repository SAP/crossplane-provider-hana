# KymaInstanceMapping: Instance Mapping Architecture

- Status: implemented
- Date: 2026-02-18
- Tags: KymaInstanceMapping, HANA Cloud, instance-mapping, data-flow

## Context

HANA Cloud supports "instance mapping" - binding a database instance to a Kubernetes cluster+namespace so applications can auto-discover connection info. In Kyma environments, HANA ServiceInstances are provisioned via BTP Service Operator, which stores admin credentials in ServiceBindings. The provider needs to extract this data (possibly from a remote cluster) and create mappings via HANA Cloud Admin API.

## Challenge

**Data Extraction Problem:**
1. ServiceInstance (instance ID) - created by BTP operator, not provider
2. ServiceBinding secret (admin API credentials) - on Kyma cluster, not in ProviderConfig
3. ConfigMap (cluster ID) - managed by BTP operator
4. Controller may run on different cluster (management cluster pattern)

Provider must gather info from resources it didn't create, potentially cross-cluster, to call HANA Cloud API.

## Data Flow

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
         │               │            │   data.url          ─────┼──┼─┐
         │               │            │   data.uaa               │  │ │
         │               │            │                          │  │ │
         │               └────────────┤ ConfigMap                │  │ │
         │                            │   data.CLUSTER_ID   ─────┼──┼─┼─┐
         │                            └──────────────────────────┘  │ │ │
         │                                                          │ │ │
         └───────── Controller extracts: ◄──────────────────────────┴─┴─┘
                    - instanceID
                    - adminAPICreds
                    - clusterID
                    - namespace (from CR)
                              │
                              ▼
                    ┌──────────────────────┐
                    │ HANA Cloud Admin API │
                    │ POST /instance-mapping
                    │ {
                    │   serviceInstanceID,
                    │   platform: "kubernetes",
                    │   primaryID: clusterID,
                    │   secondaryID: namespace
                    │ }
                    └──────────────────────┘
```

### Extraction Steps (Connect Phase)

1. **Get cluster client**: If `kymaConnectionRef` nil → use local client, else create remote client from kubeconfig
2. **Read ServiceInstance**: Extract `status.instanceID` and check `ready` condition
3. **Read ServiceBinding**: Get `spec.secretName`, then read Secret for `url` + `uaa` (admin API creds)
4. **Read ConfigMap**: Extract `CLUSTER_ID` (default: `kyma-system/sap-btp-operator-config`)
5. **Create HANA Cloud client**: New instance per reconcile with extracted creds (OAuth2)

### Reconciliation

**Observe**: List mappings from HANA Cloud API, check if `{clusterID, namespace}` exists
**Create**: POST mapping to HANA Cloud API
**Delete**: DELETE mapping from HANA Cloud API

## Key Decisions

### 1. Extract Credentials On-Demand
**Decision**: Read ServiceBinding secret on every reconcile
**Why**: ServiceBinding is source of truth, supports rotation, no credential storage in provider
**Alternative**: Store in ProviderConfig → credential sprawl, rotation complexity

### 2. Per-Reconcile HANA Cloud Client
**Decision**: New `hanacloud.Client` instance per reconcile
**Why**: Credential isolation between mappings, avoid HTTP client state leakage
**Alternative**: Shared client → credential conflicts, race conditions

### 3. Optional kymaConnectionRef
**Decision**: Make field optional (pointer type)
**Why**: Single-cluster deployments use in-cluster client, no kubeconfig needed
**Alternative**: Always require → poor UX, unnecessary security surface

### 4. ConfigMap for Cluster ID
**Decision**: Read from BTP operator ConfigMap, allow override
**Why**: BTP operator owns cluster ID, follows Kyma conventions
**Alternative**: Derive from kubeconfig → unreliable, breaks single-cluster pattern

## Security

- **Never stored in ProviderConfig**: Only SQL credentials there
- **Never stored in CR status**: Only non-sensitive metadata
- **Extracted on-demand**: Fresh read each reconcile
- **Scoped lifetime**: Client discarded after reconcile

## Status Structure

```yaml
status:
  atProvider:
    kyma:                          # Data from Kyma cluster
      serviceInstanceID: "..."
      clusterID: "..."
      serviceInstanceReady: true
    hana:                          # Data from HANA Cloud API
      mappingId: {...}
      ready: true
```

## Trade-offs

**Positive:**
- Follows Kyma patterns (BTP Service Operator)
- Secure (no stored admin creds)
- Multi-cluster ready
- Credential rotation-friendly

**Negative:**
- More API calls per reconcile (4 Kubernetes resources)
- Two code paths (local vs remote client)
- Dependency on BTP operator ConfigMap structure

## References

- Commits: 7f56d7b (initial), 1690289 (per-reconcile client), 383c432 (optional kubeconfig)
- [HANA Cloud Admin API](https://help.sap.com/docs/HANA_CLOUD/9ae9104a46f74a6583ce5182e7fb20cb/6a541bca5e144be891e8f64dbc92f7d2.html)
- [SAP BTP Service Operator](https://github.com/SAP/sap-btp-service-operator)
