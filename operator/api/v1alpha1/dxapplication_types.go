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

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types reported in DXApplicationStatus (ADR 0004).
const (
	// ConditionValid: the spec passed semantic validation (manifest.Validate).
	ConditionValid = "Valid"
	// ConditionSchemaInSync: the live domain table matches the spec, or was converged additively.
	ConditionSchemaInSync = "SchemaInSync"
	// ConditionPermissionsInSync: DDL grants and Hasura metadata were applied.
	ConditionPermissionsInSync = "PermissionsInSync"
	// ConditionPublished: core.apps holds this version with status Ready, together
	// with the application's roles and capabilities.
	ConditionPublished = "Published"
	// ConditionReady: all of the above hold for the observed generation.
	ConditionReady = "Ready"

	// Finalizer guards uninstall: grants are revoked and core.apps is marked
	// Removed. Domain data, roles and role assignments are retained.
	Finalizer = "dxos.io/cleanup"
)

// UUIDPattern matches user and record identifiers.
const UUIDPattern = `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`

// DXApplicationSpec mirrors the DXApplication manifest contract
// (docs/contracts/data-contracts.md). JSON field names are identical to the
// manifest so that both forms convert losslessly.
type DXApplicationSpec struct {
	// DisplayName is shown by the App Shell.
	// +kubebuilder:validation:MinLength=1
	DisplayName string `json:"displayName"`

	// Version of the application package.
	// +kubebuilder:validation:MinLength=1
	Version string `json:"version"`

	// Capabilities lists the Tier 2 capabilities the application depends on.
	// cap-process is required.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^cap-[a-z]+$`
	Capabilities []string `json:"capabilities"`

	// Roles are owned by this application; no other application may use them.
	// +kubebuilder:validation:MinItems=1
	Roles []Role `json:"roles"`

	// SharedAccess declares the shared Tier 1 and Tier 2 tables, columns and
	// roles the application reads.
	// +optional
	SharedAccess []SharedAccess `json:"sharedAccess,omitempty"`

	// Tables declares the application's domain tables (ADR 0008). The Operator
	// generates each of them. A table with a state machine is a process table;
	// one without is a plain table.
	// +kubebuilder:validation:MinItems=1
	Tables []Table `json:"tables"`

	// Workflows declares the automation flows and their business effects.
	// +optional
	Workflows []Workflow `json:"workflows,omitempty"`
}

// Role is one application role (core.roles).
type Role struct {
	// Role names start with role_: Hasura treats some names (admin) as
	// privileged, and the prefix keeps application roles out of that space.
	// +kubebuilder:validation:Pattern=`^role_[a-z0-9_]+$`
	Name string `json:"name"`
	// +optional
	Description string `json:"description,omitempty"`
}

// SharedAccess grants read access to a shared Tier 1 or Tier 2 table.
type SharedAccess struct {
	// +kubebuilder:validation:Pattern=`^(core|cap_[a-z]+)\.[a-z][a-z0-9_]*$`
	Table string `json:"table"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9_]*$`
	Roles []string `json:"roles"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9_]*$`
	Columns []string `json:"columns"`
}

// Table describes one domain table with its access rules, lifecycle and UI.
type Table struct {
	// +kubebuilder:validation:Pattern=`^domain\.[a-z][a-z0-9_]*$`
	Name string `json:"name"`
	// +kubebuilder:validation:MinItems=1
	Columns []Column `json:"columns"`
	// +optional
	ForeignKeys []ForeignKey `json:"foreignKeys,omitempty"`

	// Access declares per-role select/insert/update rules on the table.
	Access Access `json:"access"`

	// StateMachine declares states and the commands that move between them. A
	// table without one is edited freely within its access rules.
	// +optional
	StateMachine *StateMachine `json:"stateMachine,omitempty"`

	// Attachments binds file columns to commands that require a CLEAN scan.
	// +optional
	Attachments []Attachment `json:"attachments,omitempty"`

	// UI configures the table's list and form in the App Shell.
	UI UISchema `json:"ui"`
}

// Column is a business column; system columns are managed by the Operator.
type Column struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Name string `json:"name"`
	// +kubebuilder:validation:Enum=uuid;text;date;"numeric(14,2)";"numeric(4,2)"
	Type string `json:"type"`
	// Nullable is required so that additive upgrades are explicit.
	Nullable bool `json:"nullable"`
	// Check is a SQL CHECK expression from the validator's allowlist.
	// +optional
	Check string `json:"check,omitempty"`
}

// ForeignKey links a business uuid column to the id of a shared table.
type ForeignKey struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Column string `json:"column"`
	// References is a shared table or another table of the same application.
	// +kubebuilder:validation:Pattern=`^(core|cap_[a-z]+|domain)\.[a-z][a-z0-9_]*$`
	References string `json:"references"`
}

// Access groups per-operation access rules of one table.
type Access struct {
	// +kubebuilder:validation:MinItems=1
	Select []AccessRule `json:"select"`
	// +kubebuilder:validation:MinItems=1
	Insert []AccessRule `json:"insert"`
	// +optional
	Update []AccessRule `json:"update,omitempty"`
}

