// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRegistryFlagOffPreservesPlan05 keeps the fixed-profile default: an ANSWERED
// answer over the configured generation profile, and a store that reports exactly
// the profiles it was configured with.
func TestRegistryFlagOffPreservesPlan05(t *testing.T) {
	s, f := answerSetup()
	o, e := s.Answer(context.Background(), answerActor, answerRequest)
	if e != nil || o.Status != "ANSWERED" || f.calls != 1 {
		t.Fatal(o, e)
	}
	store := NewStore(nil)
	store.ConfigureModels(nil, answerProfile(), answerProfile())
	embed, generate := store.ModelProfiles()
	if embed != answerProfile() || generate != answerProfile() {
		t.Fatal(embed, generate)
	}
}

// TestRegistrySharedSlotAndNoAppOverride proves no request field selects a model:
// two apps read the same configured profile, and the store has no resolver to
// consult.
func TestRegistrySharedSlotAndNoAppOverride(t *testing.T) {
	for _, app := range []string{"one", "two"} {
		s, _ := answerSetup()
		req := answerRequest
		req.Scope.AppID = app
		o, e := s.Answer(context.Background(), answerActor, req)
		if e != nil || o.ModelProfile == nil || o.ModelProfile.Revision != answerProfile().Revision {
			t.Fatal(o, e)
		}
	}
	store := NewStore(nil)
	store.ConfigureModels(nil, answerProfile(), models.Profile{})
	if _, generate := store.ModelProfiles(); generate.ID != "" {
		t.Fatal("an unconfigured generation purpose was invented")
	}
}

func TestRegistryPersonalHostedZeroEgressAndRevocation(t *testing.T) {
	s, f := answerSetup()
	s.Policy = &models.Policy{Authority: &personalAnswerAuthority{}, Enabled: true}
	req := answerRequest
	req.Scope = Scope{Kind: "PERSONAL"}
	o, e := s.Answer(context.Background(), answerActor, req)
	if e != nil || o.Status != "SEARCH_ONLY" || f.calls != 0 {
		t.Fatal(o, e)
	}
	s, f = answerSetup()
	f.after = func() { f.revoked = true }
	o, e = s.Answer(context.Background(), answerActor, answerRequest)
	if e != nil || o.Reason != "AUTHORIZATION_CHANGED" || len(o.Results) != 0 {
		t.Fatal(o, e)
	}
}

func TestRegistryRequestSelectorsRejected(t *testing.T) {
	for _, field := range []string{"provider_id", "model_id", "profile_id", "endpoint", "credential_ref", "credential"} {
		for _, path := range []string{"answer", "search"} {
			body := `{"scope":{"kind":"APP","app_id":"library"},"query":"approval","` + field + `":"override"}`
			request := httptest.NewRequest("POST", "/"+path, strings.NewReader(body))
			writer := httptest.NewRecorder()
			var target any = &AnswerRequest{}
			if path == "search" {
				target = &SearchRequest{}
			}
			if err := decodeRequest(writer, request, target); err == nil {
				t.Fatal("selector accepted", field, path)
			}
		}
	}
}

// TestRegistryAnswerPinsRevisionAcrossSlotChange replaces the slot pinning test: the
// configured profile is immutable for the lifetime of the service, so a change to
// the database selection cannot make an in-flight answer switch routes.
func TestRegistryAnswerPinsRevisionAcrossSlotChange(t *testing.T) {
	service, provider := answerSetup()
	provider.after = func() { service.Profile = models.Profile{ID: "other", Revision: 2, Purpose: "GENERATE"} }
	first, err := service.Answer(context.Background(), answerActor, answerRequest)
	if err != nil || first.ModelProfile == nil || first.ModelProfile.Revision != 1 || provider.calls != 1 {
		t.Fatal(first, err)
	}
	second, err := service.Answer(context.Background(), answerActor, answerRequest)
	if err != nil || second.ModelProfile == nil || second.ModelProfile.Revision != 2 {
		t.Fatal(second, err)
	}
}
