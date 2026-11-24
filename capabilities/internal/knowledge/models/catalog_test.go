// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const catalogFixture = `{"schema_version":2,"providers":[{"id":"provider","adapter":"bounded-http-v1","endpoint":"https://adapter.invalid/invoke","credential_ref":"MODEL_KEY"}],"models":[{"adapter":"bounded-http-v1","profile":{"id":"embed","revision":1,"purpose":"EMBED","execution":"HOSTED","provider_id":"provider","model_id":"model","credential_ref":"MODEL_KEY","dimension":3,"limits":{"max_input_tokens":100,"max_output_tokens":100,"max_bytes":1024,"timeout_ms":1000},"enabled":false}}]}`

func TestParseCatalogStrict(t *testing.T) {
	c, e := ParseCatalog([]byte(catalogFixture))
	if e != nil || len(c.Models) != 1 || c.Models[0].Profile.Enabled {
		t.Fatalf("valid: %+v %v", c, e)
	}
	cases := map[string]string{"trailing": catalogFixture + ` {}`, "oversize": catalogFixture + strings.Repeat(" ", 65537), "null": "null"}
	changes := [][2]string{{`"schema_version":2`, `"schema_version":1`}, {`"schema_version":2`, `"schema_version":2,"extra":true`}, {`"revision":1`, `"revision":1,"revision":2`}, {`"revision":1`, `"revision":1,"Revision":2`}, {`"adapter":"bounded-http-v1"`, `"adapter":"openai"`}, {`"provider_id":"provider"`, `"provider_id":"missing"`}, {`"credential_ref":"MODEL_KEY"`, `"credential_ref":"OTHER_KEY"`}, {`"enabled":false`, `"enabled":true`}, {`"HOSTED"`, `"LOCAL"`}, {`"EMBED"`, `"VLM"`}, {`"dimension":3`, `"dimension":0`}, {`"dimension":3`, `"dimension":16001`}, {`"timeout_ms":1000`, `"timeout_ms":1000,"input_unit_price":1`}, {`"timeout_ms":1000`, `"timeout_ms":1000,"output_unit_price":null`}, {`"enabled":false`, `"enabled":false,"pricing_revision":"p1"`}, {`"max_bytes":1024`, `"max_bytes":1048577`}, {`"timeout_ms":1000`, `"timeout_ms":120001`}, {`"max_input_tokens":100`, `"max_input_tokens":0`}, {`"max_input_tokens":100`, `"max_input_tokens":null`}, {`"revision":1`, `"revision":0`}, {`https://adapter.invalid/invoke`, `/invoke`}, {`https://adapter.invalid/invoke`, `https://adapter.invalid/?redirect=x`}, {`https://adapter.invalid/invoke`, `https://user:password@adapter.invalid`}, {`"max_bytes":1024`, `"max_bytes":1024,"extra":1`}}
	for _, p := range changes {
		cases[p[1]] = strings.Replace(catalogFixture, p[0], p[1], 1)
	}
	for _, name := range []string{"providers", "models"} {
		var obj map[string]any
		_ = json.Unmarshal([]byte(catalogFixture), &obj)
		list := obj[name].([]any)
		obj[name] = append(list, list[0])
		raw, _ := json.Marshal(obj)
		cases["duplicate "+name] = string(raw)
		cases["normalized duplicate "+name] = strings.Replace(string(raw), `"id":"`, `"id":" `, 1)
		n := 11
		if name == "models" {
			n = 101
		}
		many := make([]any, n)
		for i := range many {
			var item map[string]any
			b, _ := json.Marshal(list[0])
			_ = json.Unmarshal(b, &item)
			if name == "providers" {
				if i > 0 {
					item["id"] = fmt.Sprintf("provider%d", i)
				}
			} else {
				item["profile"].(map[string]any)["id"] = fmt.Sprintf("embed%d", i)
			}
			many[i] = item
		}
		obj[name] = many
		raw, _ = json.Marshal(obj)
		cases["too many "+name] = string(raw)
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := ParseCatalog([]byte(raw))
			if err == nil || len(got.Models) != 0 || len(got.Providers) != 0 {
				t.Fatalf("accepted: %+v %v", got, err)
			}
		})
	}
	normalized := strings.ReplaceAll(catalogFixture, `"provider"`, `" provider "`)
	c, e = ParseCatalog([]byte(normalized))
	if e != nil || c.Providers[0].ID != "provider" || c.Models[0].Profile.ProviderID != "provider" {
		t.Fatalf("normalize: %+v %v", c, e)
	}
	generate := strings.Replace(strings.Replace(catalogFixture, "EMBED", "GENERATE", 1), `"dimension":3,`, "", 1)
	if _, e := ParseCatalog([]byte(generate)); e != nil {
		t.Fatal(e)
	}
}

