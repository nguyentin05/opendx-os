// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package knowledge

import (
	"context"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
)

// TestRuntimeDeploymentConfigKeepsLexicalSearch fixes the partial-availability
// contract: with no configured or authorized generation profile the answer stays
// available as authorized search-only, and no model call is made.
func TestRuntimeDeploymentConfigKeepsLexicalSearch(t *testing.T) {
	cases := map[string]func(*AnswerService){
		"no gateway": func(s *AnswerService) { s.Gateway, s.Policy = nil, nil },
		"no profile": func(s *AnswerService) { s.Profile = models.Profile{} },
		"hosted denied": func(s *AnswerService) {
			s.Policy = &models.Policy{Authority: &personalAnswerAuthority{}, Enabled: true}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			service, fixture := answerSetup()
			mutate(service)
			if name == "no gateway" {
				service.Policy = nil
			}
			out, err := service.Answer(context.Background(), answerActor, answerRequest)
			if err != nil || out.Status != "SEARCH_ONLY" || len(out.Results) != 1 || len(out.Citations) != 0 {
				t.Fatalf("search-only fallback: %+v %v", out, err)
			}
			if fixture.calls != 0 {
				t.Fatal("an unavailable model was dispatched")
			}
		})
	}
}
