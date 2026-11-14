// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func requestFor(data []byte, media string) ParseRequest {
	h := sha256.Sum256(data)
	return ParseRequest{RequestID: "fixture", MediaType: media, SHA256: fmt.Sprintf("%x", h), RecipeID: "standard-text-v1", IRVersion: 1, Limits: DefaultLimits()}
}
func TestDocIRProvenance(t *testing.T) {
	for _, f := range []struct {
		path, media string
		page        int
	}{{"policy.md", "text/markdown", 0}, {"table.pdf", "application/pdf", 4}} {
		data, e := os.ReadFile("testdata/" + f.path)
		if e != nil {
			t.Fatal(e)
		}
		r, e := (Parser{}).Parse(context.Background(), requestFor(data, f.media), bytes.NewReader(data))
		if e != nil {
			t.Fatal(f.path, e)
		}
		found := false
		for _, el := range r.Elements {
			if el.Kind == "table" && strings.Contains(el.Text, "Director") && strings.Contains(el.Text, "10,000,000") {
				found = true
				if el.PageNo != f.page || len(el.SectionPath) == 0 {
					t.Fatal("table provenance lost", el)
				}
				if f.page > 0 && el.BBox == nil {
					t.Fatal("PDF bbox missing")
				}
			}
		}
		if !found {
			t.Fatal("table relation lost", f.path)
		}
		r.Elements[0].ParentID = r.Elements[0].LocalID
		if Validate(r, DefaultLimits()) == nil {
			t.Fatal("cyclic parent accepted")
		}
	}
}
func TestParserLimits(t *testing.T) {
	for _, name := range []string{"scan.pdf", "too-many-pages.pdf"} {
		b, _ := os.ReadFile("testdata/" + name)
		_, e := (Parser{}).Parse(context.Background(), requestFor(b, "application/pdf"), bytes.NewReader(b))
		if e == nil {
			t.Fatal("unsupported PDF accepted", name)
		}
		if name == "scan.pdf" && !errors.Is(e, ErrOCRUnavailable) {
			t.Fatal("missing OCR outcome", e)
		}
	}
	b := []byte("# safe")
	req := requestFor(b, "text/markdown")
	req.Limits.Deadline = 121 * time.Second
	if _, e := (Parser{}).Parse(context.Background(), req, bytes.NewReader(b)); e == nil {
		t.Fatal("oversized deadline")
	}
	req = requestFor(b, "text/markdown")
	if _, e := (Parser{}).Parse(context.Background(), req, bytes.NewReader(bytes.Repeat([]byte("x"), 20<<20+1))); e == nil {
		t.Fatal("source bomb accepted")
	}
}
func TestParserNoNetworkOrStorageKeys(t *testing.T) {
	b := []byte("# Links\n\n![image](https://invalid.test/secret)\n\n[link](file:///etc/passwd)\n")
	r, e := (Parser{}).Parse(context.Background(), requestFor(b, "text/markdown"), bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(fmt.Sprint(r), "root:x:") {
		t.Fatal("parser followed file link")
	}
	bad := requestFor(b, "text/markdown")
	bad.SHA256 = strings.Repeat("0", 64)
	if _, e = (Parser{}).Parse(context.Background(), bad, bytes.NewReader(b)); e == nil {
		t.Fatal("digest mismatch accepted")
	}
}
