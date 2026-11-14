// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package planner

import (
	"encoding/json"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"strings"
	"testing"
)

func TestPrivateHasuraLiveGuards(t *testing.T) {
	a := &manifest.DXApplication{Metadata: manifest.AppMetadata{Name: "library"}, Spec: manifest.AppSpec{Knowledge: &manifest.KnowledgeDef{Version: 1, PersonalLibrary: &manifest.PersonalLibraryDef{Enabled: true, AllowedRoles: []string{"role_library_user"}}}}}
	raw, _ := json.Marshal(sharedFilter(a, "core.files", "role_library_user"))
	s := string(raw)
	for _, required := range []string{"read_policy", "OWNER_ONLY", "APP_ATTACHMENT", "ACTIVE", "Ready", "user_roles", "role_library_user", "X-Hasura-User-Id"} {
		if !strings.Contains(s, required) {
			t.Fatalf("private metadata lacks %s: %s", required, s)
		}
	}
}
