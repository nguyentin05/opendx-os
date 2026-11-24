// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package registry

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

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
