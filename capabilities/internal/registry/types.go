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

// Package registry reads installed applications from core.apps, where the
// operator records each application's definition once it is Ready.
package registry

import "time"

// AppDefinition is one installed application.
type AppDefinition struct {
	AppID       string
	DisplayName string
	Version     string
	Spec        Spec
	UpdatedAt   time.Time
}

// Spec is the part of the manifest's spec that capability services act on.
// Fields the services do not use (uiSchema, sharedAccess) are ignored.
type Spec struct {
	Capabilities []string     `json:"capabilities"`
	Roles        []Role       `json:"roles"`
	DomainTable  DomainTable  `json:"domainTable"`
	DomainAccess DomainAccess `json:"domainAccess"`
	StateMachine StateMachine `json:"stateMachine"`
	Attachments  []Attachment `json:"attachments"`
	Workflows    []Workflow   `json:"workflows"`
}
type Role struct {
	Name string `json:"name"`
}
type DomainTable struct {
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	ForeignKeys []ForeignKey `json:"foreignKeys"`
}
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type ForeignKey struct {
	Column     string `json:"column"`
	References string `json:"references"`
}
type DomainAccess struct {
	Select []Permission `json:"select"`
	Insert []Permission `json:"insert"`
	Update []Permission `json:"update"`
}
type Filter map[string]any

// Scopes of an access rule (manifest field scope).
const (
	ScopeOwn          = "own"
	ScopeDepartment   = "department"
	ScopeOrganization = "organization"
)

type Permission struct {
	Role         string   `json:"role"`
	Scope        string   `json:"scope"`
	Columns      []string `json:"columns"`
	RecordFilter Filter   `json:"recordFilter"`
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
	RecordFilter Filter   `json:"recordFilter"`
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
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	AssigneeID  string `json:"assigneeId"`
}
