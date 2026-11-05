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

package manifest

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

var systemManagedColumns = map[string]bool{
	"id":           true,
	"workspace_id": true,
	"status":       true,
	"version":      true,
	"updated_at":   true,
}

// Parse parses a DXApplication manifest from raw bytes.
func Parse(r io.Reader) (*DXApplication, error) {
	dec := yaml.NewDecoder(r)
	var app DXApplication
	if err := dec.Decode(&app); err != nil {
		return nil, fmt.Errorf("failed to decode manifest: %w", err)
	}

	if app.Kind != "DXApplication" {
		return nil, fmt.Errorf("invalid manifest kind: %q (expected DXApplication)", app.Kind)
	}
	if app.Metadata.Name == "" {
		return nil, fmt.Errorf("manifest metadata.name is required")
	}
	if app.Spec.Role == "" {
		return nil, fmt.Errorf("manifest spec.role is required")
	}
	if app.Spec.DomainTable.Name == "" {
		return nil, fmt.Errorf("manifest spec.domainTable.name is required")
	}

	// Validate domainAccess: system managed columns cannot be in insert or update
	for _, rule := range app.Spec.DomainAccess.Insert {
		for _, col := range rule.Columns {
			if systemManagedColumns[col] {
				return nil, fmt.Errorf("system managed column %q cannot be in domainAccess.insert allowlist", col)
			}
		}
	}
	for _, rule := range app.Spec.DomainAccess.Update {
		for _, col := range rule.Columns {
			if systemManagedColumns[col] {
				return nil, fmt.Errorf("system managed column %q cannot be in domainAccess.update allowlist", col)
			}
		}
	}

	return &app, nil
}

// ParseFile parses a manifest from a file path.
func ParseFile(path string) (*DXApplication, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open manifest file: %w", err)
	}
	defer f.Close()
	return Parse(f)
}
