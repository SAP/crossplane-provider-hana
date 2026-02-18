# Instance Mapping Examples

This directory contains examples for mapping HANA Cloud instances to Kubernetes namespaces.

## KymaInstanceMapping

KymaInstanceMapping supports two deployment patterns:

### 1. Single-Cluster Deployment
For environments where the provider controller runs in the same cluster as the HANA ServiceInstance:
- [kymainstancemapping-local.yaml](kymainstancemapping-local.yaml) - Local cluster example
- No kubeconfig required - controller uses local cluster client

### 2. Cross-Cluster Deployment
For architectures with a management cluster running Crossplane and a separate Kyma cluster hosting services:
- [KYMA_README.md](KYMA_README.md) - Comprehensive cross-cluster guide
- [kymainstancemapping-example.yaml](kymainstancemapping-example.yaml) - Cross-cluster example
- Requires kubeconfig secret for remote cluster access

## Provider Configuration

See [providerconfig.yaml](providerconfig.yaml) for setting up SQL credentials. Note that KymaInstanceMapping retrieves Admin API credentials directly from the ServiceBinding, not from ProviderConfig.

## Architecture Note

Based on customer feedback, KymaInstanceMapping is the preferred approach for instance mapping.
It supports both single-cluster and cross-cluster deployments by connecting to the target
cluster to fetch HANA Cloud instance details.
