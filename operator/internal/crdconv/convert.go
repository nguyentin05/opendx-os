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

// Package crdconv converts between application packages (manifest.yaml plus
// workflow files) and DXApplication custom resources (ADR 0004).
//
// Both forms share JSON field names, so the spec converts through JSON. Two
// details differ: the resource inlines each workflow definition because the
// controller cannot read package files (the package path is kept as an
// informational field), and a Kubernetes name cannot contain
// "_", so the manifest identifier maps to the resource name by replacing "_"
// with "-" (manifest identifiers never contain "-", which keeps the mapping
// reversible).
package crdconv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	dxosv1alpha1 "github.com/opendx-os/opendx-os/operator/api/v1alpha1"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

const (
	apiVersion  = "dxos.io/v1alpha1"
	kind        = "DXApplication"
	workflowDir = "workflows"
)

// NameFromAppID maps a manifest identifier to a DNS-1123 resource name.
func NameFromAppID(appID string) string { return strings.ReplaceAll(appID, "_", "-") }

// AppIDFromName reverses NameFromAppID.
func AppIDFromName(name string) string { return strings.ReplaceAll(name, "-", "_") }

// Workflows maps a workflow ID to its inline engine definition.
type Workflows map[string]string

// ToManifest converts a resource to the manifest model consumed by the planner and
// runs the semantic validator. The returned Workflows hold the inline definitions
// that the manifest model only references by file name.
func ToManifest(app *dxosv1alpha1.DXApplication) (*manifest.DXApplication, Workflows, error) {
	if app == nil {
		return nil, nil, errors.New("resource is nil")
	}
	raw, err := json.Marshal(app.Spec)
	if err != nil {
		return nil, nil, fmt.Errorf("encode spec: %w", err)
	}
	var spec manifest.AppSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&spec); err != nil {
		return nil, nil, fmt.Errorf("decode spec as manifest: %w", err)
	}
	flows := make(Workflows, len(app.Spec.Workflows))
	for i, wf := range app.Spec.Workflows {
		if wf.File == "" {
			spec.Workflows[i].File = workflowDir + "/" + wf.ID + ".yaml"
		}
		flows[wf.ID] = wf.Definition
	}
	m := &manifest.DXApplication{
		APIVersion: apiVersion,
		Kind:       kind,
		Metadata: manifest.AppMetadata{
			Name:        AppIDFromName(app.Name),
			DisplayName: app.Spec.DisplayName,
			Version:     app.Spec.Version,
		},
		Spec: spec,
	}
	if err := manifest.Validate(m); err != nil {
		return nil, nil, err
	}
	return m, flows, nil
}

// FromPackage builds a resource from an application package directory. The
// package is validated by manifest.ParseFile.
func FromPackage(dir string) (*dxosv1alpha1.DXApplication, error) {
	m, err := manifest.ParseFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(m.Spec)
	if err != nil {
		return nil, fmt.Errorf("encode manifest spec: %w", err)
	}
	var spec dxosv1alpha1.DXApplicationSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("decode manifest spec as resource: %w", err)
	}
	for i, wf := range m.Spec.Workflows {
		// wf.File matched ^workflows/[a-z][a-z0-9_]*\.yaml$ during validation.
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(wf.File)))
		if err != nil {
			return nil, fmt.Errorf("read workflow %s: %w", wf.ID, err)
		}
		if len(bytes.TrimSpace(content)) == 0 {
			return nil, fmt.Errorf("workflow %s: empty definition", wf.ID)
		}
		spec.Workflows[i].Definition = string(content)
	}
	spec.DisplayName = m.Metadata.DisplayName
	spec.Version = m.Metadata.Version

	return &dxosv1alpha1.DXApplication{
		TypeMeta:   metav1.TypeMeta{APIVersion: apiVersion, Kind: kind},
		ObjectMeta: metav1.ObjectMeta{Name: NameFromAppID(m.Metadata.Name)},
		Spec:       spec,
	}, nil
}

// MarshalYAML renders a resource as a single YAML document.
func MarshalYAML(app *dxosv1alpha1.DXApplication) ([]byte, error) {
	return yaml.Marshal(app)
}
