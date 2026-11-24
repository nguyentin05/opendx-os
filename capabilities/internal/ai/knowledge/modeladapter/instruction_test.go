// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modeladapter

import (
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// TestGenerationInstructionIsShared compares the adapter's instruction with the
// shared constant across packages. The adapter no longer holds its own copy: it
// binds generationSystemInstruction to models.GenerationSystemInstruction, so a
// re-typed literal in either package fails here instead of serving two different
// instructions on two transports.
func TestGenerationInstructionIsShared(t *testing.T) {
	if generationSystemInstruction != models.GenerationSystemInstruction {
		t.Fatalf("adapter instruction = %q, want the shared models.GenerationSystemInstruction", generationSystemInstruction)
	}
	if !strings.HasPrefix(generationSystemInstruction, models.BoundedSystemInstruction) {
		t.Fatalf("adapter instruction = %q, want it to extend models.BoundedSystemInstruction", generationSystemInstruction)
	}
	if generationSystemInstruction == models.BoundedSystemInstruction {
		t.Fatal("adapter instruction is the bare bounded instruction, want the JSON answer contract appended")
	}
}
