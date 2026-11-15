// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package v1alpha1

type KnowledgeDef struct {
	// +kubebuilder:validation:Enum=1
	Version         int                       `yaml:"version" json:"version"`
	PersonalLibrary *PersonalLibraryDef       `yaml:"personalLibrary,omitempty" json:"personalLibrary,omitempty"`
	Sources         []KnowledgeSourceDef      `yaml:"sources,omitempty" json:"sources,omitempty"`
	GrantPolicies   map[string]GrantPolicyDef `yaml:"grantPolicies,omitempty" json:"grantPolicies,omitempty"`
}
type PersonalLibraryDef struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^role_[a-z0-9_]+$`
	AllowedRoles []string `yaml:"allowedRoles" json:"allowedRoles"`
}
type KnowledgeSourceDef struct {
	ID   string `yaml:"id" json:"id"`
	Path string `yaml:"path" json:"path"`
	// +kubebuilder:validation:Enum=text/markdown;application/pdf
	MediaType string             `yaml:"mediaType" json:"mediaType"`
	Access    KnowledgeAccessDef `yaml:"access" json:"access"`
	// +kubebuilder:validation:Enum=standard-text-v1
	IndexingProfile string `yaml:"indexingProfile" json:"indexingProfile"`
}
type KnowledgeAccessDef struct {
	// +kubebuilder:validation:Enum=ROLE;RESTRICTED
	Mode string `yaml:"mode" json:"mode"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^role_[a-z0-9_]+$`
	AllowedRoles    []string `yaml:"allowedRoles" json:"allowedRoles"`
	GrantsManagedBy string   `yaml:"grantsManagedBy,omitempty" json:"grantsManagedBy,omitempty"`
}
type GrantPolicyDef struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^role_[a-z0-9_]+$`
	AllowedRoles []string `yaml:"allowedRoles" json:"allowedRoles"`
}
