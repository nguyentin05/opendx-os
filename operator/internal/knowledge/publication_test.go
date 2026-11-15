// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"testing"
)

func packageApp() manifest.DXApplication {
	return manifest.DXApplication{Metadata: manifest.AppMetadata{Name: "personnel", DisplayName: "Personnel", Version: "1.0.0"}, Spec: manifest.AppSpec{Capabilities: []string{"cap-process", "cap-ai"}, Roles: []manifest.RoleDef{{Name: "role_hr"}, {Name: "role_manager"}, {Name: "role_director"}}, Knowledge: &manifest.KnowledgeDef{Version: 1, Sources: []manifest.KnowledgeSourceDef{{ID: "policy", Path: "knowledge/policy.md", MediaType: "text/markdown", IndexingProfile: "standard-text-v1", Access: manifest.KnowledgeAccessDef{Mode: "RESTRICTED", AllowedRoles: []string{"role_hr"}, GrantsManagedBy: "managers"}}}, GrantPolicies: map[string]manifest.GrantPolicyDef{"managers": {AllowedRoles: []string{"role_manager"}}}}}}
}
func next(t *testing.T, p Publication, a manifest.DXApplication) Publication {
	t.Helper()
	n, e := NextPublication(p, a)
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func TestMissedAuthorizationABA(t *testing.T) {
	a := packageApp()
	first := next(t, Publication{}, a)
	a.Spec.Knowledge.Sources[0].Access.Mode = "ROLE"
	a.Spec.Knowledge.Sources[0].Access.GrantsManagedBy = ""
	b := next(t, first, a)
	a = packageApp()
	last := next(t, b, a)
	if last.Sources[0].AuthorizationGeneration <= first.Sources[0].AuthorizationGeneration || last.Sources[0].Incarnation != first.Sources[0].Incarnation {
		t.Fatal("authorization history erased")
	}
	a.Spec.Knowledge.GrantPolicies["managers"] = manifest.GrantPolicyDef{AllowedRoles: []string{"role_director"}}
	managers := next(t, last, a)
	if managers.Sources[0].AuthorizationGeneration <= last.Sources[0].AuthorizationGeneration {
		t.Fatal("resolved manager role change ignored")
	}
	unchanged := next(t, managers, a)
	if unchanged.Generation != managers.Generation {
		t.Fatal("unchanged reconcile republished")
	}
}
func TestReintroducedSourceIncarnation(t *testing.T) {
	a := packageApp()
	first := next(t, Publication{}, a)
	a.Spec.Knowledge = nil
	removed := next(t, first, a)
	readded := next(t, removed, packageApp())
	if readded.Sources[0].Incarnation == first.Sources[0].Incarnation {
		t.Fatal("source identity restored")
	}
	if readded.Generation <= removed.Generation {
		t.Fatal("publication generation went backwards")
	}
}
