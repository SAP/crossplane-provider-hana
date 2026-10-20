# KymaInstanceMapping - Cross-Cluster Instance Mapping

## Overview

`KymaInstanceMapping` enables **HANA Cloud instance mapping** with two deployment patterns:

### Single-Cluster Deployment
- Provider controller and HANA ServiceInstance run on the **same cluster**
- No kubeconfig required - controller uses local cluster client
- Simpler setup for teams with unified infrastructure
- See [kymainstancemapping-local.yaml](./kymainstancemapping-local.yaml) for example

### Cross-Cluster Deployment (This Guide)
- A **management cluster** runs Crossplane and the HANA provider
- A separate **Kyma cluster** hosts the actual HANA Cloud ServiceInstance and BTP services
- Common in managed control plane architectures where teams want centralized Crossplane management but keep their runtime workloads in dedicated clusters
- Requires kubeconfig secret for remote cluster access

## Architecture

```
┌─────────────────────────────────────┐
│   Management Cluster (API Server A) │
│                                     │
│  ┌───────────────────────────────┐ │
│  │  KymaInstanceMapping CR       │ │
│  │  - kymaConnectionRef          │ │
│  │  - serviceInstanceRef         │ │
│  │  - adminBindingRef            │ │
│  └───────────────────────────────┘ │
│             │                       │
│             │ Controller reads      │
│             ▼                       │
│  ┌───────────────────────────────┐ │
│  │  Secret (kubeconfig)          │ │
│  │  - key: kubeconfig            │ │
│  └───────────────────────────────┘ │
│             │                       │
└─────────────│───────────────────────┘
              │
              │ REST API calls
              │
              ▼
┌─────────────────────────────────────┐
│     Kyma Cluster (API Server B)     │
│                                     │
│  ┌───────────────────────────────┐ │
│  │  ServiceInstance              │ │
│  │  status.instanceID            │ │
│  └───────────────────────────────┘ │
│  ┌───────────────────────────────┐ │
│  │  ServiceBinding               │ │
│  │  → Secret (admin API creds)   │ │
│  └───────────────────────────────┘ │
│  ┌───────────────────────────────┐ │
│  │  ConfigMap (CLUSTER_ID)       │ │
│  └───────────────────────────────┘ │
└─────────────────────────────────────┘
              │
              │ HANA Cloud Admin API
              ▼
┌─────────────────────────────────────┐
│      HANA Cloud Control Plane       │
│                                     │
│  Instance Mapping Created:          │
│  - serviceInstanceID: cf923...      │
│  - primaryID: shoot--prod--...      │
│  - secondaryID: my-app-namespace    │
└─────────────────────────────────────┘
```

## Prerequisites

### On Management Cluster

1. **Crossplane** installed with provider-hana
2. **Kubeconfig secret** containing access to the Kyma cluster
3. **ProviderConfig** (placeholder - actual credentials come from Kyma)

### On Kyma Cluster

1. **SAP BTP Service Operator** installed and configured
2. **HANA Cloud ServiceInstance** created and ready
3. **Admin API ServiceInstance** created (plan: `admin-api-access`)
4. **ServiceBinding** for the admin API ServiceInstance
5. **BTP Operator ConfigMap** with `CLUSTER_ID`

### RBAC Requirements

The kubeconfig must grant permissions to read from the Kyma cluster:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hana-provider-remote-access
rules:
- apiGroups: ["services.cloud.sap.com"]
  resources: ["serviceinstances", "servicebindings"]
  verbs: ["get", "list"]
- apiGroups: [""]
  resources: ["secrets", "configmaps"]
  verbs: ["get"]
```

## Setup Guide

### Step 1: Create Admin API ServiceInstance on Kyma

```yaml
apiVersion: services.cloud.sap.com/v1
kind: ServiceInstance
metadata:
  name: hana-admin-api
  namespace: default
spec:
  serviceOfferingName: hana-cloud
  servicePlanName: admin-api-access
  parameters:
    data:
      platform: "kubernetes"
```

Wait for the ServiceInstance to become ready:

```bash
kubectl wait --for=condition=Ready serviceinstance/hana-admin-api -n default --timeout=10m
```

### Step 2: Create ServiceBinding for Admin API

```yaml
apiVersion: services.cloud.sap.com/v1
kind: ServiceBinding
metadata:
  name: hana-admin-api-binding
  namespace: default
spec:
  serviceInstanceName: hana-admin-api
  secretName: hana-admin-api-secret
