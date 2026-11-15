// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"
)

type ChunkRecipe struct {
	Version, Tokenizer                     string
	TargetTokens, MaxTokens, OverlapTokens int
}

func DefaultChunkRecipe() ChunkRecipe {
	return ChunkRecipe{"chunk-v1", "unicode-rune-v1", 512, 768, 64}
}

type ChunkRecord struct {
	Ordinal     int      `json:"ordinal"`
	Text        string   `json:"text"`
	SectionPath []string `json:"section_path"`
	PageStart   int      `json:"page_start,omitempty"`
	PageEnd     int      `json:"page_end,omitempty"`
	ElementIDs  []string `json:"element_local_ids"`
	ContentHash string   `json:"content_hash"`
	TokenCount  int      `json:"token_count"`
	Warnings    []string `json:"warnings"`
}

func Chunk(r Result, recipe ChunkRecipe) ([]ChunkRecord, error) {
	if recipe.Version != "chunk-v1" || recipe.Tokenizer != "unicode-rune-v1" || recipe.TargetTokens < 1 || recipe.TargetTokens > recipe.MaxTokens || recipe.MaxTokens > 768 || recipe.OverlapTokens < 0 || recipe.OverlapTokens > 64 || Validate(r, DefaultLimits()) != nil {
		return nil, ErrInvalid
	}
	out := []ChunkRecord{}
	emit := func(text string, e Element, warnings []string) error {
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}
		n := utf8.RuneCountInString(text)
		if n > recipe.MaxTokens {
			return ErrLimit
		}
		h := sha256.Sum256([]byte(text))
		out = append(out, ChunkRecord{Ordinal: len(out), Text: text, SectionPath: append([]string{}, e.SectionPath...), PageStart: e.PageNo, PageEnd: e.PageNo, ElementIDs: []string{e.LocalID}, ContentHash: fmt.Sprintf("%x", h), TokenCount: n, Warnings: warnings})
		return nil
	}
	for _, e := range r.Elements {
		if e.Provenance == "SYNTHETIC" || strings.TrimSpace(e.Text) == "" {
			continue
		}
		if e.Kind == "table" {
			rows := map[int][]Cell{}
			maxRow := -1
			for _, c := range e.Structured.Cells {
				rows[c.Row] = append(rows[c.Row], c)
				if c.Row > maxRow {
					maxRow = c.Row
				}
			}
			for row := 0; row <= maxRow; row++ {
				parts := []string{fmt.Sprintf("row %d", row)}
				for _, c := range rows[row] {
					parts = append(parts, e.Structured.Columns[c.Column].Label+": "+c.Text)
				}
				text := strings.Join(parts, " | ")
				if utf8.RuneCountInString(text) <= recipe.MaxTokens {
					if err := emit(text, e, nil); err != nil {
						return nil, err
					}
					continue
				}
				for _, cell := range rows[row] {
					var label strings.Builder
					fmt.Fprintf(&label, "row %d | %s: ", row, e.Structured.Columns[cell.Column].Label)
					for _, other := range rows[row] {
						if other.Column != cell.Column && utf8.RuneCountInString(other.Text) < 64 {
							label.WriteString(e.Structured.Columns[other.Column].Label)
							label.WriteString(": ")
							label.WriteString(other.Text)
							label.WriteString(" | ")
						}
					}
					segments, err := splitWords(cell.Text, recipe.MaxTokens-utf8.RuneCountInString(label.String()))
					if err != nil {
						return nil, err
					}
					for _, s := range segments {
						if err = emit(label.String()+s, e, []string{"OVERSIZE_TABLE_ROW"}); err != nil {
							return nil, err
						}
					}
				}
			}
			continue
		}
		segments := []string{}
		if e.Kind == "code" {
			current := ""
			for line := range strings.SplitSeq(e.Text, "\n") {
				if utf8.RuneCountInString(line) > recipe.MaxTokens {
					return nil, ErrLimit
				}
				if utf8.RuneCountInString(current+line+"\n") > recipe.MaxTokens {
					segments = append(segments, current)
					current = ""
				}
				current += line + "\n"
			}
			if strings.TrimSpace(current) != "" {
				segments = append(segments, current)
			}
		} else {
			var err error
			segments, err = splitWords(e.Text, recipe.TargetTokens)
			if err != nil {
				return nil, err
			}
		}
		for _, s := range segments {
			if err := emit(s, e, nil); err != nil {
				return nil, err
			}
		}
	}
	if len(out) > DefaultLimits().MaxChunks {
		return nil, ErrLimit
	}
	return out, nil
}

func splitWords(text string, max int) ([]string, error) {
	if max < 1 {
		return nil, ErrLimit
	}
	parts := []string{}
	current := ""
	for word := range strings.FieldsSeq(text) {
		if utf8.RuneCountInString(word) > max {
			return nil, ErrLimit
		}
		next := word
		if current != "" {
			next = current + " " + word
		}
		if utf8.RuneCountInString(next) > max {
			parts = append(parts, current)
			current = word
		} else {
			current = next
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts, nil
}
