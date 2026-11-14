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
	"github.com/opendx-os/opendx-os/operator/internal/apply"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

// inSync reports whether core.apps already holds desired as the Ready
// definition of the application. Roles and capabilities are written in the
// same transaction as that row, so they match too. A true result skips DDL and
// registry writes; Hasura-side drift is checked separately.
func inSync(desired *manifest.DXApplication, rows []apply.AppRow) bool {
	want, ok := planner.Canonical(desired.Spec)
	if !ok {
		return false
	}
	for _, row := range rows {
		if row.AppID != desired.Metadata.Name {
			continue
		}
		if row.Status != "Ready" || row.Version != desired.Metadata.Version {
			return false
		}
		got, ok := planner.Canonical(row.Spec)
		return ok && got == want
	}
	return false
}
