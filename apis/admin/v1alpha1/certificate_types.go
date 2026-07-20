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

// CertificateParameters are the configurable fields of a HANA Certificate.
type CertificateParameters struct {
	// Name of the certificate in HANA.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// Reference to the Kubernetes Secret containing the PEM encoded certificate.
	// +kubebuilder:validation:Required
	CertificateSecretRef *xpv1.SecretKeySelector `json:"certificateSecretRef"`
}

// CertificateObservation represents the observed state of a Certificate.
type CertificateObservation struct {
	// Identifier assigned by HANA.
	// +kubebuilder:validation:Optional
	ID *int `json:"id,omitempty"`
	// Name of the certificate.
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`
}

// CertificateSpec defines the desired state of Certificate.
type CertificateSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider CertificateParameters `json:"forProvider"`
}

// CertificateStatus represents the observed state of Certificate.
type CertificateStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider CertificateObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// Certificate is a managed HANA certificate.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hana},shortName={cert}
type Certificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertificateSpec   `json:"spec"`
	Status CertificateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CertificateList contains a list of Certificates.
type CertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Certificate `json:"items"`
}

// Certificate type metadata.
var (
	CertificateKind             = reflect.TypeFor[Certificate]().Name()
	CertificateGroupKind        = schema.GroupKind{Group: Group, Kind: CertificateKind}.String()
	CertificateKindAPIVersion   = CertificateKind + "." + SchemeGroupVersion.String()
	CertificateGroupVersionKind = SchemeGroupVersion.WithKind(CertificateKind)
)

func init() {
	SchemeBuilder.Register(&Certificate{}, &CertificateList{})
}