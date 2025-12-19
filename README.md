[![REUSE status](https://api.reuse.software/badge/github.com/SAP/crossplane-provider-hana)](https://api.reuse.software/info/github.com/SAP/crossplane-provider-hana)

# crossplane-provider-hana

![logo](/Logo.png)


## About this project

`crossplane-provider-hana` is a minimal [Crossplane](https://crossplane.io/) Provider
that is meant to be used as a hana for implementing new Providers. It comes
with the following features that are meant to be refactored:

- A `ProviderConfig` type that only points to a credentials `Secret`.
- A `MyType` resource type that serves as an example managed resource.
- A managed resource controller that reconciles `MyType` objects and simply
  prints their configuration in its `Observe` method.

## Requirements and Setup

### Provider 

1. Use this repository as a hana to create a new one.
1. Run `make submodules` to initialize the "build" Make submodule we use for CI/CD.
1. Rename the provider by running the follwing command:
```
  make provider.prepare provider={PascalProviderName}
```
4. Add your new type by running the following command:
```
make provider.addtype provider={PascalProviderName} group={group} kind={type}
```
5. Replace the *sample* group with your new group in apis/{provider}.go
5. Replace the *mytype* type with your new type in internal/controller/{provider}.go
5. Replace the default controller and ProviderConfig implementations with your own
5. Run `make generate` to run code generation, this created the CRDs from the API definition.
5. Run `make build` to build the provider.

### Client

The [HANA client repo](https://github.com/SAP/go-hdb) is used for this provider.

## Testing

### Unit Tests
Unit tests can be executed via `go test` or you can use the predefined rule in the Makefile.

Run unit test via make rule
```bash
make test.run
```

### E2E Tests
The E2E tests are located in the `{project_root}/test/e2e` directory. 

_You will need to build the provider before running E2E tests._ 


E2E tests are based on the [k8s e2e-framework](https://github.com/kubernetes-sigs/e2e-framework). Executing an E2E test
will start a kind cluster that installs crossplane, the **UUT_CONFIG** (Crossplane Package **U**nit **u**nder **T**est),
**UUT_CONTROLLER** (Crossplane Provider Controller) and any CRs and Provider Config defined in `test/e2e/testdata`, env variables are defined in `dev.env`.

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

Copyright 2025 SAP SE or an SAP affiliate company and crossplane-provider-hana contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/SAP/crossplane-provider-hana).
