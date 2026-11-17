-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Optional: apply explicitly after provisioning pgvector. FTS bootstrap skips it.

DO $$
BEGIN
  IF NOT EXISTS(SELECT FROM pg_available_extensions
    WHERE name = 'vector') THEN
    RAISE EXCEPTION 'pgvector unavailable; model schema disabled';
  END IF;
  IF NOT EXISTS(SELECT FROM pg_roles
    WHERE rolname = 'dxos_ai_gateway') THEN
    CREATE ROLE dxos_ai_gateway LOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT;
  END IF;
END
$$;

CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;

ALTER TABLE cap_ai.processing_policies ADD COLUMN query_app_id text REFERENCES core.apps (
    app_id
),
ADD COLUMN provider_id text,
ADD COLUMN retention_approved boolean NOT NULL DEFAULT false;

CREATE TABLE cap_ai.model_profiles (
    id text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    purpose text NOT NULL CHECK (
        purpose IN ('EMBED', 'RERANK', 'GENERATE', 'OCR')
    ),
    execution text NOT NULL CHECK (execution IN ('LOCAL', 'HOSTED')),
    provider_id text NOT NULL,
    model_id text NOT NULL,
    dimension integer,
    credential_ref text,
    limits jsonb NOT NULL CHECK (
        limits
        ?& ARRAY[
            'max_input_tokens',
            'max_output_tokens',
            'max_bytes',
            'input_unit_price',
            'output_unit_price',
            'timeout_ms'
        ]
        AND jsonb_typeof(limits) = 'object'
        AND (limits ->> 'max_input_tokens')::integer > 0
        AND (limits ->> 'max_output_tokens')::integer > 0
        AND (limits ->> 'max_bytes')::integer BETWEEN 1 AND 1048576
        AND (limits ->> 'input_unit_price')::bigint >= 0
        AND (limits ->> 'output_unit_price')::bigint >= 0
        AND (limits ->> 'timeout_ms')::integer BETWEEN 1 AND 120000
    ),
    pricing_revision text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, revision),
    CHECK (
        (purpose = 'EMBED' AND dimension BETWEEN 1 AND 16000)
        OR (purpose <> 'EMBED' AND dimension IS null)
    )
);

CREATE TABLE cap_ai.processing_policy_profiles (
    policy_id text NOT NULL,
    policy_version bigint NOT NULL,
    model_profile_id text NOT NULL,
    profile_revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (policy_id, policy_version, model_profile_id, profile_revision),
    FOREIGN KEY (
        policy_id, policy_version
    ) REFERENCES cap_ai.processing_policies (id, version),
    FOREIGN KEY (
        model_profile_id, profile_revision
    ) REFERENCES cap_ai.model_profiles (id, revision)
);

CREATE TABLE cap_ai.embedding_generations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES cap_ai.processing_runs (id),
    model_profile_id text NOT NULL,
    profile_revision bigint NOT NULL,
    dimension integer NOT NULL CHECK (dimension > 0),
    state text NOT NULL DEFAULT 'BUILDING' CHECK (
        state IN ('BUILDING', 'READY', 'FAILED', 'RETIRED')
    ),
    expected_count integer NOT NULL CHECK (expected_count > 0),
    completed_count integer NOT NULL DEFAULT 0 CHECK (
        completed_count >= 0 AND completed_count <= expected_count
    ),
    job_id uuid REFERENCES cap_ai.ingestion_jobs (id),
    fencing_token bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, model_profile_id, profile_revision),
    UNIQUE (id, run_id),
    FOREIGN KEY (
        model_profile_id, profile_revision
    ) REFERENCES cap_ai.model_profiles (id, revision),
    CHECK (state <> 'READY' OR completed_count = expected_count)
);

CREATE TABLE cap_ai.chunk_embeddings (
    generation_id uuid NOT NULL,
    run_id uuid NOT NULL,
    chunk_id uuid NOT NULL,
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    embedding public.vector NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (generation_id, chunk_id),
    FOREIGN KEY (
        generation_id, run_id
    ) REFERENCES cap_ai.embedding_generations (id, run_id),
    FOREIGN KEY (run_id, chunk_id) REFERENCES cap_ai.knowledge_chunks (
        run_id, id
    )
);

CREATE TABLE cap_ai.budget_accounts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scope text NOT NULL CHECK (scope IN ('INSTALLATION', 'APP', 'USER')),
    scope_key text NOT NULL,
    period_start timestamptz NOT NULL,
    period_end timestamptz NOT NULL,
    limit_units bigint NOT NULL CHECK (limit_units >= 0),
    reserved_units bigint NOT NULL DEFAULT 0 CHECK (reserved_units >= 0),
    spent_units bigint NOT NULL DEFAULT 0 CHECK (spent_units >= 0),
    pricing_revision text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (scope, scope_key, period_start),
    CHECK (period_end > period_start)
);

CREATE TABLE cap_ai.model_reservations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    operation_key text NOT NULL UNIQUE CHECK (
        length(operation_key) BETWEEN 1 AND 200
    ),
    actor_id uuid REFERENCES core.users (id),
    app_id text NOT NULL REFERENCES core.apps (app_id),
    profile_id text NOT NULL,
    profile_revision bigint NOT NULL,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    state text NOT NULL DEFAULT 'RESERVED' CHECK (
        state IN ('RESERVED', 'IN_FLIGHT', 'UNCERTAIN', 'SETTLED', 'CANCELLED')
    ),
    expires_at timestamptz NOT NULL,
    estimated_units bigint NOT NULL CHECK (estimated_units > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (
        profile_id, profile_revision
    ) REFERENCES cap_ai.model_profiles (id, revision)
);

CREATE TABLE cap_ai.reservation_allocations (
    reservation_id uuid NOT NULL REFERENCES cap_ai.model_reservations (id),
    budget_account_id uuid NOT NULL REFERENCES cap_ai.budget_accounts (id),
    reserved_units bigint NOT NULL CHECK (reserved_units > 0),
    settled_units bigint CHECK (settled_units >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (reservation_id, budget_account_id)
);

CREATE TABLE cap_ai.model_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id uuid NOT NULL REFERENCES cap_ai.model_reservations (id),
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    provider_request_id text,
    state text NOT NULL DEFAULT 'PREPARED' CHECK (
        state IN ('PREPARED', 'SENT', 'SUCCEEDED', 'FAILED', 'UNCERTAIN')
    ),
    input_tokens bigint CHECK (input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens >= 0),
    latency_ms bigint CHECK (latency_ms >= 0),
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (reservation_id, attempt_no)
);

