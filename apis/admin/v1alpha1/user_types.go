/*
Copyright 2025 SAP SE.
*/

package v1alpha1

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"
)

// Authentication includes different authentication methods
type Authentication struct {
	Password Password `json:"password,omitempty"`
}

// Password authentication type
type Password struct {
	PasswordSecretRef        *xpv1.SecretKeySelector `json:"passwordSecretRef,omitempty"`
	ForceFirstPasswordChange bool                    `json:"forceFirstPasswordChange,omitempty"`
}

// UserParameters are the configurable fields of a User.
type UserParameters struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:validation:Pattern:=`^[^",\$\.'\+\-<>|\[\]\{\}\(\)!%*,/:;=\?@\\^~\x60a-z]+$`
	Username string `json:"username"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	// +kubebuilder:default:=false
	RestrictedUser bool `json:"restrictedUser" default:"false"`

	Authentication Authentication `json:"authentication,omitempty"`

	// +listType=set
	Privileges []string `json:"privileges,omitempty"`

	// +listType=set
	Roles []string `json:"roles,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Value is immutable"
	Parameters map[string]string `json:"parameters,omitempty"`

	// +kubebuilder:validation:Pattern:=`^[^",\$\.'\+\-<>|\[\]\{\}\(\)!%*,/:;=\?@\\^~\x60a-z]+$`
	// +kubebuilder:default:=DEFAULT
	Usergroup string `json:"usergroup,omitempty" default:"DEFAULT"`

	// +kubebuilder:default:=true
	// +kubebuilder:validation:Optional
	IsPasswordLifetimeCheckEnabled bool `json:"isPasswordLifetimeCheckEnabled" default:"true"`
}

// UserObservation are the observable fields of a User.
type UserObservation struct {
	// +kubebuilder:validation:Optional
	Username *string `json:"username,omitempty"`

	// +kubebuilder:validation:Optional
	RestrictedUser *bool `json:"restrictedUser,omitempty"`

	// +kubebuilder:validation:Optional
	LastPasswordChangeTime metav1.Time `json:"lastPasswordChangeTime,omitempty"`

	// +kubebuilder:validation:Optional
	PasswordUpToDate *bool `json:"passwordUpToDate,omitempty"`

	// +kubebuilder:validation:Optional
	CreatedAt metav1.Time `json:"createdAt,omitempty"`

	// +kubebuilder:validation:Optional
	Privileges []string `json:"privileges,omitempty"`

	// +kubebuilder:validation:Optional
	Roles []string `json:"roles,omitempty"`

	// +kubebuilder:validation:Optional
	Parameters map[string]string `json:"parameters,omitempty"`

	// +kubebuilder:validation:Optional
	Usergroup *string `json:"usergroup,omitempty"`

	// +kubebuilder:validation:Optional
	IsPasswordLifetimeCheckEnabled *bool `json:"isPasswordLifetimeCheckEnabled,omitempty"`
}

// A UserSpec defines the desired state of a User.
type UserSpec struct {
	xpv1.ResourceSpec `json:",inline"`
	ForProvider       UserParameters `json:"forProvider"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=strict;lax
	// +kubebuilder:default:=strict
	// PrivilegeManagementPolicy defines the privilege management policy for the user.
	// 'strict' means that all privileges are managed by crossplane, and other privileges not defined in the spec will be removed.
	// 'lax' means that crossplane will only manage the privileges defined in the spec, and other privileges will not be removed.
	PrivilegeManagementPolicy string `json:"privilegeManagementPolicy,omitempty"`
}

// A UserStatus represents the observed state of a User.
type UserStatus struct {
	xpv1.ResourceStatus `json:",inline"`
	AtProvider          UserObservation `json:"atProvider,omitempty"`
}

// +kubebuilder:object:root=true

// A User is an example API type.
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="SYNCED",type="string",JSONPath=".status.conditions[?(@.type=='Synced')].status"
// +kubebuilder:printcolumn:name="EXTERNAL-NAME",type="string",JSONPath=".metadata.annotations.crossplane\\.io/external-name"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories={crossplane,managed,sql}
type User struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UserSpec   `json:"spec"`
	Status UserStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// UserList contains a list of User
type UserList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []User `json:"items"`
}

// User type metadata.
var (
	UserKind             = reflect.TypeOf(User{}).Name()
	UserGroupKind        = schema.GroupKind{Group: Group, Kind: UserKind}.String()
	UserKindAPIVersion   = UserKind + "." + SchemeGroupVersion.String()
	UserGroupVersionKind = SchemeGroupVersion.WithKind(UserKind)
)

func init() {
	SchemeBuilder.Register(
		&User{},
		&UserList{},
	)
}
