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

package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"
)

// Parse parses and validates a DXApplication manifest from an io.Reader.
func Parse(r io.Reader) (*DXApplication, error) {
	rawBytes, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if len(bytes.TrimSpace(rawBytes)) == 0 {
		return nil, errors.New("empty manifest")
	}

	// 1. Decode into generic shape to check unknown fields, null scalars, and multiple documents
	shapeDec := yaml.NewDecoder(bytes.NewReader(rawBytes))
	var shape any
	if err := shapeDec.Decode(&shape); err != nil {
		return nil, fmt.Errorf("decode manifest YAML: %w", err)
	}
	if err := shapeDec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("multiple YAML documents are not allowed")
	}
	if err := checkShape(shape, reflect.TypeOf(DXApplication{})); err != nil {
		return nil, fmt.Errorf("invalid manifest structure: %w", err)
	}

	// 2. Normalize through JSON with UseNumber so numeric literals in recordFilter preserve exact precision
	jsonBytes, err := json.Marshal(shape)
	if err != nil {
		return nil, fmt.Errorf("marshal manifest shape: %w", err)
	}
	var app DXApplication
	jsonDec := json.NewDecoder(bytes.NewReader(jsonBytes))
	jsonDec.DisallowUnknownFields()
	jsonDec.UseNumber()
	if err := jsonDec.Decode(&app); err != nil {
		return nil, fmt.Errorf("decode normalized manifest: %w", err)
	}

	// 3. Validate semantic and C0 contract rules
	if err := Validate(&app); err != nil {
		return nil, err
	}

	return &app, nil
}

// ParseFile parses and validates a manifest from a file path.
func ParseFile(path string) (*DXApplication, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open manifest file: %w", err)
	}
	defer f.Close()
	return Parse(f)
}
