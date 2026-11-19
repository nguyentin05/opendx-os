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

export interface NavigationSpec {
  title: string;
  icon: string;
  order: number;
}

export interface UISchemaSpec {
  navigation: NavigationSpec;
  primaryView: string;
  listColumns: string[];
  quickActions: string[];
}

export interface TransitionSpec {
  from: string;
  to: string;
  action: string;
  roles: string[];
}

export interface StateMachineSpec {
  initialState: string;
  states: string[];
  transitions: TransitionSpec[];
}

export interface KnowledgeSpec {
  version: 1;
  personalLibrary?: { enabled: boolean; allowedRoles: string[] };
  grantPolicies?: Record<string, { allowedRoles: string[] }>;
  sources?: Array<{
    id: string;
    path: string;
    mediaType: 'text/markdown' | 'application/pdf';
    indexingProfile: 'standard-text-v1';
    access: { mode: 'ROLE' | 'RESTRICTED'; allowedRoles: string[]; grantsManagedBy?: string };
  }>;
}

export interface ManifestSpec {
  knowledge?: KnowledgeSpec;
  role: string;
  domainTable: {
    name: string;
    columns: Array<{ name: string; type: string }>;
  };
  stateMachine: StateMachineSpec;
  uiSchema: UISchemaSpec;
}

export interface RegisteredApp {
  id: string;
  appName: string;
  version: string;
  manifestSpec: ManifestSpec;
  status: string;
}

export interface UserContext {
  userId: string;
  workspaceId: string;
  role: string;
}

// Current registry/session contracts. Legacy single-table fixtures above are
// retained for compatibility; live callers use these ADR0007/0008 shapes.
export interface RoleGrant { name: string; app_id?: string; cap_id?: string; description?: string }
export interface CurrentUser { id: string; email: string; display_name: string; roles: RoleGrant[] }
export interface FormField {
  name: string;
  label: string;
  type: string;
  required: boolean;
}

interface TableAccessRule {
  role: string;
  columns: string[];
  scope?: 'own' | 'department' | 'organization';
  recordFilter?: Record<string, unknown>;
  presets?: Record<string, string>;
}

interface TableCommand {
  name: string;
  from: string[];
  to: string;
  allowedRoles: string[];
  recordFilter?: Record<string, unknown>;
  payload?: Array<{ column: string; required: boolean }>;
}

export interface InstalledTable {
  name: string;
  columns?: Array<{ name: string; type: string; nullable: boolean; check?: string }> | null;
  foreignKeys?: Array<{ column: string; references: string }>;
  access: {
    select: TableAccessRule[];
    insert?: TableAccessRule[] | null;
    update?: TableAccessRule[];
    delete?: Array<{ role: string }>;
  };
  ui: {
    listColumns: string[];
    formFields?: FormField[] | null;
    actions?: string[];
  };
  stateMachine?: {
    initial: string;
    editableStates: string[];
    terminalStates: string[];
    commands: TableCommand[];
  };
  attachments?: Array<{ column: string; requireCleanOnCommands: string[] }>;
  belongsTo?: { table: string; column: string };
}

export interface InstalledApp {
  app_id: string;
  display_name: string;
  version: string;
  capabilities: string[] | null;
  roles: RoleGrant[];
  tables: InstalledTable[];
  knowledge?: KnowledgeSpec | null;
  work_item_roles?: string[];
}
