//go:build integration && rag_live

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package files

import (
	"os/exec"
	"testing"
)

// TestPrivateHasuraStaleSubscription needs the explicit disposable real-service
// environment. It never substitutes a fake Hasura or skips missing prerequisites.
func TestPrivateHasuraStaleSubscription(t *testing.T) {
	out, err := exec.Command("../../../scripts/test-rag.sh", "source").CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal("live private source qualification did not pass:", err)
	}
}
