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
	"io"
	"net/http"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// FakeKestra models the flow endpoints of the Kestra API the operator uses:
// basic authentication, create, read, update and delete of a flow by
// namespace and id, with a revision that increases when the source changes.
type FakeKestra struct {
	User, Password string

	mu     sync.Mutex
	flows  map[string]*fakeFlow
	writes int
	// FailOn makes every call for the flow "<namespace>/<id>" answer HTTP 500.
	FailOn string
}

type fakeFlow struct {
	source   string
	revision int
	disabled bool
}

// NewFakeKestra returns an empty engine that accepts the given credentials.
func NewFakeKestra(user, password string) *FakeKestra {
	return &FakeKestra{User: user, Password: password, flows: map[string]*fakeFlow{}}
}

func (f *FakeKestra) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, password, ok := r.BasicAuth()
	if !ok || user != f.User || password != f.Password {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	key := strings.TrimPrefix(r.URL.Path, "/api/v1/flows")
	key = strings.TrimPrefix(key, "/")
	if r.Method == http.MethodPost && key == "" {
		var head struct {
			ID        string `yaml:"id"`
			Namespace string `yaml:"namespace"`
		}
		if yaml.Unmarshal(body, &head) != nil || head.ID == "" || head.Namespace == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		key = head.Namespace + "/" + head.ID
		if f.fails(w, key) {
			return
		}
		if _, exists := f.flows[key]; exists {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		f.flows[key] = &fakeFlow{source: string(body), revision: 1}
		f.writes++
		f.reply(w, key)
		return
	}
	if f.fails(w, key) {
		return
	}
	flow := f.flows[key]
	switch r.Method {
	case http.MethodGet:
		if flow == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.reply(w, key)
	case http.MethodPut:
		if flow == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if flow.source != string(body) || flow.disabled {
			flow.source, flow.disabled = string(body), false
			flow.revision++
		}
		f.writes++
		f.reply(w, key)
	case http.MethodDelete:
		if flow == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(f.flows, key)
		f.writes++
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *FakeKestra) fails(w http.ResponseWriter, key string) bool {
	if f.FailOn != "" && f.FailOn == key {
		http.Error(w, "injected failure", http.StatusInternalServerError)
		return true
	}
	return false
}

func (f *FakeKestra) reply(w http.ResponseWriter, key string) {
	flow := f.flows[key]
	parts := strings.SplitN(key, "/", 2)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{ // Best-effort; the test client is local.
		"namespace": parts[0], "id": parts[1], "revision": flow.revision, "disabled": flow.disabled, "deleted": false, "source": flow.source,
	})
}

// Source returns the deployed source of "<namespace>/<id>" and whether it exists.
func (f *FakeKestra) Source(key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	flow, ok := f.flows[key]
	if !ok {
		return "", false
	}
	return flow.source, true
}

// Flows returns the keys of the deployed flows.
func (f *FakeKestra) Flows() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.flows))
	for k := range f.flows {
		keys = append(keys, k)
	}
	return keys
}

// Remove deletes a flow out of band, as an administrator would in the engine's UI.
func (f *FakeKestra) Remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.flows, key)
}

// Edit changes a flow out of band: new source and a new revision.
func (f *FakeKestra) Edit(key, source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if flow := f.flows[key]; flow != nil {
		flow.source = source
		flow.revision++
	}
}

// Disable switches a flow off out of band.
func (f *FakeKestra) Disable(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if flow := f.flows[key]; flow != nil {
		flow.disabled = true
		flow.revision++
	}
}

// Writes counts state-changing calls.
func (f *FakeKestra) Writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}
