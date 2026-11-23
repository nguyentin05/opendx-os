// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type safetyCheck func(context.Context, io.Reader) error

func (f safetyCheck) Scan(c context.Context, r io.Reader) error { return f(c, r) }
func TestArtifactsImmutableAndBounded(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "knowledge"), 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "knowledge/policy.md")
	if e := os.WriteFile(path, []byte("# Original\n"), 0600); e != nil {
		t.Fatal(e)
	}
	a := packageApp()
	a.PackageRoot = root
	store := ArtifactStore{Root: filepath.Join(t.TempDir(), "artifacts"), Scanner: safetyCheck(func(_ context.Context, r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })}
	staged, e := store.Stage(context.Background(), *a.Clone())
	if e != nil {
		t.Fatal(e)
	}
	source := staged.Spec.Publication.Sources[0]
	if e := os.WriteFile(path, []byte("# Changed\n"), 0600); e != nil {
		t.Fatal(e)
	}
	b, e := store.Read(context.Background(), source)
	if e != nil || string(b) != "# Original\n" {
		t.Fatal("source did not survive mutable package", e)
	}
	store.Scanner = safetyCheck(func(context.Context, io.Reader) error { return errors.New("blocked") })
	if _, e = store.Stage(context.Background(), a); e == nil {
		t.Fatal("unsafe source staged")
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if e := os.WriteFile(outside, []byte("outside"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, path); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Stage(context.Background(), a); e == nil {
		t.Fatal("symlink escape staged")
	}
	source.ArtifactRef = "../outside"
	if _, e = store.Read(context.Background(), source); e == nil {
		t.Fatal("untrusted artifact ref read")
	}
}
