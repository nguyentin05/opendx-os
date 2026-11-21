// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientCredentialsCachesAndRenewsAtThreeQuarters(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" ||
			r.Form.Get("client_id") != "service" || r.Form.Get("client_secret") != "secret-value" {
			t.Error("unexpected token request")
		}
		next := calls.Add(1)
		if next == 1 {
			_, _ = w.Write([]byte(`{"access_token":"access-1","token_type":"Bearer","expires_in":4}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"access-2","token_type":"Bearer","expires_in":4}`))
	}))
	defer server.Close()

	source, err := NewClientCredentials(server.URL, "service", "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	source.now = func() time.Time { return now }

	token, err := source.Token(context.Background())
	if err != nil || token != "access-1" {
		t.Fatalf("first token: %q %v", token, err)
	}
	now = now.Add(2900 * time.Millisecond)
	token, err = source.Token(context.Background())
	if err != nil || token != "access-1" || calls.Load() != 1 {
		t.Fatalf("cached token: %q %v calls=%d", token, err, calls.Load())
	}
	now = now.Add(100 * time.Millisecond)
	token, err = source.Token(context.Background())
	if err != nil || token != "access-2" || calls.Load() != 2 {
		t.Fatalf("renewed token: %q %v calls=%d", token, err, calls.Load())
	}
}

func TestClientCredentialsSharesOneFetch(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"access_token":"shared","token_type":"Bearer","expires_in":60}`))
	}))
	defer server.Close()

	source, err := NewClientCredentials(server.URL, "service", "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	tokens := make([]string, 2)
	errs := make([]error, 2)
	for i := range tokens {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = source.Token(context.Background())
		}(i)
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("token requests = %d, want 1", calls.Load())
	}
	for i := range tokens {
		if errs[i] != nil || tokens[i] != "shared" {
			t.Fatalf("caller %d: %q %v", i, tokens[i], errs[i])
		}
	}
}

func TestClientCredentialsErrorsNeverContainTheSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "refused", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	source, err := NewClientCredentials(server.URL, "service", "do-not-leak-this")
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Token(context.Background())
	if err == nil || strings.Contains(err.Error(), "do-not-leak-this") {
		t.Fatalf("error leaks or is missing: %v", err)
	}

	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"","token_type":"Bearer","expires_in":0}`))
	}))
	defer invalid.Close()
	source, err = NewClientCredentials(invalid.URL, "service", "do-not-leak-this")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Token(context.Background()); err == nil || strings.Contains(err.Error(), "do-not-leak-this") {
		t.Fatalf("invalid response error leaks or is missing: %v", err)
	}
}

func TestTokenGetterFromKeepsAStaticToken(t *testing.T) {
	getter, err := TokenGetterFrom("static-token", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if token, err := getter(context.Background()); err != nil || token != "static-token" {
		t.Fatalf("static token: %q %v", token, err)
	}
	if _, err := TokenGetterFrom("", "://bad", "service", "secret"); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}

func TestClientCredentialsRejectsUnsafeEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"", "ftp://host/token", "http://user:pass@host/token", "http://host/token?x=1", "http://host/token#fragment",
	} {
		if _, err := NewClientCredentials(endpoint, "service", "secret"); err == nil {
			t.Fatalf("endpoint %q accepted", endpoint)
		}
	}
	if _, err := NewClientCredentials("http://host/token", "", "secret"); err == nil {
		t.Fatal("empty client id accepted")
	}
	u, _ := url.Parse("http://host/token")
	if u.Host == "" {
		t.Fatal("test fixture invalid")
	}
}
