-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Optional registry foundation. Requires optional 0004; no activation authority.

DO $$
BEGIN
  IF to_regclass('cap_ai.model_profiles') IS NULL THEN
    RAISE EXCEPTION 'model registry requires migration 0004';
  END IF;
  IF NOT EXISTS(SELECT FROM pg_roles
    WHERE rolname = 'dxos_ai_model_catalog') THEN
    CREATE ROLE dxos_ai_model_catalog LOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT;
  END IF;
END
$$;

CREATE TABLE cap_ai.model_candidates (
    profile_id text NOT NULL,
    revision bigint NOT NULL,
    adapter_key text NOT NULL CHECK (
        adapter_key ~ '^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$'
    ),
    declaration_digest text NOT NULL CHECK (
        declaration_digest ~ '^[0-9a-f]{64}$'
    ),
    declaration jsonb NOT NULL CHECK (jsonb_typeof(declaration) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (profile_id, revision),
    FOREIGN KEY (profile_id, revision) REFERENCES cap_ai.model_profiles (
        id, revision
    )
);

CREATE TABLE cap_ai.global_model_slots (
    capability text PRIMARY KEY CHECK (
        capability IN ('EMBEDDING', 'LLM', 'VLM', 'RERANKING')
    ),
    profile_id text,
    profile_revision bigint,
    slot_revision bigint NOT NULL DEFAULT 0 CHECK (slot_revision >= 0),
    FOREIGN KEY (
        profile_id, profile_revision
    ) REFERENCES cap_ai.model_profiles (id, revision) MATCH FULL,
    CHECK (capability IN ('EMBEDDING', 'LLM') OR profile_id IS NULL)
);

INSERT INTO cap_ai.global_model_slots (capability)
VALUES ('EMBEDDING'), ('LLM'), ('VLM'), ('RERANKING');

CREATE TABLE cap_ai.model_slot_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    capability text NOT NULL REFERENCES cap_ai.global_model_slots (capability),
    slot_revision bigint NOT NULL CHECK (slot_revision > 0),
    profile_id text,
    profile_revision bigint,
    actor_id uuid NOT NULL REFERENCES core.users (id),
    action text NOT NULL CHECK (action IN ('PROMOTE', 'ROLLBACK', 'DISABLE')),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (capability, slot_revision),
    FOREIGN KEY (
        profile_id, profile_revision
    ) REFERENCES cap_ai.model_profiles (id, revision) MATCH FULL
);

CREATE FUNCTION cap_ai.registry_guard_history()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
  RAISE EXCEPTION 'immutable registry history' USING ERRCODE = '23514';
END
$$;

CREATE TRIGGER candidate_history BEFORE UPDATE OR DELETE ON cap_ai.model_candidates FOR EACH ROW EXECUTE FUNCTION cap_ai.registry_guard_history();

CREATE TRIGGER slot_event_history BEFORE UPDATE OR DELETE ON cap_ai.model_slot_events FOR EACH ROW EXECUTE FUNCTION cap_ai.registry_guard_history();

CREATE FUNCTION cap_ai.registry_guard_slot()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, cap_ai
AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'registry slots cannot be deleted' USING ERRCODE = '23514';
  END IF;
  IF NEW.capability <> OLD.capability OR NEW.slot_revision <= OLD.slot_revision THEN
    RAISE EXCEPTION 'slot revision must increase' USING ERRCODE = '23514';
  END IF;
  IF NEW.profile_id IS NOT NULL AND NOT EXISTS(SELECT FROM cap_ai.model_profiles
    WHERE id = NEW.profile_id AND revision = NEW.profile_revision AND purpose = CASE NEW.capability WHEN 'EMBEDDING'
    THEN
    'EMBED' WHEN 'LLM' THEN 'GENERATE' END) THEN RAISE EXCEPTION 'slot purpose mismatch' USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER slot_revision_guard BEFORE UPDATE OR DELETE ON cap_ai.global_model_slots FOR EACH ROW EXECUTE FUNCTION cap_ai.registry_guard_slot();

