// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Notification struct {
	EventID    string `json:"event_id"`
	AppID      string `json:"app_id"`
	Generation int64  `json:"publication_generation"`
	SpecDigest string `json:"spec_digest"`
}
type Dispatcher struct {
	DB              *sql.DB
	Endpoint, Token string
	Client          *http.Client
}

func (d *Dispatcher) Deliver(ctx context.Context, n Notification) error {
	u, e := url.Parse(d.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || (u.Scheme != "http" && u.Scheme != "https") || d.Token == "" {
		return fmt.Errorf("invalid package delivery configuration")
	}
	body, _ := json.Marshal(map[string]any{"schema_version": 1, "app_id": n.AppID, "publication_generation": n.Generation, "spec_digest": n.SpecDigest})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	r, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(d.Endpoint, "/")+"/internal/v1/package-sources/reconcile", bytes.NewReader(body))
	if e != nil {
		return fmt.Errorf("invalid package delivery")
	}
	r.Header.Set("Authorization", "Bearer "+d.Token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", n.EventID)
	client := http.Client{Timeout: 20 * time.Second}
	if d.Client != nil {
		client = *d.Client
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, e := client.Do(r)
	if e != nil {
		return fmt.Errorf("package delivery unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != 204 {
		return fmt.Errorf("package delivery refused")
	}
	return nil
}
func (d *Dispatcher) Once(ctx context.Context) (bool, error) {
	if d.DB == nil {
		return false, fmt.Errorf("dispatcher database unavailable")
	}
	token, e := newID()
	if e != nil {
		return false, e
	}
	var raw []byte
	if e = d.DB.QueryRowContext(ctx, `SELECT core.claim_knowledge_publication($1)`, token).Scan(&raw); e != nil {
		return false, fmt.Errorf("notification lease unavailable")
	}
	if len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}
	var n Notification
	if e = json.Unmarshal(raw, &n); e != nil {
		return false, fmt.Errorf("invalid notification")
	}
	delivered := d.Deliver(ctx, n) == nil
	ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	var ack bool
	if e = d.DB.QueryRowContext(ackCtx, `SELECT core.ack_knowledge_publication($1,$2,$3)`, n.EventID, token, delivered).Scan(&ack); e != nil || !ack {
		return true, fmt.Errorf("notification acknowledgement unavailable")
	}
	if !delivered {
		return true, fmt.Errorf("package delivery deferred")
	}
	return true, nil
}
