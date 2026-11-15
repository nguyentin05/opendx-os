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
	os.Mkdir(filepath.Join(root, "knowledge"), 0700)
	path := filepath.Join(root, "knowledge/policy.md")
	os.WriteFile(path, []byte("# Original\n"), 0600)
	a := packageApp()
	a.PackageRoot = root
	store := ArtifactStore{Root: filepath.Join(t.TempDir(), "artifacts"), Scanner: safetyCheck(func(_ context.Context, r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })}
	staged, e := store.Stage(context.Background(), *a.Clone())
	if e != nil {
		t.Fatal(e)
	}
	source := staged.Spec.Publication.Sources[0]
	os.WriteFile(path, []byte("# Changed\n"), 0600)
	b, e := store.Read(context.Background(), source)
	if e != nil || string(b) != "# Original\n" {
		t.Fatal("source did not survive mutable package", e)
	}
	store.Scanner = safetyCheck(func(context.Context, io.Reader) error { return errors.New("blocked") })
	if _, e = store.Stage(context.Background(), a); e == nil {
		t.Fatal("unsafe source staged")
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	os.WriteFile(outside, []byte("outside"), 0600)
	os.Remove(path)
	os.Symlink(outside, path)
	if _, e = store.Stage(context.Background(), a); e == nil {
		t.Fatal("symlink escape staged")
	}
	source.ArtifactRef = "../outside"
	if _, e = store.Read(context.Background(), source); e == nil {
		t.Fatal("untrusted artifact ref read")
	}
}
