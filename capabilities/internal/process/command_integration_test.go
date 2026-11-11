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

package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/lib/pq"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
	"os"
	"sync"
	"testing"
)

const entity = "33333333-3333-4333-8333-333333333333"

func commandFixture(t *testing.T) (*Engine, *sql.DB, *auth.Identity) {
	t.Helper()
	db := testutil.Database(t)
	testutil.ProcessSchema(t, db, commandDomainDDL)
	b, e := os.ReadFile("../../../contracts/fixtures/registry-ready.json")
	if e != nil {
		t.Fatal(e)
	}
	a, e := registry.Decode(b)
	if e != nil {
		t.Fatal(e)
	}
	spec, _ := json.Marshal(a.ManifestSpec)
	ui, _ := json.Marshal(a.UISchema)
	sm, _ := json.Marshal(a.StateMachine)
	_, e = db.Exec(`INSERT INTO core.capability_registry VALUES('cap-process',true);`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`INSERT INTO core.app_registry VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULL,$12)`, a.WorkspaceID, a.AppID, a.DisplayName, a.Version, a.RoleName, a.DomainTable, string(spec), string(ui), string(sm), pq.Array(a.DependentCapabilities), a.Status, a.UpdatedAt)
	if e != nil {
		t.Fatal(e)
	}
	id := &auth.Identity{WorkspaceID: a.WorkspaceID, UserID: "22222222-2222-4222-8222-222222222222", Role: "role_procurement", AllowedRoles: []string{"role_procurement", "role_manager_procurement"}}
	_, e = db.Exec(`INSERT INTO domain.procurement_requests(id,workspace_id,status,requester_id,vendor_name,item_description,quoted_amount,quote_file_id) VALUES($1,$2,'DRAFT',$3,'v','item',12,'44444444-4444-4444-8444-444444444444')`, entity, id.WorkspaceID, id.UserID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO core.file_objects VALUES('44444444-4444-4444-8444-444444444444','11111111-1111-4111-8111-111111111111','procurement','CLEAN')`); e != nil {
		t.Fatal(e)
	}
	return NewEngine(db, registry.NewStore(db)), db, id
}
func execute(e *Engine, id *auth.Identity, key string, v int64) (*CommandResponse, int, error) {
	return e.Execute(context.Background(), id, "procurement", "submit", key, &CommandRequest{EntityID: entity, ExpectedVersion: v, Payload: map[string]any{}})
}
func counts(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	for _, table := range []string{"audit_logs", "outbox_events", "command_receipts"} {
		var n int
		if e := db.QueryRow(`SELECT count(*) FROM core.` + table).Scan(&n); e != nil || n != want {
			t.Fatalf("%s count=%d want=%d: %v", table, n, want, e)
		}
	}
}
func TestCommandDerivesStateAndTable(t *testing.T) {
	e, db, id := commandFixture(t)
	r, s, err := execute(e, id, "a", 1)
	if err != nil || s != 200 || r.Version != 2 || r.Status != "SUBMITTED" {
		t.Fatalf("%+v %d %v", r, s, err)
	}
	counts(t, db, 1)
	retry, s, err := execute(e, id, "a", 1)
	if err != nil || s != 200 || *retry != *r {
		t.Fatalf("retry %+v %d %v", retry, s, err)
	}
	_, s, _ = execute(e, id, "b", 1)
	if s != 409 {
		t.Fatal(s)
	}
	counts(t, db, 1)
}
func TestReceiptPayloadConflict(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, status, err := execute(e, id, "a", 1); err != nil || status != 200 {
		t.Fatalf("setup command: status=%d err=%v", status, err)
	}
	_, s, _ := execute(e, id, "a", 2)
	if s != 409 {
		t.Fatal(s)
	}
	counts(t, db, 1)
}
func TestRetryRechecksCurrentPermission(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, status, err := execute(e, id, "a", 1); err != nil || status != 200 {
		t.Fatalf("setup command: status=%d err=%v", status, err)
	}
	id.Role = "role_ungranted"
	_, s, _ := execute(e, id, "a", 1)
	if s != 403 {
		t.Fatal(s)
	}
	id.Role = "role_procurement"
	if _, err := db.Exec(`UPDATE core.capability_registry SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	_, s, _ = execute(e, id, "a", 1)
	if s != 503 {
		t.Fatal(s)
	}
	counts(t, db, 1)
}
func TestConcurrentSameKeyOneEffect(t *testing.T)  { concurrent(t, true) }
func TestConcurrentCommandsOneWinner(t *testing.T) { concurrent(t, false) }
func concurrent(t *testing.T, same bool) {
	e, db, id := commandFixture(t)
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "a"
			if !same && i == 1 {
				key = "b"
			}
			_, s, _ := execute(e, id, key, 1)
			statuses <- s
		}(i)
	}
	wg.Wait()
	close(statuses)
	success := 0
	for s := range statuses {
		if s == 200 {
			success++
		} else if s != 409 {
			t.Fatal(s)
		}
	}
	want := 1
	if same {
		want = 2
	}
	if success != want {
		t.Fatalf("success=%d", success)
	}
	counts(t, db, 1)
}
func TestCommandRollbackOutboxFailure(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, err := db.Exec(`ALTER TABLE core.outbox_events ADD CHECK(false)`); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "a", 1)
	if s != 503 {
		t.Fatal(s)
	}
	counts(t, db, 0)
	var v int
	if err := db.QueryRow(`SELECT version FROM domain.procurement_requests`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatal(v)
	}
}
func TestCommandRejectsSelfApproval(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, status, err := execute(e, id, "a", 1); err != nil || status != 200 {
		t.Fatalf("setup command: status=%d err=%v", status, err)
	}
	id.Role = "role_manager_procurement"
	_, s, _ := e.Execute(context.Background(), id, "procurement", "approve", "approve", &CommandRequest{EntityID: entity, ExpectedVersion: 2, Payload: map[string]any{}})
	if s != 403 {
		t.Fatal(s)
	}
	counts(t, db, 1)
}
func TestRetryRechecksRowPermission(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, err := db.Exec(`UPDATE core.app_registry SET manifest_spec=jsonb_set(manifest_spec,'{domainAccess,select,0,recordFilter}','{"requester_id":{"_eq":"X-Hasura-User-Id"}}')`); err != nil {
		t.Fatal(err)
	}
	if _, status, err := execute(e, id, "a", 1); err != nil || status != 200 {
		t.Fatalf("setup command: status=%d err=%v", status, err)
	}
	_, err := db.Exec(`UPDATE domain.procurement_requests SET requester_id='55555555-5555-4555-8555-555555555555'`)
	if err == nil {
		t.Fatal("unvalidated domain update accepted")
	}
	_, err = db.Exec(`SELECT set_config('dxos.actor_id',$1,false),set_config('dxos.action','test_edit',false)`, id.UserID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE domain.procurement_requests SET requester_id='55555555-5555-4555-8555-555555555555'`)
	if err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "a", 1)
	if s != 404 {
		t.Fatal(s)
	}
}
func TestCommandUncleanAttachment(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, err := db.Exec(`UPDATE core.file_objects SET scan_status='QUARANTINED'`); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "a", 1)
	if s != 422 {
		t.Fatal(s)
	}
	counts(t, db, 0)
}
func TestCommandTriggerRollback(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, err := db.Exec(`ALTER TABLE core.audit_logs ADD CHECK(false)`); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "a", 1)
	if s != 503 {
		t.Fatal(s)
	}
	counts(t, db, 0)
}

func TestCommandNumericFilters(t *testing.T) {
	for _, tc := range []struct {
		name, amount, literal, op string
		want                      int
	}{{"integer_eq", "12", "12", "_eq", 200}, {"decimal_eq", "12.00", "12", "_eq", 200}, {"decimal_neq", "12.00", "12", "_neq", 403}, {"large_eq", "9007199254740993.01", "9007199254740993.01", "_eq", 200}, {"large_neq", "9007199254740993.01", "9007199254740993.02", "_neq", 200}} {
		t.Run(tc.name, func(t *testing.T) {
			e, db, id := commandFixture(t)
			if _, err := db.Exec(`ALTER TABLE domain.procurement_requests DISABLE TRIGGER USER`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE domain.procurement_requests SET quoted_amount=$1::numeric`, tc.amount); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`ALTER TABLE domain.procurement_requests ENABLE TRIGGER USER`); err != nil {
				t.Fatal(err)
			}
			filter := `{"quoted_amount":{"` + tc.op + `":` + tc.literal + `}}`
			if _, err := db.Exec(`UPDATE core.app_registry SET manifest_spec=jsonb_set(manifest_spec,'{stateMachine,commands,0,recordFilter}',$1::jsonb),state_machine=jsonb_set(state_machine,'{commands,0,recordFilter}',$1::jsonb)`, filter); err != nil {
				t.Fatal(err)
			}
			_, s, _ := execute(e, id, "numeric", 1)
			if s != tc.want {
				t.Fatalf("status %d want %d", s, tc.want)
			}
		})
	}
}
func TestRetryNumericGuardDeniesCache(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, s, err := execute(e, id, "numeric", 1); s != 200 || err != nil {
		t.Fatal(s, err)
	}
	filter := `{"quoted_amount":{"_neq":12.00}}`
	if _, err := db.Exec(`UPDATE core.app_registry SET manifest_spec=jsonb_set(manifest_spec,'{stateMachine,commands,0,recordFilter}',$1::jsonb),state_machine=jsonb_set(state_machine,'{commands,0,recordFilter}',$1::jsonb)`, filter); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "numeric", 1)
	if s != 403 {
		t.Fatal(s)
	}
	counts(t, db, 1)
}
func TestRetryManifestRoleRevoked(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, status, err := execute(e, id, "a", 1); err != nil || status != 200 {
		t.Fatalf("setup command: status=%d err=%v", status, err)
	}
	if _, err := db.Exec(`UPDATE core.app_registry SET manifest_spec=jsonb_set(manifest_spec,'{stateMachine,commands,0,allowedRoles}','["role_manager_procurement"]'),state_machine=jsonb_set(state_machine,'{commands,0,allowedRoles}','["role_manager_procurement"]')`); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "a", 1)
	if s != 403 {
		t.Fatal(s)
	}
	counts(t, db, 1)
}
func TestCommandReceiptFailureRollback(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, err := db.Exec(`ALTER TABLE core.command_receipts ADD CHECK(false)`); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "a", 1)
	if s != 503 {
		t.Fatal(s)
	}
	counts(t, db, 0)
	var v int
	if err := db.QueryRow(`SELECT version FROM domain.procurement_requests`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatal(v)
	}
}

