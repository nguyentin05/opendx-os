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

// Package registry consumes the proposed C0 active application contract.
package registry

import "time"

type AppDefinition struct {
	WorkspaceID           string       `json:"workspace_id"`
	AppID                 string       `json:"app_id"`
	DisplayName           string       `json:"display_name"`
	Version               string       `json:"version"`
	RoleName              string       `json:"role_name"`
	DomainTable           string       `json:"domain_table"`
	ManifestSpec          Spec         `json:"manifest_spec"`
	UISchema              UISchema     `json:"ui_schema"`
	StateMachine          StateMachine `json:"state_machine"`
	DependentCapabilities []string     `json:"dependent_capabilities"`
	Status                string       `json:"status"`
	LastError             *string      `json:"last_error"`
	UpdatedAt             time.Time    `json:"updated_at"`
}
type Spec struct {
	Role         string         `json:"role"`
	SharedAccess []SharedAccess `json:"sharedAccess"`
	DomainTable  DomainTable    `json:"domainTable"`
	DomainAccess DomainAccess   `json:"domainAccess"`
	StateMachine StateMachine   `json:"stateMachine"`
	Attachments  []Attachment   `json:"attachments"`
	Workflows    []Workflow     `json:"workflows"`
	Capabilities []string       `json:"capabilities"`
	UISchema     UISchema       `json:"uiSchema"`
}
type SharedAccess struct {
	Table   string   `json:"table"`
	Roles   []string `json:"roles"`
	Columns []string `json:"columns"`
}
type DomainTable struct {
	Name                 string       `json:"name"`
	Columns              []Column     `json:"columns"`
	CompositeForeignKeys []ForeignKey `json:"compositeForeignKeys"`
}
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Check    string `json:"check,omitempty"`
}
type ForeignKey struct {
	Columns           []string `json:"columns"`
	ReferencesTable   string   `json:"referencesTable"`
	ReferencesColumns []string `json:"referencesColumns"`
}
type DomainAccess struct {
	Select []Permission `json:"select"`
	Insert []Permission `json:"insert"`
	Update []Permission `json:"update"`
}
type Filter map[string]any
type Permission struct {
	Role         string            `json:"role"`
	Columns      []string          `json:"columns"`
	RecordFilter Filter            `json:"recordFilter,omitempty"`
	Presets      map[string]string `json:"presets,omitempty"`
}
type StateMachine struct {
	Initial        string    `json:"initial"`
	EditableStates []string  `json:"editableStates"`
	TerminalStates []string  `json:"terminalStates"`
	Commands       []Command `json:"commands"`
}
type Command struct {
	Name         string   `json:"name"`
	From         []string `json:"from"`
	To           string   `json:"to"`
	AllowedRoles []string `json:"allowedRoles"`
	RecordFilter Filter   `json:"recordFilter,omitempty"`
}
type Attachment struct {
	Column                 string   `json:"column"`
	RequireCleanOnCommands []string `json:"requireCleanOnCommands"`
}
type Workflow struct {
	ID               string   `json:"id"`
	File             string   `json:"file"`
	SubscribedEvents []string `json:"subscribedEvents"`
	Effects          []Effect `json:"effects"`
}
type Effect struct {
	StepName     string       `json:"stepName"`
	Kind         string       `json:"kind"`
	WhenCommands []string     `json:"whenCommands"`
	Fields       EffectFields `json:"fields"`
}
type EffectFields struct {
	Title              string `json:"title"`
	Description        string `json:"description"`
	Priority           string `json:"priority"`
	AssignedEmployeeID string `json:"assignedEmployeeId,omitempty"`
}
type UISchema struct {
	ListColumns []string    `json:"listColumns"`
	FormFields  []FormField `json:"formFields"`
	Actions     []string    `json:"actions"`
}
type FormField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}