CREATE TABLE cap_ai.usage_ledger (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    reservation_id uuid NOT NULL REFERENCES cap_ai.model_reservations (id),
    call_id uuid REFERENCES cap_ai.model_calls (id),
    account_id uuid NOT NULL REFERENCES cap_ai.budget_accounts (id),
    event_key text NOT NULL UNIQUE,
    kind text NOT NULL CHECK (
        kind IN ('RESERVE', 'RELEASE', 'CHARGE', 'ADJUST')
    ),
    units bigint NOT NULL CHECK (units >= 0),
    pricing_revision text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION cap_ai.guard_model_history()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, cap_ai, public
AS $$
BEGIN
  IF TG_OP = 'DELETE' OR TG_TABLE_NAME IN('usage_ledger', 'processing_policy_profiles') OR(to_jsonb(NEW) - 'enabled') IS DISTINCT FROM(to_jsonb(OLD) - 'enabled')
    THEN
    RAISE EXCEPTION 'immutable model policy or history';
  END IF;
  RETURN NEW;
END
$$;

CREATE TRIGGER profile_history BEFORE UPDATE OR DELETE ON cap_ai.model_profiles FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_model_history();

CREATE TRIGGER policy_history BEFORE UPDATE OR DELETE ON cap_ai.processing_policies FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_model_history();

CREATE TRIGGER policy_profile_history BEFORE UPDATE OR DELETE ON cap_ai.processing_policy_profiles FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_model_history();

CREATE TRIGGER ledger_history BEFORE UPDATE OR DELETE ON cap_ai.usage_ledger FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_model_history();

CREATE FUNCTION cap_ai.model_policy(
    actor uuid,
    acting_role text,
    app text,
    scope_kind text,
    profile text,
    profile_rev bigint,
    sources jsonb
)
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  mp cap_ai.model_profiles;
  s jsonb;
  current_scope jsonb;
  d cap_ai.knowledge_documents;
  p cap_ai.processing_policies;
  personal boolean := scope_kind = 'PERSONAL';
  revision bigint := 1;
  classification text := 'UNKNOWN';
BEGIN
  IF jsonb_typeof(sources) <> 'array' OR jsonb_array_length(sources) > 50 OR scope_kind NOT IN('PERSONAL', 'APP')
    THEN
    RETURN '{}'::jsonb;
  END IF;
  IF NOT EXISTS(SELECT FROM core.users u
    JOIN core.user_roles ur ON ur.user_id = u.id
    JOIN core.roles r ON r.name = ur.role_name
    JOIN core.apps a ON a.app_id = r.app_id
    WHERE u.id = actor AND u.status = 'ACTIVE' AND ur.role_name = acting_role AND a.status = 'Ready' AND(scope_kind = 'PERSONAL'
    OR r.app_id = app)) THEN
    RETURN '{}'::jsonb;
  END IF;
  SELECT * INTO mp
    FROM cap_ai.model_profiles
    WHERE id = profile AND model_profiles.revision = profile_rev AND enabled;
  IF NOT FOUND THEN
    RETURN '{}'::jsonb;
  END IF;
  -- Query classification requires its own reviewed app-bound policy, independently of documents.
  SELECT pp.classification INTO classification
    FROM cap_ai.processing_policies pp
    JOIN cap_ai.processing_policy_profiles x ON x.policy_id = pp.id AND x.policy_version = pp.version
    WHERE pp.query_app_id = app AND pp.enabled AND pp.classification = 'NON_PERSONAL' AND pp.execution = 'APPROVED_PROVIDER'
    AND pp.approved_by IS NOT NULL AND pp.approved_at IS NOT NULL AND pp.retention_approved AND pp.provider_id = mp.provider_id
    AND x.model_profile_id = profile AND x.profile_revision = profile_rev
    LIMIT 1;
  FOR s IN SELECT value
    FROM jsonb_array_elements(sources) LOOP
    current_scope := cap_ai.read_document(actor, acting_role, (s->>'document_id')::uuid);
    IF current_scope IS NULL OR current_scope <> s THEN
      RETURN '{}'::jsonb;
    END IF;
    SELECT doc.* INTO STRICT d
      FROM cap_ai.knowledge_documents doc
      JOIN cap_ai.knowledge_collections c ON c.id = doc.collection_id
      WHERE doc.id = (s->>'document_id')::uuid AND(scope_kind = 'PERSONAL' OR c.app_id = app);
    personal := personal OR d.access_mode = 'PRIVATE' OR d.owner_user_id IS NOT NULL;
    SELECT * INTO STRICT p
      FROM cap_ai.processing_policies
      WHERE id = d.processing_policy_id AND version = d.processing_policy_version AND enabled;
    revision := greatest(revision, d.policy_revision);
    IF NOT EXISTS(SELECT FROM cap_ai.processing_policy_profiles
      WHERE policy_id = p.id AND policy_version = p.version AND model_profile_id = profile AND profile_revision = profile_rev)
      THEN
      RETURN '{}'::jsonb;
    END IF;
    IF mp.execution = 'HOSTED' AND(personal OR p.execution <> 'APPROVED_PROVIDER' OR p.classification <> 'NON_PERSONAL'
      OR p.approved_by IS NULL OR p.approved_at IS NULL OR NOT p.retention_approved OR p.provider_id IS DISTINCT FROM mp.provider_id)
      THEN
      RETURN '{}'::jsonb;
    END IF;
  END LOOP;
  RETURN jsonb_build_object('allowed', true, 'profile', to_jsonb(mp), 'personal', personal, 'policy_revision', revision, 'query_classification', coalesce(classification, 'UNKNOWN'));
END
$$;

DO $$
DECLARE
  r record;
BEGIN
  FOR r IN SELECT tablename
    FROM pg_tables
    WHERE schemaname = 'cap_ai' AND tablename IN('model_profiles', 'processing_policy_profiles', 'embedding_generations', 'chunk_embeddings', 'budget_accounts', 'model_reservations', 'reservation_allocations', 'model_calls', 'usage_ledger') LOOP
    EXECUTE format('ALTER TABLE cap_ai.%I OWNER TO dxos_ai_owner', r.tablename);
  END LOOP;
END
$$;

ALTER FUNCTION cap_ai.model_policy(
    uuid, text, text, text, text, bigint, jsonb
) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.model_policy(
    uuid, text, text, text, text, bigint, jsonb
),
cap_ai.guard_model_history()
FROM public;

GRANT USAGE ON SCHEMA cap_ai TO dxos_ai_gateway;

GRANT EXECUTE ON FUNCTION cap_ai.model_policy(
    uuid, text, text, text, text, bigint, jsonb
) TO dxos_ai_gateway,
dxos_ai_api,
dxos_ai_worker;

CREATE FUNCTION cap_ai.reserve_model(req jsonb)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  r cap_ai.model_reservations;
  a cap_ai.budget_accounts;
  n integer := 0;
  estimate bigint := (req->>'estimate')::bigint;
BEGIN
  IF estimate < 1 OR req->>'pricing_revision' IS NULL OR req->>'operation_key' IS NULL THEN
    RAISE EXCEPTION 'invalid reservation' USING ERRCODE = 'P0409';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended(req->>'operation_key', 719));
  SELECT * INTO r
    FROM cap_ai.model_reservations
    WHERE operation_key = req->>'operation_key';
  IF FOUND THEN
    IF r.actor_id IS DISTINCT FROM(req->>'actor_id')::uuid OR r.app_id <> req->>'app_id' OR r.profile_id <> req->>'profile_id'
      OR r.profile_revision <> (req->>'profile_revision')::bigint OR r.estimated_units <> estimate OR r.fingerprint <> req->>'fingerprint'
      THEN
      RAISE EXCEPTION 'operation conflict' USING ERRCODE = 'P0409';
    END IF;
    RETURN jsonb_build_object('id', r.id, 'state', r.state);
  END IF;
  IF NOT EXISTS(SELECT FROM cap_ai.model_profiles
    WHERE id = req->>'profile_id' AND revision = (req->>'profile_revision')::bigint AND enabled AND pricing_revision = req->>'pricing_revision')
    THEN
    RAISE EXCEPTION 'profile unavailable' USING ERRCODE = 'P0403';
  END IF;
  -- Lock every applicable original-period account in UUID order, before checking
  -- any limit. Missing or overlapping accounts deny; no implicit unlimited scope.
  FOR a IN SELECT *
    FROM cap_ai.budget_accounts
    WHERE period_start <= now() AND period_end > now() AND((scope = 'INSTALLATION' AND scope_key = 'installation')
    OR(scope = 'APP' AND scope_key = req->>'app_id') OR(scope = 'USER' AND scope_key = req->>'actor_id'))
    ORDER BY id FOR UPDATE LOOP
    n := n + 1;
    IF a.pricing_revision <> req->>'pricing_revision' OR a.limit_units - a.spent_units - a.reserved_units < estimate
      THEN
      RAISE EXCEPTION 'quota' USING ERRCODE = 'P0429';
    END IF;
  END LOOP;
  IF n <> 3 OR(SELECT count(DISTINCT scope)
    FROM cap_ai.budget_accounts
    WHERE period_start <= now() AND period_end > now() AND((scope = 'INSTALLATION' AND scope_key = 'installation')
    OR(scope = 'APP' AND scope_key = req->>'app_id') OR(scope = 'USER' AND scope_key = req->>'actor_id'))) <> 3
    THEN
    RAISE EXCEPTION 'budget accounts unavailable' USING ERRCODE = 'P0429';
  END IF;
  INSERT INTO cap_ai.model_reservations(operation_key, actor_id, app_id, profile_id, profile_revision, fingerprint, expires_at, estimated_units)
    VALUES(req->>'operation_key', (req->>'actor_id')::uuid, req->>'app_id', req->>'profile_id', (req->>'profile_revision')::bigint, req->>'fingerprint', now() + interval '5 minutes', estimate)
    RETURNING * INTO r;
  FOR a IN SELECT *
    FROM cap_ai.budget_accounts
    WHERE period_start <= now() AND period_end > now() AND((scope = 'INSTALLATION' AND scope_key = 'installation')
    OR(scope = 'APP' AND scope_key = req->>'app_id') OR(scope = 'USER' AND scope_key = req->>'actor_id'))
    ORDER BY id LOOP
    UPDATE cap_ai.budget_accounts SET reserved_units = reserved_units + estimate, updated_at = now()
      WHERE id = a.id;
    INSERT INTO cap_ai.reservation_allocations(reservation_id, budget_account_id, reserved_units)
      VALUES(r.id, a.id, estimate);
    INSERT INTO cap_ai.usage_ledger(reservation_id, account_id, event_key, kind, units, pricing_revision)
      VALUES(r.id, a.id, r.id || ':reserve:' || a.id, 'RESERVE', estimate, a.pricing_revision);
  END LOOP;
  RETURN jsonb_build_object('id', r.id, 'state', r.state);
