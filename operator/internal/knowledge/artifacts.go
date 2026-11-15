// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"unicode/utf8"
)

const MaxSourceSize = 20 << 20

type SafetyScanner interface {
	Scan(context.Context, io.Reader) error
}
type ArtifactStore struct {
	Root    string
	Scanner SafetyScanner
}

func (s ArtifactStore) Stage(ctx context.Context, a manifest.DXApplication) (manifest.DXApplication, error) {
	if s.Root == "" || s.Scanner == nil || a.PackageRoot == "" {
		return a, fmt.Errorf("package safety storage unavailable")
	}
	if err := manifest.ValidateKnowledgePaths(a.PackageRoot, a.Spec.Knowledge); err != nil {
		return a, fmt.Errorf("unsafe package source")
	}
	root, e := os.OpenRoot(a.PackageRoot)
	if e != nil {
		return a, fmt.Errorf("package root unavailable")
	}
	defer root.Close()
	if e = os.MkdirAll(s.Root, 0700); e != nil {
		return a, fmt.Errorf("artifact storage unavailable")
	}
	info, e := os.Stat(s.Root)
	if e != nil || info.Mode().Perm()&0077 != 0 {
		return a, fmt.Errorf("artifact storage must be private")
	}
	dest, e := os.OpenRoot(s.Root)
	if e != nil {
		return a, fmt.Errorf("artifact storage unavailable")
	}
	defer dest.Close()
	publication := Publication{Sources: []PublishedSource{}}
	if a.Spec.Knowledge != nil {
		for _, source := range a.Spec.Knowledge.Sources {
			if err := ctx.Err(); err != nil {
				return a, err
			}
			file, e := root.Open(source.Path)
			if e != nil {
				return a, fmt.Errorf("package source unavailable")
			}
			info, e := file.Stat()
			if e != nil || !info.Mode().IsRegular() || info.Size() > MaxSourceSize {
				file.Close()
				return a, fmt.Errorf("invalid source size/type")
			}
			b, e := io.ReadAll(io.LimitReader(file, MaxSourceSize+1))
			file.Close()
			if e != nil || len(b) == 0 || len(b) > MaxSourceSize {
				return a, fmt.Errorf("invalid package source")
			}
			if source.MediaType == "application/pdf" && http.DetectContentType(b) != "application/pdf" || source.MediaType == "text/markdown" && (!utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0) {
				return a, fmt.Errorf("source media mismatch")
			}
			if e = s.Scanner.Scan(ctx, bytes.NewReader(b)); e != nil {
				return a, fmt.Errorf("source safety admission failed")
			}
			sum := sha256.Sum256(b)
			hash := hex.EncodeToString(sum[:])
			ref := a.Metadata.Name + "/" + source.ID + "/" + hash
			if e = dest.MkdirAll(path.Dir(ref), 0700); e != nil {
				return a, fmt.Errorf("artifact directory unavailable")
			}
			nonce, e := newID()
			if e != nil {
				return a, e
			}
			tmp := ref + "." + nonce
			f, e := dest.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return a, fmt.Errorf("artifact staging failed")
			}
			_, e = f.Write(b)
			if e == nil {
				e = f.Sync()
			}
			ce := f.Close()
			if e == nil {
				e = ce
			}
			if e == nil {
				e = dest.Link(tmp, ref)
				if errors.Is(e, os.ErrExist) {
					e = nil
				}
			}
			_ = dest.Remove(tmp)
			if e != nil {
				return a, fmt.Errorf("artifact staging failed")
			}
			published := PublishedSource{SourceID: source.ID, SourcePath: source.Path, MediaType: source.MediaType, SizeBytes: int64(len(b)), SHA256: hash, ArtifactRef: ref, ParserProfile: source.IndexingProfile, SafetyState: "APPROVED"}
			if _, e = s.Read(ctx, published); e != nil {
				return a, e
			}
			publication.Sources = append(publication.Sources, published)
		}
	}
	a.Spec.Publication = &publication
	return a, nil
}
func (s ArtifactStore) Read(ctx context.Context, source PublishedSource) ([]byte, error) {
	if source.SafetyState != "APPROVED" || len(source.SHA256) != 64 || source.SizeBytes <= 0 || source.SizeBytes > MaxSourceSize || path.Clean(source.ArtifactRef) != source.ArtifactRef || strings.HasPrefix(source.ArtifactRef, "/") || strings.Contains(source.ArtifactRef, "..") || strings.Contains(source.ArtifactRef, "\\") {
		return nil, fmt.Errorf("invalid artifact identity")
	}
	root, e := os.OpenRoot(s.Root)
	if e != nil {
		return nil, fmt.Errorf("artifact storage unavailable")
	}
	defer root.Close()
	f, e := root.Open(source.ArtifactRef)
	if e != nil {
		return nil, fmt.Errorf("artifact unavailable")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() != source.SizeBytes {
		return nil, fmt.Errorf("artifact identity mismatch")
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxSourceSize+1))
	if e != nil {
		return nil, fmt.Errorf("artifact read failed")
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != source.SizeBytes || hex.EncodeToString(sum[:]) != source.SHA256 {
		return nil, fmt.Errorf("artifact integrity mismatch")
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	return b, nil
}
