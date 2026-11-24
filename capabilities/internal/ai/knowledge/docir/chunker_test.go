// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestChunkKeepsTableRelation(t *testing.T) {
	b, _ := os.ReadFile("testdata/policy.md")
	r, e := (Parser{}).Parse(context.Background(), requestFor(b, "text/markdown"), bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	chunks, e := Chunk(r, DefaultChunkRecipe())
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, c := range chunks {
		if c.TokenCount > 768 || len(c.ElementIDs) == 0 {
			t.Fatal("chunk bounds/provenance", c)
		}
		if strings.Contains(c.Text, "10,000,000") && strings.Contains(c.Text, "Director") {
			found = true
		}
		if len(c.SectionPath) > 1 && strings.Contains(c.Text, "Amounts are expressed") {
			t.Fatal("overlap crossed section")
		}
	}
	if !found {
		t.Fatal("amount/approver relation split")
	}
}