END
$$;

CREATE FUNCTION cap_ai.settle_model(reservation uuid, event text, usage jsonb)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  r cap_ai.model_reservations;
  a record;
  units bigint := (usage->>'units')::bigint;
  existing cap_ai.usage_ledger;
BEGIN
  IF units IS NULL OR units < 0 OR event IS NULL OR length(event) NOT BETWEEN 1 AND 200 THEN
    RAISE EXCEPTION 'invalid settlement' USING ERRCODE = 'P0409';
  END IF;
  SELECT * INTO STRICT r
    FROM cap_ai.model_reservations
    WHERE id = reservation FOR UPDATE;
  IF r.state = 'CANCELLED' THEN
    RAISE EXCEPTION 'cancelled reservation' USING ERRCODE = 'P0409';
  END IF;
  IF r.state = 'SETTLED' THEN
    SELECT * INTO existing
      FROM cap_ai.usage_ledger
      WHERE reservation_id = r.id AND kind = 'CHARGE'
      ORDER BY account_id
      LIMIT 1;
    IF NOT FOUND OR existing.units <> units OR existing.pricing_revision <> usage->>'pricing_revision' OR existing.event_key <> r.id || ':' || event || ':' || existing.account_id
      THEN
      RAISE EXCEPTION 'settlement conflict' USING ERRCODE = 'P0409';
    END IF;
    RETURN true;
  END IF;
  FOR a IN SELECT ba.*, ra.reserved_units allocation
    FROM cap_ai.budget_accounts ba
    JOIN cap_ai.reservation_allocations ra ON ra.budget_account_id = ba.id
    WHERE ra.reservation_id = r.id
    ORDER BY ba.id FOR UPDATE OF ba LOOP
    IF a.pricing_revision IS DISTINCT FROM usage->>'pricing_revision' THEN
      RAISE EXCEPTION 'pricing conflict' USING ERRCODE = 'P0409';
    END IF;
    -- Actual reconciliation may reveal overage: record the debt in the original
    -- period; future reservations deny. Never discard a possibly billed charge.
    UPDATE cap_ai.budget_accounts SET reserved_units = reserved_units - a.allocation, spent_units = spent_units + units, updated_at = now()
      WHERE id = a.id;
    UPDATE cap_ai.reservation_allocations SET settled_units = units
      WHERE reservation_id = r.id AND budget_account_id = a.id;
    INSERT INTO cap_ai.usage_ledger(reservation_id, account_id, event_key, kind, units, pricing_revision)
      VALUES(r.id, a.id, r.id || ':release:' || a.id, 'RELEASE', a.allocation, a.pricing_revision), (r.id, a.id, r.id || ':' || event || ':' || a.id, 'CHARGE', units, a.pricing_revision);
  END LOOP;
  UPDATE cap_ai.model_calls SET input_tokens = (usage->>'input_tokens')::bigint, output_tokens = (usage->>'output_tokens')::bigint, updated_at = now()
    WHERE reservation_id = r.id;
  UPDATE cap_ai.model_reservations SET state = 'SETTLED', updated_at = now()
    WHERE id = r.id;
  RETURN true;