CREATE FUNCTION cap_ai.registry_resolve_slot(capability text)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT coalesce((SELECT CASE WHEN p.id IS NOT NULL THEN jsonb_build_object('capability', s.capability, 'slot_revision', s.slot_revision, 'profile', to_jsonb(p), 'adapter_key', c.adapter_key, 'declaration_digest', c.declaration_digest)
  ELSE jsonb_build_object('capability', s.capability, 'slot_revision', s.slot_revision, 'status', 'MODEL_UNAVAILABLE') END FROM cap_ai.global_model_slots s LEFT JOIN cap_ai.model_profiles p
  ON p.id = s.profile_id AND p.revision = s.profile_revision AND p.enabled AND p.purpose = CASE s.capability WHEN 'EMBEDDING'
  THEN 'EMBED' WHEN 'LLM' THEN 'GENERATE' END LEFT JOIN cap_ai.model_candidates c ON c.profile_id = p.id AND c.revision = p.revision WHERE s.capability = $1), jsonb_build_object('capability', $1, 'slot_revision', 0, 'status', 'MODEL_UNAVAILABLE'))
$$;

CREATE FUNCTION cap_ai.registry_import_candidates(payload jsonb)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
DECLARE
  item jsonb;
  p jsonb;
  l jsonb;
  normalized jsonb;
  existing jsonb;
  stored cap_ai.model_candidates;
  k text;
  n integer := 0;
  created_count integer := 0;
  inserted_count integer;
BEGIN
  -- SECURITY DEFINER changes current_user: authenticate the actual deployment session.
  IF session_user <> 'dxos_ai_model_catalog' OR current_setting('role') NOT IN('none', 'dxos_ai_model_catalog')
    OR EXISTS(SELECT FROM pg_roles
    WHERE rolname = session_user AND(rolsuper OR rolbypassrls OR rolcreaterole)) THEN
    RAISE EXCEPTION 'catalog deployment session required' USING ERRCODE = '42501';
  END IF;
  IF payload IS NULL OR jsonb_typeof(payload) <> 'object' OR payload - 'candidates' <> '{}'::jsonb OR jsonb_typeof(payload->'candidates') IS DISTINCT FROM 'array'
    THEN
    RAISE EXCEPTION 'invalid candidate payload' USING ERRCODE = '22023';
  END IF;
  IF jsonb_array_length(payload->'candidates') > 100 OR octet_length(payload::text) > 1048576 THEN
    RAISE EXCEPTION 'candidate payload too large' USING ERRCODE = '22023';
  END IF;
  FOR item IN SELECT value
    FROM jsonb_array_elements(payload->'candidates') LOOP
    p := item->'profile';
    l := p->'limits';
    IF jsonb_typeof(item) IS DISTINCT FROM 'object' OR item - ARRAY['profile', 'adapter_key', 'declaration_digest'] <> '{}'::jsonb
      OR NOT item ?& ARRAY['profile', 'adapter_key', 'declaration_digest'] OR jsonb_typeof(p) IS DISTINCT FROM 'object'
      OR p - ARRAY['id', 'revision', 'purpose', 'execution', 'provider_id', 'model_id', 'dimension', 'credential_ref', 'limits', 'pricing_revision'] <> '{}'::jsonb
      OR NOT p ?& ARRAY['id', 'revision', 'purpose', 'execution', 'provider_id', 'model_id', 'limits', 'pricing_revision']
      OR jsonb_typeof(l) IS DISTINCT FROM 'object' OR l - ARRAY['max_input_tokens', 'max_output_tokens', 'max_bytes', 'input_unit_price', 'output_unit_price', 'timeout_ms'] <> '{}'::jsonb
      OR NOT l ?& ARRAY['max_input_tokens', 'max_output_tokens', 'max_bytes', 'input_unit_price', 'output_unit_price', 'timeout_ms']
      THEN
      RAISE EXCEPTION 'invalid candidate declaration' USING ERRCODE = '22023';
    END IF;
    FOREACH k IN ARRAY ARRAY['id', 'provider_id', 'model_id', 'pricing_revision'] LOOP IF jsonb_typeof(p->k) IS DISTINCT FROM 'string'
      OR length(p->>k) NOT BETWEEN 1 AND 200 OR(p->>k) !~ '^[a-zA-Z0-9][a-zA-Z0-9_.:/-]*$' THEN RAISE EXCEPTION 'invalid profile identifier'
      USING ERRCODE = '22023';
  END IF;
