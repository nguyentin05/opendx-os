// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import "time"

// RecipeConfig pins local execution limits without claiming a qualified parser vendor.
type RecipeConfig struct {
	ID                                         string
	MaxBytes, MaxPages, MaxElements, MaxChunks int
	Timeout                                    time.Duration
	LocalOnly                                  bool
}

func (c RecipeConfig) Validate() error {
	if c.ID != "standard-text-v1" || !c.LocalOnly || c.MaxBytes < 1 || c.MaxBytes > 20<<20 || c.MaxPages < 1 || c.MaxPages > 200 || c.MaxElements < 1 || c.MaxElements > 50000 || c.MaxChunks < 1 || c.MaxChunks > 20000 || c.Timeout <= 0 || c.Timeout > 120*time.Second {
		return ErrInvalid
	}
	return nil
}

func DefaultRecipe() RecipeConfig {
	return RecipeConfig{ID: "standard-text-v1", MaxBytes: 20 << 20, MaxPages: 200, MaxElements: 50000, MaxChunks: 20000, Timeout: 120 * time.Second, LocalOnly: true}
}
