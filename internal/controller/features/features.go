/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package features

import (
	"github.com/crossplane/crossplane-runtime/pkg/feature"
	"github.com/crossplane/crossplane-runtime/pkg/reconciler/managed"

	"github.com/SAP/crossplane-provider-hana/internal/controller"
)

// Feature flags.
const (
	// EnableAlphaExternalSecretStores enables alpha support for
	// External Secret Stores. See the below design for more details.
	// https://github.com/crossplane/crossplane/blob/390ddd/design/design-doc-external-secret-stores.md
	EnableAlphaExternalSecretStores feature.Flag = "EnableAlphaExternalSecretStores"

	// EnableAlphaManagementPolicies enables alpha support for
	// Management Policies. See the below design for more details.
	// https://github.com/crossplane/crossplane/blob/master/design/design-doc-observe-only-resources.md
	EnableAlphaManagementPolicies feature.Flag = "EnableAlphaManagementPolicies"
)

// ConfigureBetaManagementPolicies configures the management policies feature.
func ConfigureBetaManagementPolicies(o controller.Options) managed.ReconcilerOption {
	return func(r *managed.Reconciler) {
		if o.Features.Enabled(EnableAlphaManagementPolicies) {
			managed.WithManagementPolicies()(r)
		}
	}
}
