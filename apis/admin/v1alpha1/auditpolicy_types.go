/*
Copyright 2026 SAP SE or an SAP affiliate company and contributors.
*/

package v1alpha1

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv2 "github.com/crossplane/crossplane/apis/v2/core/v2"
)

// AuditPrincipal identifies a single principal an audit policy applies to. It
// maps to one "USER <name>" or "USERGROUP <name>" entry of the principal list
// in a CREATE AUDIT POLICY statement.
// +kubebuilder:validation:XValidation:rule="self.type != 'USERGROUP' || !self.name.contains('-')",message="a USERGROUP name must not contain '-'"
type AuditPrincipal struct {
	// Type selects whether the principal is a single user or a user group.
	// +kubebuilder:validation:Enum:=USER;USERGROUP
	Type string `json:"type"`

	// Name is the name of the user or user group. It must be a valid HANA
	// identifier; the pattern matches the Username rule from the User resource.
	// USERGROUP names are additionally not allowed to contain '-' (see the
	// object-level validation), matching the Usergroup rule.
	// +kubebuilder:validation:MaxLength:=127
	// +kubebuilder:validation:Pattern:=`^[^",\$\.'\+<>|\[\]\{\}\(\)!%*,/:;=\?@\\^~\x60]+$`
	Name string `json:"name"`
}

// AuditPolicyParameters are the configurable fields of a AuditPolicy.
type AuditPolicyParameters struct {
	PolicyName string `json:"policyName"`

	// +kubebuilder:validation:items:Pattern:=`^[^",\$'\+<>|\[\]\{\}\(\)!%,/:;=\?@\\^~\x60]+$`
	// +listType=set
	AuditActions []string `json:"auditActions"`

	// +kubebuilder:default:=ALL
	// +kubebuilder:validation:Enum:=SUCCESSFUL;UNSUCCESSFUL;ALL
	AuditStatus string `json:"auditStatus,omitempty"`

	// +kubebuilder:default:=CRITICAL
	// +kubebuilder:validation:Enum:=EMERGENCY;ALERT;CRITICAL;WARNING;INFO
	AuditLevel string `json:"auditLevel,omitempty"`

	// AuditPrincipals is an optional, ordered list of principals (users and/or
	// user groups) the audit policy applies to. It maps to the principal list of
	// the "[EXCEPT] FOR PRINCIPALS ..." clause of the CREATE AUDIT POLICY
	// statement and can mix users and user groups in any order, for example:
	// "FOR PRINCIPALS USER user1, USERGROUP usergroup1, USER user2".
	// Combine it with ExceptPrincipals to render "EXCEPT FOR PRINCIPALS ..."
	// instead. Principals can be combined with any audit action; note that the
	// ACTIONS audit action in turn requires a principal list.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems:=256
	AuditPrincipals []AuditPrincipal `json:"auditPrincipals,omitempty"`

	// ExceptPrincipals, when set to true, renders the principal clause as
	// "EXCEPT FOR PRINCIPALS ..." instead of "FOR PRINCIPALS ...", meaning the
	// audit policy applies to everyone except the configured principals. It has
	// no effect when no principals are configured.
	// +kubebuilder:default:=false
	// +kubebuilder:validation:Optional
	ExceptPrincipals bool `json:"exceptPrincipals,omitempty"`

	// +kubebuilder:default:=7
	AuditTrailRetention *int `json:"auditTrailRetention,omitempty"`

	// +kubebuilder:default:=false
	Enabled *bool `json:"enabled,omitempty"`
}

// AuditPolicyObservation are the observable fields of a AuditPolicy.
type AuditPolicyObservation struct {

	// +kubebuilder:validation:Optional
	PolicyName string `json:"policyName,omitempty"`

	// +kubebuilder:validation:Optional
	AuditActions []string `json:"auditActions"`

	// +kubebuilder:validation:Optional
	AuditStatus string `json:"auditStatus,omitempty"`

	// +kubebuilder:validation:Optional
	AuditLevel string `json:"auditLevel,omitempty"`

	// AuditPrincipals is the list of principals observed from HANA that the
	// audit policy applies to. It is derived from the principal columns of the
	// AUDIT_POLICIES system view and is used to detect drift against the
	// desired AuditPrincipals.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems:=256
	AuditPrincipals []AuditPrincipal `json:"auditPrincipals,omitempty"`

	// ExceptPrincipals reflects whether the observed principal clause is an
	// "EXCEPT FOR PRINCIPALS ..." clause rather than a "FOR PRINCIPALS ..."
	// clause.
	// +kubebuilder:validation:Optional
	ExceptPrincipals bool `json:"exceptPrincipals,omitempty"`

	// +kubebuilder:validation:Optional
	AuditTrailRetention *int `json:"auditTrailRetention,omitempty"`

	// +kubebuilder:validation:Optional
	Enabled *bool `json:"enabled,omitempty"`
}

// A AuditPolicySpec defines the desired state of a AuditPolicy.
type AuditPolicySpec struct {
	xpv2.ClusterManagedResourceSpec `json:",inline"`
	ForProvider                     AuditPolicyParameters `json:"forProvider"`
}

// A AuditPolicyStatus represents the observed state of a AuditPolicy.
type AuditPolicyStatus struct {
	xpv2.ManagedResourceStatus `json:",inline"`
	AtProvider                 AuditPolicyObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A AuditPolicy is a managed resource for managing HANA audit policies.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hana}
type AuditPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AuditPolicySpec   `json:"spec"`
	Status AuditPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AuditPolicyList contains a list of AuditPolicy
type AuditPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AuditPolicy `json:"items"`
}

// AuditPolicy type metadata.
var (
	AuditPolicyKind             = reflect.TypeFor[AuditPolicy]().Name()
	AuditPolicyGroupKind        = schema.GroupKind{Group: Group, Kind: AuditPolicyKind}.String()
	AuditPolicyKindAPIVersion   = AuditPolicyKind + "." + SchemeGroupVersion.String()
	AuditPolicyGroupVersionKind = SchemeGroupVersion.WithKind(AuditPolicyKind)
)

func init() {
	SchemeBuilder.Register(&AuditPolicy{}, &AuditPolicyList{})
}
