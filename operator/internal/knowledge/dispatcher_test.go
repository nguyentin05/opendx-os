// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDispatcherUsesBoundedPrivateNotification(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if len(body) != 4 || body["app_id"] != "library" || body["publication_generation"] != float64(3) || r.Header.Get("Authorization") != "Bearer test-service" || r.URL.Path != "/internal/v1/package-sources/reconcile" {
			t.Error("notification authority/transport mismatch", body)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	d := Dispatcher{Endpoint: server.URL, Token: StaticToken("test-service"), Client: server.Client()}
	n := Notification{AppID: "library", Generation: 3, SpecDigest: "digest", EventID: "event"}
	if e := d.Deliver(context.Background(), n); e != nil || calls != 1 {
		t.Fatal(e, calls)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, server.URL, 307) }))
	defer redirect.Close()
	d.Endpoint = redirect.URL
	if e := d.Deliver(context.Background(), n); e == nil || calls != 1 {
		t.Fatal("dispatcher followed redirect", e, calls)
	}
}
