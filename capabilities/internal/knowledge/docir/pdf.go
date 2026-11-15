// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type limitedBuffer struct {
	bytes.Buffer
	max int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.Len()+len(p)) > b.max {
		return 0, ErrLimit
	}
	return b.Buffer.Write(p)
}
func localCommand(ctx context.Context, limit int64, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	out := &limitedBuffer{max: limit}
	cmd.Stdout = out
	cmd.Stderr = &limitedBuffer{max: 4096}
	if e := cmd.Run(); e != nil {
		return nil, ErrInvalid
	}
	return out.Bytes(), nil
}

type pdfWord struct {
	X1   float64 `xml:"xMin,attr"`
	Y1   float64 `xml:"yMin,attr"`
	X2   float64 `xml:"xMax,attr"`
	Y2   float64 `xml:"yMax,attr"`
	Text string  `xml:",chardata"`
}
type pdfLine struct {
	Words []pdfWord `xml:"word"`
}

func parsePDF(ctx context.Context, b []byte, l Limits, r *Result) error {
	f, e := os.CreateTemp("", "dxos-source-*.pdf")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	f.Close()
	info, e := localCommand(ctx, 64<<10, "pdfinfo", f.Name())
	if e != nil {
		return e
	}
	pages := 0
	for line := range strings.SplitSeq(string(info), "\n") {
		if after, ok := strings.CutPrefix(line, "Pages:"); ok {
			pages, _ = strconv.Atoi(strings.TrimSpace(after))
		}
	}
	if pages < 1 || pages > l.MaxPages {
		return ErrLimit
	}
	images, e := localCommand(ctx, 1<<20, "pdfimages", "-list", f.Name())
	if e != nil {
		return e
	}
	if len(strings.Split(strings.TrimSpace(string(images)), "\n")) > 2 {
		return ErrOCRUnavailable
	}
	data, e := localCommand(ctx, l.MaxDecodedBytes, "pdftotext", "-bbox-layout", "-enc", "UTF-8", f.Name(), "-")
	if e != nil {
		return e
	}
	versionCmd := exec.CommandContext(ctx, "pdftotext", "-v")
	versionCmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	versionRaw, versionErr := versionCmd.CombinedOutput()
	versionFields := strings.Fields(string(versionRaw))
	if versionErr != nil || len(versionFields) < 3 {
		return ErrInvalid
	}
	r.Parser = ParserInfo{Name: "poppler-bbox", Version: versionFields[2], Recipe: "standard-text-v1"}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	page := 0
	var width, height float64
	var words []pdfWord
	flush := func() error {
		if len(words) == 0 {
			return nil
		}
		sort.Slice(words, func(i, j int) bool {
			if math.Abs(words[i].Y1-words[j].Y1) > 2 {
				return words[i].Y1 < words[j].Y1
			}
			return words[i].X1 < words[j].X1
		})
		rows := [][]pdfWord{}
		for _, w := range words {
			if len(rows) == 0 || math.Abs(rows[len(rows)-1][0].Y1-w.Y1) > 2 {
				rows = append(rows, []pdfWord{w})
			} else {
				rows[len(rows)-1] = append(rows[len(rows)-1], w)
			}
		}
		groups := func(row []pdfWord) [][]pdfWord {
			out := [][]pdfWord{}
			for _, w := range row {
				if len(out) == 0 || w.X1-out[len(out)-1][len(out[len(out)-1])-1].X2 > 24 {
					out = append(out, []pdfWord{w})
				} else {
					out[len(out)-1] = append(out[len(out)-1], w)
				}
			}
			return out
		}
		cellText := func(ws []pdfWord) string {
			out := []string{}
			for _, w := range ws {
				out = append(out, w.Text)
			}
			return strings.Join(out, " ")
		}
		section := []string{"Document"}
		for i := 0; i < len(rows); i++ {
			g := groups(rows[i])
			box := [4]float64{rows[i][0].X1 / width, rows[i][0].Y1 / height, rows[i][len(rows[i])-1].X2 / width, rows[i][0].Y2 / height}
			if len(g) == 1 {
				text := cellText(g[0])
				kind := "paragraph"
				if i == 0 {
					kind = "heading"
					section = []string{text}
				}
				appendElement(r, kind, text, "", section, page, &box, nil)
				continue
			}
			table := &Table{}
			for col, ws := range g {
				table.Columns = append(table.Columns, Column{ID: fmt.Sprintf("c%d", col), Label: cellText(ws)})
			}
			body := []string{}
			count := 0
			for i+1 < len(rows) {
				next := groups(rows[i+1])
				if len(next) != len(g) || rows[i+1][0].Y1-rows[i][0].Y1 > 32 {
					break
				}
				for col := range next {
					if math.Abs(next[col][0].X1-g[col][0].X1) > 6 {
						return ErrLayout
					}
				}
				values := []string{}
				for col, ws := range next {
					value := cellText(ws)
					table.Cells = append(table.Cells, Cell{Row: count, Column: col, RowSpan: 1, ColumnSpan: 1, Text: value})
					values = append(values, table.Columns[col].Label+": "+value)
				}
				body = append(body, strings.Join(values, " | "))
				count++
				i++
				box[3] = rows[i][0].Y2 / height
			}
			if count == 0 {
				return ErrLayout
			}
			appendElement(r, "table", strings.Join(body, "\n"), "", section, page, &box, table)
		}
		words = nil
		return nil
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return ErrInvalid
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Local == "page" {
				page++
				for _, a := range t.Attr {
					if a.Name.Local == "width" {
						width, _ = strconv.ParseFloat(a.Value, 64)
					}
					if a.Name.Local == "height" {
						height, _ = strconv.ParseFloat(a.Value, 64)
					}
				}
				if width <= 0 || height <= 0 {
					return ErrInvalid
				}
			}
			if t.Name.Local == "line" {
				var line pdfLine
				if err = decoder.DecodeElement(&line, &t); err != nil {
					return ErrInvalid
				}
				words = append(words, line.Words...)
			}
		case xml.EndElement:
			if t.Name.Local == "page" {
				if err = flush(); err != nil {
					return err
				}
			}
		}
	}
	if len(r.Elements) == 0 {
		return ErrOCRUnavailable
	}
	r.Warnings = append(r.Warnings, "TEXT_PDF_ONLY")
	return nil
}
