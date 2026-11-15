// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package registry

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

func hasKnowledge(k *KnowledgeDef) bool {
	return k != nil && (len(k.Sources) > 0 || k.PersonalLibrary != nil && k.PersonalLibrary.Enabled)
}
func validateKnowledge(k *KnowledgeDef, roles map[string]bool, ai bool) error {
	if k == nil {
		return nil
	}
	if k.Version != 1 || !ai || !hasKnowledge(k) {
		return fmt.Errorf("knowledge requires v1, cap-ai and an enabled feature")
	}
	checkRoles := func(rs []string) error {
		seen := map[string]bool{}
		if len(rs) == 0 {
			return fmt.Errorf("knowledge roles required")
		}
		for _, r := range rs {
			if !roles[r] || seen[r] {
				return fmt.Errorf("invalid knowledge role %q", r)
			}
			seen[r] = true
		}
		return nil
	}
	if l := k.PersonalLibrary; l != nil {
		if err := checkRoles(l.AllowedRoles); err != nil {
			return err
		}
	}
	for id, p := range k.GrantPolicies {
		if !regexp.MustCompile(`^[a-z][a-z0-9_-]*$`).MatchString(id) {
			return fmt.Errorf("invalid grant policy")
		}
		if err := checkRoles(p.AllowedRoles); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, s := range k.Sources {
		if !regexp.MustCompile(`^[a-z][a-z0-9_-]*$`).MatchString(s.ID) || seen[s.ID] {
			return fmt.Errorf("invalid source id")
		}
		seen[s.ID] = true
		if !strings.HasPrefix(s.Path, "knowledge/") || strings.Contains(s.Path, "\\") || path.Clean(s.Path) != s.Path || strings.Contains(s.Path, "..") {
			return fmt.Errorf("unsafe knowledge source path")
		}
		if s.MediaType != "text/markdown" && s.MediaType != "application/pdf" {
			return fmt.Errorf("unsupported knowledge media type")
		}
		if s.MediaType == "text/markdown" && !strings.HasSuffix(s.Path, ".md") || s.MediaType == "application/pdf" && !strings.HasSuffix(s.Path, ".pdf") {
			return fmt.Errorf("knowledge media/path mismatch")
		}
		if s.IndexingProfile != "standard-text-v1" {
			return fmt.Errorf("unsupported indexing profile")
		}
		if err := checkRoles(s.Access.AllowedRoles); err != nil {
			return err
		}
		switch s.Access.Mode {
		case "ROLE":
			if s.Access.GrantsManagedBy != "" {
				return fmt.Errorf("ROLE cannot manage grants")
			}
		case "RESTRICTED":
			if _, ok := k.GrantPolicies[s.Access.GrantsManagedBy]; !ok {
				return fmt.Errorf("unknown grant policy")
			}
		default:
			return fmt.Errorf("invalid knowledge mode")
		}
	}
	return nil
}

// AllowsPersonalLibrary is declaration admission; callers must separately verify current DB grants.
func (a *AppDefinition) AllowsPersonalLibrary(role string) bool {
	k := a.Spec.Knowledge
	if k == nil || k.Version != 1 || k.PersonalLibrary == nil || !k.PersonalLibrary.Enabled {
		return false
	}
	for _, r := range k.PersonalLibrary.AllowedRoles {
		if r == role {
			return true
		}
	}
	return false
}
