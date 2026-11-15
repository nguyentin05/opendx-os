// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package docir

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"io"
	"strings"
	"unicode/utf8"
)

type Parser struct{}

func (Parser) Parse(ctx context.Context, req ParseRequest, source io.Reader) (Result, error) {
	var r Result
	if req.Limits.Validate() != nil || req.IRVersion != 1 || req.RecipeID != "standard-text-v1" || req.RequestID == "" {
		return r, ErrLimit
	}
	ctx, cancel := context.WithTimeout(ctx, req.Limits.Deadline)
	defer cancel()
	b, e := io.ReadAll(io.LimitReader(source, req.Limits.MaxSourceBytes+1))
	if e != nil {
		return r, e
	}
	if int64(len(b)) > req.Limits.MaxSourceBytes {
		return r, ErrLimit
	}
	h := sha256.Sum256(b)
	if fmt.Sprintf("%x", h) != req.SHA256 {
		return r, ErrInvalid
	}
	r = Result{IRVersion: 1, SourceSHA256: req.SHA256, Parser: ParserInfo{Name: "goldmark", Version: "1.7.8", Recipe: req.RecipeID}, Elements: []Element{}, Assets: []Asset{}, Warnings: []string{}}
	switch req.MediaType {
	case "text/markdown":
		if !utf8.Valid(b) || bytes.ContainsRune(b, 0) {
			return Result{}, ErrInvalid
		}
		e = parseMarkdown(ctx, b, &r)
	case "application/pdf":
		e = parsePDF(ctx, b, req.Limits, &r)
	default:
		e = ErrOCRUnavailable
	}
	if e != nil {
		return Result{}, e
	}
	if e = ctx.Err(); e != nil {
		return Result{}, e
	}
	if e = Validate(r, req.Limits); e != nil {
		return Result{}, e
	}
	return r, nil
}
func appendElement(r *Result, kind, body, parent string, path []string, page int, box *[4]float64, table *Table) {
	i := len(r.Elements)
	r.Elements = append(r.Elements, Element{LocalID: fmt.Sprintf("e%d", i+1), ParentID: parent, Kind: kind, Ordinal: i, SectionPath: append([]string{}, path...), PageNo: page, BBox: box, Text: body, Structured: table, Confidence: 1, Provenance: "EXTRACTED"})
}
func inline(n ast.Node, b []byte) string {
	var s strings.Builder
	_ = ast.Walk(n, func(c ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		switch x := c.(type) {
		case *ast.Text:
			s.Write(x.Value(b))
			if x.SoftLineBreak() || x.HardLineBreak() {
				s.WriteByte('\n')
			}
		case *ast.String:
			s.Write(x.Value)
		case *ast.RawHTML:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(s.String())
}
func parseMarkdown(ctx context.Context, b []byte, r *Result) error {
	root := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(b))
	path := []string{"Document"}
	parents := []string{}
	return ast.Walk(root, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		if err := ctx.Err(); err != nil {
			return ast.WalkStop, err
		}
		parent := ""
		if len(parents) > 0 {
			parent = parents[len(parents)-1]
		}
		switch x := n.(type) {
		case *ast.Heading:
			label := inline(n, b)
			level := x.Level
			if level > len(parents)+1 {
				level = len(parents) + 1
			}
			if level <= len(parents) {
				parents = parents[:level-1]
				path = path[:level-1]
			}
			if len(parents) == 0 {
				path = []string{}
			}
			path = append(path, label)
			parent = ""
			if len(parents) > 0 {
				parent = parents[len(parents)-1]
			}
			appendElement(r, "heading", label, parent, path, 0, nil, nil)
			parents = append(parents, r.Elements[len(r.Elements)-1].LocalID)
			return ast.WalkSkipChildren, nil
		case *east.Table:
			table := &Table{}
			rows := []string{}
			rowIndex := 0
			for row := n.FirstChild(); row != nil; row = row.NextSibling() {
				values := []string{}
				col := 0
				for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
					value := inline(cell, b)
					if rowIndex == 0 {
						table.Columns = append(table.Columns, Column{ID: fmt.Sprintf("c%d", col), Label: value})
					} else {
						if col >= len(table.Columns) {
							return ast.WalkStop, ErrInvalid
						}
						table.Cells = append(table.Cells, Cell{Row: rowIndex - 1, Column: col, RowSpan: 1, ColumnSpan: 1, Text: value})
						values = append(values, table.Columns[col].Label+": "+value)
					}
					col++
				}
				if rowIndex > 0 {
					rows = append(rows, strings.Join(values, " | "))
				}
				rowIndex++
			}
			appendElement(r, "table", strings.Join(rows, "\n"), parent, path, 0, nil, table)
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			var code strings.Builder
			for i := 0; i < n.Lines().Len(); i++ {
				line := n.Lines().At(i)
				code.Write(line.Value(b))
			}
			appendElement(r, "code", code.String(), parent, path, 0, nil, nil)
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph, *ast.TextBlock:
			kind := "paragraph"
			if _, ok := n.Parent().(*ast.ListItem); ok {
				kind = "list"
			}
			body := inline(n, b)
			if body != "" {
				appendElement(r, kind, body, parent, path, 0, nil, nil)
			}
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
}
