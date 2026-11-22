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

package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenGetter returns an access token without the "Bearer " prefix.
type TokenGetter func(context.Context) (string, error)

// StaticToken returns a getter for an operator-supplied token.
func StaticToken(token string) TokenGetter {
	return func(context.Context) (string, error) { return token, nil }
}

// TokenGetterFrom keeps the static token when one is configured and otherwise
// exchanges the client credentials for tokens.
func TokenGetterFrom(static, endpoint, clientID, clientSecret string) (TokenGetter, error) {
	if strings.TrimSpace(static) != "" {
		return StaticToken(strings.TrimSpace(static)), nil
	}
	source, err := NewClientCredentials(endpoint, clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	return source.Token, nil
}

type tokenCall struct {
	done  chan struct{}
	token string
	err   error
}

// ClientCredentials caches one service token and renews it at 75% of its
// lifetime. Concurrent callers share a single request.
type ClientCredentials struct {
	endpoint     string
	clientID     string
	clientSecret string
	client       *http.Client
	now          func() time.Time

	mu       sync.Mutex
	token    string
	renewAt  time.Time
	inflight *tokenCall
}

// NewClientCredentials validates the endpoint and the client credentials.
func NewClientCredentials(endpoint, clientID, clientSecret string) (*ClientCredentials, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") ||
		u.RawQuery != "" || u.Fragment != "" ||
		strings.TrimSpace(clientID) == "" || strings.TrimSpace(clientSecret) == "" {
		return nil, errors.New("invalid client credentials configuration")
	}
	return &ClientCredentials{
		endpoint:     u.String(),
		clientID:     clientID,
		clientSecret: clientSecret,
		client:       &http.Client{Timeout: 10 * time.Second},
		now:          time.Now,
	}, nil
}

// Token returns a cached token, a shared in-flight result, or a fresh token.
func (c *ClientCredentials) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && c.now().Before(c.renewAt) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	if c.inflight != nil {
		call := c.inflight
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.token, call.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	call := &tokenCall{done: make(chan struct{})}
	c.inflight = call
	c.mu.Unlock()

	token, lifetime, err := c.fetch()
	c.mu.Lock()
	if c.inflight == call {
		c.inflight = nil
	}
	if err == nil {
		c.token = token
		c.renewAt = c.now().Add(lifetime * 3 / 4)
	}
	c.mu.Unlock()
	call.token, call.err = token, err
	close(call.done)
	return token, err
}

func (c *ClientCredentials) fetch() (string, time.Duration, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	request, err := http.NewRequest(http.MethodPost, c.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, errors.New("token request unavailable")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.client.Do(request)
	if err != nil {
		return "", 0, errors.New("token endpoint unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("token endpoint returned %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if err != nil {
		return "", 0, errors.New("token response unavailable")
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if json.Unmarshal(raw, &body) != nil || body.AccessToken == "" ||
		!strings.EqualFold(body.TokenType, "bearer") || body.ExpiresIn < 1 {
		return "", 0, errors.New("token response invalid")
	}
	return body.AccessToken, time.Duration(body.ExpiresIn) * time.Second, nil
}
