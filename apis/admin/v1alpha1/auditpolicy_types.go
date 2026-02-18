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

// AuditPolicyParameters are the configurable fields of a AuditPolicy.
type AuditPolicyParameters struct {
	PolicyName string `json:"policyName"`

	// +kubebuilder:validation:items:Pattern:=`^[^",\$\.'\+\-<>|\[\]\{\}\(\)!%*,/:;=\?@\\^~\x60]+$`
	// +listType=set
	AuditActions []string `json:"auditActions"`

	// +kubebuilder:default:=ALL
	// +kubebuilder:validation:Enum:=SUCCESSFUL;UNSUCCESSFUL;ALL
	AuditStatus string `json:"auditStatus,omitempty"`

	// +kubebuilder:default:=CRITICAL
	// +kubebuilder:validation:Enum:=EMERGENCY;ALERT;CRITICAL;WARNING;INFO
	AuditLevel string `json:"auditLevel,omitempty"`

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

	// +kubebuilder:validation:Optional
	AuditTrailRetention *int `json:"auditTrailRetention,omitempty"`

	// +kubebuilder:validation:Optional
	Enabled *bool `json:"enabled,omitempty"`
}

// A AuditPolicySpec defines the desired state of a AuditPolicy.
type AuditPolicySpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       AuditPolicyParameters `json:"forProvider"`
}

// A AuditPolicyStatus represents the observed state of a AuditPolicy.
type AuditPolicyStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          AuditPolicyObservation `json:"atProvider,omitempty"`
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
	AuditPolicyKind             = reflect.TypeOf(AuditPolicy{}).Name()
	AuditPolicyGroupKind        = schema.GroupKind{Group: Group, Kind: AuditPolicyKind}.String()
	AuditPolicyKindAPIVersion   = AuditPolicyKind + "." + SchemeGroupVersion.String()
	AuditPolicyGroupVersionKind = SchemeGroupVersion.WithKind(AuditPolicyKind)
)

func init() {
	SchemeBuilder.Register(&AuditPolicy{}, &AuditPolicyList{})
}
