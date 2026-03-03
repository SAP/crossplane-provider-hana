# Instance Mapping Examples

This directory contains examples for mapping HANA Cloud instances to Kubernetes namespaces or Cloud Foundry spaces.

## InstanceMapping (Low-Level)

InstanceMapping is the low-level CR that directly manages HANA Cloud instance mappings via the Admin API.

### Cloud Foundry

- [instancemapping-cloudfoundry.yaml](instancemapping-cloudfoundry.yaml) - Map a HANA instance to a CF org/space
- [kro-cloudfoundry-rgd.yaml](kro-cloudfoundry-rgd.yaml) - KRO ResourceGraphDefinition for automatic GUID extraction from CF Provider

### Kubernetes

For Kubernetes environments, use KymaInstanceMapping which automatically extracts configuration from Kyma resources.

## KymaInstanceMapping (Kyma/BTP)

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

- **KymaInstanceMapping**: Preferred for Kubernetes/Kyma environments. Automatically extracts instance ID, credentials, and cluster ID from BTP Service Operator resources.
- **InstanceMapping**: Use directly for Cloud Foundry or when you have raw credentials and instance details.

