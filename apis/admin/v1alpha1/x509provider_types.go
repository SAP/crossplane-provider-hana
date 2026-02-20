/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package v1alpha1

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// X509ProviderParameters are the configurable fields of a X509Provider.
type X509ProviderParameters struct {
	// Name of the X509 provider
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=127
	Name string `json:"name"`

	// Issuer distinguished name
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Issuer string `json:"issuer"`

	// Matching rules for certificate subject mapping
	// +kubebuilder:validation:Optional
	MatchingRules []string `json:"matchingRules,omitempty"`

	// Priority for provider selection
	// +kubebuilder:validation:Optional
	Priority *int `json:"priority,omitempty"`
}

// X509ProviderObservation are the observable fields of a X509Provider.
type X509ProviderObservation struct {
	// Name of the X509 provider
	// +kubebuilder:validation:Optional
	Name *string `json:"name,omitempty"`

	// Issuer distinguished name
	// +kubebuilder:validation:Optional
	Issuer *string `json:"issuer,omitempty"`

	// Matching rules for certificate subject mapping
	// +kubebuilder:validation:Optional
	MatchingRules []string `json:"matchingRules,omitempty"`

	// Priority for provider selection
	// +kubebuilder:validation:Optional
	Priority *int `json:"priority,omitempty"`
}

// A X509ProviderSpec defines the desired state of a X509Provider.
type X509ProviderSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       X509ProviderParameters `json:"forProvider"`
}

// A X509ProviderStatus represents the observed state of a X509Provider.
type X509ProviderStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          X509ProviderObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A X509Provider is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hana}
type X509Provider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   X509ProviderSpec   `json:"spec"`
	Status X509ProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// X509ProviderList contains a list of X509Provider
type X509ProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []X509Provider `json:"items"`
}

// X509Provider type metadata.
var (
	X509ProviderKind             = reflect.TypeOf(X509Provider{}).Name()
	X509ProviderGroupKind        = schema.GroupKind{Group: Group, Kind: X509ProviderKind}.String()
	X509ProviderKindAPIVersion   = X509ProviderKind + "." + SchemeGroupVersion.String()
	X509ProviderGroupVersionKind = SchemeGroupVersion.WithKind(X509ProviderKind)
)

func init() {
	SchemeBuilder.Register(&X509Provider{}, &X509ProviderList{})
}
