// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

// Package contracts defines the read scope shared by knowledge services and
// the model boundary.
package contracts

type ReadScope struct {
	DocumentID               string `json:"document_id"`
	VersionID                string `json:"version_id"`
	RunID                    string `json:"run_id"`
	PolicyRevision           int64  `json:"policy_revision"`
	AuthorizationGeneration  int64  `json:"authorization_generation"`
	SourceIncarnation        string `json:"source_incarnation"`
	AppPublicationGeneration int64  `json:"app_publication_generation"`
	ProcessingPolicyID       string `json:"processing_policy_id"`
	ProcessingPolicyVersion  int64  `json:"processing_policy_version"`
}