END
$$;

CREATE FUNCTION cap_ai.begin_model_call(reservation uuid)
RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  r cap_ai.model_reservations;
  call uuid;
BEGIN
  SELECT * INTO STRICT r
    FROM cap_ai.model_reservations
    WHERE id = reservation FOR UPDATE;
  IF r.state <> 'RESERVED' OR r.expires_at <= now() THEN
    RETURN '';
  END IF;
  INSERT INTO cap_ai.model_calls(reservation_id, attempt_no, state)
    VALUES(r.id, 1, 'SENT')
    RETURNING id INTO call;
  UPDATE cap_ai.model_reservations SET state = 'IN_FLIGHT', updated_at = now()
    WHERE id = r.id;
  RETURN call::text;
END
$$;

CREATE FUNCTION cap_ai.finish_model_call(
    reservation uuid, outcome text, provider_request text
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  r cap_ai.model_reservations;
  a record;
BEGIN
  SELECT * INTO STRICT r
    FROM cap_ai.model_reservations
    WHERE id = reservation FOR UPDATE;
  IF(outcome = 'CANCELLED' AND r.state = 'RESERVED') OR(outcome = 'CANCELLED_UNSENT' AND r.state = 'IN_FLIGHT')
    THEN
    UPDATE cap_ai.model_calls SET state = 'FAILED', error_code = 'CANCELLED_UNSENT', updated_at = now()
      WHERE reservation_id = r.id AND state = 'SENT';
    FOR a IN SELECT ba.*, ra.reserved_units allocation
      FROM cap_ai.budget_accounts ba
      JOIN cap_ai.reservation_allocations ra ON ra.budget_account_id = ba.id
      WHERE ra.reservation_id = r.id
      ORDER BY ba.id FOR UPDATE OF ba LOOP
      UPDATE cap_ai.budget_accounts SET reserved_units = reserved_units - a.allocation, updated_at = now()
        WHERE id = a.id;
      INSERT INTO cap_ai.usage_ledger(reservation_id, account_id, event_key, kind, units, pricing_revision)
        VALUES(r.id, a.id, r.id || ':cancel:' || a.id, 'RELEASE', a.allocation, a.pricing_revision);
    END LOOP;
    UPDATE cap_ai.model_reservations SET state = 'CANCELLED', updated_at = now()
      WHERE id = r.id;
    RETURN true;
  END IF;
  IF r.state NOT IN('IN_FLIGHT', 'UNCERTAIN') OR outcome NOT IN('UNCERTAIN', 'SUCCEEDED') OR length(coalesce(provider_request, '')) > 200
    THEN
    RETURN false;
  END IF;
  UPDATE cap_ai.model_calls SET latency_ms = greatest(0, (extract(epoch FROM(clock_timestamp() - created_at)) * 1000)::bigint), state = outcome, provider_request_id = nullif(provider_request, ''), error_code = CASE
    WHEN outcome = 'UNCERTAIN' THEN 'PROVIDER_UNCERTAIN' ELSE NULL END, updated_at = now()
    WHERE reservation_id = r.id AND attempt_no = 1;
  UPDATE cap_ai.model_reservations SET state = 'UNCERTAIN', updated_at = now()
    WHERE id = r.id;
  RETURN true;
END
$$;

DO $$
DECLARE
  r record;
BEGIN
  FOR r IN SELECT p.oid::regprocedure signature
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'cap_ai' AND p.proname IN('reserve_model', 'settle_model', 'begin_model_call', 'finish_model_call') LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner', r.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', r.signature);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO dxos_ai_gateway', r.signature);
  END LOOP;
END
$$;

CREATE FUNCTION cap_ai.pending_model_charges()
RETURNS TABLE (
    reservation_id uuid,
    provider_id text,
    provider_request_id text,
    pricing_revision text,
    updated_at timestamptz
)
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT r.id, p.provider_id, c.provider_request_id, p.pricing_revision, r.updated_at
  FROM cap_ai.model_reservations r
  JOIN cap_ai.model_profiles p ON p.id = r.profile_id AND p.revision = r.profile_revision
  LEFT JOIN cap_ai.model_calls c ON c.reservation_id = r.id AND c.attempt_no = 1
  WHERE r.state IN('IN_FLIGHT', 'UNCERTAIN') AND r.updated_at < now() - interval '2 minutes'
  ORDER BY r.updated_at, r.id
  LIMIT 100
$$;

ALTER FUNCTION cap_ai.pending_model_charges() OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.pending_model_charges()
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.pending_model_charges() TO dxos_ai_gateway;

