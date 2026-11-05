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

// CommandRequest represents a state transition command.
type CommandRequest struct {
	CommandID       string         `json:"command_id"`
	TargetTable     string         `json:"target_table"`
	TargetID        string         `json:"target_id"`
	Action          string         `json:"action"`
	ExpectedVersion int            `json:"expected_version"`
	TargetStatus    string         `json:"target_status"`
	AttachmentID    string         `json:"attachment_id,omitempty"`
	Payload         map[string]any `json:"payload,omitempty"`
}

// CommandResponse represents the execution result.
type CommandResponse struct {
	CommandID    string    `json:"command_id"`
	TargetID     string    `json:"target_id"`
	Status       string    `json:"status"`
	Version      int       `json:"version"`
	ExecutedAt   time.Time `json:"executed_at"`
	IdempotentRe bool      `json:"idempotent_re"`
}
