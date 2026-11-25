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

// TestAnswerPinsProfileAcrossInFlightChange keeps the property the retired slot
// test protected: an answer cannot switch models while it is in flight. The service
// holds one fixed configured profile, captured when the turn starts, so replacing
// that profile while the call is outstanding changes nothing about the answer that
// is already running — neither the dispatched operation, nor the reported profile
// revision. The next turn binds whatever the service is configured with, because a
// redeploy rebinds the service; it never mutates an answer in progress.
func TestAnswerPinsProfileAcrossInFlightChange(t *testing.T) {
	service, provider := answerSetup()
	replacement := models.Profile{ID: "other", Revision: 2, Purpose: "GENERATE", Execution: "HOSTED", Limits: answerProfile().Limits}
	provider.after = func() { service.Profile = replacement }
	first, err := service.Answer(context.Background(), answerActor, answerRequest)
	if err != nil || first.Status != "ANSWERED" || first.ModelProfile == nil {
		t.Fatal(first, err)
	}
	if first.ModelProfile.ID != "gen" || first.ModelProfile.Revision != 1 {
		t.Fatalf("an in-flight answer reported a switched profile: %+v", first.ModelProfile)
	}
	if provider.op.ProfileID != "gen" || provider.op.ProfileRevision != 1 {
		t.Fatalf("an in-flight answer dispatched another profile: %+v", provider.op)
	}
	if provider.calls != 1 {
		t.Fatalf("an in-flight answer made %d calls", provider.calls)
	}
	// A later turn uses the replacement: the change reaches new turns only.
	provider.after = nil
	second, err := service.Answer(context.Background(), answerActor, answerRequest)
	if err != nil || second.ModelProfile == nil || second.ModelProfile.ID != replacement.ID || second.ModelProfile.Revision != replacement.Revision {
		t.Fatal(second, err)
	}
	if provider.op.ProfileID != replacement.ID || provider.op.ProfileRevision != replacement.Revision {
		t.Fatalf("a later turn did not bind the configured profile: %+v", provider.op)
	}
}
