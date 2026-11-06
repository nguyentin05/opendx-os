//go:build integration

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

package testutil

import (
	"database/sql"
	"testing"
)

// ProcessSchema installs disposable proposed-contract tables, never production migrations.
func ProcessSchema(t *testing.T, db *sql.DB, domainDDL string) {
	t.Helper()
	_, e := db.Exec(`CREATE SCHEMA core;CREATE SCHEMA domain;
CREATE TABLE core.capability_registry(cap_id text PRIMARY KEY,enabled boolean NOT NULL);
CREATE TABLE core.app_registry(workspace_id uuid,app_id text,display_name text,version text,role_name text,domain_table text,manifest_spec jsonb,ui_schema jsonb,state_machine jsonb,dependent_capabilities text[],status text,last_error text,updated_at timestamptz,PRIMARY KEY(workspace_id,app_id));
CREATE TABLE core.command_receipts(workspace_id uuid,actor_id text,app_id text,entity_id uuid,command text,idempotency_key text,payload_hash text,response_status integer,response_body jsonb,created_at timestamptz DEFAULT now(),PRIMARY KEY(workspace_id,actor_id,app_id,entity_id,command,idempotency_key));
CREATE TABLE core.outbox_events(id uuid PRIMARY KEY,workspace_id uuid,app_id text,event_type text,entity_type text,entity_id uuid,entity_version bigint,payload jsonb,created_at timestamptz DEFAULT now(),UNIQUE(workspace_id,id),UNIQUE(workspace_id,app_id,entity_type,entity_id,entity_version,event_type));
CREATE TABLE core.audit_logs(workspace_id uuid,actor_id uuid,action text,entity_id uuid,entity_version bigint);
CREATE TABLE core.file_objects(id uuid,workspace_id uuid,app_id text,scan_status text,PRIMARY KEY(workspace_id,id));
CREATE TABLE core.work_items(id uuid,workspace_id uuid,title text,description text,priority text,assigned_employee_id uuid,PRIMARY KEY(workspace_id,id));
CREATE TABLE core.processed_workflow_steps(event_id uuid,step_name text,workspace_id uuid,work_item_id uuid,result jsonb,outcome text,processed_at timestamptz DEFAULT now(),PRIMARY KEY(event_id,step_name));
`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(domainDDL); e != nil {
		t.Fatal(e)
	}
}
