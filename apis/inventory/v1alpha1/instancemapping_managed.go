/*
Copyright 2026 SAP SE.
*/

package v1alpha1

import xpv1 "github.com/crossplane/crossplane-runtime/apis/common/v1"

// Manual implementation of resource.Managed interface for InstanceMapping.
// This is needed because InstanceMapping does NOT embed xpv1.ResourceSpec
// since it doesn't require a ProviderConfig.

// GetCondition of this InstanceMapping.
func (mg *InstanceMapping) GetCondition(ct xpv1.ConditionType) xpv1.Condition {
	return mg.Status.GetCondition(ct)
}

// GetDeletionPolicy of this InstanceMapping.
// Returns Delete by default - when the CR is deleted, the mapping is deleted.
func (mg *InstanceMapping) GetDeletionPolicy() xpv1.DeletionPolicy {
	return xpv1.DeletionDelete
}

// GetManagementPolicies of this InstanceMapping.
// Returns full management by default.
func (mg *InstanceMapping) GetManagementPolicies() xpv1.ManagementPolicies {
	return xpv1.ManagementPolicies{xpv1.ManagementActionAll}
}

// GetProviderConfigReference of this InstanceMapping.
// Returns nil because InstanceMapping doesn't use ProviderConfig.
func (mg *InstanceMapping) GetProviderConfigReference() *xpv1.Reference {
	return nil
}

// GetPublishConnectionDetailsTo of this InstanceMapping.
// Returns nil - InstanceMapping doesn't publish connection details.
func (mg *InstanceMapping) GetPublishConnectionDetailsTo() *xpv1.PublishConnectionDetailsTo {
	return nil
}

// GetWriteConnectionSecretToReference of this InstanceMapping.
// Returns nil - InstanceMapping doesn't write connection secrets.
func (mg *InstanceMapping) GetWriteConnectionSecretToReference() *xpv1.SecretReference {
	return nil
}

// SetConditions of this InstanceMapping.
func (mg *InstanceMapping) SetConditions(c ...xpv1.Condition) {
	mg.Status.SetConditions(c...)
}

// SetDeletionPolicy of this InstanceMapping.
// No-op: InstanceMapping uses fixed deletion policy.
func (mg *InstanceMapping) SetDeletionPolicy(_ xpv1.DeletionPolicy) {
	// No-op: InstanceMapping doesn't support configurable deletion policy
}

// SetManagementPolicies of this InstanceMapping.
// No-op: InstanceMapping uses fixed management policies.
func (mg *InstanceMapping) SetManagementPolicies(_ xpv1.ManagementPolicies) {
	// No-op: InstanceMapping doesn't support configurable management policies
}

// SetProviderConfigReference of this InstanceMapping.
// No-op: InstanceMapping doesn't use ProviderConfig.
func (mg *InstanceMapping) SetProviderConfigReference(_ *xpv1.Reference) {
	// No-op: InstanceMapping doesn't use ProviderConfig
}

// SetPublishConnectionDetailsTo of this InstanceMapping.
// No-op: InstanceMapping doesn't publish connection details.
func (mg *InstanceMapping) SetPublishConnectionDetailsTo(_ *xpv1.PublishConnectionDetailsTo) {
	// No-op
}

// SetWriteConnectionSecretToReference of this InstanceMapping.
// No-op: InstanceMapping doesn't write connection secrets.
func (mg *InstanceMapping) SetWriteConnectionSecretToReference(_ *xpv1.SecretReference) {
	// No-op
}
