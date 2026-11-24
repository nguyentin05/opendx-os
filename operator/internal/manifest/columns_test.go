// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package manifest

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestValidateColumnRefusalReasons(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DXApplication)
		want   []string
	}{
		{
			"invalid identifier",
			func(a *DXApplication) { a.Spec.Tables[0].Columns[0].Name = "Calibrated" },
			[]string{`column "Calibrated": invalid identifier`},
		},
		{
			"duplicate column",
			func(a *DXApplication) {
				a.Spec.Tables[0].Columns = append(a.Spec.Tables[0].Columns, ColumnDef{Name: "vendor_name", Type: "text"})
			},
			[]string{`column "vendor_name": duplicate column`},
		},
		{
			"reserved system column",
			func(a *DXApplication) { a.Spec.Tables[0].Columns[0].Name = "id" },
			[]string{`column "id": reserved name`},
		},
		{
			"status reserved on a plain table",
			func(a *DXApplication) { a.Spec.Tables[1].Columns[0].Name = "status" },
			[]string{`column "status": reserved name`},
		},
		{
			"org_unit_id reserved",
			func(a *DXApplication) { a.Spec.Tables[1].Columns[0].Name = "org_unit_id" },
			[]string{`column "org_unit_id": reserved name`},
		},
		{
			"unsupported type",
			func(a *DXApplication) {
				a.Spec.Tables[0].Columns[0].Name = "calibrated"
				a.Spec.Tables[0].Columns[0].Type = "boolean"
			},
			[]string{`column "calibrated": unsupported type "boolean"`, "uuid, text, date, numeric(14,2), numeric(4,2)"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := loadFixture(t, "procurement")
			tc.mutate(app)
			err := Validate(app)
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

func TestSupportedColumnTypesMatchSchema(t *testing.T) {
	b, err := os.ReadFile("../../../contracts/manifest-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Properties struct {
			Spec struct {
				Properties struct {
					Tables struct {
						Items struct {
							Properties struct {
								Columns struct {
									Items struct {
										Properties struct {
											Type struct {
												Enum []string `json:"enum"`
											} `json:"type"`
										} `json:"properties"`
									} `json:"items"`
								} `json:"columns"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"tables"`
				} `json:"properties"`
			} `json:"spec"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	schemaTypes := doc.Properties.Spec.Properties.Tables.Items.Properties.Columns.Items.Properties.Type.Enum
	if !slices.Equal(schemaTypes, SupportedColumnTypes) {
		t.Fatalf("supported column types %v do not match the schema %v", SupportedColumnTypes, schemaTypes)
	}
}
