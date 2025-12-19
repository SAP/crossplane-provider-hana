/*
Copyright 2025 SAP SE.
*/

// Package v1alpha1 contains the v1alpha1 group Sample resources of the hana provider.
// +kubebuilder:object:generate=true
// +groupName=admin.hana.sap.crossplane.io
// +versionName=v1alpha1
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

// Package type metadata.
const (
	Group   = "admin.hana.sap.crossplane.io"
	Version = "v1alpha1"
)

var (
	// SchemeGroupVersion is group version used to register these objects
	SchemeGroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: SchemeGroupVersion}

	// AddToScheme is used by e2e tests
	AddToScheme = SchemeBuilder.AddToScheme
)
