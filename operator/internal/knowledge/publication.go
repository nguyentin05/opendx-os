// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"math"
	"sort"
)

type Publication = manifest.PackagePublication
type PublishedSource = manifest.PublishedSource

func digest(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func newID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func sorted(v []string) []string { out := append([]string{}, v...); sort.Strings(out); return out }

// NextPublication is pure: generation history comes from the locked authoritative
// row, never the delayed replica or event. Artifact staging happens separately.
func NextPublication(previous Publication, desired manifest.DXApplication) (Publication, error) {
	if previous.Generation == math.MaxInt64 {
		return Publication{}, fmt.Errorf("publication generation exhausted")
	}
	spec := desired.Spec
	spec.Publication = nil
	out := Publication{Generation: previous.Generation + 1, SpecDigest: digest(spec), Sources: []PublishedSource{}}
	old := map[string]PublishedSource{}
	for _, s := range previous.Sources {
		old[s.SourceID] = s
	}
	staged := map[string]PublishedSource{}
	if desired.Spec.Publication != nil {
		for _, s := range desired.Spec.Publication.Sources {
			staged[s.SourceID] = s
		}
	}
	if k := spec.Knowledge; k != nil {
		for _, s := range k.Sources {
			var managers []string
			if s.Access.GrantsManagedBy != "" {
				managers = k.GrantPolicies[s.Access.GrantsManagedBy].AllowedRoles
			}
			ceiling := digest(struct {
				Mode     string
				Roles    []string
				Manager  string
				Managers []string
			}{s.Access.Mode, sorted(s.Access.AllowedRoles), s.Access.GrantsManagedBy, sorted(managers)})
			n := staged[s.ID]
			n.SourceID = s.ID
			n.SourcePath = s.Path
			n.MediaType = s.MediaType
			n.ParserProfile = s.IndexingProfile
			n.DeclarationHash = ceiling
			p, exists := old[s.ID]
			if exists {
				n.Incarnation = p.Incarnation
				n.AuthorizationGeneration = p.AuthorizationGeneration
				if p.DeclarationHash != ceiling {
					if n.AuthorizationGeneration == math.MaxInt64 {
						return Publication{}, fmt.Errorf("authorization generation exhausted")
					}
					n.AuthorizationGeneration++
				}
			} else {
				var e error
				n.Incarnation, e = newID()
				if e != nil {
					return Publication{}, e
				}
				n.AuthorizationGeneration = out.Generation
			}
			out.Sources = append(out.Sources, n)
		}
	}
	sort.Slice(out.Sources, func(i, j int) bool { return out.Sources[i].SourceID < out.Sources[j].SourceID })
	content := append([]PublishedSource{}, out.Sources...)
	for i := range content {
		content[i].Incarnation = ""
		content[i].AuthorizationGeneration = 0
	}
	out.PackageDigest = digest(content)
	if out.SpecDigest == previous.SpecDigest && out.PackageDigest == previous.PackageDigest {
		return previous, nil
	}
	return out, nil
}
