// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"encoding/json"
	"math"
	"regexp"
)

var (
	localID  = regexp.MustCompile(`^e[0-9]+$`)
	digestID = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func Validate(r Result, l Limits) error {
	if l.Validate() != nil || r.IRVersion != 1 || !digestID.MatchString(r.SourceSHA256) || r.Parser.Recipe != "standard-text-v1" || r.Parser.Name == "" || r.Parser.Version == "" || len(r.Elements) < 1 || len(r.Elements) > l.MaxElements {
		return ErrInvalid
	}
	ids := map[string]Element{}
	for i, e := range r.Elements {
		if !localID.MatchString(e.LocalID) || e.Ordinal != i || e.PageNo < 0 || e.PageNo > l.MaxPages || len(e.SectionPath) == 0 || math.IsNaN(e.Confidence) || e.Confidence < 0 || e.Confidence > 1 {
			return ErrInvalid
		}
		if _, ok := ids[e.LocalID]; ok {
			return ErrInvalid
		}
		switch e.Kind {
		case "heading", "paragraph", "list", "code", "table", "figure", "formula":
		default:
			return ErrInvalid
		}
		switch e.Provenance {
		case "EXTRACTED", "OCR", "SYNTHETIC":
		default:
			return ErrInvalid
		}
		if e.BBox != nil {
			b := *e.BBox
			for _, v := range b {
				if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
					return ErrInvalid
				}
			}
			if b[0] > b[2] || b[1] > b[3] || e.PageNo == 0 {
				return ErrInvalid
			}
		}
		if e.Kind == "table" {
			if e.Structured == nil || len(e.Structured.Columns) == 0 || len(e.Structured.Cells) == 0 {
				return ErrInvalid
			}
			for _, c := range e.Structured.Cells {
				if c.Row < 0 || c.Column < 0 || c.Column >= len(e.Structured.Columns) || c.RowSpan < 1 || c.ColumnSpan < 1 {
					return ErrInvalid
				}
			}
		}
		ids[e.LocalID] = e
	}
	for _, e := range r.Elements {
		seen := map[string]bool{e.LocalID: true}
		p := e.ParentID
		for p != "" {
			parent, ok := ids[p]
			if !ok || seen[p] {
				return ErrInvalid
			}
			seen[p] = true
			p = parent.ParentID
		}
	}
	assets := map[string]bool{}
	var total int64
	for _, a := range r.Assets {
		if !localID.MatchString(a.LocalID) || assets[a.LocalID] || !digestID.MatchString(a.SHA256) || a.SizeBytes < 1 || a.Width < 1 || a.Height < 1 || int64(a.Width) > l.MaxImagePixels/int64(a.Height) {
			return ErrInvalid
		}
		if _, ok := ids[a.ElementID]; !ok {
			return ErrInvalid
		}
		assets[a.LocalID] = true
		total += a.SizeBytes
	}
	raw, err := json.Marshal(r)
	if err != nil || int64(len(raw))+total > l.MaxDecodedBytes {
		return ErrLimit
	}
	return nil
}
