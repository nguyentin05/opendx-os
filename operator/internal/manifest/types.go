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

// Package manifest defines, parses, and validates C0 v1 application manifests.
package manifest

// DXApplication defines the root C0 v1 manifest structure.
type DXApplication struct {
	APIVersion string      `yaml:"apiVersion" json:"apiVersion"`
	Kind       string      `yaml:"kind" json:"kind"`
	Metadata   AppMetadata `yaml:"metadata" json:"metadata"`
	Spec       AppSpec     `yaml:"spec" json:"spec"`
}

// AppMetadata defines metadata for the manifest.
type AppMetadata struct {
	Name        string `yaml:"name" json:"name"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	Version     string `yaml:"version" json:"version"`
}

// AppSpec defines the full specification of a DXApplication.
type AppSpec struct {
	Role         string            `yaml:"role" json:"role"`
	SharedAccess []SharedAccessDef `yaml:"sharedAccess" json:"sharedAccess"`
	DomainTable  DomainTableDef    `yaml:"domainTable" json:"domainTable"`
	DomainAccess DomainAccessDef   `yaml:"domainAccess" json:"domainAccess"`
	StateMachine StateMachineDef   `yaml:"stateMachine" json:"stateMachine"`
	Attachments  []AttachmentDef   `yaml:"attachments" json:"attachments"`
	Workflows    []WorkflowDef     `yaml:"workflows" json:"workflows"`
	Capabilities []string          `yaml:"capabilities" json:"capabilities"`
	UISchema     UISchemaDef       `yaml:"uiSchema" json:"uiSchema"`
}

// SharedAccessDef defines how app roles access shared core entities.
type SharedAccessDef struct {
	Table   string   `yaml:"table" json:"table"`
	Roles   []string `yaml:"roles" json:"roles"`
	Columns []string `yaml:"columns" json:"columns"`
}

// DomainTableDef defines the domain table specification.
type DomainTableDef struct {
	Name                 string          `yaml:"name" json:"name"`
	Columns              []ColumnDef     `yaml:"columns" json:"columns"`
	CompositeForeignKeys []ForeignKeyDef `yaml:"compositeForeignKeys" json:"compositeForeignKeys"`
}

// ColumnDef defines a business column in a domain table.
type ColumnDef struct {
	Name     string `yaml:"name" json:"name"`
	Type     string `yaml:"type" json:"type"`
	Nullable bool   `yaml:"nullable" json:"nullable"`
	Check    string `yaml:"check,omitempty" json:"check,omitempty"`
}

// ForeignKeyDef defines a same-workspace composite foreign key.
type ForeignKeyDef struct {
	Columns           []string `yaml:"columns" json:"columns"`
	ReferencesTable   string   `yaml:"referencesTable" json:"referencesTable"`
	ReferencesColumns []string `yaml:"referencesColumns" json:"referencesColumns"`
}

// DomainAccessDef defines CRUD permissions on the domain table.
type DomainAccessDef struct {
	Select []AccessRule `yaml:"select" json:"select"`
	Insert []AccessRule `yaml:"insert" json:"insert"`
	Update []AccessRule `yaml:"update" json:"update"`
}

// AccessRule defines role-scoped permissions for select, insert, or update.
type AccessRule struct {
	Role         string            `yaml:"role" json:"role"`
	Columns      []string          `yaml:"columns" json:"columns"`
	RecordFilter map[string]any    `yaml:"recordFilter,omitempty" json:"recordFilter,omitempty"`
	Presets      map[string]string `yaml:"presets,omitempty" json:"presets,omitempty"`
}

// StateMachineDef defines allowed states and transitions.
type StateMachineDef struct {
	Initial        string       `yaml:"initial" json:"initial"`
	EditableStates []string     `yaml:"editableStates" json:"editableStates"`
	TerminalStates []string     `yaml:"terminalStates" json:"terminalStates"`
	Commands       []CommandDef `yaml:"commands" json:"commands"`
}

// CommandDef defines a version-checked state transition command.
type CommandDef struct {
	Name         string         `yaml:"name" json:"name"`
	From         []string       `yaml:"from" json:"from"`
	To           string         `yaml:"to" json:"to"`
	AllowedRoles []string       `yaml:"allowedRoles" json:"allowedRoles"`
	RecordFilter map[string]any `yaml:"recordFilter,omitempty" json:"recordFilter,omitempty"`
}

// AttachmentDef defines file attachment clean-status requirements.
type AttachmentDef struct {
	Column                 string   `yaml:"column" json:"column"`
	RequireCleanOnCommands []string `yaml:"requireCleanOnCommands" json:"requireCleanOnCommands"`
}

// WorkflowDef defines workflow file bindings and effect declarations.
type WorkflowDef struct {
	ID               string      `yaml:"id" json:"id"`
	File             string      `yaml:"file" json:"file"`
	SubscribedEvents []string    `yaml:"subscribedEvents" json:"subscribedEvents"`
	Effects          []EffectDef `yaml:"effects" json:"effects"`
}

// EffectDef defines a repeatable workflow effect.
type EffectDef struct {
	StepName     string          `yaml:"stepName" json:"stepName"`
	Kind         string          `yaml:"kind" json:"kind"`
	WhenCommands []string        `yaml:"whenCommands" json:"whenCommands"`
	Fields       EffectFieldsDef `yaml:"fields" json:"fields"`
}

// EffectFieldsDef defines literal parameters for a create_work_item effect.
type EffectFieldsDef struct {
	Title              string `yaml:"title" json:"title"`
	Description        string `yaml:"description" json:"description"`
	Priority           string `yaml:"priority" json:"priority"`
	AssignedEmployeeID string `yaml:"assignedEmployeeId,omitempty" json:"assignedEmployeeId,omitempty"`
}

// UISchemaDef defines the schema-driven UI metadata.
type UISchemaDef struct {
	ListColumns []string       `yaml:"listColumns" json:"listColumns"`
	FormFields  []FormFieldDef `yaml:"formFields" json:"formFields"`
	Actions     []string       `yaml:"actions" json:"actions"`
}

// FormFieldDef defines a form input field in the dynamic UI shell.
type FormFieldDef struct {
	Name     string `yaml:"name" json:"name"`
	Label    string `yaml:"label" json:"label"`
	Type     string `yaml:"type" json:"type"`
	Required bool   `yaml:"required" json:"required"`
}
