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
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// FakeHasura is an in-memory stand-in for the Hasura metadata API. It keeps
// tracked tables, permissions and relationships, and answers export_metadata in
// the Hasura v2 (version 3) shape so drift detection can be exercised. Stored
// permission bodies are returned verbatim; real Hasura may normalize defaults.
type FakeHasura struct {
	mu     sync.Mutex
	tables map[string]*fakeTable
	writes int
}

type tableRef struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
}

type fakeTable struct {
	ref   tableRef
	perms map[string]map[string]json.RawMessage // op -> role -> permission
	rels  map[string]string                     // name -> object|array
}

// NewFakeHasura returns an empty fake.
func NewFakeHasura() *FakeHasura { return &FakeHasura{tables: map[string]*fakeTable{}} }

func (f *FakeHasura) table(ref tableRef) *fakeTable {
	key := ref.Schema + "." + ref.Name
	t := f.tables[key]
	if t == nil {
		t = &fakeTable{ref: ref, perms: map[string]map[string]json.RawMessage{}, rels: map[string]string{}}
		f.tables[key] = t
	}
	return t
}

// ServeHTTP implements the subset of /v1/metadata used by the operator.
func (f *FakeHasura) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		Args struct {
			Table        tableRef        `json:"table"`
			Role         string          `json:"role"`
			Permission   json.RawMessage `json:"permission"`
			Name         string          `json:"name"`
			Relationship string          `json:"relationship"`
		} `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"code":"parse-failed"}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	reply := func(status int, body string) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
	switch {
	case req.Type == "export_metadata":
		reply(http.StatusOK, f.exportLocked())
	case req.Type == "pg_track_table":
		f.table(req.Args.Table)
		reply(http.StatusOK, `{"message":"success"}`)
	case strings.HasPrefix(req.Type, "pg_create_") && strings.HasSuffix(req.Type, "_permission"):
		op := strings.TrimSuffix(strings.TrimPrefix(req.Type, "pg_create_"), "_permission")
		t := f.table(req.Args.Table)
		if t.perms[op] == nil {
			t.perms[op] = map[string]json.RawMessage{}
		}
		if _, exists := t.perms[op][req.Args.Role]; exists {
			reply(http.StatusBadRequest, `{"code":"already-exists"}`)
			return
		}
		t.perms[op][req.Args.Role] = req.Args.Permission
		f.writes++
		reply(http.StatusOK, `{"message":"success"}`)
	case strings.HasPrefix(req.Type, "pg_drop_") && strings.HasSuffix(req.Type, "_permission"):
		op := strings.TrimSuffix(strings.TrimPrefix(req.Type, "pg_drop_"), "_permission")
		t := f.tables[req.Args.Table.Schema+"."+req.Args.Table.Name]
		if t == nil || t.perms[op] == nil || t.perms[op][req.Args.Role] == nil {
			reply(http.StatusBadRequest, `{"code":"not-exists"}`)
			return
		}
		delete(t.perms[op], req.Args.Role)
		f.writes++
		reply(http.StatusOK, `{"message":"success"}`)
	case req.Type == "pg_create_object_relationship" || req.Type == "pg_create_array_relationship":
		t := f.table(req.Args.Table)
		if _, exists := t.rels[req.Args.Name]; exists {
			reply(http.StatusBadRequest, `{"code":"already-exists"}`)
			return
		}
		t.rels[req.Args.Name] = strings.TrimSuffix(strings.TrimPrefix(req.Type, "pg_create_"), "_relationship")
		f.writes++
		reply(http.StatusOK, `{"message":"success"}`)
	case req.Type == "pg_drop_relationship":
		t := f.tables[req.Args.Table.Schema+"."+req.Args.Table.Name]
		if t == nil {
			reply(http.StatusBadRequest, `{"code":"not-exists"}`)
			return
		}
		if _, exists := t.rels[req.Args.Relationship]; !exists {
			reply(http.StatusBadRequest, `{"code":"not-exists"}`)
			return
		}
		delete(t.rels, req.Args.Relationship)
		f.writes++
		reply(http.StatusOK, `{"message":"success"}`)
	default:
		reply(http.StatusOK, `{"message":"success"}`)
	}
}

func (f *FakeHasura) exportLocked() string {
	keys := make([]string, 0, len(f.tables))
	for k := range f.tables {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	type perm struct {
		Role       string          `json:"role"`
		Permission json.RawMessage `json:"permission"`
	}
	type rel struct {
		Name string `json:"name"`
	}
	tables := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		t := f.tables[k]
		entry := map[string]any{"table": t.ref}
		for _, op := range []string{"select", "insert", "update"} {
			roles := make([]string, 0, len(t.perms[op]))
			for role := range t.perms[op] {
				roles = append(roles, role)
			}
			sort.Strings(roles)
			var list []perm
			for _, role := range roles {
				list = append(list, perm{Role: role, Permission: t.perms[op][role]})
			}
			if len(list) > 0 {
				entry[op+"_permissions"] = list
			}
		}
		for _, kind := range []string{"object", "array"} {
			var list []rel
			for name, k := range t.rels {
				if k == kind {
					list = append(list, rel{Name: name})
				}
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			if len(list) > 0 {
				entry[kind+"_relationships"] = list
			}
		}
		tables = append(tables, entry)
	}
	out, _ := json.Marshal(map[string]any{
		"version": 3,
		"sources": []any{map[string]any{"name": "default", "kind": "postgres", "tables": tables}},
	})
	return string(out)
}

// Has reports whether a permission exists; key is "op:schema.table:role".
func (f *FakeHasura) Has(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, table, role, ok := splitKey(key)
	if !ok || f.tables[table] == nil || f.tables[table].perms[op] == nil {
		return false
	}
	_, exists := f.tables[table].perms[op][role]
	return exists
}

// Drop removes a permission out of band, simulating manual drift.
func (f *FakeHasura) Drop(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if op, table, role, ok := splitKey(key); ok && f.tables[table] != nil && f.tables[table].perms[op] != nil {
		delete(f.tables[table].perms[op], role)
	}
}

// Writes counts state-changing metadata calls.
func (f *FakeHasura) Writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func splitKey(key string) (op, table, role string, ok bool) {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// PermKey formats a permission key for Has and Drop.
func PermKey(op, schema, table, role string) string {
	return fmt.Sprintf("%s:%s.%s:%s", op, schema, table, role)
}
