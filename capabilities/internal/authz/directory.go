// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package authz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// Accounts reads the people of the identity provider. The platform never
// creates or changes them there: Keycloak, or the directory it federates, is
// the only place people are created (ADR 0007).
type Accounts interface {
	// List returns every enabled person with an email; service accounts are left out.
	List(ctx context.Context) ([]auth.Principal, error)
}

// accountSync is the least time between two reads of the identity provider.
const accountSync = 10 * time.Second

// MirrorAccounts lets the operator's account list include people who have not
// signed in yet, read from accounts, so they can be given roles at once.
func (r *Resolver) MirrorAccounts(accounts Accounts) {
	r.accounts = accounts
}

// syncAccounts mirrors the identity provider's people into core.users, as their
// first sign-in would, at most once per accountSync. A failure leaves the list
// as recorded.
func (r *Resolver) syncAccounts(ctx context.Context) {
	if r.accounts == nil {
		return
	}
	r.syncMu.Lock()
	defer r.syncMu.Unlock()
	if !r.synced.IsZero() && r.now().Sub(r.synced) < accountSync {
		return
	}
	r.synced = r.now()
	people, err := r.accounts.List(ctx)
	if err != nil {
		logf("reading the identity provider's accounts failed: %v", err)
		return
	}
	for _, p := range people {
		if _, err := r.store.Mirror(ctx, p); err != nil {
			logf("mirroring account %s failed: %v", p.Subject, err)
		}
	}
}

// KeycloakAccounts reads a realm's users with the Keycloak Admin REST API, as
// the service account of the client dxos-directory, which holds only view-users.
type KeycloakAccounts struct {
	BaseURL string // internal address, for example http://keycloak:8085
	Realm   string
	Token   auth.TokenGetter
	Client  *http.Client
}

// NewKeycloakAccounts uses the client dxos-directory of realm at baseURL.
func NewKeycloakAccounts(baseURL, realm, clientSecret string) (*KeycloakAccounts, error) {
	base := strings.TrimRight(baseURL, "/")
	cc, err := auth.NewClientCredentials(base+"/realms/"+url.PathEscape(realm)+"/protocol/openid-connect/token",
		"dxos-directory", clientSecret)
	if err != nil {
		return nil, err
	}
	return &KeycloakAccounts{BaseURL: base, Realm: realm, Token: cc.Token,
		Client: &http.Client{Timeout: 10 * time.Second}}, nil
}

// keycloakPage and keycloakLimit bound one read; a realm is assumed to hold
// at most keycloakLimit people.
const keycloakPage, keycloakLimit = 200, 20000

// List implements Accounts.
func (k *KeycloakAccounts) List(ctx context.Context) ([]auth.Principal, error) {
	token, err := k.Token(ctx)
	if err != nil {
		return nil, err
	}
	var people []auth.Principal
	for first := 0; first < keycloakLimit; first += keycloakPage {
		target := k.BaseURL + "/admin/realms/" + url.PathEscape(k.Realm) + "/users?briefRepresentation=true&first=" +
			strconv.Itoa(first) + "&max=" + strconv.Itoa(keycloakPage)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := k.Client.Do(request)
		if err != nil {
			return nil, err
		}
		var page []struct {
			ID, Username, Email, FirstName, LastName string
			Enabled                                  bool
		}
		if response.StatusCode == http.StatusOK {
			err = json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&page)
		} else {
			err = fmt.Errorf("keycloak answered %d", response.StatusCode)
		}
		_ = response.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, u := range page {
			if !u.Enabled || u.Email == "" || strings.HasPrefix(u.Username, "service-account-") || !uuidPattern.MatchString(u.ID) {
				continue
			}
			// Keycloak's name claim is "firstName lastName"; a person without one shows the username.
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if name == "" {
				name = u.Username
			}
			people = append(people, auth.Principal{Subject: strings.ToLower(u.ID), Email: u.Email, Name: name})
		}
		if len(page) < keycloakPage {
			return people, nil
		}
	}
	return people, nil
}
