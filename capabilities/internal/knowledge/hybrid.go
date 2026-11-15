// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import "sort"

// Fuse implements reciprocal-rank fusion over bounded, already authorized lists.
// A chunk contributes at most once per list. Scores never cross incompatible
// profiles; that boundary is enforced by the scoped semantic query.
func Fuse(lexical, semantic []SearchHit, limit int) []SearchHit {
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	type scored struct {
		hit   SearchHit
		score float64
	}
	scores := map[string]*scored{}
	for _, list := range [][]SearchHit{lexical, semantic} {
		seen := map[string]bool{}
		for rank, h := range list {
			if rank >= 50 {
				break
			}
			key := h.DocumentID + "/" + h.VersionID + "/" + h.ChunkID
			if seen[key] {
				continue
			}
			seen[key] = true
			if scores[key] == nil {
				scores[key] = &scored{hit: h}
			}
			scores[key].score += 1 / float64(60+rank+1)
		}
	}
	ordered := make([]*scored, 0, len(scores))
	for _, s := range scores {
		ordered = append(ordered, s)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.hit.DocumentID != b.hit.DocumentID {
			return a.hit.DocumentID < b.hit.DocumentID
		}
		if a.hit.VersionID != b.hit.VersionID {
			return a.hit.VersionID < b.hit.VersionID
		}
		return a.hit.ChunkID < b.hit.ChunkID
	})
	out := []SearchHit{}
	counts := map[string]int{}
	for _, s := range ordered {
		if counts[s.hit.DocumentID] >= 4 {
			continue
		}
		out = append(out, s.hit)
		counts[s.hit.DocumentID]++
		if len(out) == limit {
			break
		}
	}
	return out
}
