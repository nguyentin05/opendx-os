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
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

func TestStrictCommandRequest(t *testing.T) {
	for _, s := range []string{`{"entity_id":"x","expected_version":1,"payload":{},"target_table":"domain.x"}`, `{"entity_id":"x","entity_id":"y","expected_version":1,"payload":{}}`, `{} {}`} {
		if _, e := decodeRequest(strings.NewReader(s)); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestGuardNullSemantics(t *testing.T) {
	ok, e := registry.Matches(map[string]any{"x": map[string]any{"_neq": "a"}}, map[string]any{"x": nil}, "")
	if e != nil || ok {
		t.Fatalf("NULL comparison matched: %v %v", ok, e)
	}
}
func TestCanonicalHTTPKeys(t *testing.T) {
	if _, err := decodeRequest(strings.NewReader(`{"ENTITY_ID":"33333333-3333-4333-8333-333333333333","expected_version":1,"payload":{}}`)); err == nil {
		t.Fatal("accepted noncanonical key")
	}
}
