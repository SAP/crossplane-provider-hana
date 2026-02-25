[![REUSE status](https://api.reuse.software/badge/github.com/SAP/crossplane-provider-hana)](https://api.reuse.software/info/github.com/SAP/crossplane-provider-hana)

# Crossplane Provider for SAP HANA

![logo](/Logo.png)

## About this project

`crossplane-provider-hana` is a [Crossplane](https://crossplane.io/) Provider for managing SAP HANA Cloud resources and instance mappings. It provides Kubernetes-native management of:

- **HANA Database Resources**: Users, roles, schemas, audit policies, and security configurations

See the [examples directory](./examples/) for detailed usage guides and example manifests.

## Requirements and Setup

### Installation

1. Install Crossplane on your Kubernetes cluster:

```bash
helm repo add crossplane-stable https://charts.crossplane.io/stable
helm repo update
helm install crossplane \
--namespace crossplane-system \
--create-namespace crossplane-stable/crossplane
```

2. Install the HANA provider:

```bash
kubectl apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: crossplane-provider-hana
spec:
  package: ghcr.io/sap/crossplane/provider-hana:latest
EOF
```

3. Configure the secret in `examples/provider/config.yaml` with the appropriate credentials and apply the provider config:

```bash
kubectl apply -f examples/provider/config.yaml
```

4. Create resources:

```bash
# For creating a user, see examples/user/
kubectl apply -f examples/user/user.yaml
```

### Development Setup

1. Clone the repository and initialize submodules:

```bash
git clone https://github.com/SAP/crossplane-provider-hana.git
cd crossplane-provider-hana
make submodules
```

2. Build the provider:

```bash
make build
```

3. Run locally for development:

```bash
make dev
```

### Client

The [go-hdb client](https://github.com/SAP/go-hdb) is used by this provider.

## Testing

### Unit Tests

Unit tests can be executed via `go test` or you can use the predefined rule in the Makefile.

Run unit test via make rule

```bash
make test
```

### E2E Tests

The E2E tests are located in the `{project_root}/test/e2e` directory.

E2E tests are based on the [k8s e2e-framework](https://github.com/kubernetes-sigs/e2e-framework).

Before executing an E2E test, the `HANA_BINDINGS` env variable has to be set (see `test/e2e/secrets/secret.sample.env`).

To run E2E tests via make rule

```bash
make e2e.run
```

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/SAP/crossplane-provider-hana/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure

If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/SAP/crossplane-provider-hana/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2026 SAP SE or an SAP affiliate company and crossplane-provider-hana contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/SAP/crossplane-provider-hana).
