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
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
)

// LoadApp parses the sample application package apps/<name>.
func LoadApp(t *testing.T, name string) *manifest.DXApplication {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	app, err := manifest.ParseFile(filepath.Join(root, "apps", name, "manifest.yaml"))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return app
}

// LabelsTable is the name WithLabels gives the plain table it adds to app.
func LabelsTable(app *manifest.DXApplication) string {
	return "domain." + app.Metadata.Name + "_labels"
}

// WithLabels returns a copy of app with a second table: a plain list of labels
// that every role of the first table reads and its first insert role
// maintains, and a nullable label_id foreign key from the first table to it.
func WithLabels(app *manifest.DXApplication) *manifest.DXApplication {
	out := app.Clone()
	first := &out.Spec.Tables[0]
	labels := manifest.TableDef{
		Name:    LabelsTable(app),
		Columns: []manifest.ColumnDef{{Name: "title", Type: "text", Check: "length(trim(title)) > 0"}},
		UI: manifest.UISchemaDef{
			ListColumns: []string{"title"},
			FormFields:  []manifest.FormFieldDef{{Name: "title", Label: "Title", Type: "text", Required: true}},
		},
	}
	for _, sel := range first.Access.Select {
		labels.Access.Select = append(labels.Access.Select, manifest.AccessRule{
			Role: sel.Role, Scope: manifest.ScopeOrganization, Columns: []string{"id", "title", "version"},
		})
	}
	writer := first.Access.Insert[0].Role
	labels.Access.Insert = []manifest.AccessRule{{Role: writer, Columns: []string{"title"}}}
	labels.Access.Update = []manifest.AccessRule{{Role: writer, Scope: manifest.ScopeOwn, Columns: []string{"title"}}}

	first.Columns = append(first.Columns, manifest.ColumnDef{Name: "label_id", Type: "uuid", Nullable: true})
	first.ForeignKeys = append(first.ForeignKeys, manifest.ForeignKeyDef{Column: "label_id", References: labels.Name})
	for i := range first.Access.Select {
		first.Access.Select[i].Columns = append(first.Access.Select[i].Columns, "label_id")
	}
	first.Access.Insert[0].Columns = append(first.Access.Insert[0].Columns, "label_id")
	out.Spec.Tables = append(out.Spec.Tables, labels)
	return out
}

// TableName returns the unqualified name of a domain table.
func TableName(qualified string) string { return strings.TrimPrefix(qualified, "domain.") }
