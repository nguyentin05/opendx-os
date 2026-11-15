// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package models

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"testing"
)

func TestKillSwitchFallback(t *testing.T) {
	g := Gateway{Policy: &Policy{Enabled: false}}
	if _, err := g.Call(context.Background(), &auth.Identity{UserID: "actor"}, Operation{Key: "k"}, Payload{Query: "private"}); err != ErrPolicy {
		t.Fatal("kill switch allowed model", err)
	}
}
