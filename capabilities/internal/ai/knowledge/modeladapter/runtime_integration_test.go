//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modeladapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// Pinned providers retain their route when a replacement is added, and fail
// closed when deployment removes that old digest.
func TestHostedRouteOverlapAndRemoval(t *testing.T) {
	cfg := mustParseConfig(t, configFixture)
	old := cfg.Routes[0]
	next := routeCopy(old)
	next.CredentialRef = "REPLACEMENT_VENDOR_KEY"
	oldPath, err := RoutePath(old)
	if err != nil {
		t.Fatal(err)
	}
	nextPath, err := RoutePath(next)
	if err != nil || nextPath == oldPath {
		t.Fatal("route revision lost identity", err)
	}
	sink := &stubUpstream{result: models.Result{Vector: make([]float32, 1536)}}
	cfg.Routes = []Route{old, next}
	handler, err := NewHandler(cfg, adapterToken, sink)
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Value
	active.Store(handler)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { active.Load().(http.Handler).ServeHTTP(w, r) }))
	defer server.Close()
	prior := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = prior }()
	provider, err := models.NewHTTPProvider(server.URL+oldPath, adapterToken, false)
	if err != nil {
		t.Fatal(err)
	}
	profile := models.Profile{ID: "pinned", Revision: 1, ProviderID: "old-provider", ModelID: old.Models[0].ModelID, Purpose: "EMBED", Dimension: 1536, Limits: old.Models[0].Limits}
	if _, err = provider.Invoke(context.Background(), profile, models.Payload{Texts: []string{"synthetic"}}); err != nil || sink.count() != 1 {
		t.Fatal("old route unavailable during overlap", err, sink.count())
	}
	cfg.Routes = []Route{next}
	handler, err = NewHandler(cfg, adapterToken, sink)
	if err != nil {
		t.Fatal(err)
	}
	active.Store(handler)
	if _, err = provider.Invoke(context.Background(), profile, models.Payload{Texts: []string{"synthetic"}}); err == nil || sink.count() != 1 {
		t.Fatal("removed route dispatched", err, sink.count())
	}
	replacement, err := models.NewHTTPProvider(server.URL+nextPath, adapterToken, false)
	if err != nil {
		t.Fatal(err)
	}
	profile.ProviderID = "replacement-provider"
	profile.Revision = 2
	if _, err = replacement.Invoke(context.Background(), profile, models.Payload{Texts: []string{"synthetic"}}); err != nil || sink.count() != 2 {
		t.Fatal("replacement route unavailable", err, sink.count())
	}
}