```

Wait for binding:

```bash
kubectl wait --for=condition=Ready servicebinding/hana-admin-api-binding -n default --timeout=5m
```

Verify the secret was created:

```bash
kubectl get secret hana-admin-api-secret -n default
```

### Step 3: Export Kubeconfig from Kyma Cluster

```bash
# Export a minimal kubeconfig with only what's needed
kubectl config view --minify --flatten > kyma-kubeconfig.yaml

# Verify it works
kubectl --kubeconfig=kyma-kubeconfig.yaml get nodes
```

### Step 4: Create Kubeconfig Secret on Management Cluster

```bash
# Switch to management cluster context
kubectl config use-context management-cluster

# Create the secret
kubectl create secret generic kyma-cluster-kubeconfig \
  --from-file=kubeconfig=kyma-kubeconfig.yaml \
  -n crossplane-system

# Verify
kubectl get secret kyma-cluster-kubeconfig -n crossplane-system
```

### Step 5: Create KymaInstanceMapping

```yaml
apiVersion: inventory.hana.orchestrate.cloud.sap/v1alpha1
kind: KymaInstanceMapping
metadata:
  name: my-app-mapping
spec:
  providerConfigRef:
    name: default
  forProvider:
    kymaConnectionRef:
      secretRef:
        name: kyma-cluster-kubeconfig
        namespace: crossplane-system
      kubeconfigKey: kubeconfig
    serviceInstanceRef:
      name: my-hana-instance  # ServiceInstance on Kyma cluster
      namespace: default
    adminBindingRef:
      name: hana-admin-api-binding  # ServiceBinding on Kyma cluster
      namespace: default
    targetNamespace: my-application  # Namespace on Kyma cluster to map
    isDefault: false
```

Apply it:

```bash
kubectl apply -f kymainstancemapping.yaml
```

### Step 6: Verify the Mapping

Check status:

```bash
kubectl get kymainstancemapping my-app-mapping -o yaml
```

Expected status:

```yaml
status:
  conditions:
  - type: Ready
    status: "True"
  - type: Synced
    status: "True"
  atProvider:
    kyma:
      serviceInstanceID: "cf923d7d-7661-48f2-aaa2-d4dbb151a708"
      clusterID: "shoot--prod--cluster-abc123"
      serviceInstanceName: "my-hana-instance"
      serviceInstanceReady: true
    hana:
      mappingId:
        serviceInstanceID: "cf923d7d-7661-48f2-aaa2-d4dbb151a708"
        primaryID: "shoot--prod--cluster-abc123"
        secondaryID: "my-application"
      ready: true
```

Check kubectl output:

```bash
kubectl get kymainstancemappings

