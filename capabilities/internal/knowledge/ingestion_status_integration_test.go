//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
package knowledge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/knowledge/models"
)

// TestIngestionUncertainStatusPublicContract drives the real uncertain
// embedding path (leased EMBED job, gateway send that may have been sent,
// worker cleanup through cap_ai.fail_embedding) and then reads the job through
// the public ingestion route. An uncertain job remains an ingestion job FAILED
// carrying the public uncertainty code, while the durable operation keeps its
// separate UNCERTAIN state. Only a reader with current source READ may learn
// either fact.
func TestIngestionUncertainStatusPublicContract(t *testing.T) {
	ctx := context.Background()
	db, s, a, profile := semanticFixture(t)
	provider := &embeddingProvider{fail: true}
	s.ConfigureModels(&models.Gateway{Policy: &models.Policy{Enabled: true, Authority: models.DatabaseAuthority{DB: db}}, Operations: &models.OperationStore{DB: gatewayLogin(t, db)}, Providers: map[string]models.Provider{"fake": provider}}, profile, models.Profile{})
	if e := s.EnqueueEmbeddings(ctx, a, profile); e != nil {
		t.Fatal(e)
	}
	lease, e := s.ClaimEmbedding(ctx, ownerID)
	if e != nil || lease == nil {
		t.Fatal(e)
	}
	if e = s.BuildEmbeddings(ctx, *lease, profile); e == nil {
		t.Fatal("provider uncertainty swallowed")
	}
	// The gateway sends once per chunk and never resends an operation that may
	// have been sent, so worker cleanup cannot produce a second provider call.
	if provider.calls != 1 {
		t.Fatalf("duplicate or missing provider send: %d", provider.calls)
	}
	// The durable operation and the ingestion job keep separate states: the
	// operation records that a call may have been sent, the job that the
	// embedding build failed. Neither is a monetary fact.
	var operationState string
	if e = db.QueryRow(`SELECT state FROM cap_ai.model_operations WHERE operation_key LIKE 'embed:%' ORDER BY claimed_at LIMIT 1`).Scan(&operationState); e != nil {
		t.Fatal(e)
	}
	if operationState != "UNCERTAIN" {
		t.Fatalf("durable operation state changed: %q", operationState)
	}
	var jobState string
	if e = db.QueryRow(`SELECT state FROM cap_ai.ingestion_jobs WHERE id=$1`, lease.JobID).Scan(&jobState); e != nil {
		t.Fatal(e)
	}
	if jobState != "FAILED" {
		t.Fatalf("uncertain embedding job state: %q", jobState)
	}
	// Lexical retrieval is untouched by the failed embedding build.
	result, e := s.Search(ctx, a, SearchRequest{Scope: Scope{Kind: "APP", AppID: "library"}, Query: "Director"})
	if e != nil || len(result.Results) != 1 {
		t.Fatal("lexical retrieval lost", result, e)
	}

	// The currently authorized reader sees the job status and the public code.
	body, status := ingestionGET(t, s, a, lease.JobID)
	if status != http.StatusOK {
		t.Fatalf("authorized ingestion read: %d %s", status, body)
	}
	for _, want := range []string{`"status":"FAILED"`, `"error_code":"MODEL_CALL_UNCERTAIN"`, `"job_id":"` + lease.JobID + `"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("public ingestion contract missing %s: %s", want, body)
		}
	}
	for _, unwanted := range []string{"MODEL_CHARGE_UNCERTAIN", "MODEL_UNAVAILABLE", "POLICY_BLOCKED"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("public ingestion body leaked %s: %s", unwanted, body)
		}
	}

	// Concealment follows current source READ, not job ownership: another
	// registered user who currently READs the role-granted package source sees
	// the same status, and stops seeing it the moment the granting role is gone.
	foreign := &auth.Identity{UserID: otherID, Role: a.Role}
	if body, status = ingestionGET(t, s, foreign, lease.JobID); status != http.StatusOK || !strings.Contains(body, `"error_code":"MODEL_CALL_UNCERTAIN"`) {
		t.Fatalf("foreign reader with current READ: %d %s", status, body)
	}
	if _, e = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, otherID); e != nil {
		t.Fatal(e)
	}
	assertConcealedIngestion(t, s, foreign, lease.JobID)
	// Losing the granting role also conceals the job from its own actor.
	if _, e = db.Exec(`DELETE FROM core.user_roles WHERE user_id=$1`, a.UserID); e != nil {
		t.Fatal(e)
	}
	assertConcealedIngestion(t, s, a, lease.JobID)
	// An unknown subject cannot resolve authority at all and gets the same answer.
	assertConcealedIngestion(t, s, &auth.Identity{UserID: "99999999-9999-4999-8999-999999999999", Role: a.Role}, lease.JobID)
}

// assertConcealedIngestion requires the existing concealed not-found outcome
// with no job status, state or error code in the response body.
func assertConcealedIngestion(t *testing.T, s *Store, actor *auth.Identity, jobID string) {
	t.Helper()
	body, status := ingestionGET(t, s, actor, jobID)
	if status != http.StatusNotFound || !strings.Contains(body, `"code":"NOT_FOUND"`) {
		t.Fatalf("ingestion read not concealed: %d %s", status, body)
	}
	for _, unwanted := range []string{`"status":"FAILED"`, `"error_code"`, "MODEL_CALL_UNCERTAIN", "MODEL_CHARGE_UNCERTAIN", "MODEL_UNAVAILABLE", jobID} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("concealed ingestion body leaked %s: %s", unwanted, body)
		}
	}
}

// ingestionGET issues the authenticated public ingestion status request.
func ingestionGET(t *testing.T, s *Store, actor *auth.Identity, jobID string) (string, int) {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/cap/ai/ingestions/"+jobID, nil)
	r = r.WithContext(auth.WithIdentity(r.Context(), actor))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w.Body.String(), w.Code
}
