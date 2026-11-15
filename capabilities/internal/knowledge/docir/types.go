// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"errors"
	"io"
	"time"
)

var (
	ErrInvalid        = errors.New("INVALID_DOCIR")
	ErrLimit          = errors.New("PARSER_LIMIT")
	ErrOCRUnavailable = errors.New("OCR_UNAVAILABLE")
	ErrLayout         = errors.New("LAYOUT_UNSUPPORTED")
)

type Limits struct {
	MaxSourceBytes  int64         `json:"max_source_bytes"`
	MaxPages        int           `json:"max_pages"`
	Deadline        time.Duration `json:"deadline"`
	MaxMemoryBytes  int64         `json:"max_memory_bytes"`
	MaxDecodedBytes int64         `json:"max_decoded_bytes"`
	MaxImagePixels  int64         `json:"max_image_pixels"`
	MaxElements     int           `json:"max_elements"`
	MaxChunks       int           `json:"max_chunks"`
}

func DefaultLimits() Limits {
	return Limits{20 << 20, 200, 120 * time.Second, 512 << 20, 200 << 20, 25000000, 50000, 20000}
}

func (l Limits) Validate() error {
	d := DefaultLimits()
	if l.MaxSourceBytes < 1 || l.MaxSourceBytes > d.MaxSourceBytes || l.MaxPages < 1 || l.MaxPages > d.MaxPages || l.Deadline < 1 || l.Deadline > d.Deadline || l.MaxMemoryBytes < 1 || l.MaxMemoryBytes > d.MaxMemoryBytes || l.MaxDecodedBytes < 1 || l.MaxDecodedBytes > d.MaxDecodedBytes || l.MaxImagePixels < 1 || l.MaxImagePixels > d.MaxImagePixels || l.MaxElements < 1 || l.MaxElements > d.MaxElements || l.MaxChunks < 1 || l.MaxChunks > d.MaxChunks {
		return ErrLimit
	}
	return nil
}

type ParseRequest struct {
	RequestID string `json:"request_id"`
	MediaType string `json:"media_type"`
	SHA256    string `json:"sha256"`
	RecipeID  string `json:"recipe_id"`
	IRVersion int    `json:"ir_version"`
	Limits    Limits `json:"limits"`
}
type ParserInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Recipe  string `json:"recipe"`
}
type Column struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Unit  string `json:"unit,omitempty"`
}
type Cell struct {
	Row        int    `json:"row"`
	Column     int    `json:"column"`
	RowSpan    int    `json:"row_span"`
	ColumnSpan int    `json:"column_span"`
	Text       string `json:"text"`
}
type Table struct {
	Columns []Column `json:"columns"`
	Cells   []Cell   `json:"cells"`
}
type Element struct {
	LocalID     string      `json:"local_id"`
	ParentID    string      `json:"parent_local_id,omitempty"`
	Kind        string      `json:"kind"`
	Ordinal     int         `json:"ordinal"`
	SectionPath []string    `json:"section_path"`
	PageNo      int         `json:"page_no,omitempty"`
	BBox        *[4]float64 `json:"bbox,omitempty"`
	Text        string      `json:"text"`
	Structured  *Table      `json:"structured,omitempty"`
	Confidence  float64     `json:"confidence"`
	Provenance  string      `json:"provenance"`
}
type Asset struct {
	LocalID   string    `json:"local_id"`
	ElementID string    `json:"element_local_id"`
	Kind      string    `json:"kind"`
	MediaType string    `json:"media_type"`
	SHA256    string    `json:"sha256"`
	SizeBytes int64     `json:"size_bytes"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Data      io.Reader `json:"-"`
}
type Result struct {
	IRVersion    int        `json:"ir_version"`
	SourceSHA256 string     `json:"source_sha256"`
	Parser       ParserInfo `json:"parser"`
	Elements     []Element  `json:"elements"`
	Assets       []Asset    `json:"assets"`
	Warnings     []string   `json:"warnings"`
}