NAME               READY   SYNCED   CLUSTER-ID                       SERVICE-INSTANCE                      NAMESPACE        AGE
my-app-mapping     True    True     shoot--prod--cluster-abc123      cf923d7d-7661-48f2-aaa2-d4dbb151a708   my-application   5m
```

## Troubleshooting

### Issue: Kubeconfig secret not found

```
Error: cannot get kubeconfig secret: secrets "kyma-cluster-kubeconfig" not found
```

**Solution:**
- Verify the secret exists: `kubectl get secret kyma-cluster-kubeconfig -n crossplane-system`
- Check the namespace matches `kymaConnectionRef.secretRef.namespace`
- Verify the secret name matches `kymaConnectionRef.secretRef.name`

### Issue: ServiceInstance not found on remote cluster

```
Error: cannot get ServiceInstance from remote cluster: serviceinstances.services.cloud.sap.com "my-hana-instance" not found
```

**Solution:**
- Check ServiceInstance exists on Kyma cluster: `kubectl --kubeconfig=kyma-kubeconfig.yaml get serviceinstance my-hana-instance -n default`
- Verify namespace matches `serviceInstanceRef.namespace`
- Verify name matches `serviceInstanceRef.name`

### Issue: Permission denied accessing remote cluster

```
Error: serviceinstances.services.cloud.sap.com is forbidden: User "..." cannot get resource "serviceinstances"
```

**Solution:**
- The kubeconfig user needs read permissions for ServiceInstances, ServiceBindings, Secrets, and ConfigMaps
- Apply the ClusterRole shown in RBAC Requirements
- Create a RoleBinding or ClusterRoleBinding for the kubeconfig user

### Issue: ServiceInstance not ready

```
Error: ServiceInstance on remote cluster is not ready
```

**Solution:**
- Check ServiceInstance status on Kyma: `kubectl --kubeconfig=kyma-kubeconfig.yaml get serviceinstance my-hana-instance -n default -o yaml`
- Wait for `status.ready: true`
- Check BTP Service Operator logs if instance creation failed

### Issue: CLUSTER_ID not found

```
Error: CLUSTER_ID not found in ConfigMap
```

**Solution:**
- Verify ConfigMap exists: `kubectl --kubeconfig=kyma-kubeconfig.yaml get configmap sap-btp-operator-config -n kyma-system`
- Check it contains `CLUSTER_ID` key: `kubectl --kubeconfig=kyma-kubeconfig.yaml get configmap sap-btp-operator-config -n kyma-system -o yaml`
- If using custom ConfigMap, specify `clusterIdConfigMapRef` in the spec

### Issue: Admin API credentials missing keys

```
Error: admin API credentials secret missing required keys
```

**Solution:**
- The admin API secret must contain both `url` and `uaa` keys
- Verify: `kubectl --kubeconfig=kyma-kubeconfig.yaml get secret hana-admin-api-secret -n default -o yaml`
- Check ServiceBinding status if secret is incomplete

## Security Considerations

1. **Kubeconfig Storage**: The kubeconfig is stored as a Kubernetes Secret on the management cluster. Ensure RBAC restricts access.

2. **Minimal Permissions**: The kubeconfig should use a ServiceAccount with minimal RBAC permissions (read-only access to specific resource types).

3. **Credential Flow**: Admin API credentials are read from the Kyma cluster but never stored on the management cluster. They're only passed to the HANA Cloud API.

4. **Network Security**: Ensure network policies allow communication from the management cluster to the Kyma API server.

5. **Kubeconfig Rotation**: When rotating kubeconfig credentials, update the secret on the management cluster. The controller will use the new credentials on the next reconciliation.

6. **Audit Logging**: All cross-cluster access attempts are logged by the Kubernetes API server on the Kyma cluster.

## Use Cases

### Cross-Cluster Deployment (this guide)
- **Multi-cluster architectures**: Separate management and runtime clusters
- **Managed control planes**: Centralized Crossplane management
- **Security boundaries**: Different teams manage different clusters
- **Kyma-specific workflows**: ServiceInstance and ServiceBinding on remote Kyma

### Single-Cluster Deployment
- **Unified infrastructure**: Provider and workloads on same cluster
- **Simpler setup**: No kubeconfig management required
- **Direct access**: Controller reads resources from local cluster
- See [kymainstancemapping-local.yaml](./kymainstancemapping-local.yaml) for example

## Examples

**Cross-cluster setup (this guide):**
- [kymainstancemapping-example.yaml](./kymainstancemapping-example.yaml) - Complete example with kubeconfig secret

**Single-cluster setup:**
- [kymainstancemapping-local.yaml](./kymainstancemapping-local.yaml) - Simplified example without kubeconfig

## API Reference

### KymaConnectionReference

Optional reference to a kubeconfig secret for connecting to a remote cluster.

```go
type KymaConnectionReference struct {
    SecretRef     SecretReference  // Secret containing kubeconfig
    KubeconfigKey string          // Key in secret (default: "kubeconfig")
}
```

**When to omit**: For single-cluster deployments where the controller runs on the same cluster as the ServiceInstance. The controller will use its local cluster client instead.

**When to provide**: For cross-cluster deployments where the controller needs to access a remote Kyma cluster.

### ServiceInstanceRef / AdminBindingRef

```go
type ResourceReference struct {
    Name      string  // Resource name on Kyma cluster
    Namespace string  // Resource namespace on Kyma cluster
}
```

### Status

```go
type KymaInstanceMappingObservation struct {
    Kyma *KymaClusterObservation   // Data from Kyma cluster
    Hana *HANACloudObservation      // HANA Cloud mapping status
}

type KymaClusterObservation struct {
    ServiceInstanceID    string  // From ServiceInstance.status.instanceID
    ClusterID            string  // From ConfigMap.data["CLUSTER_ID"]
    ServiceInstanceName  string  // Name of ServiceInstance
    ServiceInstanceReady bool    // From ServiceInstance.status.ready
}

type HANACloudObservation struct {
    MappingID *MappingID  // Full mapping identifier
    Ready     bool        // Mapping is active
}
```

## Further Reading

- [HANA Cloud Admin API](https://help.sap.com/docs/HANA_CLOUD/9ae9104a46f74a6583ce5182e7fb20cb/6a541bca5e144be891e8f64dbc92f7d2.html)
- [SAP BTP Service Operator](https://github.com/SAP/sap-btp-service-operator)
- [Crossplane Providers](https://docs.crossplane.io/latest/concepts/providers/)
