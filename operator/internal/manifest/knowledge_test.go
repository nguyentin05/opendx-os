// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const libraryYAML = `apiVersion: dxos.io/v1alpha1
kind: DXApplication
metadata: {name: personal_library, displayName: Library, version: 1.0.0}
spec:
  capabilities: [cap-process, cap-ai]
  roles: [{name: role_library_user}]
  tables: []
  knowledge:
    version: 1
    personalLibrary: {enabled: true, allowedRoles: [role_library_user]}
`

func TestKnowledgeRoundTrip(t *testing.T) {
	a, e := Parse(strings.NewReader(libraryYAML))
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(a.Spec)
	if e != nil {
		t.Fatal(e)
	}
	s, e := DecodeStoredSpec(b)
	if e != nil || s.Knowledge.Version != 1 {
		t.Fatal("knowledge lost", e)
	}
}
func TestCapabilityOnlyLibrary(t *testing.T) {
	if _, e := Parse(strings.NewReader(libraryYAML)); e != nil {
		t.Fatal(e)
	}
	if _, e := Parse(strings.NewReader(strings.Split(libraryYAML, "  knowledge:")[0])); e == nil {
		t.Fatal("empty app accepted")
	}
}
func TestKnowledgeRejectsAliasesAndEscapes(t *testing.T) {
	for _, raw := range []string{strings.Replace(libraryYAML, "version: 1\n", "version: 2\n", 1), strings.Replace(libraryYAML, "allowedRoles: [role_library_user]", "allowedRoles: [role_other]", 1), libraryYAML + "    visibility: PRIVATE\n", libraryYAML + "    sources: [{id: bad, path: ../secret.md, mediaType: text/markdown, indexingProfile: standard-text-v1, access: {mode: ROLE, allowedRoles: [role_library_user]}}]\n"} {
		if _, e := Parse(strings.NewReader(raw)); e == nil {
			t.Fatal("invalid knowledge accepted", raw)
		}
	}
}
func TestOneEnabledLibrary(t *testing.T) {
	a, e := Parse(strings.NewReader(libraryYAML))
	if e != nil {
		t.Fatal(e)
	}
	b := a.Clone()
	b.Metadata.Name = "other_library"
	if e = ValidateKnowledgeSet([]DXApplication{*a, *b}); e == nil {
		t.Fatal("second library accepted")
	}
}
func TestKnowledgeSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "knowledge"), 0700); e != nil {
		t.Fatal(e)
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	if e := os.WriteFile(outside, []byte("secret"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "knowledge", "secret.md")); e != nil {
		t.Fatal(e)
	}
	raw := libraryYAML + "    sources: [{id: secret, path: knowledge/secret.md, mediaType: text/markdown, indexingProfile: standard-text-v1, access: {mode: ROLE, allowedRoles: [role_library_user]}}]\n"
	p := filepath.Join(root, "manifest.yaml")
	if e := os.WriteFile(p, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ParseFile(p); e == nil {
		t.Fatal("escaping symlink accepted")
	}
}

func TestPublicationEnvelopeIsServerOnly(t *testing.T) {
	if _, err := Parse(strings.NewReader(libraryYAML + "  _publication: {generation: 99}\n")); err == nil {
		t.Fatal("user supplied publication accepted")
	}
	spec, err := DecodeStoredSpec([]byte(`{"capabilities":["cap-ai"],"tables":[],"_publication":{"generation":7,"spec_digest":"test","sources":[]}}`))
	if err != nil || spec.Publication == nil || spec.Publication.Generation != 7 {
		t.Fatal("stored publication lost", err)
	}
}

func TestKnowledgeFileContentFingerprint(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "knowledge"), 0700); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(root, "knowledge/policy.md")
	if e := os.WriteFile(source, []byte("one"), 0600); e != nil {
		t.Fatal(e)
	}
	raw := libraryYAML + "    sources: [{id: policy, path: knowledge/policy.md, mediaType: text/markdown, indexingProfile: standard-text-v1, access: {mode: ROLE, allowedRoles: [role_library_user]}}]\n"
	file := filepath.Join(root, "manifest.yaml")
	if e := os.WriteFile(file, []byte(raw), 0600); e != nil {
		t.Fatal(e)
	}
	first, err := ParseFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if e := os.WriteFile(source, []byte("two"), 0600); e != nil {
		t.Fatal(e)
	}
	second, err := ParseFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if first.SourceDigests["policy"] == "" || first.SourceDigests["policy"] == second.SourceDigests["policy"] {
		t.Fatal("content-only package edit lost")
	}
}
