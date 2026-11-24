//go:build integration

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors

package modelprovisioning

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/opendx-os/opendx-os/capabilities/internal/ai/models"
	"github.com/opendx-os/opendx-os/capabilities/internal/auth"
	"github.com/opendx-os/opendx-os/capabilities/internal/testutil"
)

// The administration surface is exercised only through the deployment logins the
// migration created: session authority is part of the interface under test, so no
// fixture calls a management function as a superuser.
const (
	provisionOperator = "11111111-1111-4111-8111-111111111111"
	provisionPlain    = "22222222-2222-4222-8222-222222222222"
	provisionInactive = "33333333-3333-4333-8333-333333333333"
	provisionRevoked  = "44444444-4444-4444-8444-444444444444"

	provisionApp      = "provisioning"
	provisionGenerate = "provision-gen"
	provisionEmbed    = "provision-embed"
	provisionProvider = "test"

	provisionStateSQL = `SELECT jsonb_build_object(
  'slots',(SELECT jsonb_agg(jsonb_build_object('capability',capability,'profile_id',profile_id,'profile_revision',profile_revision,'slot_revision',slot_revision) ORDER BY capability) FROM cap_ai.global_model_slots),
  'events',(SELECT coalesce(jsonb_agg(jsonb_build_object('capability',capability,'slot_revision',slot_revision,'profile_id',profile_id,'profile_revision',profile_revision,'action',action,'review',review) ORDER BY capability,slot_revision),'[]'::jsonb) FROM cap_ai.model_slot_events),
  'profiles',(SELECT coalesce(jsonb_agg(jsonb_build_object('id',id,'revision',revision,'purpose',purpose,'execution',execution,'enabled',enabled) ORDER BY id,revision),'[]'::jsonb) FROM cap_ai.model_profiles))::text`

	provisionApprovalSQL = `SELECT jsonb_build_object(
  'approvals',(SELECT coalesce(jsonb_agg(jsonb_build_object('id',id,'enabled',enabled,'approved_by',approved_by,'approved_session',approved_session,'review_digest',encode(review_digest,'hex')) ORDER BY id),'[]'::jsonb) FROM cap_ai.agent_processing_approvals),
  'events',(SELECT coalesce(jsonb_agg(jsonb_build_object('approval_id',approval_id,'action',action,'session_identity',session_identity,'actor_id',actor_id,'reason',reason) ORDER BY occurred_at,action),'[]'::jsonb) FROM cap_ai.agent_processing_approval_events))::text`
)

// integrationVerifier maps one token to the subject it authenticates. The real
// verifier is exercised at the admin boundary in the unit tests; here the point is
// the database's independent authority check for each subject.
type integrationVerifier map[string]auth.Subject

func (v integrationVerifier) VerifySubject(_ context.Context, token string) (auth.Subject, error) {
	subject, ok := v[token]
	if !ok {
		return auth.Subject{}, errors.New("unknown token")
	}
	return subject, nil
}

// provisionCatalog declares the two disabled HOSTED candidates the fixture
// imports: one generation and one embedding profile.
func provisionCatalog() string {
	limits := `{"max_input_tokens":1000,"max_output_tokens":100,"max_bytes":1024,"timeout_ms":1000}`
	return `{"schema_version":2,"providers":[{"id":"` + provisionProvider +
		`","adapter":"bounded-http-v1","endpoint":"https://cap-ai-model-adapter:8443/v1/routes/provision","credential_ref":"PROVISION_TEST_KEY"}],` +
		`"models":[` +
		`{"adapter":"bounded-http-v1","profile":{"id":"` + provisionGenerate + `","revision":1,"purpose":"GENERATE","execution":"HOSTED","provider_id":"` + provisionProvider + `","model_id":"model-gen","credential_ref":"PROVISION_TEST_KEY","limits":` + limits + `}},` +
		`{"adapter":"bounded-http-v1","profile":{"id":"` + provisionEmbed + `","revision":1,"purpose":"EMBED","execution":"HOSTED","provider_id":"` + provisionProvider + `","model_id":"model-embed","credential_ref":"PROVISION_TEST_KEY","dimension":8,"limits":` + limits + `}}]}`
}

