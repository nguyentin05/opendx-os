// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package manifest

import (
	"bytes"
	"encoding/json"
)

type KnowledgeDef struct {
	Version         int                       `yaml:"version" json:"version"`
	PersonalLibrary *PersonalLibraryDef       `yaml:"personalLibrary,omitempty" json:"personalLibrary,omitempty"`
	Sources         []KnowledgeSourceDef      `yaml:"sources,omitempty" json:"sources,omitempty"`
	GrantPolicies   map[string]GrantPolicyDef `yaml:"grantPolicies,omitempty" json:"grantPolicies,omitempty"`
}
type PersonalLibraryDef struct {
	Enabled      bool     `yaml:"enabled" json:"enabled"`
	AllowedRoles []string `yaml:"allowedRoles" json:"allowedRoles"`
}
type KnowledgeSourceDef struct {
	ID              string             `yaml:"id" json:"id"`
	Path            string             `yaml:"path" json:"path"`
	MediaType       string             `yaml:"mediaType" json:"mediaType"`
	Access          KnowledgeAccessDef `yaml:"access" json:"access"`
	IndexingProfile string             `yaml:"indexingProfile" json:"indexingProfile"`
}
type KnowledgeAccessDef struct {
	Mode            string   `yaml:"mode" json:"mode"`
	AllowedRoles    []string `yaml:"allowedRoles" json:"allowedRoles"`
	GrantsManagedBy string   `yaml:"grantsManagedBy,omitempty" json:"grantsManagedBy,omitempty"`
}
type GrantPolicyDef struct {
	AllowedRoles []string `yaml:"allowedRoles" json:"allowedRoles"`
}

func (k *KnowledgeDef) UnmarshalJSON(raw []byte) error {
	type plain KnowledgeDef
	var value plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&value); err != nil {
		return err
	}
	*k = KnowledgeDef(value)
	return nil
}
