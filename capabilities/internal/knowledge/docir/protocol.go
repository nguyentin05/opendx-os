// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package docir

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"
	"time"
)

type ParseAdapter interface {
	Parse(context.Context, ParseRequest, io.Reader) (Result, error)
}

func Handler(token string) http.Handler { return HandlerWithAdapter(token, Parser{}) }
func HandlerWithAdapter(token string, adapter ParseAdapter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if token == "" || len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			w.WriteHeader(401)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/internal/v1/parse" {
			w.WriteHeader(404)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, (20<<20)+(64<<10))
		reader, e := r.MultipartReader()
		if e != nil {
			w.WriteHeader(400)
			return
		}
		part, e := reader.NextPart()
		if e != nil || part.FormName() != "metadata" {
			w.WriteHeader(400)
			return
		}
		raw, e := io.ReadAll(io.LimitReader(part, (64<<10)+1))
		part.Close()
		var req ParseRequest
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if e != nil || len(raw) > 64<<10 || d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
			w.WriteHeader(400)
			return
		}
		part, e = reader.NextPart()
		if e != nil || part.FormName() != "source" {
			w.WriteHeader(400)
			return
		}
		result, e := adapter.Parse(r.Context(), req, part)
		part.Close()
		if e != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(422)
			code := "PARSER_REJECTED"
			for _, known := range []error{ErrOCRUnavailable, ErrLayout, ErrLimit, ErrInvalid} {
				if errors.Is(e, known) {
					code = known.Error()
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
			return
		}
		if _, e = reader.NextPart(); e != io.EOF {
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
}

type Client struct {
	http  *http.Client
	token string
}

func NewUnixClient(socket, token string) *Client {
	return &Client{http: &http.Client{Timeout: 125 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, token: token}
}

func (c *Client) Parse(ctx context.Context, req ParseRequest, source io.Reader) (Result, error) {
	var result Result
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormField("metadata")
	_ = json.NewEncoder(part).Encode(req)
	part, _ = writer.CreateFormFile("source", "source")
	n, e := io.Copy(part, io.LimitReader(source, req.Limits.MaxSourceBytes+1))
	if e != nil || n > req.Limits.MaxSourceBytes {
		return result, ErrLimit
	}
	writer.Close()
	r, e := http.NewRequestWithContext(ctx, "POST", "http://parser/internal/v1/parse", &body)
	if e != nil {
		return result, e
	}
	r.Header.Set("Authorization", "Bearer "+c.token)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	response, e := c.http.Do(r)
	if e != nil {
		return result, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, req.Limits.MaxDecodedBytes+1))
	if e != nil || int64(len(raw)) > req.Limits.MaxDecodedBytes {
		return result, ErrLimit
	}
	if response.StatusCode != 200 {
		for _, known := range []error{ErrOCRUnavailable, ErrLayout, ErrLimit} {
			if strings.Contains(string(raw), known.Error()) {
				return result, known
			}
		}
		return result, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || d.Decode(new(any)) != io.EOF || result.SourceSHA256 != req.SHA256 || len(result.Assets) > 0 {
		return Result{}, ErrInvalid
	}
	return result, Validate(result, req.Limits)
}