const commandDomainDDL = `CREATE TABLE domain.procurement_requests(id uuid PRIMARY KEY,workspace_id uuid,work_item_id uuid,status text,version bigint DEFAULT 1,updated_at timestamptz DEFAULT now(),requester_id uuid,vendor_name text,item_description text,quoted_amount numeric,quote_file_id uuid,ai_policy_summary text,rejection_reason text);
CREATE FUNCTION domain.test_bump() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.version:=OLD.version+1;NEW.updated_at:=now(); RETURN NEW;END$$;
CREATE FUNCTION domain.test_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO core.audit_logs VALUES(NEW.workspace_id,current_setting('dxos.actor_id')::uuid,current_setting('dxos.action'),NEW.id,NEW.version);RETURN NEW;END$$;
CREATE TRIGGER bump BEFORE UPDATE ON domain.procurement_requests FOR EACH ROW EXECUTE FUNCTION domain.test_bump();
CREATE TRIGGER audit AFTER UPDATE ON domain.procurement_requests FOR EACH ROW EXECUTE FUNCTION domain.test_audit();`

func TestRetryNumericReadFilterDeniesCache(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, status, err := execute(e, id, "a", 1); err != nil || status != 200 {
		t.Fatalf("setup command: status=%d err=%v", status, err)
	}
	if _, err := db.Exec(`UPDATE core.app_registry SET manifest_spec=jsonb_set(manifest_spec,'{domainAccess,select,0,recordFilter}','{"quoted_amount":{"_neq":12.00}}')`); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "a", 1)
	if s != 404 {
		t.Fatal(s)
	}
	counts(t, db, 1)
}
func TestCommandUUIDLiteralCase(t *testing.T) {
	e, db, id := commandFixture(t)
	if _, err := db.Exec(`UPDATE core.app_registry SET manifest_spec=jsonb_set(manifest_spec,'{stateMachine,commands,0,recordFilter}','{"quote_file_id":{"_eq":"aAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"}}'),state_machine=jsonb_set(state_machine,'{commands,0,recordFilter}','{"quote_file_id":{"_eq":"aAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"}}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE domain.procurement_requests DISABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE domain.procurement_requests SET quote_file_id='aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE domain.procurement_requests ENABLE TRIGGER USER`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO core.file_objects VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','11111111-1111-4111-8111-111111111111','procurement','CLEAN')`); err != nil {
		t.Fatal(err)
	}
	_, s, _ := execute(e, id, "uuid", 1)
	if s != 200 {
		t.Fatal(s)
	}
}
