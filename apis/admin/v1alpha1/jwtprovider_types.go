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

// JWTClaimFilter restricts incoming tokens by requiring that the named claim
// contains a specific value. Maps to
// `ALTER JWT PROVIDER <name> SET CLAIM '<claim>' HAS MEMBER '<value>'`.
type JWTClaimFilter struct {
	// Claim is the JWT claim to inspect (e.g. "groups").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Claim string `json:"claim"`

	// Value that must appear in the claim. For array-valued claims, HANA
	// checks for membership; for scalar claims it checks equality.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Value string `json:"value"`
}

// JWTProviderParameters are the configurable fields of a JWTProvider.
type JWTProviderParameters struct {
	// Name of the JWT provider.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=127
	Name string `json:"name"`

	// Issuer URL exactly as it appears in the `iss` claim of accepted tokens.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Issuer string `json:"issuer"`

	// ExternalIdentityClaim is the JWT claim HANA uses to look up the mapped
	// database user via `ADD IDENTITY '<value>' FOR JWT PROVIDER <name>`.
	// Defaults to "sub" if unset.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:="sub"
	ExternalIdentityClaim string `json:"externalIdentityClaim,omitempty"`

	// CaseInsensitiveIdentity controls whether the external identity is
	// matched case-insensitively. Recommended for email-based identities.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=true
	CaseInsensitiveIdentity bool `json:"caseInsensitiveIdentity,omitempty"`

	// ApplicationUserClaim is the claim mapped to XS_APPLICATIONUSER on the
	// resulting session. Empty leaves the binding unset.
	// +kubebuilder:validation:Optional
	ApplicationUserClaim string `json:"applicationUserClaim,omitempty"`

	// Priority for provider selection when multiple JWT providers match the
	// same issuer. Lower numbers take precedence. HANA defaults new providers
	// to 100 when omitted from CREATE JWT PROVIDER; we mirror that default so
	// the reconciler can always drive the value authoritatively.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=100
	Priority int `json:"priority,omitempty"`

	// ClaimFilters reject incoming tokens whose values for the named claim
	// do not include the listed value.
	// +kubebuilder:validation:Optional
	ClaimFilters []JWTClaimFilter `json:"claimFilters,omitempty"`
}

// JWTProviderObservation are the observable fields of a JWTProvider.
type JWTProviderObservation struct {
	// +kubebuilder:validation:Optional
	Name *string `json:"name,omitempty"`

	// +kubebuilder:validation:Optional
	Issuer *string `json:"issuer,omitempty"`

	// +kubebuilder:validation:Optional
	ExternalIdentityClaim *string `json:"externalIdentityClaim,omitempty"`

	// +kubebuilder:validation:Optional
	CaseInsensitiveIdentity *bool `json:"caseInsensitiveIdentity,omitempty"`

	// +kubebuilder:validation:Optional
	ApplicationUserClaim string `json:"applicationUserClaim,omitempty"`

	// +kubebuilder:validation:Optional
	Priority *int `json:"priority,omitempty"`

	// +kubebuilder:validation:Optional
	ClaimFilters []JWTClaimFilter `json:"claimFilters,omitempty"`
}

// A JWTProviderSpec defines the desired state of a JWTProvider.
type JWTProviderSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       JWTProviderParameters `json:"forProvider"`
}

// A JWTProviderStatus represents the observed state of a JWTProvider.
type JWTProviderStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          JWTProviderObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A JWTProvider models a HANA JWT PROVIDER configuration.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hana}
type JWTProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   JWTProviderSpec   `json:"spec"`
	Status JWTProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// JWTProviderList contains a list of JWTProvider.
type JWTProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []JWTProvider `json:"items"`
}

// JWTProviderRef references a JWT provider, either by HANA name or via a
// Crossplane reference to a JWTProvider managed resource.
type JWTProviderRef struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=""
	Name string `json:"name,omitempty"`

	// +kubebuilder:validation:Optional
	ProviderRef *xpv1.Reference `json:"providerRef,omitempty"`
}

// JWTUserMapping defines the mapping of an external JWT identity to a database
// user.
type JWTUserMapping struct {
	// Reference to JWTProvider.
	// +kubebuilder:validation:Optional
	JWTProviderRef `json:",inline"`

	// ExternalIdentity is the value of the claim configured as EXTERNAL
	// IDENTITY on the provider (commonly the user's email or subject ID).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ExternalIdentity string `json:"externalIdentity"`
}

// JWTProvider type metadata.
var (
	JWTProviderKind             = reflect.TypeFor[JWTProvider]().Name()
	JWTProviderGroupKind        = schema.GroupKind{Group: Group, Kind: JWTProviderKind}.String()
	JWTProviderKindAPIVersion   = JWTProviderKind + "." + SchemeGroupVersion.String()
	JWTProviderGroupVersionKind = SchemeGroupVersion.WithKind(JWTProviderKind)
)

func init() {
	SchemeBuilder.Register(&JWTProvider{}, &JWTProviderList{})
}
