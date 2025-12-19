/*
Copyright 2026 SAP SE.
*/

package v1alpha1

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// CertificateRef references certificates
// +kubebuilder:validation:XValidation:rule="has(self.id) || has(self.name)"
type CertificateRef struct {
	// Identifier for the certificate
	// Mandatory if Name is not provided
	// +kubebuilder:validation:Optional
	ID *int `json:"id,omitempty"`

	// Name of the certificate
	// Mandatory if ID is not provided
	// +kubebuilder:validation:Optional
	Name *string `json:"name,omitempty"`
}

// X509UserMapping defines the mapping of an X.509 certificate to a database user
type X509UserMapping struct {
	// Reference to X509Provider
	// +kubebuilder:validation:Optional
	X509ProviderRef `json:",inline"`

	// Subject distinguished name to be used as identity
	// +kubebuilder:validation:Optional
	SubjectName string `json:"subjectName,omitempty"`
}

// X509ProviderRef references X.509 providers
type X509ProviderRef struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=""
	Name string `json:"name,omitempty"`

	// +kubebuilder:validation:Optional
	ProviderRef *xpv1.Reference `json:"providerRef,omitempty"`
}

// PersonalSecurityEnvironmentParameters defines the parameters for PSE
type PersonalSecurityEnvironmentParameters struct {
	// Name for the PSE
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Reference to X509Provider
	// +kubebuilder:validation:Optional
	X509ProviderRef *X509ProviderRef `json:"x509ProviderRef,omitempty"`

	// Certificate references to add to the PSE
	// +kubebuilder:validation:Optional
	CertificateRefs []CertificateRef `json:"certificateRefs,omitempty"`
}

// PersonalSecurityEnvironmentSpec defines the desired state of PersonalSecurityEnvironment
type PersonalSecurityEnvironmentSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       PersonalSecurityEnvironmentParameters `json:"forProvider"`
}

type PersonalSecurityEnvironmentStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          PersonalSecurityEnvironmentObservation `json:"atProvider,omitempty"`
}

// PersonalSecurityEnvironmentObservation defines the observed state of PersonalSecurityEnvironment
type PersonalSecurityEnvironmentObservation struct {
	// Name of the PSE
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`

	// Name of the X.509 provider associated with the PSE
	// +kubebuilder:validation:Optional
	X509ProviderName string `json:"x509ProviderName,omitempty"`

	// Certificate references to add to the PSE
	// +kubebuilder:validation:Optional
	CertificateRefs []CertificateRef `json:"certificateRefs,omitempty"`
}

// +kubebuilder:object:root=true

// A PersonalSecurityEnvironment is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hana},shortName={pse}
type PersonalSecurityEnvironment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PersonalSecurityEnvironmentSpec   `json:"spec"`
	Status PersonalSecurityEnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PersonalSecurityEnvironmentList contains a list of PersonalSecurityEnvironment
type PersonalSecurityEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PersonalSecurityEnvironment `json:"items"`
}

// PersonalSecurityEnvironment type metadata.
var (
	PersonalSecurityEnvironmentKind             = reflect.TypeOf(PersonalSecurityEnvironment{}).Name()
	PersonalSecurityEnvironmentGroupKind        = schema.GroupKind{Group: Group, Kind: PersonalSecurityEnvironmentKind}.String()
	PersonalSecurityEnvironmentKindAPIVersion   = PersonalSecurityEnvironmentKind + "." + SchemeGroupVersion.String()
	PersonalSecurityEnvironmentGroupVersionKind = SchemeGroupVersion.WithKind(PersonalSecurityEnvironmentKind)
)

func init() {
	SchemeBuilder.Register(&PersonalSecurityEnvironment{}, &PersonalSecurityEnvironmentList{})
}
