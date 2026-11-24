// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package registry

import (
	"strings"
	"testing"
)

func TestColumnRefusalReasons(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   []string
	}{
		{
			"invalid identifier",
			func(table map[string]any) {
				table["columns"].([]any)[0].(map[string]any)["name"] = "Bad"
			},
			[]string{`column "Bad": invalid identifier`},
		},
		{
			"duplicate column",
			func(table map[string]any) {
				columns := table["columns"].([]any)
				table["columns"] = append(columns, map[string]any{
					"name": "vendor_name", "type": "text", "nullable": true,
				})
			},
			[]string{`column "vendor_name": duplicate column`},
		},
		{
			"reserved system column",
			func(table map[string]any) {
				table["columns"].([]any)[0].(map[string]any)["name"] = "created_by"
			},
			[]string{`column "created_by": reserved system column`},
		},
		{
			"unsupported type",
			func(table map[string]any) {
				column := table["columns"].([]any)[0].(map[string]any)
				column["name"] = "calibrated"
				column["type"] = "boolean"
			},
			[]string{`column "calibrated": unsupported type "boolean"`, "uuid, text, date, numeric(14,2), numeric(4,2)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := readyRow(t)
			table := row["spec"].(map[string]any)["tables"].([]any)[0].(map[string]any)
			tc.mutate(table)
			_, err := decodeRow(row)
			if err == nil {
				t.Fatal("column must be refused")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}
