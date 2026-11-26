// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package authz

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
)

// ErrExists means the identity provider already has an account with that
// email or username.
var ErrExists = errors.New("account exists")

// NewAccount is what an operator gives to create a sign-in account.
type NewAccount struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Username    string `json:"username,omitempty"`
}

// Accounts creates sign-in accounts in the identity provider. Keycloak still
// only authenticates (ADR 0007): no role is ever written there.
type Accounts interface {
	// Create makes an enabled account that must replace temporaryPassword at
	// its first sign-in, and returns the account id (the tokens' sub).
	Create(ctx context.Context, a NewAccount, temporaryPassword string) (string, error)
	// Delete removes an account Create made, when the platform cannot record it.
	Delete(ctx context.Context, id string) error
}

// Provision lets operators create accounts through accounts.
func (r *Resolver) Provision(accounts Accounts) {
	r.accounts = accounts
}

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

// validate trims a and reports the first invalid field.
func (a *NewAccount) validate() error {
	a.Email = strings.TrimSpace(a.Email)
	a.DisplayName = strings.Join(strings.Fields(a.DisplayName), " ")
	a.Username = strings.ToLower(strings.TrimSpace(a.Username))
	if addr, err := mail.ParseAddress(a.Email); err != nil || addr.Address != a.Email || len(a.Email) > 254 {
		return errors.New("email must be a plain address such as name@example.org")
	}
	if a.DisplayName == "" || len([]rune(a.DisplayName)) > 200 {
		return errors.New("display_name is required (at most 200 characters)")
	}
	if a.Username == "" {
		a.Username = strings.ToLower(a.Email)
	} else if !usernamePattern.MatchString(a.Username) {
		return errors.New("username must be 3-64 lowercase letters, digits, '.', '_' or '-'")
	}
	return nil
}

// temporaryPassword returns 26 random base32 characters (130 bits), shown to
// the operator once.
func temporaryPassword() string {
	return rand.Text()
}

// createAccount answers POST /api/auth/users for the operator in s.
func (r *Resolver) createAccount(w http.ResponseWriter, req *http.Request, s *Session) {
	if r.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account creation is not configured; create the account in the identity provider"})
		return
	}
	var a NewAccount
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&a); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the body must be {\"email\", \"display_name\", \"username\"?}"})
		return
	}
	if err := a.validate(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	password := temporaryPassword()
	id, err := r.accounts.Create(req.Context(), a, password)
	switch {
	case errors.Is(err, ErrExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an account with this email or username already exists"})
		return
	case err != nil:
		logf("account creation failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account creation unavailable"})
		return
	}
	// Recorded now, so roles and a department can be given before the first sign-in.
	if err := r.directory.CreateUser(req.Context(), s.User.Subject, id, a.Email, a.DisplayName); err != nil {
		logf("recording account %s failed: %v", id, err)
		if derr := r.accounts.Delete(context.WithoutCancel(req.Context()), id); derr != nil {
			logf("removing unrecorded account %s failed: %v", id, derr)
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "account creation unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{
		"id": id, "email": a.Email, "display_name": a.DisplayName, "username": a.Username,
		"temporary_password": password,
	})
}

// KeycloakAccounts creates accounts with the Keycloak Admin REST API, as a
// service account that holds only manage-users and view-users in its realm.
type KeycloakAccounts struct {
	BaseURL string // internal address, for example http://keycloak:8085
	Realm   string
	Token   auth.TokenGetter
	Client  *http.Client
}

// NewKeycloakAccounts uses the client dxos-provisioner of realm at baseURL.
func NewKeycloakAccounts(baseURL, realm, clientSecret string) (*KeycloakAccounts, error) {
	base := strings.TrimRight(baseURL, "/")
	cc, err := auth.NewClientCredentials(base+"/realms/"+url.PathEscape(realm)+"/protocol/openid-connect/token",
		"dxos-provisioner", clientSecret)
	if err != nil {
		return nil, err
	}
	return &KeycloakAccounts{BaseURL: base, Realm: realm, Token: cc.Token,
		Client: &http.Client{Timeout: 10 * time.Second}}, nil
}

func (k *KeycloakAccounts) users() string {
	return k.BaseURL + "/admin/realms/" + url.PathEscape(k.Realm) + "/users"
}

func (k *KeycloakAccounts) do(ctx context.Context, method, target string, body any) (*http.Response, error) {
	token, err := k.Token(ctx)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return k.Client.Do(request)
}

// Create implements Accounts.
func (k *KeycloakAccounts) Create(ctx context.Context, a NewAccount, temporaryPassword string) (string, error) {
	// Keycloak's name claim is "firstName lastName"; this keeps the display name whole.
	first, last := a.DisplayName, ""
	if i := strings.LastIndex(a.DisplayName, " "); i > 0 {
		first, last = a.DisplayName[:i], a.DisplayName[i+1:]
	}
	response, err := k.do(ctx, http.MethodPost, k.users(), map[string]any{
		"username": a.Username, "email": a.Email, "firstName": first, "lastName": last,
		"enabled": true, "emailVerified": true, "requiredActions": []string{"UPDATE_PASSWORD"},
		"credentials": []map[string]any{{"type": "password", "value": temporaryPassword, "temporary": true}},
	})
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusCreated:
	case http.StatusConflict:
		return "", ErrExists
	default:
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return "", fmt.Errorf("keycloak answered %d: %s", response.StatusCode, bytes.TrimSpace(detail))
	}
	location, err := url.Parse(response.Header.Get("Location"))
	id := ""
	if err == nil {
		id = path.Base(location.Path)
	}
	if !uuidPattern.MatchString(id) {
		return "", fmt.Errorf("keycloak answered 201 without a user id")
	}
	return strings.ToLower(id), nil
}

// Delete implements Accounts.
func (k *KeycloakAccounts) Delete(ctx context.Context, id string) error {
	response, err := k.do(ctx, http.MethodDelete, k.users()+"/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotFound {
		return fmt.Errorf("keycloak answered %d", response.StatusCode)
	}
	return nil
}
