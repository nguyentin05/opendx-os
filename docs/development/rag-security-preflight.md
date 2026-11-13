# Native RAG security preflight

Baseline: `feat/cap-ai`, `70271c335e58714301d502f0547d9eb76c8b05e8`, inspected 2026-11-13. Scope is read-only inspection, not deployment qualification.

| Check | Observation | Status |
| --- | --- | --- |
| `capabilities/cmd/cap-ai/main.go` | POST `/api/cap/ai/rag` calls filesystem keyword lookup without verified identity or source ACL; mux is not wrapped in auth middleware | PASS: defect confirmed by source inspection |
| `compose.yaml`, `compose.integration.yaml` | No cap-ai service published | PASS: static configuration inspection |
| `platform/apisix/apisix.yaml` | Routes to Hasura, cap-process and Keycloak; no cap-ai upstream | PASS: static configuration inspection |
| `docker compose ps --format json` | Exit 0, no service records for this Compose project | PASS: local project observation only |
| External ingress/direct service, actual deployed revision | No deployment target supplied; no remote requests made | NOT_RUN |

Absence from these files does not establish absence of exposure elsewhere. External exposure remains an unresolved deployment blocker. P0 documentation can proceed; continued operation of any exposed unscoped RAG route is not approved.

## Historical containment specification (implemented locally by P1-A)

If deployed/exposed, prepare a separate hotfix that retires keyword lookup with fixed HTTP 503 and no content until policy-backed RAG is available. Adding JWT verification alone is insufficient. Keep `/healthz` available. Do not expose raw filesystem errors, excerpts or paths.

Before implementation, add `TestLegacyRAGUnavailable` in `capabilities/cmd/cap-ai/main_test.go`; assert guessed app names, traversal, authenticated and unauthenticated requests cannot invoke lookup or disclose content. Run `(cd capabilities && go test ./cmd/cap-ai -run TestLegacyRAGUnavailable -v)` RED then GREEN; verify gateway and direct-service boundaries against the deployed revision. No hotfix, deployment or containment runtime test was executed in P0.

## P1-A local implementation evidence — 2026-11-13

The revised cap-ai mux has no AIEngine, APPS_DIR or source-store dependency. POST `/api/cap/ai/rag` now always returns a fixed 503 with code `CAP_AI_UNAVAILABLE`; it never parses app_name/query or performs filesystem keyword lookup. `/extract` verifies bearer subject/current DB role then returns the same unavailable response, rather than mock extraction. GET `/healthz` remains public liveness. Existing engine helpers are not reachable from these handlers.

Verified via `TestLegacyRAGUnavailable`, `TestExtractUnavailable`, `TestRAGAuthentication`, `TestRAGMissingDependencies` and `TestLoadConfig`: anonymous/authenticated/malformed/traversal inputs expose no private phrase, filename, object key or mocked vendor/amount. Missing/invalid auth gives 401, missing/ungranted selected role 403, DB authority unavailable 503 with fixed errors. This is in-process direct HTTP evidence; no gateway/deployed revision was exercised.

Subject tests verify RS256/JWKS issuer/audience/expiry, absent workspace/role claims, forged business claims, unavailable/expired keys and cancellation. Existing legacy-auth regressions still pass. Current membership is checked on a fresh bounded primary DB query; no source READ is inferred.

Remote exposure status remains NOT_RUN: this code change does not establish that an externally deployed legacy binary was replaced. The historical source-inspection defect above describes base 70271c3, not the new local handler.