END LOOP;
IF jsonb_typeof(item->'adapter_key') IS DISTINCT FROM 'string' OR(item->>'adapter_key') !~ '^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$'
  OR jsonb_typeof(item->'declaration_digest') IS DISTINCT FROM 'string' OR(item->>'declaration_digest') !~ '^[0-9a-f]{64}$'
  OR p->>'execution' IS DISTINCT FROM 'HOSTED' OR(p->>'purpose') NOT IN('EMBED', 'GENERATE') OR p->>'purpose' IS NULL
  OR jsonb_typeof(p->'revision') IS DISTINCT FROM 'number' OR(p->>'revision') !~ '^[1-9][0-9]{0,17}$' OR(p ? 'credential_ref'
  AND p->'credential_ref' <> 'null'::jsonb AND(jsonb_typeof(p->'credential_ref') <> 'string' OR(p->>'credential_ref') !~ '^[A-Za-z_][A-Za-z0-9_]{0,127}$'))
  THEN
  RAISE EXCEPTION 'invalid candidate configuration' USING ERRCODE = '22023';
END IF;
IF p->>'purpose' = 'EMBED' THEN
  IF jsonb_typeof(p->'dimension') IS DISTINCT FROM 'number' OR(p->>'dimension') !~ '^[1-9][0-9]{0,4}$' OR(p->>'dimension')::integer > 16000
    THEN
    RAISE EXCEPTION 'invalid embedding dimension' USING ERRCODE = '22023';
  END IF;
ELSIF p ? 'dimension' AND p->'dimension' <> 'null'::jsonb THEN RAISE EXCEPTION 'generation dimension forbidden'
  USING ERRCODE = '22023';
END IF;
FOREACH k IN ARRAY ARRAY['max_input_tokens', 'max_output_tokens', 'max_bytes', 'input_unit_price', 'output_unit_price', 'timeout_ms'] LOOP IF jsonb_typeof(l->k) IS DISTINCT FROM 'number'
  OR(l->>k) !~ '^(0|[1-9][0-9]{0,17})$' THEN RAISE EXCEPTION 'invalid model limit' USING ERRCODE = '22023';
END IF;
END LOOP;
IF(l->>'max_input_tokens')::numeric NOT BETWEEN 1 AND 2147483647 OR(l->>'max_output_tokens')::numeric NOT BETWEEN 1
  AND 2147483647 OR(l->>'max_bytes')::numeric NOT BETWEEN 1 AND 1048576 OR(l->>'timeout_ms')::numeric NOT BETWEEN 1
  AND 120000 THEN
  RAISE EXCEPTION 'invalid model limits' USING ERRCODE = '22023';
END IF;
normalized := p || jsonb_build_object('dimension', p->'dimension', 'credential_ref', p->'credential_ref');
INSERT INTO cap_ai.model_profiles(id, revision, purpose, execution, provider_id, model_id, dimension, credential_ref, limits, pricing_revision)
  VALUES(p->>'id', (p->>'revision')::bigint, p->>'purpose', p->>'execution', p->>'provider_id', p->>'model_id', (p->>'dimension')::integer, p->>'credential_ref', l, p->>'pricing_revision')
  ON CONFLICT DO NOTHING;
SELECT to_jsonb(mp) - ARRAY['created_at', 'enabled'] INTO existing
  FROM cap_ai.model_profiles mp
  WHERE id = p->>'id' AND revision = (p->>'revision')::bigint FOR UPDATE;
IF existing IS DISTINCT FROM normalized THEN
  RAISE EXCEPTION 'immutable profile revision conflict' USING ERRCODE = '23505';
