/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package v1alpha1

import (
	"reflect"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// RolegroupParameters are the configurable fields of a Rolegroup.
type RolegroupParameters struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	RolegroupName string `json:"rolegroupName"`

	DisableRoleAdmin bool `json:"disableRoleAdmin,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	NoGrantToCreator bool `json:"noGrantToCreator,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	ForGrantsOnTenantObjects bool `json:"forGrantsOnTenantObjects,omitempty"`
}

// RolegroupObservation are the observable fields of a Rolegroup.
type RolegroupObservation struct {
	// +kubebuilder:validation:Optional
	RolegroupName string `json:"rolegroupName"`

	DisableRoleAdmin bool `json:"disableRoleAdmin,omitempty"`
}

// A RolegroupSpec defines the desired state of a Rolegroup.
type RolegroupSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       RolegroupParameters `json:"forProvider"`
}

// A RolegroupStatus represents the observed state of a Rolegroup.
type RolegroupStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          RolegroupObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Rolegroup is a managed resource that represents a SAP HANA rolegroup.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,sql}
type Rolegroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RolegroupSpec   `json:"spec"`
	Status RolegroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RolegroupList contains a list of Rolegroup
type RolegroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Rolegroup `json:"items"`
}

// Rolegroup type metadata.
var (
	RolegroupKind             = reflect.TypeFor[Rolegroup]().Name()
	RolegroupGroupKind        = schema.GroupKind{Group: Group, Kind: RolegroupKind}.String()
	RolegroupKindAPIVersion   = RolegroupKind + "." + SchemeGroupVersion.String()
	RolegroupGroupVersionKind = SchemeGroupVersion.WithKind(RolegroupKind)
)

func init() {
	SchemeBuilder.Register(
		&Rolegroup{},
		&RolegroupList{},
	)
}
