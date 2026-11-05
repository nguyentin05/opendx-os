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

// Package manifest defines and parses application manifests.
package manifest

// ColumnDef defines a column in a domain table.
type ColumnDef struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"`
	PrimaryKey bool   `yaml:"primaryKey,omitempty"`
	Nullable   bool   `yaml:"nullable,omitempty"`
	Default    string `yaml:"default,omitempty"`
	References string `yaml:"references,omitempty"`
}

// DomainTableDef defines the domain table specification.
type DomainTableDef struct {
	Name    string      `yaml:"name"`
	Columns []ColumnDef `yaml:"columns"`
}

// RelationshipDef defines a Hasura object/array relationship.
type RelationshipDef struct {
	Name        string            `yaml:"name"`
	TargetTable string            `yaml:"targetTable"`
	Mapping     map[string]string `yaml:"mapping"`
}

// SharedAccessDef defines how an app role accesses shared core entities.
type SharedAccessDef struct {
	Table         string            `yaml:"table"`
	Columns       []string          `yaml:"columns"`
	Filter        map[string]any    `yaml:"filter"`
	Relationships []RelationshipDef `yaml:"relationships,omitempty"`
}

// AccessRule defines permissions for select, insert, or update.
type AccessRule struct {
	Filter  map[string]any `yaml:"filter,omitempty"`
	Check   map[string]any `yaml:"check,omitempty"`
	Columns []string       `yaml:"columns"`
}

// DomainAccessDef defines CRUD permissions on the domain table.
type DomainAccessDef struct {
	Select []AccessRule `yaml:"select,omitempty"`
	Insert []AccessRule `yaml:"insert,omitempty"`
	Update []AccessRule `yaml:"update,omitempty"`
	Delete []AccessRule `yaml:"delete,omitempty"`
}

// TransitionDef defines a valid state transition in the state machine.
type TransitionDef struct {
	From   string   `yaml:"from"`
	To     string   `yaml:"to"`
	Action string   `yaml:"action"`
	Roles  []string `yaml:"roles"`
}

// StateMachineDef defines allowed states and transitions.
type StateMachineDef struct {
	InitialState string          `yaml:"initialState"`
	States       []string        `yaml:"states"`
	Transitions  []TransitionDef `yaml:"transitions"`
}

// AttachmentsDef defines file attachments policy.
type AttachmentsDef struct {
	AllowedFileTypes   []string `yaml:"allowedFileTypes"`
	MaxSizeBytes       int64    `yaml:"maxSizeBytes"`
	EnforceCleanStatus bool     `yaml:"enforceCleanStatus"`
}

// WorkflowBindingDef defines workflow file bindings.
type WorkflowBindingDef struct {
	Name           string `yaml:"name"`
	TriggerEvent   string `yaml:"triggerEvent"`
	DefinitionFile string `yaml:"definitionFile"`
}

// CapabilityRefDef defines capability dependencies.
type CapabilityRefDef struct {
	Name     string `yaml:"name"`
	Required bool   `yaml:"required"`
}

// NavigationDef defines UI navigation metadata.
type NavigationDef struct {
	Title string `yaml:"title"`
	Icon  string `yaml:"icon"`
	Order int    `yaml:"order"`
}

// UISchemaDef defines the schema-driven UI metadata.
type UISchemaDef struct {
	Navigation   NavigationDef `yaml:"navigation"`
	PrimaryView  string        `yaml:"primaryView"`
	ListColumns  []string      `yaml:"listColumns"`
	QuickActions []string      `yaml:"quickActions"`
}

// AppSpec defines the full specification of a DXApplication.
type AppSpec struct {
	Role         string               `yaml:"role"`
	SharedAccess []SharedAccessDef    `yaml:"sharedAccess"`
	DomainTable  DomainTableDef       `yaml:"domainTable"`
	DomainAccess DomainAccessDef      `yaml:"domainAccess"`
	StateMachine StateMachineDef      `yaml:"stateMachine"`
	Attachments  AttachmentsDef       `yaml:"attachments"`
	Workflows    []WorkflowBindingDef `yaml:"workflows"`
	Capabilities []CapabilityRefDef   `yaml:"capabilities"`
	UISchema     UISchemaDef          `yaml:"uiSchema"`
}

// AppMetadata defines metadata for the manifest.
type AppMetadata struct {
	Name           string `yaml:"name"`
	WorkspaceScope string `yaml:"workspaceScope"`
	Version        string `yaml:"version"`
}

// DXApplication defines the root manifest structure.
type DXApplication struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   AppMetadata `yaml:"metadata"`
	Spec       AppSpec     `yaml:"spec"`
}