// provisioningReview is the exact review a promotion of the named revision needs:
// the stored digest, the Task 4 probe report as its qualification and a separate
// human attestation reference.
func provisioningReview(profile string, revision int64, digest, purpose string, dimension int) []byte {
	qualification := fmt.Sprintf(`{"profile_id":%q,"revision":%d,"declaration_digest":%q,"purpose":%q,"passed":true,"checked_at":"2026-11-24T10:00:00Z"`,
		profile, revision, digest, purpose)
	if purpose == "EMBED" {
		qualification += fmt.Sprintf(`,"dimension":%d`, dimension)
	} else {
		qualification += `,"citation_ids":["E1"]`
	}
	qualification += `,"usage":{"input_tokens":10,"output_tokens":0}}`
	return []byte(fmt.Sprintf(`{"declaration_digest":%q,"qualification":%s,"attestation_ref":"review/2026-11-24/model-board-17"}`,
		digest, qualification))
}

// provisioningApprovalReview is the exact PERSONAL review the 0008 function
// accepts; the function re-validates every field against current authority.
func provisioningApprovalReview() []byte {
	return []byte(`{"profile_id":"` + provisionGenerate + `","profile_revision":1,"provider_id":"` + provisionProvider +
		`","purpose":"GENERATE","source_policy_id":"local-only","source_policy_version":1,"source_classification":"PERSONAL",` +
		`"query_app_id":"` + provisionApp + `","query_classification":"PERSONAL",` +
		`"retention_evidence":"review/retention/30-days","training_use_evidence":"review/no-training","region_evidence":"review/region-eu"}`)
}

