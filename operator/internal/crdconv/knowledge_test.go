// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package crdconv

import (
	"encoding/json"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"path/filepath"
	"testing"
)

func TestKnowledgeRoundTrip(t *testing.T) {
	cr, err := FromPackage(filepath.Join("..", "..", "..", "apps", "personal-library"))
	if err != nil {
		t.Fatal(err)
	}
	copy := cr.DeepCopy()
	copy.Spec.Knowledge.PersonalLibrary.AllowedRoles[0] = "role_other"
	if cr.Spec.Knowledge.PersonalLibrary.AllowedRoles[0] != "role_library_user" {
		t.Fatal("deepcopy aliased library roles")
	}
	app, _, err := ToManifest(cr)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(app.Spec)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := manifest.DecodeStoredSpec(raw)
	if err != nil || stored.Knowledge.Version != 1 || len(stored.Tables) != 0 {
		t.Fatal("CRD/stored knowledge lost", err)
	}
}
