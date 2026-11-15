// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

type Citation = SearchHit

func citationURL(doc, version, chunk string) string {
	return "/api/cap/ai/documents/" + doc + "/citations/" + chunk + "?version_id=" + version
}

func (s *Store) Citation(ctx context.Context, actor *auth.Identity, documentID, versionID, chunkID string) (Citation, error) {
	var result Citation
	if !idPattern.MatchString(documentID) || !idPattern.MatchString(versionID) || !idPattern.MatchString(chunkID) {
		return result, ErrNotFound
	}
	before, err := NewPolicy(s.db).CanRead(ctx, actor, documentID)
	if err != nil {
		return result, err
	}
	if before.VersionID != versionID {
		return result, ErrNotFound
	}
	if err = decodeRow(s.db.QueryRowContext(ctx, `SELECT cap_ai.citation_data($1,$2,$3,$4,$5)`, actor.UserID, actor.Role, documentID, versionID, chunkID), &result); err != nil {
		return result, err
	}
	after, err := NewPolicy(s.db).CanRead(ctx, actor, documentID)
	if err != nil {
		return Citation{}, err
	}
	if before != after || result.RunID != after.RunID {
		return Citation{}, ErrNotFound
	}
	result.RunID = ""
	result.URL = citationURL(documentID, versionID, chunkID)
	return result, nil
}
