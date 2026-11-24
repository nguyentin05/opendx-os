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

type fixedResolver struct {
	profile models.Profile
	err     error
	calls   int
}

func (r *fixedResolver) Resolve(_ context.Context, c models.Capability) (models.ActiveBinding, error) {
	r.calls++
	return models.ActiveBinding{Capability: c, SlotRevision: 1, Profile: r.profile}, r.err
}
func TestRegistryFlagOffPreservesPlan05(t *testing.T) {
	s, f := answerSetup()
	o, e := s.Answer(context.Background(), answerActor, answerRequest)
	if e != nil || o.Status != "ANSWERED" || f.calls != 1 {
		t.Fatal(o, e)
	}
	store := NewStore(nil)
	store.ConfigureModels(nil, answerProfile(), answerProfile())
	p, e := store.modelProfile(context.Background(), models.CapabilityLLM)
	if e != nil || p != answerProfile() {
		t.Fatal(p, e)
	}
}
func TestRegistrySharedSlotAndNoAppOverride(t *testing.T) {
	r := &fixedResolver{profile: answerProfile()}
	r.profile.Revision = 9
	for _, app := range []string{"one", "two"} {
		s, _ := answerSetup()
		s.Resolver = r
		req := answerRequest
		req.Scope.AppID = app
		o, e := s.Answer(context.Background(), answerActor, req)
		if e != nil || o.ModelProfile == nil || o.ModelProfile.Revision != 9 {
			t.Fatal(o, e)
		}
	}
	store := NewStore(nil)
	store.ConfigureModels(nil, answerProfile(), answerProfile())
	store.ConfigureModelResolver(&fixedResolver{err: models.ErrUnavailable})
	if _, e := store.modelProfile(context.Background(), models.CapabilityLLM); e != models.ErrUnavailable {
		t.Fatal(e)
	}
}
func TestRegistryPersonalHostedZeroEgressAndRevocation(t *testing.T) {
	s, f := answerSetup()
	s.Policy = &models.Policy{Authority: &personalAnswerAuthority{}, Enabled: true}
	s.Resolver = &fixedResolver{profile: answerProfile()}
	req := answerRequest
	req.Scope = Scope{Kind: "PERSONAL"}
	o, e := s.Answer(context.Background(), answerActor, req)
	if e != nil || o.Status != "SEARCH_ONLY" || f.calls != 0 {
		t.Fatal(o, e)
	}
	s, f = answerSetup()
	s.Resolver = &fixedResolver{profile: answerProfile()}
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

func TestRegistryAnswerPinsRevisionAcrossSlotChange(t *testing.T) {
	resolver := &fixedResolver{profile: answerProfile()}
	service, provider := answerSetup()
	service.Resolver = resolver
	provider.after = func() { resolver.profile.Revision = 2 }
	first, err := service.Answer(context.Background(), answerActor, answerRequest)
	if err != nil || first.ModelProfile == nil || first.ModelProfile.Revision != 1 || resolver.calls != 1 {
		t.Fatal(first, err, resolver.calls)
	}
	second, err := service.Answer(context.Background(), answerActor, answerRequest)
	if err != nil || second.ModelProfile == nil || second.ModelProfile.Revision != 2 || resolver.calls != 2 {
		t.Fatal(second, err, resolver.calls)
	}
}
