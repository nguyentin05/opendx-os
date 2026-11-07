// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package process implements work item state commands and their request and response types.
package process

import "time"

// CommandRequest contains only caller-owned command inputs.
type CommandRequest struct {
	EntityID        string         `json:"entity_id"`
	ExpectedVersion int64          `json:"expected_version"`
	Payload         map[string]any `json:"payload"`
}
type CommandResponse struct {
	EntityID   string    `json:"entity_id"`
	Status     string    `json:"status"`
	Version    int64     `json:"version"`
	ExecutedAt time.Time `json:"executed_at"`
}