func TestCatalogGenerationDimensionAndOffline(t *testing.T) {
	generate := strings.Replace(strings.Replace(catalogFixture, "EMBED", "GENERATE", 1), `"dimension":3,`, "", 1)
	if _, e := ParseCatalog([]byte(strings.Replace(generate, `"purpose":"GENERATE"`, `"purpose":"GENERATE","dimension":0`, 1))); e == nil {
		t.Fatal("explicit generation dimension accepted")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	raw := strings.Replace(catalogFixture, "https://adapter.invalid/invoke", server.URL, 1)
	raw = strings.Replace(raw, `"endpoint":`, `"loopback_test":true,"endpoint":`, 1)
	if _, e := ParseCatalog([]byte(raw)); e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("parser sent a request")
	}
	// Registry declarations must not affect the existing disabled deployment path.
	g, _, _, close, e := FromEnvironment(context.Background(), func(key string) string {
		if key == "RAG_MODELS_ENABLED" {
			return "false"
		}
		t.Fatalf("disabled path read %s", key)
		return ""
	})
	defer close()
	if e != nil || g != nil {
		t.Fatalf("legacy OFF: %v", e)
	}
}

// The strict catalog contract is version 2 and carries no monetary fields.
func TestCatalogV2Contract(t *testing.T) {
	catalog, err := ParseCatalog([]byte(catalogFixture))
	if err != nil || catalog.SchemaVersion != 2 || len(catalog.Models) != 1 {
		t.Fatalf("v2 catalog rejected: %+v %v", catalog, err)
	}
	// Version 1 is refused before the body is interpreted, however malformed.
	for _, raw := range []string{
		`{"schema_version":1,"providers":[],"models":[]}`,
		`{"schema_version":1,"not_a_catalog":true}`,
		strings.Replace(catalogFixture, `"schema_version":2`, `"schema_version":1`, 1),
	} {
		got, err := ParseCatalog([]byte(raw))
		if !errors.Is(err, ErrCatalogVersion) || len(got.Models) != 0 || len(got.Providers) != 0 {
			t.Fatalf("v1 catalog accepted or not diagnosed: %v", err)
		}
	}
	for _, raw := range []string{
		strings.Replace(catalogFixture, `"timeout_ms":1000`, `"timeout_ms":1000,"input_unit_price":0`, 1),
		strings.Replace(catalogFixture, `"enabled":false`, `"enabled":false,"pricing_revision":"p1"`, 1),
	} {
		if _, err := ParseCatalog([]byte(raw)); err == nil || errors.Is(err, ErrCatalogVersion) {
			t.Fatalf("legacy price field accepted: %v", err)
		}
	}
	for _, change := range [][2]string{{`"max_bytes":1024`, `"max_bytes":0`}, {`"max_output_tokens":100`, `"max_output_tokens":0`}, {`"timeout_ms":1000`, `"timeout_ms":0`}} {
		if _, err := ParseCatalog([]byte(strings.Replace(catalogFixture, change[0], change[1], 1))); err == nil {
			t.Fatalf("invalid bound accepted: %s", change[1])
		}
	}
}
