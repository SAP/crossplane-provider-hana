// Package apis contains Kubernetes API for the provider.
package apis

import (
	adminv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/admin/v1alpha1"
	inventoryv1alpha1 "github.com/SAP/crossplane-provider-hana/apis/inventory/v1alpha1"
	schemav1alpha1 "github.com/SAP/crossplane-provider-hana/apis/schema/v1alpha1"
)

func init() {
	// Register the types with the Scheme so the components can map objects to GroupVersionKinds and back
	AddToSchemes = append(AddToSchemes,
		adminv1alpha1.SchemeBuilder.AddToScheme,
		inventoryv1alpha1.SchemeBuilder.AddToScheme,
		schemav1alpha1.SchemeBuilder.AddToScheme,
	)
}
