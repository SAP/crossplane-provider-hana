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

// PublicKeyParameters are the configurable fields of a PublicKey.
type PublicKeyParameters struct {
	// Name for the public key in HANA. Used as the identifier in
	// `CREATE PUBLIC KEY <name> FROM '<pem>'` and as the join target on
	// `ALTER PSE ... ADD PUBLIC KEY`.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=127
	Name string `json:"name"`

	// PEM is the public key in PEM form, including the
	// `-----BEGIN PUBLIC KEY-----` and `-----END PUBLIC KEY-----` markers.
	// Immutable: HANA has no ALTER for the key material, and any bound PSE
	// would silently keep validating with the old key. Rotate via delete +
	// recreate so the PSE cascade is explicit.
	// The pattern is a coarse shape check; the reconciler parses the block
	// with `encoding/pem` + `crypto/x509` before issuing DDL, so real
	// validation lives on the client side.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Pattern:=`^-----BEGIN [A-Z ]+-----[\s\S]+-----END [A-Z ]+-----\s*$`
	PEM string `json:"pem"`

	// Comment stored alongside the public key in SYS.PUBLIC_KEYS.
	// +kubebuilder:validation:Optional
	Comment string `json:"comment,omitempty"`
}

// PublicKeyObservation are the observable fields of a PublicKey.
type PublicKeyObservation struct {
	// +kubebuilder:validation:Optional
	Name *string `json:"name,omitempty"`

	// Algorithm reported by HANA (e.g. "RSA").
	// +kubebuilder:validation:Optional
	Algorithm *string `json:"algorithm,omitempty"`

	// Fingerprint of the imported key, used to detect drift.
	// +kubebuilder:validation:Optional
	Fingerprint *string `json:"fingerprint,omitempty"`

	// +kubebuilder:validation:Optional
	Comment *string `json:"comment,omitempty"`
}

// A PublicKeySpec defines the desired state of a PublicKey.
type PublicKeySpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       PublicKeyParameters `json:"forProvider"`
}

// A PublicKeyStatus represents the observed state of a PublicKey.
type PublicKeyStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          PublicKeyObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A PublicKey models a HANA `CREATE PUBLIC KEY` object. Used to import the
// signing key of an external JWT issuer for later binding to a PSE via the
// PersonalSecurityEnvironment resource.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hana}
type PublicKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PublicKeySpec   `json:"spec"`
	Status PublicKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PublicKeyList contains a list of PublicKey.
type PublicKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PublicKey `json:"items"`
}

// PublicKeyRef references a public key, either by HANA name or via a
// Crossplane reference to a PublicKey managed resource.
type PublicKeyRef struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=""
	Name string `json:"name,omitempty"`

	// +kubebuilder:validation:Optional
	ProviderRef *xpv1.Reference `json:"providerRef,omitempty"`
}

// PublicKey type metadata.
var (
	PublicKeyKind             = reflect.TypeFor[PublicKey]().Name()
	PublicKeyGroupKind        = schema.GroupKind{Group: Group, Kind: PublicKeyKind}.String()
	PublicKeyKindAPIVersion   = PublicKeyKind + "." + SchemeGroupVersion.String()
	PublicKeyGroupVersionKind = SchemeGroupVersion.WithKind(PublicKeyKind)
)

func init() {
	SchemeBuilder.Register(&PublicKey{}, &PublicKeyList{})
}
