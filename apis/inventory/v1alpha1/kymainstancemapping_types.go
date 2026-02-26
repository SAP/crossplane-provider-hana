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

// SecretReference references a Secret in a specific namespace
type SecretReference struct {
	// Name is the name of the Secret
	Name string `json:"name"`
	// Namespace is the namespace of the Secret
	Namespace string `json:"namespace"`
}

// ResourceReference references a Kubernetes resource in a specific namespace
type ResourceReference struct {
	// Name is the name of the resource
	Name string `json:"name"`
	// Namespace is the namespace of the resource
	Namespace string `json:"namespace"`
}

// KymaConnectionReference describes how to connect to the remote Kyma cluster
type KymaConnectionReference struct {
	// SecretRef references a Secret containing the kubeconfig on the management cluster
	SecretRef SecretReference `json:"secretRef"`

	// KubeconfigKey is the key in the secret containing kubeconfig data
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="kubeconfig"
	KubeconfigKey string `json:"kubeconfigKey,omitempty"`
}

// KymaInstanceMappingParameters are the configurable fields of a KymaInstanceMapping.
type KymaInstanceMappingParameters struct {
	// KymaConnectionRef references the kubeconfig secret for connecting to a remote Kyma cluster.
	// If not specified, the controller uses the local cluster where it's running.
	// +kubebuilder:validation:Optional
	KymaConnectionRef *KymaConnectionReference `json:"kymaConnectionRef,omitempty"`

	// AdminBindingRef references the ServiceBinding that provides admin API credentials
	// +kubebuilder:validation:Required
	AdminBindingRef ResourceReference `json:"adminBindingRef"`

	// ServiceInstanceRef references the ServiceInstance (to extract instanceID)
	// +kubebuilder:validation:Required
	ServiceInstanceRef ResourceReference `json:"serviceInstanceRef"`

	// TargetNamespace is the Kubernetes namespace to map (immutable)
	// If not specified, defaults to the namespace of the ServiceInstance
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="targetNamespace is immutable"
	TargetNamespace *string `json:"targetNamespace,omitempty"`

	// ClusterIDConfigMapRef references the ConfigMap containing CLUSTER_ID
	// Defaults to kyma-system/sap-btp-operator-config if not specified
	// +kubebuilder:validation:Optional
	ClusterIDConfigMapRef *ResourceReference `json:"clusterIdConfigMapRef,omitempty"`

	// IsDefault sets this mapping as the default for the namespace
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	IsDefault bool `json:"isDefault,omitempty"`

	// CredentialsSecretNamespace is the namespace where the intermediate credentials
	// Secret and InstanceMapping CR will be created.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="crossplane-system"
	CredentialsSecretNamespace string `json:"credentialsSecretNamespace,omitempty"`
}

// KymaClusterObservation contains information extracted from the remote Kyma cluster
type KymaClusterObservation struct {
	// ServiceInstanceID is the GUID extracted from the ServiceInstance status
	// +kubebuilder:validation:Optional
	ServiceInstanceID string `json:"serviceInstanceID,omitempty"`

	// ClusterID is extracted from the BTP operator ConfigMap
	// +kubebuilder:validation:Optional
	ClusterID string `json:"clusterID,omitempty"`

	// ServiceInstanceName is the name of the ServiceInstance on the Kyma cluster
	// +kubebuilder:validation:Optional
	ServiceInstanceName string `json:"serviceInstanceName,omitempty"`

	// ServiceInstanceReady indicates if the ServiceInstance on Kyma is ready
	// +kubebuilder:validation:Optional
	ServiceInstanceReady bool `json:"serviceInstanceReady,omitempty"`
}

// MappingID uniquely identifies a mapping in the HANA Cloud API
type MappingID struct {
	// ServiceInstanceID is the GUID of the HANA Cloud instance
	ServiceInstanceID string `json:"serviceInstanceID"`
	// PrimaryID is the cluster ID
	PrimaryID string `json:"primaryID"`
	// SecondaryID is the namespace (optional)
	// +kubebuilder:validation:Optional
	SecondaryID *string `json:"secondaryID,omitempty"`
}

