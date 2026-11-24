// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package manifest_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/testutil"
)

var procurementManifest = filepath.Join("..", "..", "..", "apps", "procurement", "manifest.yaml")

func TestAllColumnsExpansion(t *testing.T) {
	written, err := manifest.ParseFileAsWritten(procurementManifest)
	if err != nil {
		t.Fatal(err)
	}
	if got := written.Table("domain.procurement_requests").Access.Select[0].Columns; !slices.Equal(got, []string{"*"}) {
		t.Fatalf("as written, the sample keeps the shorthand; got %v", got)
	}

	// The sample's "*" select rules expand to the list it wrote out before.
	app := testutil.LoadApp(t, "procurement")
	want := []string{"id", "created_by", "org_unit_id", "vendor_name", "item_description", "quoted_amount",
		"quote_file_id", "rejection_reason", "vendor_id", "status", "version", "created_at", "updated_at"}
	for _, rule := range app.Table("domain.procurement_requests").Access.Select {
		if !slices.Equal(rule.Columns, want) {
			t.Fatalf("%s select: got %v, want %v", rule.Role, rule.Columns, want)
		}
	}

	// insert and update get business columns only; an insert preset is not
	// writable; a child table reads its parent column but never writes it.
	requests := written.Table("domain.procurement_requests")
	requests.Access.Insert[0].Columns = manifest.Columns{"*"}
	requests.Access.Insert[0].Presets = map[string]string{"vendor_id": "X-Hasura-User-Id"}
	requests.Access.Update[0].Columns = manifest.Columns{"*"}
	items := written.Table("domain.procurement_items")
	items.Access.Select[0].Columns = manifest.Columns{"*"}
	items.Access.Insert[0].Columns = manifest.Columns{"*"}
	if err := manifest.Validate(written); err != nil {
		t.Fatal(err)
	}
	manifest.ExpandColumns(written)
	for _, c := range []struct {
		name      string
		got, want []string
	}{
		{"insert with preset", requests.Access.Insert[0].Columns, []string{"vendor_name", "item_description", "quoted_amount", "quote_file_id", "rejection_reason"}},
		{"update", requests.Access.Update[0].Columns, []string{"vendor_name", "item_description", "quoted_amount", "quote_file_id", "rejection_reason", "vendor_id"}},
		{"child select", items.Access.Select[0].Columns, []string{"id", "request_id", "created_by", "item", "quantity", "unit_price", "version", "created_at", "updated_at"}},
		{"child insert", items.Access.Insert[0].Columns, []string{"item", "quantity", "unit_price"}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	// Every system column is covered by a select "*".
	if got := len(items.Access.Select[0].Columns); got != len(manifest.SystemColumns(written, items))+len(items.Columns) {
		t.Errorf("child select expanded to %d columns", got)
	}
}

func TestAllColumnsRefusals(t *testing.T) {
	raw, err := os.ReadFile(procurementManifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ columns, want string }{
		{`"all"`, `columns must be a list of column names or "*"`},
		{`["*", "vendor_name"]`, "invalid permission rule"},
		{`[]`, "invalid permission rule"},
	} {
		text := strings.Replace(string(raw), `columns: "*"`, "columns: "+c.columns, 1)
		if _, err := manifest.Parse(strings.NewReader(text)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("columns: %s: got %v, want an error containing %q", c.columns, err, c.want)
		}
	}
	omitted := strings.Replace(string(raw), "        columns: \"*\"\n", "", 1)
	if _, err := manifest.Parse(strings.NewReader(omitted)); err == nil || !strings.Contains(err.Error(), `missing required field "columns"`) {
		t.Errorf("omitted columns: got %v", err)
	}
}
