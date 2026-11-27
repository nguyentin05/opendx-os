// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package authz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/telemetry"
)

// Person is one account of the identity provider.
type Person struct {
	auth.Principal
	Enabled bool
	// Service marks a client's service account: it counts as present, but is
	// not a person to mirror.
	Service bool
}

// Accounts reads the people of the identity provider. The platform never
// creates or changes them there: Keycloak, or the directory it federates, is
// the only place people are created (ADR 0007).
type Accounts interface {
	// List returns every account of the realm, or an error when it could not
	// read all of them: a partial list must never disable anyone.
	List(ctx context.Context) ([]Person, error)
}

// accountSync is the least time between two reads of the identity provider.
const accountSync = 10 * time.Second

// MirrorAccounts lets the platform follow the identity provider through
// accounts: people appear before their first sign-in, and an account disabled
// or deleted there is refused here.
func (r *Resolver) MirrorAccounts(accounts Accounts) {
	r.accounts = accounts
}

// RunAccountSync mirrors the identity provider every interval until ctx ends.
func (r *Resolver) RunAccountSync(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		r.SyncAccounts(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SyncAccounts mirrors the identity provider into core.users, at most once per
// accountSync: every person is recorded as their first sign-in would record
// them, and their status follows the identity provider. Accounts missing from a
// complete read are disabled; platform service accounts are never touched (the
// database refuses it). Failures are logged; the recorded state stays.
func (r *Resolver) SyncAccounts(ctx context.Context) {
	if r.accounts == nil || r.directory == nil {
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
	present := make(map[string]bool, len(people))
	changed := false
	for _, p := range people {
		present[strings.ToLower(p.Subject)] = true
		if p.Service || p.Email == "" {
			continue
		}
		if _, err := r.store.Mirror(ctx, p.Principal); err != nil {
			logf("mirroring account %s failed: %v", p.Subject, err)
			continue
		}
		if moved, err := r.directory.MirrorStatus(ctx, p.Subject, p.Enabled); err != nil {
			logf("mirroring the status of account %s failed: %v", p.Subject, err)
		} else if moved {
			changed = true
			r.forget(p.Subject)
		}
	}
	recorded, err := r.directory.UserIDs(ctx)
	if err != nil {
		logf("listing recorded accounts failed: %v", err)
		return
	}
	for _, id := range recorded {
		if present[strings.ToLower(id)] {
			continue
		}
		if moved, err := r.directory.MirrorStatus(ctx, id, false); err != nil {
			logf("disabling deleted account %s failed: %v", id, err)
		} else if moved {
			changed = true
			r.forget(id)
		}
	}
	if changed {
		logf("account status follows the identity provider (%d accounts read)", len(people))
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
		Client: &http.Client{Timeout: 10 * time.Second, Transport: telemetry.Client(nil)}}, nil
}

// keycloakPage and keycloakLimit bound one read.
const keycloakPage, keycloakLimit = 200, 20000

// errTooManyAccounts keeps a realm larger than keycloakLimit from being read as complete.
var errTooManyAccounts = errors.New("the realm has more accounts than one read covers")

// List implements Accounts.
func (k *KeycloakAccounts) List(ctx context.Context) ([]Person, error) {
	token, err := k.Token(ctx)
	if err != nil {
		return nil, err
	}
	var people []Person
	for first := 0; ; first += keycloakPage {
		if first >= keycloakLimit {
			return nil, errTooManyAccounts
		}
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
			if !uuidPattern.MatchString(u.ID) {
				continue
			}
			// Keycloak's name claim is "firstName lastName"; a person without one shows the username.
			name := strings.TrimSpace(u.FirstName + " " + u.LastName)
			if name == "" {
				name = u.Username
			}
			people = append(people, Person{
				Principal: auth.Principal{Subject: strings.ToLower(u.ID), Email: u.Email, Name: name},
				Enabled:   u.Enabled,
				Service:   strings.HasPrefix(u.Username, "service-account-"),
			})
		}
		if len(page) < keycloakPage {
			return people, nil
		}
	}
}
