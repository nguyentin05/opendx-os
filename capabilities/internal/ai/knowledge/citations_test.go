// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"testing"
)

func TestCitationRejectsGuessedMalformedIDs(t *testing.T) {
	s := NewStore(nil)
	for _, id := range []string{"../asset", "not-a-uuid", ""} {
		if _, e := s.Citation(context.Background(), nil, id, id, id); e != ErrNotFound {
			t.Fatal("malformed citation not concealed", e)
		}
	}
}
