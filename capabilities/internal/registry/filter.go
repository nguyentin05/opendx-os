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

package registry

import (
	"encoding/json"
	"errors"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"math/big"
	"reflect"
	"strings"
)

// Matches evaluates the validated core filter grammar with SQL NULL semantics.
func Matches(f map[string]any, row map[string]any, id *auth.Identity, types ...map[string]string) (bool, error) {
	for k, v := range f {
		if k == "_and" || k == "_or" {
			a, ok := v.([]any)
			if !ok || len(a) == 0 {
				return false, errors.New("unsupported filter")
			}
			result := k == "_and"
			for _, x := range a {
				m, ok := x.(map[string]any)
				if !ok {
					return false, errors.New("unsupported filter")
				}
				b, e := Matches(m, row, id, types...)
				if e != nil {
					return false, e
				}
				if k == "_and" {
					result = result && b
				} else {
					result = result || b
				}
			}
			if !result {
				return false, nil
			}
			continue
		}
		actual, exists := row[k]
		if !exists {
			return false, errors.New("unknown column")
		}
		ops, ok := v.(map[string]any)
		if !ok || len(ops) == 0 {
			return false, errors.New("unsupported filter")
		}
		for op, want := range ops {
			if s, ok := want.(string); ok && id != nil {
				if s == "X-Hasura-User-Id" {
					want = id.UserID
				}
				if s == "X-Hasura-Workspace-Id" {
					want = id.WorkspaceID
				}
			}
			if op != "_eq" && op != "_neq" {
				return false, errors.New("unsupported operator")
			}
			if want != nil && actual == nil {
				return false, nil
			}
			kind := ""
			if len(types) > 0 {
				kind = types[0][k]
			}
			eq, err := scalarEqual(actual, want, kind)
			if err != nil {
				return false, err
			}
			if op == "_eq" && !eq || op == "_neq" && eq {
				return false, nil
			}
		}
	}
	return true, nil
}

// scalarEqual mirrors scalar SQL equality without binary floating-point conversion.
func scalarEqual(actual, want any, kind string) (bool, error) {
	if actual == nil || want == nil {
		return actual == nil && want == nil, nil
	}
	if kind == "uuid" {
		a, ok := actual.(string)
		b, bok := want.(string)
		if !ok || !bok || !uuidPattern.MatchString(a) || !uuidPattern.MatchString(b) {
			return false, errors.New("invalid UUID comparison")
		}
		return strings.EqualFold(a, b), nil
	}
	if kind == "integer" || kind == "bigint" || kind == "numeric" || strings.HasPrefix(kind, "numeric(") {
		a, ok := actual.(json.Number)
		b, bok := want.(json.Number)
		if !ok || !bok {
			return false, errors.New("invalid numeric comparison")
		}
		ar, aok := new(big.Rat).SetString(string(a))
		br, bok := new(big.Rat).SetString(string(b))
		if !aok || !bok {
			return false, errors.New("invalid numeric comparison")
		}
		return ar.Cmp(br) == 0, nil
	}
	return reflect.DeepEqual(actual, want), nil
}