-- Reading approved sources remains independent from model availability. Egress
-- is checked separately by model_policy/worker_model_policy.

CREATE OR REPLACE FUNCTION cap_ai.package_version_current(
    ver uuid, desired boolean
)
RETURNS boolean
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT EXISTS(SELECT FROM cap_ai.document_versions v
  JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
  JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
  JOIN cap_ai.package_sources s ON s.id = d.package_source_id
  JOIN cap_ai.package_source_revisions r ON r.id = v.package_revision_id AND r.package_source_id = s.id
  JOIN cap_ai.processing_policies p ON p.id = d.processing_policy_id AND p.version = d.processing_policy_version
  CROSS JOIN LATERAL cap_ai.current_package_source(c.app_id, s.source_id) current_source
  WHERE v.id = ver AND d.source_kind = 'PACKAGE' AND d.state = 'LIVE' AND c.kind = 'APP' AND c.status = 'ACTIVE'
  AND s.state = 'DECLARED' AND current_source IS NOT NULL AND s.incarnation = (current_source->>'incarnation')::uuid
  AND r.incarnation = s.incarnation AND r.safety_state = 'APPROVED' AND r.sha256 = v.sha256 AND r.media_type = v.media_type
  AND r.size_bytes = v.size_bytes AND p.enabled AND(NOT desired OR(d.desired_version_id = v.id AND v.sha256 = current_source->>'sha256'
  AND v.parser_profile = current_source->>'parser_profile')))
$$;

CREATE FUNCTION cap_ai.embedding_lease_valid(
    job uuid, worker text, fence bigint, run uuid
)
RETURNS boolean
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT EXISTS(SELECT FROM cap_ai.ingestion_jobs j
  JOIN cap_ai.document_versions v ON v.id = j.version_id
  JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
  JOIN cap_ai.processing_runs r ON r.id = v.active_run_id
  WHERE j.id = job AND j.kind = 'EMBED' AND j.state = 'LEASED' AND j.lease_owner = worker AND j.fencing_token = fence
  AND j.lease_until > statement_timestamp() AND r.id = run AND r.state = 'COMPLETE' AND d.active_version_id = v.id
  AND d.desired_version_id = v.id AND cap_ai.eligible_version(v.id))
$$;

CREATE FUNCTION cap_ai.embedding_scope(run uuid)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('document_id', d.id, 'version_id', v.id, 'run_id', r.id, 'policy_revision', d.policy_revision, 'authorization_generation', d.authorization_generation, 'source_incarnation', coalesce(ps.incarnation::text, ''), 'app_publication_generation', coalesce((cap_ai.current_package_source(c.app_id, ps.source_id)->>'publication_generation')::bigint, 0), 'processing_policy_id', d.processing_policy_id, 'processing_policy_version', d.processing_policy_version)
  FROM cap_ai.processing_runs r
  JOIN cap_ai.document_versions v ON v.id = r.version_id
  JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
  JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
  LEFT JOIN cap_ai.package_sources ps ON ps.id = d.package_source_id
  WHERE r.id = run AND d.active_version_id = v.id AND v.active_run_id = r.id AND r.state = 'COMPLETE' AND cap_ai.eligible_version(v.id)
$$;

