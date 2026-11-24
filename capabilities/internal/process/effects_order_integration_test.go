//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package process

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

func effectEvent(t *testing.T, o *org, entity, command string) string {
	t.Helper()
	var id string
	if err := o.p.Admin.QueryRow(`SELECT id::text FROM core.outbox_events
		WHERE entity_id=$1 AND payload->'payload'->>'command'=$2 ORDER BY entity_version DESC LIMIT 1`, entity, command).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func syntheticEvent(t *testing.T, o *org, entity string, version int64, command string) string {
	t.Helper()
	var id string
	if err := o.p.Admin.QueryRow(`INSERT INTO core.outbox_events
		(app_id,event_type,entity_type,entity_id,entity_version,payload)
		VALUES('procurement','command.committed','domain.procurement_requests',$1,$2,
		       jsonb_build_object('payload',jsonb_build_object('command',$3::text)))
		RETURNING id::text`, entity, version, command).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func applyEffect(t *testing.T, o *org, service *auth.Identity, event, step string) json.RawMessage {
	t.Helper()
	result, err := o.engine.ExecuteEffect(context.Background(), service, event, step)
	if err != nil {
		t.Fatalf("%s: %v", step, err)
	}
	return result
}

func sameJSON(t *testing.T, left, right json.RawMessage) bool {
	t.Helper()
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		t.Fatalf("invalid effect result: %s / %s", left, right)
	}
	return reflect.DeepEqual(a, b)
}

func openItems(t *testing.T, o *org, entity string) int {
	t.Helper()
	return o.count(t, `SELECT count(*) FROM cap_work.work_items WHERE source_id=$1 AND board_column<>'DONE'`, entity)
}

func TestEffectSubmitCancelOrderLeavesNoOpenItem(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		name := "in order"
		if reversed {
			name = "reversed"
		}
		t.Run(name, func(t *testing.T) {
			o := newOrg(t)
			service := o.workflow(t)
			entity := o.request(t, o.alice)
			if _, status := o.run(o.alice, "role_procurement_requester", "submit", entity, "submit", 1); status != 200 {
				t.Fatalf("submit: %d", status)
			}
			if _, status := o.run(o.alice, "role_procurement_requester", "cancel", entity, "cancel", 2); status != 200 {
				t.Fatalf("cancel: %d", status)
			}
			create := struct{ event, step string }{effectEvent(t, o, entity, "submit"), "create_review"}
			close := struct{ event, step string }{effectEvent(t, o, entity, "cancel"), "close_review"}
			calls := []struct{ event, step string }{create, close}
			if reversed {
				calls[0], calls[1] = calls[1], calls[0]
			}
			var createResult json.RawMessage
			for _, call := range calls {
				result := applyEffect(t, o, service, call.event, call.step)
				if call.step == "create_review" {
					createResult = result
				}
			}
			if n := openItems(t, o, entity); n != 0 {
				t.Fatalf("open items = %d, want 0", n)
			}
			if reversed {
				var skipped struct {
					Skipped string `json:"skipped"`
				}
				if err := json.Unmarshal(createResult, &skipped); err != nil || skipped.Skipped != "superseded" {
					t.Fatalf("reversed create result = %s, want a superseded result", createResult)
				}
				retry := applyEffect(t, o, service, create.event, create.step)
				if !sameJSON(t, createResult, retry) {
					t.Fatalf("skipped create retry changed result: %s then %s", createResult, retry)
				}
			}
		})
	}
}

func TestEffectAllArrivalOrdersKeepTheNewestCreate(t *testing.T) {
	orders := [][]int{
		{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0},
	}
	for _, order := range orders {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			o := newOrg(t)
			service := o.workflow(t)
			entity := o.request(t, o.alice)
			calls := []struct{ event, step string }{
				{syntheticEvent(t, o, entity, 1, "submit"), "create_review"},
				{syntheticEvent(t, o, entity, 2, "cancel"), "close_review"},
				{syntheticEvent(t, o, entity, 3, "submit"), "create_review"},
			}
			for _, index := range order {
				applyEffect(t, o, service, calls[index].event, calls[index].step)
			}
			if n := openItems(t, o, entity); n != 1 {
				t.Fatalf("open items = %d, want 1", n)
			}
			if n := o.count(t, `SELECT count(*) FROM cap_work.work_items WHERE source_id=$1 AND board_column<>'DONE' AND source_version=3`, entity); n != 1 {
				t.Fatalf("open items from version 3 = %d, want 1", n)
			}
		})
	}
}

func TestEffectRetriesReturnTheRecordedResult(t *testing.T) {
	o := newOrg(t)
	service := o.workflow(t)
	entity := o.request(t, o.alice)
	createEvent := syntheticEvent(t, o, entity, 1, "submit")
	closeEvent := syntheticEvent(t, o, entity, 2, "cancel")

	firstCreate := applyEffect(t, o, service, createEvent, "create_review")
	againCreate := applyEffect(t, o, service, createEvent, "create_review")
	if !sameJSON(t, firstCreate, againCreate) {
		t.Fatalf("create retry changed result: %s then %s", firstCreate, againCreate)
	}
	firstClose := applyEffect(t, o, service, closeEvent, "close_review")
	againClose := applyEffect(t, o, service, closeEvent, "close_review")
	if !sameJSON(t, firstClose, againClose) {
		t.Fatalf("close retry changed result: %s then %s", firstClose, againClose)
	}
	if n := o.count(t, `SELECT count(*) FROM cap_work.work_items WHERE source_id=$1`, entity); n != 1 {
		t.Fatalf("work items = %d, want 1", n)
	}
	if n := openItems(t, o, entity); n != 0 {
		t.Fatalf("open items = %d, want 0", n)
	}
}

func TestEffectConcurrentCreateAndCompleteLeavesNoOpenItem(t *testing.T) {
	o := newOrg(t)
	service := o.workflow(t)
	entity := o.request(t, o.alice)
	createEvent := syntheticEvent(t, o, entity, 1, "submit")
	closeEvent := syntheticEvent(t, o, entity, 2, "cancel")

	start := make(chan struct{})
	var wg sync.WaitGroup
	var createErr, closeErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, createErr = o.engine.ExecuteEffect(context.Background(), service, createEvent, "create_review")
	}()
	go func() {
		defer wg.Done()
		<-start
		_, closeErr = o.engine.ExecuteEffect(context.Background(), service, closeEvent, "close_review")
	}()
	close(start)
	wg.Wait()
	if createErr != nil || closeErr != nil {
		t.Fatalf("concurrent effects: create=%v close=%v", createErr, closeErr)
	}
	if n := openItems(t, o, entity); n != 0 {
		t.Fatalf("open items = %d, want 0", n)
	}
}
