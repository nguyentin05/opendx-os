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
	"net/http/httptest"
	"testing"
)

func TestSafeErrorEnvelope(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 503} {
		w := httptest.NewRecorder()
		WriteError(w, status)
		var body map[string]map[string]string
		if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil || w.Code != status || body["error"]["code"] == "" || body["error"]["message"] == "" {
			t.Fatalf("%d: %s", status, w.Body)
		}
	}
}