CREATE FUNCTION cap_ai.queue_embeddings(
    actor uuid, acting_role text, profile text, rev bigint
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
BEGIN
  INSERT INTO cap_ai.ingestion_jobs(version_id, kind, profile_key) SELECT v.id, 'EMBED', profile || '@' || rev
    FROM cap_ai.document_versions v
    JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
    JOIN cap_ai.processing_policies p ON p.id = d.processing_policy_id AND p.version = d.processing_policy_version
    JOIN cap_ai.processing_policy_profiles pp ON pp.policy_id = p.id AND pp.policy_version = p.version
    JOIN cap_ai.model_profiles mp ON mp.id = pp.model_profile_id AND mp.revision = pp.profile_revision
    WHERE d.active_version_id = v.id AND d.desired_version_id = v.id AND v.active_run_id IS NOT NULL AND cap_ai.eligible_version(v.id)
    AND mp.id = profile AND mp.revision = rev AND mp.purpose = 'EMBED' AND mp.enabled AND p.enabled AND(mp.execution = 'LOCAL'
    OR(d.access_mode <> 'PRIVATE' AND p.execution = 'APPROVED_PROVIDER' AND p.classification = 'NON_PERSONAL' AND p.retention_approved
    AND p.provider_id = mp.provider_id AND p.approved_at IS NOT NULL AND p.approved_by IS NOT NULL)) AND(actor IS NULL
    OR cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL) ON CONFLICT(version_id, kind, profile_key) DO NOTHING;
  RETURN true;
END
$$;

CREATE FUNCTION cap_ai.claim_embedding(worker text)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  j cap_ai.ingestion_jobs;
  run uuid;
BEGIN
  IF NOT EXISTS(SELECT FROM core.users
    WHERE id = worker::uuid AND status = 'ACTIVE') THEN
    RAISE EXCEPTION 'worker unavailable' USING ERRCODE = 'P0404';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai.worker:' || worker, 0));
  IF(SELECT count(*)
    FROM cap_ai.ingestion_jobs
    WHERE state = 'LEASED' AND lease_owner = worker AND lease_until > statement_timestamp()) >= 2 THEN
    RETURN NULL;
  END IF;
  SELECT job.* INTO j
    FROM cap_ai.ingestion_jobs job
    JOIN cap_ai.document_versions v ON v.id = job.version_id
    JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
    WHERE job.kind = 'EMBED' AND job.attempts < job.max_attempts AND((job.state IN('PENDING', 'RETRY_WAIT') AND job.next_attempt_at <= now())
    OR(job.state = 'LEASED' AND job.lease_until <= now())) AND d.active_version_id = v.id AND d.desired_version_id = v.id
    AND cap_ai.eligible_version(v.id)
    ORDER BY job.next_attempt_at, job.id FOR UPDATE OF job SKIP LOCKED
    LIMIT 1;
  IF NOT FOUND THEN
    RETURN NULL;
  END IF;
  UPDATE cap_ai.job_attempts SET finished_at = now(), outcome = 'LEASE_LOST', error_code = 'LEASE_EXPIRED'
    WHERE job_id = j.id AND finished_at IS NULL;
  UPDATE cap_ai.ingestion_jobs SET state = 'LEASED', lease_owner = worker, lease_until = statement_timestamp() + interval '60 seconds', attempts = attempts + 1, fencing_token = fencing_token + 1, updated_at = now()
    WHERE id = j.id
    RETURNING * INTO j;
  INSERT INTO cap_ai.job_attempts(job_id, attempt_no, fencing_token, worker_id)
    VALUES(j.id, j.attempts, j.fencing_token, worker);
  SELECT active_run_id INTO run
    FROM cap_ai.document_versions
    WHERE id = j.version_id;
  RETURN jsonb_build_object('job_id', j.id, 'version_id', j.version_id, 'run_id', run, 'worker_id', worker, 'fence', j.fencing_token, 'expires_at', j.lease_until);
END
$$;

CREATE FUNCTION cap_ai.worker_model_policy(
    actor uuid,
    job uuid,
    worker text,
    fence bigint,
    run uuid,
    profile text,
    rev bigint,
    app text,
    sources jsonb
)
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  p cap_ai.processing_policies;
  mp cap_ai.model_profiles;
  d cap_ai.knowledge_documents;
  scope jsonb;
BEGIN
  IF actor::text <> worker OR NOT EXISTS(SELECT FROM core.users
    WHERE id = actor AND status = 'ACTIVE') OR NOT cap_ai.embedding_lease_valid(job, worker, fence, run) THEN
    RETURN '{}'::jsonb;
  END IF;
  scope := cap_ai.embedding_scope(run);
  IF scope IS NULL OR sources <> jsonb_build_array(scope) THEN
    RETURN '{}'::jsonb;
  END IF;
  SELECT doc.* INTO STRICT d
    FROM cap_ai.knowledge_documents doc
    JOIN cap_ai.knowledge_collections c ON c.id = doc.collection_id
    WHERE doc.id = (scope->>'document_id')::uuid AND c.app_id = app;
  SELECT * INTO STRICT p
    FROM cap_ai.processing_policies
    WHERE id = d.processing_policy_id AND version = d.processing_policy_version AND enabled;
  SELECT * INTO mp
    FROM cap_ai.model_profiles
    WHERE id = profile AND revision = rev AND enabled AND purpose = 'EMBED';
  IF NOT FOUND OR NOT EXISTS(SELECT FROM cap_ai.processing_policy_profiles
    WHERE policy_id = p.id AND policy_version = p.version AND model_profile_id = profile AND profile_revision = rev)
    OR NOT EXISTS(SELECT FROM cap_ai.ingestion_jobs
    WHERE id = job AND profile_key = profile || '@' || rev) THEN
    RETURN '{}'::jsonb;
  END IF;
  IF mp.execution = 'HOSTED' AND(d.access_mode = 'PRIVATE' OR d.owner_user_id IS NOT NULL OR p.execution <> 'APPROVED_PROVIDER'
    OR p.classification <> 'NON_PERSONAL' OR NOT p.retention_approved OR p.approved_by IS NULL OR p.approved_at IS NULL
    OR p.provider_id IS DISTINCT FROM mp.provider_id) THEN
    RETURN '{}'::jsonb;
  END IF;
  RETURN jsonb_build_object('allowed', true, 'profile', to_jsonb(mp), 'personal', d.access_mode = 'PRIVATE', 'policy_revision', d.policy_revision, 'query_classification', p.classification);
END
$$;

CREATE FUNCTION cap_ai.embedding_work(
    job uuid, worker text, fence bigint, run uuid, profile text, rev bigint
)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  g cap_ai.embedding_generations;
  scope jsonb;
  app text;
  mp cap_ai.model_profiles;
  chunk jsonb;
BEGIN
  PERFORM 1
    FROM cap_ai.ingestion_jobs
    WHERE id = job FOR UPDATE;
  scope := cap_ai.embedding_scope(run);
  SELECT c.app_id INTO app
    FROM cap_ai.knowledge_documents d
    JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
    WHERE d.id = (scope->>'document_id')::uuid;
  IF NOT coalesce((cap_ai.worker_model_policy(worker::uuid, job, worker, fence, run, profile, rev, app, jsonb_build_array(scope))->>'allowed')::boolean, false)
    THEN
    RAISE EXCEPTION 'embedding authority unavailable' USING ERRCODE = 'P0410';
  END IF;
  SELECT * INTO STRICT mp
    FROM cap_ai.model_profiles
    WHERE id = profile AND revision = rev;
  INSERT INTO cap_ai.embedding_generations(run_id, model_profile_id, profile_revision, dimension, expected_count, job_id, fencing_token) SELECT run, profile, rev, mp.dimension, count(*), job, fence
    FROM cap_ai.knowledge_chunks
    WHERE run_id = run ON CONFLICT(run_id, model_profile_id, profile_revision) DO NOTHING;
  SELECT * INTO STRICT g
    FROM cap_ai.embedding_generations
    WHERE run_id = run AND model_profile_id = profile AND profile_revision = rev FOR UPDATE;
  IF g.state IN('FAILED', 'RETIRED') THEN
    RAISE EXCEPTION 'generation unavailable' USING ERRCODE = 'P0410';
  END IF;
  IF g.state = 'BUILDING' THEN
    UPDATE cap_ai.embedding_generations SET job_id = job, fencing_token = fence, updated_at = now()
      WHERE id = g.id;
  END IF;
  SELECT jsonb_build_object('id', k.id, 'text', k.text, 'content_hash', k.content_hash) INTO chunk
    FROM cap_ai.knowledge_chunks k
    WHERE k.run_id = run AND NOT EXISTS(SELECT FROM cap_ai.chunk_embeddings e
    WHERE e.generation_id = g.id AND e.chunk_id = k.id AND e.content_hash = k.content_hash)
    ORDER BY k.ordinal
    LIMIT 1;
  RETURN jsonb_build_object('generation_id', g.id, 'app_id', app, 'scope', scope, 'chunk', chunk, 'state', g.state);
END
$$;

CREATE FUNCTION cap_ai.save_embedding(
    job uuid,
    worker text,
    fence bigint,
    run uuid,
    generation uuid,
    chunk uuid,
    hash text,
    value public.vector
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  g cap_ai.embedding_generations;
BEGIN
  PERFORM 1
    FROM cap_ai.ingestion_jobs
    WHERE id = job FOR UPDATE;
  SELECT * INTO STRICT g
    FROM cap_ai.embedding_generations
    WHERE id = generation FOR UPDATE;
  IF NOT cap_ai.embedding_lease_valid(job, worker, fence, run) OR g.run_id <> run OR g.job_id <> job OR g.fencing_token <> fence
    OR g.state <> 'BUILDING' OR public.vector_dims(value) <> g.dimension OR NOT EXISTS(SELECT FROM cap_ai.knowledge_chunks
    WHERE id = chunk AND run_id = run AND content_hash = hash) THEN
    RAISE EXCEPTION 'stale embedding' USING ERRCODE = 'P0410';
  END IF;
  INSERT INTO cap_ai.chunk_embeddings(generation_id, run_id, chunk_id, content_hash, embedding)
    VALUES(generation, run, chunk, hash, value) ON CONFLICT DO NOTHING;
  UPDATE cap_ai.embedding_generations SET completed_count = (SELECT count(*)
    FROM cap_ai.chunk_embeddings
    WHERE generation_id = g.id), updated_at = now()
    WHERE id = g.id;
  UPDATE cap_ai.ingestion_jobs SET lease_until = statement_timestamp() + interval '60 seconds', updated_at = now()
    WHERE id = job;
  RETURN true;
END
$$;

CREATE FUNCTION cap_ai.finish_embedding(
    job uuid,
    worker text,
    fence bigint,
    run uuid,
    generation uuid,
    success boolean
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  g cap_ai.embedding_generations;
BEGIN
  PERFORM 1
    FROM cap_ai.ingestion_jobs
    WHERE id = job FOR UPDATE;
  SELECT * INTO STRICT g
    FROM cap_ai.embedding_generations
    WHERE id = generation FOR UPDATE;
  IF NOT cap_ai.embedding_lease_valid(job, worker, fence, run) OR g.run_id <> run OR g.job_id <> job OR g.fencing_token <> fence
    THEN
    RAISE EXCEPTION 'stale generation' USING ERRCODE = 'P0410';
  END IF;
  IF success THEN
    IF g.completed_count <> g.expected_count OR g.expected_count <> (SELECT count(*)
      FROM cap_ai.knowledge_chunks
      WHERE run_id = run) OR EXISTS(SELECT FROM cap_ai.chunk_embeddings e
      JOIN cap_ai.knowledge_chunks k ON k.id = e.chunk_id
      WHERE e.generation_id = g.id AND(e.content_hash <> k.content_hash OR public.vector_dims(e.embedding) <> g.dimension))
      THEN
      RAISE EXCEPTION 'partial generation' USING ERRCODE = 'P0410';
    END IF;
    UPDATE cap_ai.embedding_generations SET state = 'READY', updated_at = now()
      WHERE id = g.id;
  END IF;
  UPDATE cap_ai.ingestion_jobs SET state = CASE WHEN success THEN 'SUCCEEDED' WHEN attempts >= max_attempts THEN 'FAILED'
    ELSE 'RETRY_WAIT' END, lease_owner = NULL, lease_until = NULL, next_attempt_at = now() + interval '30 seconds', error_code = CASE
    WHEN success THEN NULL ELSE 'MODEL_UNAVAILABLE' END, updated_at = now()
    WHERE id = job;
  UPDATE cap_ai.job_attempts SET finished_at = now(), outcome = CASE WHEN success THEN 'SUCCEEDED' ELSE 'MODEL_UNAVAILABLE' END
    WHERE job_id = job AND fencing_token = fence;
  RETURN true;
END
$$;

CREATE FUNCTION cap_ai.search_vectors(
    actor uuid,
    acting_role text,
    app text,
    profile text,
    rev bigint,
    query public.vector,
    collections uuid[]
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
WITH authorized AS MATERIALIZED(SELECT d.id document_id, v.id version_id, r.id run_id, g.id generation_id, cap_ai.read_document(actor, acting_role, d.id) scope
  FROM cap_ai.knowledge_documents d
  JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
  JOIN cap_ai.document_versions v ON v.id = d.active_version_id
  JOIN cap_ai.processing_runs r ON r.id = v.active_run_id
  JOIN cap_ai.embedding_generations g ON g.run_id = r.id
  JOIN cap_ai.model_profiles p ON p.id = g.model_profile_id AND p.revision = g.profile_revision
  WHERE EXISTS(SELECT FROM cap_ai.processing_policy_profiles pp JOIN cap_ai.processing_policies pol ON pol.id = pp.policy_id
  AND pol.version = pp.policy_version WHERE pol.id = d.processing_policy_id AND pol.version = d.processing_policy_version
  AND pol.enabled AND pp.model_profile_id = profile AND pp.profile_revision = rev) AND c.kind = 'APP' AND c.app_id = app
  AND d.state = 'LIVE' AND r.state = 'COMPLETE' AND g.state = 'READY' AND g.completed_count = g.expected_count AND g.model_profile_id = profile
  AND g.profile_revision = rev AND p.purpose = 'EMBED' AND p.enabled AND g.dimension = p.dimension AND g.dimension = public.vector_dims(query)
  AND cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL AND(coalesce(cardinality(collections), 0) = 0 OR c.id = ANY(collections))), ranked AS(SELECT a.*, e.chunk_id, e.embedding OPERATOR(public.<=>) query distance
  FROM authorized a
  JOIN cap_ai.chunk_embeddings e ON e.generation_id = a.generation_id AND e.run_id = a.run_id
  JOIN cap_ai.knowledge_chunks k ON k.id = e.chunk_id AND k.run_id = e.run_id AND k.content_hash = e.content_hash
  ORDER BY distance, a.document_id, e.chunk_id
  LIMIT 50) SELECT coalesce(jsonb_agg(jsonb_build_object('hit', cap_ai.citation_data(actor, acting_role, document_id, version_id, chunk_id), 'scope', scope) ORDER BY distance, document_id, chunk_id), '[]'::jsonb)
  FROM ranked
$$;

DO $$
DECLARE
  r record;
BEGIN
  FOR r IN SELECT p.oid::regprocedure signature
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'cap_ai' AND p.proname IN('embedding_lease_valid', 'embedding_scope', 'queue_embeddings', 'claim_embedding', 'worker_model_policy', 'embedding_work', 'save_embedding', 'finish_embedding', 'search_vectors') LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner', r.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', r.signature);
  END LOOP;
END
$$;

GRANT EXECUTE ON FUNCTION cap_ai.queue_embeddings(
    uuid, text, text, bigint
) TO dxos_ai_worker,
dxos_ai_api;

GRANT EXECUTE ON FUNCTION cap_ai.claim_embedding(text),
cap_ai.embedding_work(uuid, text, bigint, uuid, text, bigint),
cap_ai.save_embedding(
    uuid, text, bigint, uuid, uuid, uuid, text, public.vector
),
cap_ai.finish_embedding(
    uuid, text, bigint, uuid, uuid, boolean
) TO dxos_ai_worker;

GRANT EXECUTE ON FUNCTION cap_ai.worker_model_policy(
    uuid, uuid, text, bigint, uuid, text, bigint, text, jsonb
) TO dxos_ai_gateway;

GRANT EXECUTE ON FUNCTION cap_ai.search_vectors(
    uuid, text, text, text, bigint, public.vector, uuid[]
) TO dxos_ai_api;

CREATE FUNCTION cap_ai.model_profile(profile text, rev bigint)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT to_jsonb(p)
  FROM cap_ai.model_profiles p
  WHERE id = profile AND revision = rev AND enabled
$$;

ALTER FUNCTION cap_ai.model_profile(text, bigint) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.model_profile(text, bigint)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.model_profile(text, bigint) TO dxos_ai_gateway;

-- Embedding lease exhaustion must never fail the lexical version.

CREATE OR REPLACE FUNCTION cap_ai.recover_jobs(at_time timestamptz)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  j cap_ai.ingestion_jobs;
  terminal boolean;
BEGIN
  FOR j IN SELECT *
    FROM cap_ai.ingestion_jobs
    WHERE state = 'LEASED' AND lease_until <= at_time
    ORDER BY id FOR UPDATE SKIP LOCKED LOOP
    terminal := j.attempts >= j.max_attempts;
    UPDATE cap_ai.ingestion_jobs SET state = CASE WHEN terminal THEN 'FAILED' ELSE 'RETRY_WAIT' END, lease_owner = NULL, lease_until = NULL, error_code = CASE
      WHEN terminal THEN 'RETRY_EXHAUSTED' ELSE 'LEASE_EXPIRED' END, next_attempt_at = at_time + random() * least(300, 5 * power(2, j.attempts - 1)) * interval '1 second', updated_at = at_time
      WHERE id = j.id;
    UPDATE cap_ai.processing_runs SET state = 'ABANDONED', updated_at = at_time
      WHERE ingestion_job_id = j.id AND state = 'BUILDING';
    UPDATE cap_ai.source_read_delegations SET revoked_at = at_time
      WHERE job_id = j.id AND revoked_at IS NULL;
    UPDATE cap_ai.job_attempts SET finished_at = at_time, outcome = 'LEASE_LOST', error_code = 'LEASE_EXPIRED'
      WHERE job_id = j.id AND finished_at IS NULL;
    IF terminal AND j.kind = 'PARSE' THEN
      UPDATE cap_ai.document_versions SET state = 'FAILED', error_code = 'RETRY_EXHAUSTED', updated_at = at_time
        WHERE id = j.version_id;
    END IF;
  END LOOP;
  -- No original, history or referenced staging deletion is authorized by this operation.
  RETURN true;
END
$$;

CREATE FUNCTION cap_ai.hybrid_lexical(
    actor uuid,
    acting_role text,
    app text,
    query text,
    page_size integer,
    collections uuid[]
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
WITH authorized_versions AS MATERIALIZED(SELECT d.id document_id, v.id version_id, r.id run_id
  FROM cap_ai.knowledge_documents d
  JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
  JOIN cap_ai.document_versions v ON v.id = d.active_version_id
  JOIN cap_ai.processing_runs r ON r.id = v.active_run_id
  WHERE c.app_id = app AND d.source_kind = 'PACKAGE' AND d.state = 'LIVE' AND c.kind = 'APP' AND c.status = 'ACTIVE'
  AND v.state = 'READY' AND r.state = 'COMPLETE' AND(coalesce(cardinality(collections), 0) = 0 OR c.id = ANY(collections))
  AND cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL), candidates AS(SELECT a.*, k.id chunk_id, ts_rank_cd(k.search_vector, websearch_to_tsquery('simple', normalize(query, NFC))) score
  FROM authorized_versions a
  JOIN cap_ai.knowledge_chunks k ON k.run_id = a.run_id
  WHERE k.search_vector @@ websearch_to_tsquery('simple', normalize(query, NFC))
  ORDER BY score DESC, a.document_id, k.id
  LIMIT 50) SELECT coalesce(jsonb_agg(jsonb_build_object('hit', cap_ai.citation_data(actor, acting_role, document_id, version_id, chunk_id), 'scope', cap_ai.read_document(actor, acting_role, document_id)) ORDER BY score DESC, document_id, chunk_id), '[]'::jsonb)
  FROM candidates
$$;

ALTER FUNCTION cap_ai.hybrid_lexical(
    uuid, text, text, text, integer, uuid[]
) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.hybrid_lexical(
    uuid, text, text, text, integer, uuid[]
)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.hybrid_lexical(
    uuid, text, text, text, integer, uuid[]
) TO dxos_ai_api;

CREATE FUNCTION cap_ai.fail_embedding(
    job uuid,
    worker text,
    fence bigint,
    run uuid,
    generation uuid,
    uncertain boolean
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
BEGIN
  PERFORM cap_ai.finish_embedding(job, worker, fence, run, generation, false);
  IF uncertain THEN
    UPDATE cap_ai.ingestion_jobs SET state = 'FAILED', error_code = 'MODEL_CHARGE_UNCERTAIN', updated_at = now()
      WHERE id = job;
    UPDATE cap_ai.embedding_generations SET state = 'FAILED', updated_at = now()
      WHERE id = generation AND state = 'BUILDING';
  END IF;
  RETURN true;
END
$$;

ALTER FUNCTION cap_ai.fail_embedding(
    uuid, text, bigint, uuid, uuid, boolean
) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.fail_embedding(
    uuid, text, bigint, uuid, uuid, boolean
)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.fail_embedding(
    uuid, text, bigint, uuid, uuid, boolean
) TO dxos_ai_worker;
