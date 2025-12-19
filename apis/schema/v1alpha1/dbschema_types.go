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

// DbSchemaParameters are the configurable fields of a Dbschema.
type DbSchemaParameters struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Pattern:=`^[^",\$\.'\+\-<>|\[\]\{\}\(\)!%*,/:;=\?@\\^~\x60a-z]+$`
	SchemaName string `json:"schemaName"`

	// +kubebuilder:validation:Pattern:=`^[^",\$\.'\+\-<>|\[\]\{\}\(\)!%*,/:;=\?@\\^~\x60a-z]+$`
	Owner string `json:"owner,omitempty"`
}

// DbschemaObservation are the observable fields of a Dbschema.
type DbSchemaObservation struct {
	// +kubebuilder:validation:Optional
	SchemaName string `json:"schemaName,omitempty"`
	Owner      string `json:"owner,omitempty"`
}

// A DbschemaSpec defines the desired state of a Dbschema.
type DbSchemaSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       DbSchemaParameters `json:"forProvider"`
}

// A DbschemaStatus represents the observed state of a Dbschema.
type DbSchemaStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          DbSchemaObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A Dbschema is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,hana}
type DbSchema struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DbSchemaSpec   `json:"spec"`
	Status DbSchemaStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DbschemaList contains a list of Dbschema
type DbSchemaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DbSchema `json:"items"`
}

// Dbschema type metadata.
var (
	DbSchemaKind             = reflect.TypeOf(DbSchema{}).Name()
	DbSchemaGroupKind        = schema.GroupKind{Group: Group, Kind: DbSchemaKind}.String()
	DbSchemaKindAPIVersion   = DbSchemaKind + "." + SchemeGroupVersion.String()
	DbSchemaGroupVersionKind = SchemeGroupVersion.WithKind(DbSchemaKind)
)

func init() {
	SchemeBuilder.Register(
		&DbSchema{},
		&DbSchemaList{},
	)
}
