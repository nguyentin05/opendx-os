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
	"time"
)

type Publisher struct {
	db              *sql.DB
	client          *http.Client
	endpoint, token string
}

func NewPublisher(db *sql.DB, client *http.Client, endpoint string) *Publisher {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout <= 0 || copyClient.Timeout > 20*time.Second {
		copyClient.Timeout = 20 * time.Second
	}
	return &Publisher{db: db, client: &copyClient, endpoint: endpoint}
}
func (p *Publisher) SetBearerToken(token string) { p.token = token }
func ValidateEndpoint(endpoint string) error {
	u, e := url.Parse(endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("invalid workflow endpoint")
	}
	return nil
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
	if ValidateEndpoint(p.endpoint) != nil {
		return errors.New("invalid workflow endpoint")
	}
	if limit < 1 || limit > 1000 {
		return errors.New("invalid batch limit")
	}
	for i := 0; i < limit; i++ {
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
		sendErr := errors.New("delivery failed")
		if len(payload) <= 1048576 {
			sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			req, e := http.NewRequestWithContext(sendCtx, "POST", p.endpoint, bytes.NewReader(payload))
			if e == nil {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", id)
				if p.token != "" {
					req.Header.Set("Authorization", "Bearer "+p.token)
				}
				resp, e := p.client.Do(req)
				if e == nil {
					readSize, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 65537))
					resp.Body.Close()
					if resp.StatusCode >= 200 && resp.StatusCode < 300 && readErr == nil && readSize <= 65536 {
						sendErr = nil
					}
				}
			}
			cancel()
		}
		var result sql.Result
		if sendErr == nil {
			result, err = p.db.ExecContext(ctx, `UPDATE core.outbox_events SET delivery_status='delivered',delivered_at=now(),lease_token=NULL,lease_until=NULL,last_error=NULL WHERE id=$1 AND lease_token=$2 AND lease_until>now()`, id, token)
		} else {
			result, err = p.db.ExecContext(ctx, `UPDATE core.outbox_events SET delivery_status='pending',next_attempt_at=now()+($3 * interval '1 second'),lease_token=NULL,lease_until=NULL,last_error='delivery failed' WHERE id=$1 AND lease_token=$2 AND lease_until>now()`, id, token, backoff(attempt).Seconds())
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