END IF;
INSERT INTO cap_ai.model_candidates(profile_id, revision, adapter_key, declaration_digest, declaration)
  VALUES(p->>'id', (p->>'revision')::bigint, item->>'adapter_key', item->>'declaration_digest', normalized) ON CONFLICT DO NOTHING;
GET DIAGNOSTICS inserted_count = ROW_COUNT;
created_count := created_count + inserted_count;
SELECT * INTO STRICT stored
  FROM cap_ai.model_candidates
  WHERE profile_id = p->>'id' AND revision = (p->>'revision')::bigint;
-- The supplied digest is metadata, never authority: compare the full declaration too.
IF stored.adapter_key <> item->>'adapter_key' OR stored.declaration_digest <> item->>'declaration_digest' OR stored.declaration IS DISTINCT FROM normalized
  THEN
  RAISE EXCEPTION 'immutable candidate revision conflict' USING ERRCODE = '23505';
END IF;
n := n + 1;
END LOOP;
RETURN jsonb_build_object('imported', n, 'created', created_count, 'unchanged', n - created_count);
END
$$;

DO $$
DECLARE
  r record;
BEGIN
  FOR r IN SELECT tablename
    FROM pg_tables
    WHERE schemaname = 'cap_ai' AND tablename IN('model_candidates', 'global_model_slots', 'model_slot_events') LOOP
    EXECUTE format('ALTER TABLE cap_ai.%I OWNER TO dxos_ai_owner', r.tablename);
    EXECUTE format('REVOKE ALL ON cap_ai.%I FROM PUBLIC,dxos_ai_gateway,dxos_ai_model_catalog,dxos_ai_api,dxos_ai_worker', r.tablename);
  END LOOP;
  FOR r IN SELECT p.oid::regprocedure signature
    FROM pg_proc p
    JOIN pg_namespace ns ON ns.oid = p.pronamespace
    WHERE ns.nspname = 'cap_ai' AND p.proname IN('registry_guard_history', 'registry_guard_slot', 'registry_resolve_slot', 'registry_import_candidates') LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner', r.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', r.signature);
  END LOOP;
END
$$;

GRANT USAGE ON SCHEMA cap_ai TO dxos_ai_model_catalog;

GRANT EXECUTE ON FUNCTION cap_ai.registry_resolve_slot(text) TO dxos_ai_gateway;

GRANT EXECUTE ON FUNCTION cap_ai.registry_import_candidates(
    jsonb
) TO dxos_ai_model_catalog;

-- Recovery resolves only the profile already pinned by an active embedding lease.
-- It cannot choose another profile or grant authority to perform a model call.

CREATE FUNCTION cap_ai.registry_embedding_lease_profile(
    job uuid, worker text, fence bigint, run uuid
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT jsonb_build_object('profile', to_jsonb(mp), 'adapter_key', mc.adapter_key, 'declaration_digest', mc.declaration_digest)
  FROM cap_ai.ingestion_jobs j
  JOIN cap_ai.model_profiles mp ON j.profile_key = mp.id || '@' || mp.revision
  JOIN cap_ai.model_candidates mc ON mc.profile_id = mp.id AND mc.revision = mp.revision
  JOIN cap_ai.document_versions v ON v.id = j.version_id
  JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
  JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
  WHERE j.id = job AND j.kind = 'EMBED' AND mp.enabled AND mp.purpose = 'EMBED' AND cap_ai.embedding_lease_valid(job, worker, fence, run)
  AND coalesce((cap_ai.worker_model_policy(worker::uuid, job, worker, fence, run, mp.id, mp.revision, c.app_id, jsonb_build_array(cap_ai.embedding_scope(run)))->>'allowed')::boolean, false)
$$;

ALTER FUNCTION cap_ai.registry_embedding_lease_profile(
    uuid, text, bigint, uuid
) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.registry_embedding_lease_profile(
    uuid, text, bigint, uuid
)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.registry_embedding_lease_profile(
    uuid, text, bigint, uuid
) TO dxos_ai_gateway;
