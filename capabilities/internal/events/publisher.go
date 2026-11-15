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

// Package events delivers persisted command envelopes with durable leases.
package events

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/registry"
)

// Config is where and how events reach the workflow engine.
type Config struct {
	// BaseURL of the engine, for example http://kestra:8080.
	BaseURL string
	// User and Password authenticate the publisher to the engine (basic).
	User, Password string
	// WebhookKey is the key of the platform's webhook trigger, shared with the
	// engine as a secret. It is part of the destination URL.
	WebhookKey string
}

var webhookKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// Publisher delivers pending outbox events to the workflow of their
// application, as the operator deployed it.
type Publisher struct {
	db         *sql.DB
	client     *http.Client
	base       string
	webhookKey string
	user       string
	password   string
}

// NewPublisher returns a Publisher for the engine described by cfg.
func NewPublisher(db *sql.DB, client *http.Client, cfg Config) (*Publisher, error) {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("KESTRA_URL must be the http(s) base URL of the workflow engine")
	}
	if cfg.User == "" || cfg.Password == "" || strings.ContainsAny(cfg.User+cfg.Password, "\r\n") {
		return nil, errors.New("KESTRA_USER and KESTRA_PASSWORD are required")
	}
	if !webhookKeyPattern.MatchString(cfg.WebhookKey) {
		return nil, errors.New("KESTRA_WEBHOOK_KEY must be 16 to 128 characters of A-Z, a-z, 0-9, - and _")
	}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout <= 0 || copyClient.Timeout > 20*time.Second {
		copyClient.Timeout = 20 * time.Second
	}
	return &Publisher{db: db, client: &copyClient, base: strings.TrimRight(cfg.BaseURL, "/"),
		webhookKey: cfg.WebhookKey, user: cfg.User, password: cfg.Password}, nil
}

func backoff(attempt int) time.Duration {
	if attempt > 10 {
		attempt = 10
	}
	if attempt < 1 {
		attempt = 1
	}
	return time.Second * time.Duration(1<<uint(attempt))
}
func (p *Publisher) PublishPending(ctx context.Context, limit int) error {
	if limit < 1 || limit > 1000 {
		return errors.New("invalid batch limit")
	}
	for range limit {
		var b [16]byte
		if _, e := rand.Read(b[:]); e != nil {
			return e
		}
		token := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
		var id string
		var payload []byte
		var attempt int
		err := p.db.QueryRowContext(ctx, `WITH due AS (SELECT id FROM core.outbox_events WHERE (delivery_status='pending' AND next_attempt_at<=now()) OR (delivery_status='in_flight' AND lease_until<=now()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE core.outbox_events o SET delivery_status='in_flight',attempt_count=attempt_count+1,lease_token=$1,lease_until=now()+interval '60 seconds' FROM due WHERE o.id=due.id RETURNING o.id::text,o.payload,o.attempt_count`, token).Scan(&id, &payload, &attempt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		// An event of an application that declares no workflow for it has
		// nothing to deliver; it is closed instead of retried forever.
		sendErr := errors.New("delivery failed")
		if len(payload) <= 1048576 {
			sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			endpoint, routeErr := p.resolveEndpoint(sendCtx, payload)
			switch {
			case errors.Is(routeErr, errNoSubscriber):
				sendErr = errNoSubscriber
			case routeErr != nil:
				sendErr = routeErr
				if !errors.Is(routeErr, errEnvelope) && !errors.Is(routeErr, errAmbiguous) && !errors.Is(routeErr, errNotDeployed) && !errors.Is(routeErr, registry.ErrUnavailable) {
					sendErr = errors.New("route lookup failed")
				}
			default:
				sendErr = p.send(sendCtx, endpoint, id, payload)
			}
			cancel()
		}
		var result sql.Result
		switch {
		case sendErr == nil:
			result, err = p.db.ExecContext(ctx, `UPDATE core.outbox_events SET delivery_status='delivered',delivered_at=now(),lease_token=NULL,lease_until=NULL,last_error=NULL WHERE id=$1 AND lease_token=$2 AND lease_until>now()`, id, token)
		case errors.Is(sendErr, errNoSubscriber):
			result, err = p.db.ExecContext(ctx, `UPDATE core.outbox_events SET delivery_status='delivered',delivered_at=now(),lease_token=NULL,lease_until=NULL,last_error=$3 WHERE id=$1 AND lease_token=$2 AND lease_until>now()`, id, token, sendErr.Error())
		default:
			result, err = p.db.ExecContext(ctx, `UPDATE core.outbox_events SET delivery_status='pending',next_attempt_at=now()+($3 * interval '1 second'),lease_token=NULL,lease_until=NULL,last_error=$4 WHERE id=$1 AND lease_token=$2 AND lease_until>now()`, id, token, backoff(attempt).Seconds(), sendErr.Error())
		}
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("delivery lease expired")
		}
	}
	return nil
}

// send posts one event to its flow's webhook. The event id is the idempotency
// key, so a retry after a lost acknowledgement is recognizable.
func (p *Publisher) send(ctx context.Context, endpoint, id string, payload []byte) error {
	failed := errors.New("delivery failed")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return failed
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", id)
	req.SetBasicAuth(p.user, p.password)
	resp, err := p.client.Do(req)
	if err != nil {
		return failed
	}
	readSize, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 65537))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 || readErr != nil || readSize > 65536 {
		return fmt.Errorf("delivery failed: the workflow engine answered HTTP %d", resp.StatusCode)
	}
	return nil
}
