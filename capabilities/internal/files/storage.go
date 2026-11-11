// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package process implements work item state commands and their request and response types.
package files

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// S3Storage uses path-style AWS Signature V4 requests to the private SeaweedFS gateway.
// Bucket creation and restricted credentials are deployment responsibilities.
type S3Storage struct {
	endpoint                       *url.URL
	bucket, region, access, secret string
	client                         *http.Client
}

func NewS3Storage(endpoint, bucket, region, access, secret string, timeout time.Duration) (*S3Storage, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || bucket == "" || strings.ContainsAny(bucket, "/\\ ") || region == "" || access == "" || secret == "" || timeout <= 0 {
		return nil, fmt.Errorf("invalid S3 configuration")
	}
	return &S3Storage{u, bucket, region, access, secret, &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func mac(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}
func (s *S3Storage) request(ctx context.Context, method, key string, b []byte) (*http.Response, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.ContainsAny(key, "\\\r\n") {
		return nil, ErrContent
	}
	u := *s.endpoint
	u.Path = "/" + s.bucket + "/" + key
	u.RawPath = ""
	req, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	now := time.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	hash := digest(b)
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", hash)
	headers := "host:" + req.URL.Host + "\nx-amz-content-sha256:" + hash + "\nx-amz-date:" + stamp + "\n"
	signed := "host;x-amz-content-sha256;x-amz-date"
	canonical := method + "\n" + req.URL.EscapedPath() + "\n\n" + headers + "\n" + signed + "\n" + hash
	scope := date + "/" + s.region + "/s3/aws4_request"
	keyDate := mac([]byte("AWS4"+s.secret), date)
	keyRegion := mac(keyDate, s.region)
	keyService := mac(keyRegion, "s3")
	signing := mac(keyService, "aws4_request")
	signature := hex.EncodeToString(mac(signing, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+digest([]byte(canonical))))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.access+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
	resp, e := s.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("storage request failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("storage request rejected")
	}
	return resp, nil
}
func (s *S3Storage) Put(ctx context.Context, key string, r io.Reader) error {
	b, e := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if e != nil {
		return e
	}
	if len(b) > MaxSize {
		return ErrContent
	}
	resp, e := s.request(ctx, "PUT", key, b)
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }()
	_, e = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
	return e
}
func (s *S3Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	resp, e := s.request(ctx, "GET", key, nil)
	if e != nil {
		return nil, e
	}
	return resp.Body, nil
}
