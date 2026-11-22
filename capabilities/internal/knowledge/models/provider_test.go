// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func usageProvider(t *testing.T, body string) *HTTPProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	provider, err := NewHTTPProvider(server.URL, "", true)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// TestProviderUsageDecoding pins the presence-sensitive usage contract at the
// provider boundary.
func TestProviderUsageDecoding(t *testing.T) {
	profile := Profile{ID: "gen", Revision: 1, Purpose: "GENERATE", Execution: "HOSTED", Enabled: true,
		Limits: Limits{MaxInputTokens: 4096, MaxOutputTokens: 512, MaxBytes: 16384, TimeoutMS: 1000}}
	for _, tc := range []struct {
		name  string
		body  string
		usage *Usage
		bad   bool
	}{
		{name: "no usage", body: `{"text":"answer"}`},
		{name: "null usage", body: `{"text":"answer","usage":null}`},
		{name: "explicit zero", body: `{"text":"answer","usage":{"input_tokens":0,"output_tokens":0}}`, usage: &Usage{}},
		{name: "positive counts", body: `{"text":"answer","usage":{"input_tokens":12,"output_tokens":7}}`, usage: &Usage{InputTokens: 12, OutputTokens: 7}},
		{name: "input only", body: `{"text":"answer","usage":{"input_tokens":12}}`, bad: true},
		{name: "output only", body: `{"text":"answer","usage":{"output_tokens":7}}`, bad: true},
		{name: "null counters", body: `{"text":"answer","usage":{"input_tokens":null,"output_tokens":null}}`, bad: true},
		{name: "one null counter", body: `{"text":"answer","usage":{"input_tokens":1,"output_tokens":null}}`, bad: true},
		{name: "fractional counts", body: `{"text":"answer","usage":{"input_tokens":1.5,"output_tokens":1}}`, bad: true},
		{name: "negative counts", body: `{"text":"answer","usage":{"input_tokens":-1,"output_tokens":0}}`, bad: true},
		{name: "out of range counts", body: `{"text":"answer","usage":{"input_tokens":9223372036854775808,"output_tokens":0}}`, bad: true},
		{name: "monetary usage field", body: `{"text":"answer","usage":{"input_tokens":1,"output_tokens":1,"units":5}}`, bad: true},
		{name: "usage array", body: `{"text":"answer","usage":[]}`, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := usageProvider(t, tc.body).Invoke(context.Background(), profile, Payload{Query: "q"})
			if tc.bad {
				if err == nil {
					t.Fatalf("malformed usage accepted: %+v", result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.usage == nil {
				if result.Usage != nil {
					t.Fatalf("expected no usage, got %+v", result.Usage)
				}
				return
			}
			if result.Usage == nil || *result.Usage != *tc.usage {
				t.Fatalf("usage %+v, want %+v", result.Usage, tc.usage)
			}
		})
	}
}