// provisioningFixture builds a disposable database from the platform bootstrap,
// then applies the optional model, registry, approval and administration SQL
// through the same shell bootstrap a deployment uses.
func provisioningFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db := testutil.Database(t)
	for _, path := range []string{
		"init/01_core_schema.sql", "init/02_capability_registry.sql", "init/03_app_registry.sql",
		"init/05_operator_deployments.sql", "init/06_runtime_roles.sql", "init/07_app_definitions.sql",
		"capabilities/cap_ai.sql",
	} {
		raw, err := os.ReadFile("../../../../platform/postgres/" + path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	dsn := provisioningDSN(t, db)
	out, err := provisioningBootstrap(t, dsn,
		"RAG_MODELS_SCHEMA_ENABLED=true",
		"RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true",
		"RAG_AGENT_MODELS_SCHEMA_ENABLED=true",
	)
	if err != nil {
		t.Fatalf("bootstrap: %v: %s", err, out)
	}
	if _, err = db.Exec(`INSERT INTO core.apps(app_id,display_name,version,status,spec)
	VALUES('` + provisionApp + `','Provisioning','1','Ready','{}');
INSERT INTO core.users(id,email,display_name) VALUES
 ('` + provisionOperator + `','operator@test.invalid','Operator'),
 ('` + provisionPlain + `','plain@test.invalid','Plain'),
 ('` + provisionInactive + `','inactive@test.invalid','Inactive'),
 ('` + provisionRevoked + `','revoked@test.invalid','Revoked');
INSERT INTO core.user_roles(user_id,role_name) VALUES
 ('` + provisionOperator + `','role_operator'),
 ('` + provisionInactive + `','role_operator');
UPDATE core.users SET status='DISABLED' WHERE id='` + provisionInactive + `';`); err != nil {
		t.Fatal(err)
	}
	// The candidates are declared the way a deployment declares them: through the
	// catalog login, disabled, with the digest the promotion must match.
	catalog, err := models.ParseCatalog([]byte(provisionCatalog()))
	if err != nil {
		t.Fatalf("catalog fixture: %v", err)
	}
	report, err := (&models.Registry{DB: testutil.As(t, dsn, "dxos_ai_model_catalog")}).Reconcile(context.Background(), catalog)
	if err != nil {
		t.Fatalf("candidate import: %v", err)
	}
	if report.Created != 2 {
		t.Fatalf("imported %d candidates, want 2", report.Created)
	}
	return db, dsn
}

func provisioningDSN(t *testing.T, db *sql.DB) string {
	t.Helper()
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// provisioningBootstrap is an isolated libpq environment: no inherited PG* or
// RAG_* setting leaks into the disposable fixture the runner connects to.
func provisioningBootstrap(t *testing.T, dsn string, flags ...string) (string, error) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, kv := range os.Environ() {
		name := strings.SplitN(kv, "=", 2)[0]
		if strings.HasPrefix(name, "PG") || strings.HasPrefix(name, "RAG_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "PGHOST="+u.Hostname(), "PGPORT="+u.Port(), "PGUSER="+u.User.Username(),
		"PGDATABASE="+strings.TrimPrefix(u.Path, "/"), "PGSSLMODE="+u.Query().Get("sslmode"),
		"POSTGRES_USER="+u.User.Username(), "POSTGRES_DB="+strings.TrimPrefix(u.Path, "/"))
	command := exec.Command("sh", "../../../../platform/postgres/init/09_rag_migrations.sh")
	command.Env = append(env, flags...)
	raw, err := command.CombinedOutput()
	return string(raw), err
}

func provisioningState(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	var state string
	if err := db.QueryRow(query).Scan(&state); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return state
}

func provisioningDigest(t *testing.T, db *sql.DB, profile string, revision int64) string {
	t.Helper()
	var digest string
	if err := db.QueryRow(`SELECT declaration_digest FROM cap_ai.model_candidates WHERE profile_id=$1 AND revision=$2`, profile, revision).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Fatalf("declaration digest = %q", digest)
	}
	return digest
}

func provisioningSlot(t *testing.T, db *sql.DB, capability string) (int64, sql.NullString, sql.NullInt64, bool) {
	t.Helper()
	var revision int64
	var profile sql.NullString
	var profileRevision sql.NullInt64
	var enabled bool
	err := db.QueryRow(`SELECT s.slot_revision,s.profile_id,s.profile_revision,coalesce(p.enabled,false)
	FROM cap_ai.global_model_slots s
	LEFT JOIN cap_ai.model_profiles p ON p.id=s.profile_id AND p.revision=s.profile_revision
	WHERE s.capability=$1`, capability).Scan(&revision, &profile, &profileRevision, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	return revision, profile, profileRevision, enabled
}

func provisioningEventReviews(t *testing.T, db *sql.DB, capability string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT coalesce(review::text,'') FROM cap_ai.model_slot_events WHERE capability=$1 ORDER BY slot_revision`, capability)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var reviews []string
	for rows.Next() {
		var review string
		if err = rows.Scan(&review); err != nil {
			t.Fatal(err)
		}
		reviews = append(reviews, review)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return reviews
}

// TestProvisioningAdminIntegration drives every Admin method against a real
// database: the logins, the SQL functions, their authority checks and their audit
// rows are the interface under test.
func TestProvisioningAdminIntegration(t *testing.T) {
	db, dsn := provisioningFixture(t)
	ctx := context.Background()
	verifier := integrationVerifier{
		"operator": {UserID: provisionOperator, ExpiresAt: time.Now().Add(time.Hour)},
		"plain":    {UserID: provisionPlain, ExpiresAt: time.Now().Add(time.Hour)},
		"inactive": {UserID: provisionInactive, ExpiresAt: time.Now().Add(time.Hour)},
		"revoked":  {UserID: provisionRevoked, ExpiresAt: time.Now().Add(time.Hour)},
	}
	admin := &Admin{DB: testutil.As(t, dsn, "dxos_ai_model_admin"), Verifier: verifier, Now: time.Now}
	approvals := &Admin{DB: testutil.As(t, dsn, "dxos_authz"), Verifier: verifier, Now: time.Now}

	var listing []struct {
		ProfileID         string `json:"profile_id"`
		Revision          int64  `json:"revision"`
		Purpose           string `json:"purpose"`
		Execution         string `json:"execution"`
		ProviderID        string `json:"provider_id"`
		Enabled           bool   `json:"enabled"`
		AdapterKey        string `json:"adapter_key"`
		DeclarationDigest string `json:"declaration_digest"`
	}

	t.Run("read-only commands", func(t *testing.T) {
		before := provisioningState(t, db, provisionStateSQL)
		beforeApprovals := provisioningState(t, db, provisionApprovalSQL)

		page, err := admin.List(ctx, "operator", "", 0, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if err = json.Unmarshal(page, &listing); err != nil {
			t.Fatalf("listing %s: %v", page, err)
		}
		if len(listing) != 2 {
			t.Fatalf("listing = %s", page)
		}
		for _, entry := range listing {
			if entry.Execution != "HOSTED" || entry.AdapterKey != "bounded-http-v1" || entry.DeclarationDigest == "" {
				t.Fatalf("listing entry = %+v", entry)
			}
		}
		for _, forbidden := range []string{"credential_ref", "endpoint", "PROVISION_TEST_KEY", "https://"} {
			if strings.Contains(string(page), forbidden) {
				t.Fatalf("listing leaks %q: %s", forbidden, page)
			}
		}

		// The page size is bounded: the client rejects an out-of-range page before
		// the call, and the function rejects one behind it.
		if _, err = admin.List(ctx, "operator", "", 0, 101); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit 101 = %v, want invalid", err)
		}
		var serverRejects bool
		if err = db.QueryRow(`SELECT cap_ai.model_admin_list($1,NULL,NULL,NULL,NULL,101)`, provisionOperator).Scan(&serverRejects); err == nil {
			t.Fatal("the listing function accepted an out-of-range page")
		}

		first, err := admin.List(ctx, "operator", "", 0, 1)
		if err != nil {
			t.Fatalf("first page: %v", err)
		}
		var firstPage []struct {
			ProfileID string `json:"profile_id"`
			Revision  int64  `json:"revision"`
		}
		if err = json.Unmarshal(first, &firstPage); err != nil || len(firstPage) != 1 {
			t.Fatalf("first page = %s (%v)", first, err)
		}
		next, err := admin.List(ctx, "operator", firstPage[0].ProfileID, firstPage[0].Revision, 1)
		if err != nil {
			t.Fatalf("next page: %v", err)
		}
		var nextPage []struct {
			ProfileID string `json:"profile_id"`
		}
		if err = json.Unmarshal(next, &nextPage); err != nil || len(nextPage) != 1 || nextPage[0].ProfileID == firstPage[0].ProfileID {
			t.Fatalf("next page = %s (%v)", next, err)
		}

		inspect, err := admin.Inspect(ctx, "operator", provisionGenerate, 1)
		if err != nil {
			t.Fatalf("inspect: %v", err)
		}
		var inspected []struct {
			ProfileID string `json:"profile_id"`
			Revision  int64  `json:"revision"`
			Digest    string `json:"declaration_digest"`
		}
		if err = json.Unmarshal(inspect, &inspected); err != nil || len(inspected) != 1 {
			t.Fatalf("inspect = %s (%v)", inspect, err)
		}
		if inspected[0].ProfileID != provisionGenerate || inspected[0].Revision != 1 ||
			inspected[0].Digest != provisioningDigest(t, db, provisionGenerate, 1) {
			t.Fatalf("inspect = %+v", inspected[0])
		}
		missing, err := admin.Inspect(ctx, "operator", provisionGenerate, 9)
		if err != nil || string(missing) != "[]" {
			t.Fatalf("missing revision = %s (%v)", missing, err)
		}

		slots, err := admin.Slots(ctx, "operator")
		if err != nil {
			t.Fatalf("slots: %v", err)
		}
		var listed []struct {
			Capability   string  `json:"capability"`
			SlotRevision int64   `json:"slot_revision"`
			ProfileID    *string `json:"profile_id"`
			Enabled      bool    `json:"enabled"`
		}
		if err = json.Unmarshal(slots, &listed); err != nil || len(listed) != 4 {
			t.Fatalf("slots = %s (%v)", slots, err)
		}
		for _, slot := range listed {
			if slot.ProfileID != nil || slot.Enabled || slot.SlotRevision != 0 {
				t.Fatalf("fresh slot = %+v", slot)
			}
		}

		// Reading changes nothing and approves nothing.
		if after := provisioningState(t, db, provisionStateSQL); after != before {
			t.Fatalf("read-only commands changed the registry:\n%s\n%s", before, after)
		}
		if after := provisioningState(t, db, provisionApprovalSQL); after != beforeApprovals {
			t.Fatalf("read-only commands changed the approvals:\n%s\n%s", beforeApprovals, after)
		}

		for _, token := range []string{"plain", "inactive", "revoked", "unknown"} {
			if _, err = admin.List(ctx, token, "", 0, 0); !errors.Is(err, ErrDenied) {
				t.Fatalf("%s could list: %v", token, err)
			}
			if _, err = admin.Slots(ctx, token); !errors.Is(err, ErrDenied) {
				t.Fatalf("%s could read the slots: %v", token, err)
			}
		}
	})

	digest := provisioningDigest(t, db, provisionGenerate, 1)

	// The obsolete slot mutator is closed: 0013 revokes its execute grant, so no
	// administration or approval session can move live model authority through it.
	// The command reports the refusal and leaves the registry, the slots and the
	// audit trail untouched; T9 removes the interface and this command together.
	t.Run("retired slot mutator is closed", func(t *testing.T) {
		before := provisioningState(t, db, provisionStateSQL)
		beforeApprovals := provisioningState(t, db, provisionApprovalSQL)
		for name, change := range map[string]SlotChange{
			"promote": {
				Slot: "LLM", ExpectedRevision: 0, Action: "PROMOTE", ProfileID: provisionGenerate,
				Revision: 1, ReviewedDigest: digest,
				Review: provisioningReview(provisionGenerate, 1, digest, "GENERATE", 0),
			},
			"disable": {
				Slot: "LLM", ExpectedRevision: 0, Action: "DISABLE",
				Review: []byte(`{"reason":"review withdrawn"}`),
			},
		} {
			if _, err := admin.ChangeSlot(ctx, "operator", change); !errors.Is(err, ErrDenied) {
				t.Fatalf("%s: %v, want a denial", name, err)
			}
			if _, err := approvals.ChangeSlot(ctx, "operator", change); !errors.Is(err, ErrDenied) {
				t.Fatalf("approval session %s: %v, want a denial", name, err)
			}
			for _, token := range []string{"plain", "inactive", "revoked", "unknown"} {
				if _, err := admin.ChangeSlot(ctx, token, change); !errors.Is(err, ErrDenied) {
					t.Fatalf("%s could %s: %v", token, name, err)
				}
			}
		}
		if after := provisioningState(t, db, provisionStateSQL); after != before {
			t.Fatalf("a denied slot change altered the registry:\n%s\n%s", before, after)
		}
		// Nothing was written: the slot stays unbound and no audit event exists.
		if revision, profile, profileRevision, enabled := provisioningSlot(t, db, "LLM"); revision != 0 || profile.Valid || profileRevision.Valid || enabled {
			t.Fatalf("a denied slot change wrote the slot: %d %v %v %v", revision, profile, profileRevision, enabled)
		}
		if reviews := provisioningEventReviews(t, db, "LLM"); len(reviews) != 0 {
			t.Fatalf("a denied slot change appended an audit event: %v", reviews)
		}
		if after := provisioningState(t, db, provisionApprovalSQL); after != beforeApprovals {
			t.Fatalf("a denied slot change altered the approvals:\n%s\n%s", beforeApprovals, after)
		}
	})

	t.Run("processing approval", func(t *testing.T) {
		review := provisioningApprovalReview()
		before := provisioningState(t, db, provisionStateSQL)
		// Only the dxos_authz session may approve, and only for a current operator.
		if _, err := admin.Approve(ctx, "operator", review); !errors.Is(err, ErrDenied) {
			t.Fatalf("model administrator approved: %v", err)
		}
		for _, token := range []string{"plain", "inactive", "revoked", "unknown"} {
			if _, err := approvals.Approve(ctx, token, review); !errors.Is(err, ErrDenied) {
				t.Fatalf("%s could approve: %v", token, err)
			}
		}
		// An unknown field is refused by the function itself.
		bad := append([]byte(nil), review[:len(review)-1]...)
		bad = append(bad, []byte(`,"unknown":true}`)...)
		if _, err := approvals.Approve(ctx, "operator", bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown field = %v, want invalid", err)
		}

		first, err := approvals.Approve(ctx, "operator", review)
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		if len(first) != 36 {
			t.Fatalf("approval = %q", first)
		}
		// The function owns idempotence: the same reviewed bytes return the same id.
		again, err := approvals.Approve(ctx, "operator", review)
		if err != nil || again != first {
			t.Fatalf("idempotent approval = %q, %v", again, err)
		}
		var approvedBy, session string
		var enabled bool
		if err = db.QueryRow(`SELECT approved_by,approved_session,enabled FROM cap_ai.agent_processing_approvals WHERE id=$1`, first).
			Scan(&approvedBy, &session, &enabled); err != nil {
			t.Fatal(err)
		}
		if approvedBy != provisionOperator || session != "dxos_authz" || !enabled {
			t.Fatalf("approval audit = %q %q %v", approvedBy, session, enabled)
		}
		if after := provisioningState(t, db, provisionStateSQL); after != before {
			t.Fatalf("approval changed the registry:\n%s\n%s", before, after)
		}
	})

	t.Run("revoke", func(t *testing.T) {
		var approval string
		if err := db.QueryRow(`SELECT id FROM cap_ai.agent_processing_approvals WHERE enabled`).Scan(&approval); err != nil {
			t.Fatal(err)
		}
		before := provisioningState(t, db, provisionStateSQL)
		changed, err := approvals.Revoke(ctx, "operator", approval, "  review withdrawn  ")
		if err != nil || !changed {
			t.Fatalf("revoke = %v, %v", changed, err)
		}
		// Revoking twice is a definite no-op, not an error.
		if changed, err = approvals.Revoke(ctx, "operator", approval, "review withdrawn"); err != nil || changed {
			t.Fatalf("second revoke = %v, %v", changed, err)
		}
		var enabled bool
		if err = db.QueryRow(`SELECT enabled FROM cap_ai.agent_processing_approvals WHERE id=$1`, approval).Scan(&enabled); err != nil || enabled {
			t.Fatalf("revoked approval = %v, %v", enabled, err)
		}
		var actions int
		if err = db.QueryRow(`SELECT count(*) FROM cap_ai.agent_processing_approval_events WHERE approval_id=$1 AND action IN('APPROVE','DISABLE')`, approval).Scan(&actions); err != nil || actions != 2 {
			t.Fatalf("lifecycle audit = %d, %v", actions, err)
		}
		// A disabled approval cannot be re-enabled and stays withdrawn.
		if _, err = db.Exec(`UPDATE cap_ai.agent_processing_approvals SET enabled=true WHERE id=$1`, approval); err == nil {
			t.Fatal("a withdrawn approval was re-enabled")
		}
		// The slot and the profiles are a different authority and stay untouched.
		if after := provisioningState(t, db, provisionStateSQL); after != before {
			t.Fatalf("revoke changed the registry:\n%s\n%s", before, after)
		}
		for _, token := range []string{"plain", "inactive", "revoked", "unknown"} {
			if _, err := approvals.Revoke(ctx, token, approval, "review withdrawn"); !errors.Is(err, ErrDenied) {
				t.Fatalf("%s could revoke: %v", token, err)
			}
		}
		if _, err := admin.Revoke(ctx, "operator", approval, "review withdrawn"); !errors.Is(err, ErrDenied) {
			t.Fatalf("model administrator revoked: %v", err)
		}
	})
}
