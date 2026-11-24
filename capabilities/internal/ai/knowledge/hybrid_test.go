// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"fmt"
	"testing"
)

func TestFusionDeterministic(t *testing.T) {
	lexical := []SearchHit{{DocumentID: "b", ChunkID: "b1"}, {DocumentID: "a", ChunkID: "a1"}}
	semantic := []SearchHit{{DocumentID: "a", ChunkID: "a1"}, {DocumentID: "b", ChunkID: "b1"}}
	result := Fuse(lexical, semantic, 20)
	if len(result) != 2 || result[0].ChunkID != "a1" {
		t.Fatal("unstable tie or duplicate", result)
	}
	many := []SearchHit{}
	for d := 0; d < 15; d++ {
		for c := 0; c < 7; c++ {
			many = append(many, SearchHit{DocumentID: fmt.Sprint(d), ChunkID: fmt.Sprintf("%d-%d", d, c)})
		}
	}
	hits := Fuse(many, many, 100)
	counts := map[string]int{}
	for _, h := range hits {
		counts[h.DocumentID]++
		if counts[h.DocumentID] > 4 {
			t.Fatal("document bound")
		}
	}
	if len(hits) != 20 {
		t.Fatal("result bound", len(hits))
	}
	tail := SearchHit{DocumentID: "tail", ChunkID: "tail"}
	many = append(many, tail)
	for _, h := range Fuse(many, nil, 20) {
		if h.ChunkID == "tail" {
			t.Fatal("ranked beyond top50")
		}
	}
}
