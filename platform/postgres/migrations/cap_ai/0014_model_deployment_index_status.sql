-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
--
-- The one narrow read the operator status view needs, and nothing else.
--
-- ADR 0011 decision 9 fixes the status contract as a read-only deployment report:
-- for each purpose the declared identity, the effective mode, the suspension state
-- and, when a purpose is unusable, the one reason that denies it — plus the
-- indexing state. The inventory for that decision found that only cap_ai's owner
-- holds table privileges in cap_ai and every runtime role holds none, so the report
-- needs one SECURITY DEFINER reader instead of a table grant. This is that reader.
--
-- It returns counts only. It never returns a document, version, chunk, title, app,
-- profile, endpoint, credential reference or any content, and it takes no actor: it
-- is a deployment view, not a per-user one. It is granted to the two restricted
-- administration logins the status command may open; no runtime role can execute
-- it. Additive: no table, function, row, grant or checksum is dropped or rewritten.

DO $$
BEGIN
  IF to_regclass('cap_ai.embedding_generations') IS NULL
    OR to_regclass('cap_ai.ingestion_jobs') IS NULL
    OR to_regprocedure('cap_ai.model_deployment_status()') IS NULL THEN
    RAISE EXCEPTION 'deployment index status requires migrations 0001, 0004 and 0013';
  END IF;
END
$$;

CREATE FUNCTION cap_ai.model_deployment_index_status()
RETURNS jsonb
LANGUAGE sql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT jsonb_build_object(
    'embedding_jobs_pending', (
        SELECT count(*) FROM cap_ai.ingestion_jobs
        WHERE kind = 'EMBED' AND state IN ('PENDING', 'RETRY_WAIT')
    ),
    'embedding_jobs_leased', (
        SELECT count(*) FROM cap_ai.ingestion_jobs
        WHERE kind = 'EMBED' AND state = 'LEASED'
    ),
    'embedding_jobs_failed', (
        SELECT count(*) FROM cap_ai.ingestion_jobs
        WHERE kind = 'EMBED' AND state = 'FAILED'
    ),
    'embeddings_ready', (
        SELECT count(*) FROM cap_ai.embedding_generations WHERE state = 'READY'
    ),
    'embeddings_building', (
        SELECT count(*) FROM cap_ai.embedding_generations WHERE state = 'BUILDING'
    ),
    'embeddings_failed', (
        SELECT count(*) FROM cap_ai.embedding_generations WHERE state = 'FAILED'
    ))
$$;

ALTER FUNCTION cap_ai.model_deployment_index_status() OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.model_deployment_index_status() FROM PUBLIC;

-- Only the two restricted administration logins the status command can open. The
-- deployment and gateway/worker/API logins are deliberately absent.
GRANT EXECUTE ON FUNCTION cap_ai.model_deployment_index_status() TO dxos_ai_model_catalog, dxos_ai_model_admin;

-- The retired slot lookup is no longer a runtime interface. 0013 stopped consulting
-- it and revoked the superseded slot-bound policy interfaces; this closes the
-- remaining read grants 0005 gave the two runtime logins, so no gateway, worker or
-- API session can resolve a slot at all. The function and its history are kept
-- (T9 removes the obsolete interface together with its table): this is a grant
-- withdrawal, not a data or definition change, and PUBLIC already holds no execute
-- right on it.
DO $$
DECLARE
  r record;
BEGIN
  IF to_regprocedure('cap_ai.registry_resolve_slot(text)') IS NULL THEN
    RETURN;
  END IF;
  FOR r IN SELECT rolname FROM pg_roles
    WHERE rolname IN ('dxos_ai_gateway', 'dxos_ai_worker', 'dxos_ai_api') LOOP
    EXECUTE format('REVOKE ALL ON FUNCTION cap_ai.registry_resolve_slot(text) FROM %I', r.rolname);
  END LOOP;
END
$$;