// HANACloudObservation contains information about the HANA Cloud mapping
type HANACloudObservation struct {
	// MappingID contains the full mapping identifier as known by HANA Cloud
	// +kubebuilder:validation:Optional
	MappingID *MappingID `json:"mappingId,omitempty"`

	// Ready indicates if the mapping is active and ready
	// +kubebuilder:validation:Optional
	Ready bool `json:"ready,omitempty"`
}

// ChildResourcesReference contains references to child resources created by KymaInstanceMapping
type ChildResourcesReference struct {
	// InstanceMappingName is the name of the created InstanceMapping CR
	// +kubebuilder:validation:Optional
	InstanceMappingName string `json:"instanceMappingName,omitempty"`

	// CredentialsSecretName is the name of the created credentials Secret
	// +kubebuilder:validation:Optional
	CredentialsSecretName string `json:"credentialsSecretName,omitempty"`

	// CredentialsSecretNamespace is the namespace of the created credentials Secret
	// +kubebuilder:validation:Optional
	CredentialsSecretNamespace string `json:"credentialsSecretNamespace,omitempty"`

	// InstanceMappingReady indicates if the child InstanceMapping is ready
	// +kubebuilder:validation:Optional
	InstanceMappingReady bool `json:"instanceMappingReady,omitempty"`

	// InstanceMappingSynced indicates if the child InstanceMapping is synced
	// +kubebuilder:validation:Optional
	InstanceMappingSynced bool `json:"instanceMappingSynced,omitempty"`
}

// KymaInstanceMappingObservation are the observable fields of a KymaInstanceMapping.
type KymaInstanceMappingObservation struct {
	// Kyma contains information extracted from the remote Kyma cluster
	// +kubebuilder:validation:Optional
	Kyma *KymaClusterObservation `json:"kyma,omitempty"`

	// Hana contains information about the HANA Cloud mapping status
	// +kubebuilder:validation:Optional
	Hana *HANACloudObservation `json:"hana,omitempty"`

	// ChildResources contains references to the created child resources (Secret and InstanceMapping)
	// +kubebuilder:validation:Optional
	ChildResources *ChildResourcesReference `json:"childResources,omitempty"`
}

// A KymaInstanceMappingSpec defines the desired state of a KymaInstanceMapping.
type KymaInstanceMappingSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       KymaInstanceMappingParameters `json:"forProvider"`
}

// A KymaInstanceMappingStatus represents the observed state of a KymaInstanceMapping.
type KymaInstanceMappingStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          KymaInstanceMappingObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A KymaInstanceMapping maps a HANA Cloud database instance from a remote Kyma cluster to a namespace.
// It runs on a management cluster and connects to a remote Kyma cluster to fetch ServiceInstance,
// ServiceBinding, and ConfigMap resources to create the mapping.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="CLUSTER-ID",type="string",JSONPath=".status.atProvider.kyma.clusterID"
// +kubebuilder:printcolumn:name="SERVICE-INSTANCE",type="string",JSONPath=".status.atProvider.kyma.serviceInstanceID"
// +kubebuilder:printcolumn:name="NAMESPACE",type="string",JSONPath=".spec.forProvider.targetNamespace"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,inventory}
type KymaInstanceMapping struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KymaInstanceMappingSpec   `json:"spec"`
	Status KymaInstanceMappingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KymaInstanceMappingList contains a list of KymaInstanceMapping
type KymaInstanceMappingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KymaInstanceMapping `json:"items"`
}

// KymaInstanceMapping type metadata.
var (
	KymaInstanceMappingKind             = reflect.TypeOf(KymaInstanceMapping{}).Name()
	KymaInstanceMappingGroupKind        = schema.GroupKind{Group: Group, Kind: KymaInstanceMappingKind}.String()
	KymaInstanceMappingKindAPIVersion   = KymaInstanceMappingKind + "." + SchemeGroupVersion.String()
	KymaInstanceMappingGroupVersionKind = SchemeGroupVersion.WithKind(KymaInstanceMappingKind)
)

func init() {
	SchemeBuilder.Register(
		&KymaInstanceMapping{},
		&KymaInstanceMappingList{},
	)
}