// AccessRule allows one role a set of columns within its scope, optionally
// narrowed by a record filter.
type AccessRule struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Role string `json:"role"`
	// Scope is required on select and update: own records, own department
	// (requires cap-hr) or the whole organization. Not allowed on insert.
	// +optional
	// +kubebuilder:validation:Enum=own;department;organization
	Scope string `json:"scope,omitempty"`
	// +kubebuilder:validation:MinItems=1
	Columns []string `json:"columns"`
	// RecordFilter is a Hasura boolean expression over declared columns.
	// +optional
	RecordFilter *apiextensionsv1.JSON `json:"recordFilter,omitempty"`
	// Presets set uuid columns from X-Hasura-User-Id (insert only).
	// +optional
	Presets map[string]string `json:"presets,omitempty"`
}

// StateMachine declares the lifecycle of a record of a process table.
type StateMachine struct {
	// +kubebuilder:validation:Pattern=`^[A-Za-z][A-Za-z0-9_]*$`
	Initial string `json:"initial"`
	// +kubebuilder:validation:MinItems=1
	EditableStates []string `json:"editableStates"`
	// +kubebuilder:validation:MinItems=1
	TerminalStates []string `json:"terminalStates"`
	// +kubebuilder:validation:MinItems=1
	Commands []Command `json:"commands"`
}

// Command is a role-guarded state transition executed by cap-process.
type Command struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Name string `json:"name"`
	// +kubebuilder:validation:MinItems=1
	From []string `json:"from"`
	// +kubebuilder:validation:Pattern=`^[A-Za-z][A-Za-z0-9_]*$`
	To string `json:"to"`
	// +kubebuilder:validation:MinItems=1
	AllowedRoles []string `json:"allowedRoles"`
	// +optional
	RecordFilter *apiextensionsv1.JSON `json:"recordFilter,omitempty"`
}

// Attachment binds a file column to the commands that require a CLEAN file.
type Attachment struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Column string `json:"column"`
	// +kubebuilder:validation:MinItems=1
	RequireCleanOnCommands []string `json:"requireCleanOnCommands"`
}

// Workflow is an automation flow. The definition is inlined because the
// controller cannot read package files from inside the cluster.
type Workflow struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	ID string `json:"id"`
	// File is the package-relative path the definition was read from. It is
	// informational and preserved so that a resource converts back to its package.
	// +optional
	// +kubebuilder:validation:Pattern=`^workflows/[a-z][a-z0-9_]*\.yaml$`
	File string `json:"file,omitempty"`
	// Definition is the workflow engine YAML (Kestra flow).
	// +kubebuilder:validation:MinLength=1
	Definition string `json:"definition"`
	// +kubebuilder:validation:MinItems=1
	SubscribedEvents []string `json:"subscribedEvents"`
	// +kubebuilder:validation:MinItems=1
	Effects []Effect `json:"effects"`
}

// Effect is a deduplicated business effect executed through cap-process.
type Effect struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	StepName string `json:"stepName"`
	// +kubebuilder:validation:Enum=create_work_item
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinItems=1
	WhenCommands []string     `json:"whenCommands"`
	Fields       EffectFields `json:"fields"`
}

// EffectFields are the literal work item fields owned by the manifest.
type EffectFields struct {
	// +kubebuilder:validation:MinLength=1
	Title string `json:"title"`
	// +kubebuilder:validation:MinLength=1
	Description string `json:"description"`
	// +kubebuilder:validation:Enum=LOW;MEDIUM;HIGH;CRITICAL
	Priority string `json:"priority"`
	// +optional
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	AssigneeID string `json:"assigneeId,omitempty"`
}

// UISchema drives the App Shell; it grants no permissions.
type UISchema struct {
	// +kubebuilder:validation:MinItems=1
	ListColumns []string `json:"listColumns"`
	// +kubebuilder:validation:MinItems=1
	FormFields []FormField `json:"formFields"`
	// Actions lists the commands of a process table; other tables have none.
	// +optional
	Actions []string `json:"actions,omitempty"`
}

// FormField is one input of the generated form.
type FormField struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9_]*$`
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	Label string `json:"label"`
	// +kubebuilder:validation:Enum=uuid;text;number;file;date
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

// DXApplicationStatus is the observed state (ADR 0004).
type DXApplicationStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ActiveVersion is the version recorded as Ready in core.apps.
	// +optional
	ActiveVersion string `json:"activeVersion,omitempty"`
}

// DXApplication is a cluster-scoped application definition. One resource owns an
// application's domain table, triggers, Hasura metadata, roles and workflows,
// and its row in the core.apps registry.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=dxapp
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type DXApplication struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec DXApplicationSpec `json:"spec"`

	// +optional
	Status DXApplicationStatus `json:"status,omitzero"`
}

// DXApplicationList contains a list of DXApplication.
//
// +kubebuilder:object:root=true
type DXApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DXApplication `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DXApplication{}, &DXApplicationList{})
		return nil
	})
}
