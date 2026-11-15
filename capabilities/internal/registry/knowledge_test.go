// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package registry

import (
	"strings"
	"testing"
)

func TestKnowledgeLibraryAdmission(t *testing.T) {
	raw := `{"capabilities":["cap-process","cap-ai"],"roles":[{"name":"role_library_user"}],"tables":[],"knowledge":{"version":1,"personalLibrary":{"enabled":true,"allowedRoles":["role_library_user"]}}}`
	a, e := Decode("personal_library", "Library", "1", []byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	if !a.AllowsPersonalLibrary("role_library_user") || a.AllowsPersonalLibrary("role_operator") {
		t.Fatal("library admission mismatch")
	}
	for _, bad := range []string{strings.Replace(raw, `"version":1`, `"version":2`, 1), strings.Replace(raw, `"allowedRoles":["role_library_user"]`, `"allowedRoles":["role_other"]`, 1)} {
		if _, e = Decode("personal_library", "Library", "1", []byte(bad)); e == nil {
			t.Fatal("invalid library accepted")
		}
	}
}
