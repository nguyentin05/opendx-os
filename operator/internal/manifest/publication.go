// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package manifest

// PackagePublication is a server-only envelope retained by stored-spec decoding.
// User-authored manifests cannot supply these fields.
type PackagePublication struct {
	Generation    int64             `json:"generation"`
	SpecDigest    string            `json:"spec_digest"`
	PackageDigest string            `json:"package_digest"`
	Sources       []PublishedSource `json:"sources"`
}
type PublishedSource struct {
	SourceID                string `json:"source_id"`
	Incarnation             string `json:"incarnation"`
	DeclarationHash         string `json:"declaration_hash"`
	AuthorizationGeneration int64  `json:"authorization_generation"`
	ArtifactRef             string `json:"artifact_ref"`
	SHA256                  string `json:"sha256"`
	SourcePath              string `json:"source_path"`
	MediaType               string `json:"media_type"`
	SizeBytes               int64  `json:"size_bytes"`
	ParserProfile           string `json:"parser_profile"`
	SafetyState             string `json:"safety_state"`
}
