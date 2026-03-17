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

// AdminCredentialsSecretRef references a Secret containing admin API credentials
type AdminCredentialsSecretRef struct {
	// Name is the name of the Secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the Secret
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// Key is the key in the secret containing the JSON credentials.
	// The JSON must contain: {"baseurl": "...", "uaa": {"url": "...", "clientid": "...", "clientsecret": "..."}}
	// +kubebuilder:validation:Required
	Key string `json:"key"`
}

// InstanceMappingParameters are the configurable fields of an InstanceMapping.
type InstanceMappingParameters struct {
	// ServiceInstanceID is the GUID of the HANA Cloud service instance
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="serviceInstanceID is immutable"
	ServiceInstanceID string `json:"serviceInstanceID"`

	// Platform is the deployment platform ("kubernetes" or "cloudfoundry")
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=kubernetes;cloudfoundry
	Platform string `json:"platform"`

	// PrimaryID is the cluster identifier (for kubernetes) or org GUID (for cloudfoundry)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="primaryID is immutable"
	PrimaryID string `json:"primaryID"`

	// SecondaryID is the namespace (for kubernetes) or space GUID (for cloudfoundry)
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="secondaryID is immutable"
	SecondaryID *string `json:"secondaryID,omitempty"`

	// IsDefault sets this mapping as the default for the primary ID
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	IsDefault bool `json:"isDefault,omitempty"`

	// AdminCredentialsSecretRef references a Secret containing admin API credentials
	// +kubebuilder:validation:Required
	AdminCredentialsSecretRef AdminCredentialsSecretRef `json:"adminCredentialsSecretRef"`
}

// InstanceMappingObservation are the observable fields of an InstanceMapping.
type InstanceMappingObservation struct {
	// MappingExists indicates if the mapping exists in HANA Cloud
	// +kubebuilder:validation:Optional
	MappingExists bool `json:"mappingExists,omitempty"`

	// LastSyncTime is the timestamp of the last successful sync
	// +kubebuilder:validation:Optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// InstanceMappingSpec defines the desired state of an InstanceMapping.
// NOTE: InstanceMapping does NOT embed xpv1.ResourceSpec because it does not
// require a ProviderConfig. All credentials come from AdminCredentialsSecretRef.
type InstanceMappingSpec struct {
	ForProvider InstanceMappingParameters `json:"forProvider"`
}

// InstanceMappingStatus represents the observed state of an InstanceMapping.
type InstanceMappingStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          InstanceMappingObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// InstanceMapping is a low-level resource that directly manages HANA Cloud instance mappings.
// It takes raw parameters and admin API credentials to create/delete mappings.
// For Kyma environments, use KymaInstanceMapping which automatically extracts
// the required data from Kyma resources and creates an InstanceMapping.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="PLATFORM",type="string",JSONPath=".spec.forProvider.platform"
// +kubebuilder:printcolumn:name="INSTANCE-ID",type="string",JSONPath=".spec.forProvider.serviceInstanceID"
// +kubebuilder:printcolumn:name="PRIMARY-ID",type="string",JSONPath=".spec.forProvider.primaryID"
// +kubebuilder:printcolumn:name="SECONDARY-ID",type="string",JSONPath=".spec.forProvider.secondaryID"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,inventory}
type InstanceMapping struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InstanceMappingSpec   `json:"spec"`
	Status InstanceMappingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InstanceMappingList contains a list of InstanceMapping
type InstanceMappingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InstanceMapping `json:"items"`
}

// InstanceMapping type metadata.
var (
	InstanceMappingKind             = reflect.TypeOf(InstanceMapping{}).Name()
	InstanceMappingGroupKind        = schema.GroupKind{Group: Group, Kind: InstanceMappingKind}.String()
	InstanceMappingKindAPIVersion   = InstanceMappingKind + "." + SchemeGroupVersion.String()
	InstanceMappingGroupVersionKind = SchemeGroupVersion.WithKind(InstanceMappingKind)
)

func init() {
	SchemeBuilder.Register(
		&InstanceMapping{},
		&InstanceMappingList{},
	)
}
