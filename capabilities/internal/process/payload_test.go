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
	"encoding/json"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

func TestPayloadWritesFollowTheDeclarationAndColumnTypes(t *testing.T) {
	table := &registry.Table{Name: "domain.samples", Columns: []registry.Column{
		{Name: "note", Type: "text", Nullable: true},
		{Name: "title", Type: "text"},
		{Name: "amount", Type: "numeric(14,2)", Nullable: true},
		{Name: "due_on", Type: "date", Nullable: true},
		{Name: "batch_id", Type: "uuid", Nullable: true},
	}}
	cmd := &registry.Command{Name: "close", Payload: []registry.PayloadField{
		{Column: "note", Required: true}, {Column: "title"}, {Column: "amount"}, {Column: "due_on"}, {Column: "batch_id"},
	}}

	writes, err := payloadWrites(table, cmd, map[string]any{
		"note": "done", "amount": json.Number("1250.50"), "due_on": "2026-12-01", "batch_id": "22222222-2222-4222-8222-222222222222",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, w := range writes {
		got[w.column] = w.cast + "=" + w.value.(string)
	}
	want := map[string]string{"note": "text=done", "amount": "numeric=1250.50", "due_on": "date=2026-12-01", "batch_id": "uuid=22222222-2222-4222-8222-222222222222"}
	if len(got) != len(want) {
		t.Fatalf("writes = %v, want %v (an absent column is left alone)", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}

	// A JSON number decoded without UseNumber is accepted too; null clears a nullable column.
	writes, err = payloadWrites(table, cmd, map[string]any{"note": "done", "amount": 12.5, "due_on": nil})
	if err != nil || len(writes) != 3 || writes[1].value != "12.5" || writes[2].value != nil || writes[2].cast != "date" {
		t.Fatalf("number and null: %+v %v", writes, err)
	}

	for name, payload := range map[string]map[string]any{
		"required column missing":       {"amount": 1.0},
		"required column blank":         {"note": " "},
		"null in a NOT NULL column":     {"note": "x", "title": nil},
		"text too long":                 {"note": strings.Repeat("x", maxPayloadText+1)},
		"number as text":                {"note": "x", "amount": "12"},
		"text as number":                {"note": 5},
		"a date that is not a date":     {"note": "x", "due_on": "01/12/2026"},
		"a uuid that is not a uuid":     {"note": "x", "batch_id": "abc"},
		"a nested value":                {"note": map[string]any{"a": 1}},
		"a column that is not declared": {"note": "x", "other": 1},
	} {
		if _, err := payloadWrites(table, cmd, payload); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A command without a declared payload accepts only an empty one.
	if _, err := payloadWrites(table, &registry.Command{Name: "open"}, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := payloadWrites(table, &registry.Command{Name: "open"}, map[string]any{"note": "x"}); err == nil {
		t.Fatal("a payload for a command that declares none must be refused")
	}
}
