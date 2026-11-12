// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"encoding/json"
	"strings"

	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// inSync reports whether the active definition equals desired and exactly the
// listed workspaces publish it as Ready at this version. A true result skips
// DDL and metadata writes; Hasura-side drift is detected in a later task.
func inSync(desired *manifest.DXApplication, live planner.ManagedState, rows []apply.RegistryRow, workspaces []string) bool {
	want, ok := canonical(desired)
	if !ok {
		return false
	}
	definitionMatches := false
	for _, a := range live.ActiveApps {
		if a.Metadata.Name == desired.Metadata.Name {
			got, ok := canonical(a)
			definitionMatches = ok && got == want
		}
	}
	if !definitionMatches {
		return false
	}

	wantSpec, ok := canonical(desired.Spec)
	if !ok {
		return false
	}
	published := map[string]bool{}
	for _, row := range rows {
		if row.AppID != desired.Metadata.Name {
			continue
		}
		gotSpec, ok := canonicalRaw(row.Spec)
		if !ok || row.Status != "Ready" || row.Version != desired.Metadata.Version || gotSpec != wantSpec {
			return false
		}
		published[strings.ToLower(row.WorkspaceID)] = true
	}
	if len(published) != len(workspaces) {
		return false
	}
	for _, ws := range workspaces {
		if !published[strings.ToLower(ws)] {
			return false
		}
	}
	return true
}

// canonical renders a value as JSON with sorted keys and normalized numbers, so a
// manifest parsed from YAML compares equal to one decoded from the database.
func canonical(v any) (string, bool) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return canonicalRaw(raw)
}

func canonicalRaw(raw []byte) (string, bool) {
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return "", false
	}
	out, err := json.Marshal(generic)
	if err != nil {
		return "", false
	}
	return string(out), true
}
