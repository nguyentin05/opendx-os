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
	"bytes"
	"encoding/json"
)

// DecodeStoredSpec decodes a spec stored in core.apps. Rows written before
// ADR 0008 describe one table with the fields domainTable, domainAccess,
// stateMachine, attachments and uiSchema; they are read as a single process
// table so the operator can plan around such an application and replace it
// when its package is reinstalled. Manifests themselves must use spec.tables.
func DecodeStoredSpec(raw []byte) (AppSpec, error) {
	var stored struct {
		AppSpec
		DomainTable *struct {
			Name        string          `json:"name"`
			Columns     []ColumnDef     `json:"columns"`
			ForeignKeys []ForeignKeyDef `json:"foreignKeys"`
		} `json:"domainTable"`
		DomainAccess AccessDef        `json:"domainAccess"`
		StateMachine *StateMachineDef `json:"stateMachine"`
		Attachments  []AttachmentDef  `json:"attachments"`
		UISchema     UISchemaDef      `json:"uiSchema"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&stored); err != nil {
		return AppSpec{}, err
	}
	spec := stored.AppSpec
	if len(spec.Tables) == 0 && stored.DomainTable != nil {
		spec.Tables = []TableDef{{
			Name:         stored.DomainTable.Name,
			Columns:      stored.DomainTable.Columns,
			ForeignKeys:  stored.DomainTable.ForeignKeys,
			Access:       stored.DomainAccess,
			StateMachine: stored.StateMachine,
			Attachments:  stored.Attachments,
			UI:           stored.UISchema,
		}}
	}
	return spec, nil
}
