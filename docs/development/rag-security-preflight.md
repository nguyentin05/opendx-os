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

## Containment specification

If deployed/exposed, prepare a separate hotfix that retires keyword lookup with fixed HTTP 503 and no content until policy-backed RAG is available. Adding JWT verification alone is insufficient. Keep `/healthz` available. Do not expose raw filesystem errors, excerpts or paths.

Before implementation, add `TestLegacyRAGUnavailable` in `capabilities/cmd/cap-ai/main_test.go`; assert guessed app names, traversal, authenticated and unauthenticated requests cannot invoke lookup or disclose content. Run `(cd capabilities && go test ./cmd/cap-ai -run TestLegacyRAGUnavailable -v)` RED then GREEN; verify gateway and direct-service boundaries against the deployed revision. No hotfix, deployment or containment runtime test was executed in P0.
