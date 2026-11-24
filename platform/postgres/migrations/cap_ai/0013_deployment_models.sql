-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
--
-- Deployment-bound model authority. Additive: no table, function, grant or
-- recorded history is dropped here (T9 owns removal). Applied only to a
-- model-enabled installation, after 0004/0005/0008..0012; a plain FTS
-- installation never selects this file and never needs pgvector.
--
-- Before this migration the live model authority was a pair of legacy slots
-- (cap_ai.global_model_slots) plus "any enabled profile" in model_policy. After
-- it, an exact (profile_id, profile_revision) becomes model authority only when
-- the deployment login recorded that exact profile as the current selection for
-- its purpose:
--
--   * cap_ai.model_profiles.enabled stays the live switch (at most one enabled
--     profile per purpose, enforced by a partial unique index and a
--     transaction-level advisory lock);
--   * the enabled boolean alone is *not* authority: the latest
--     cap_ai.model_deployment_events row for the purpose must be a SELECT of
--     that same immutable revision with the same recorded binding
--     (cap_ai.model_deployment_current).
--
-- Historical rows are preserved. Every currently enabled profile is moved to
-- the safe disabled state, so an upgraded installation denies model calls and
-- keeps lexical search until the deployment reconciles its exact profiles. Old
-- slot bindings, candidate declarations, approvals, embeddings, transcripts and
-- audit rows are not reinterpreted and are not deleted.

DO $$
BEGIN
  IF to_regclass('cap_ai.model_profiles') IS NULL
    OR to_regclass('cap_ai.model_candidates') IS NULL
    OR to_regclass('cap_ai.agent_processing_approvals') IS NULL
    OR to_regprocedure('cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb)') IS NULL
    OR to_regprocedure('cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint,bigint)') IS NULL
    OR to_regprocedure('cap_ai.personal_processing_approval(text,bigint,text,text,bigint)') IS NULL
    OR to_regprocedure('cap_ai.embedding_profile_key(text,bigint,jsonb)') IS NULL
    OR to_regprocedure('cap_ai.model_admin_session()') IS NULL
    OR to_regprocedure('cap_ai.model_admin_change_slot(uuid,text,bigint,text,text,bigint,text,jsonb)') IS NULL THEN
    RAISE EXCEPTION 'deployment model authority requires migrations 0004, 0005, 0008, 0009, 0010 and 0011';
  END IF;
END
$$;

-- One immutable, append-only record per selection decision. selection_revision is
-- the monotonic ordering; the latest row per purpose is the current decision and
-- the historical rows are the audit trail. A SELECT row carries the exact
-- normalized binding (never a credential reference, endpoint or content) and its
-- digest; a SUSPEND row carries the actor who withdrew the purpose.
CREATE TABLE cap_ai.model_deployment_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    selection_revision bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    purpose text NOT NULL CHECK (purpose IN ('EMBED', 'GENERATE')),
    profile_id text,
    profile_revision bigint,
    binding jsonb CHECK (
        binding IS NULL OR (
            jsonb_typeof(binding) = 'object'
            AND binding ?& ARRAY['id', 'revision', 'purpose', 'execution', 'provider_id', 'model_id', 'dimension', 'limits']
            AND binding - ARRAY['id', 'revision', 'purpose', 'execution', 'provider_id', 'model_id', 'dimension', 'limits'] = '{}'::jsonb
        )
    ),
    binding_digest text CHECK (
        binding_digest IS NULL OR binding_digest ~ '^[0-9a-f]{64}$'
    ),
    action text NOT NULL CHECK (action IN ('SELECT', 'SUSPEND')),
    actor_id uuid REFERENCES core.users (id),
    session_identity text NOT NULL CHECK (
        length(session_identity) BETWEEN 1 AND 200
    ),
    review jsonb CHECK (review IS NULL OR jsonb_typeof(review) = 'object'),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (profile_id, profile_revision)
        REFERENCES cap_ai.model_profiles (id, revision) MATCH FULL,
    CHECK ((profile_id IS NULL) = (profile_revision IS NULL)),
    CHECK (
        action <> 'SELECT' OR (
            profile_id IS NOT NULL AND binding IS NOT NULL AND binding_digest IS NOT NULL
        )
    ),
    CHECK (
        action <> 'SUSPEND' OR (profile_id IS NULL AND binding IS NULL AND binding_digest IS NULL)
    ),
    -- A human suspension always names the current administrator; the deployment
    -- login may also withdraw a purpose that its declaration omits.
    CHECK (
        action <> 'SUSPEND' OR actor_id IS NOT NULL OR session_identity = 'dxos_ai_model_catalog'
    )
);

CREATE INDEX model_deployment_events_purpose
    ON cap_ai.model_deployment_events (purpose, selection_revision DESC);

CREATE TRIGGER model_deployment_event_history
    BEFORE UPDATE OR DELETE ON cap_ai.model_deployment_events
    FOR EACH ROW EXECUTE FUNCTION cap_ai.registry_guard_history();

-- The exact immutable binding a deployment selection records. This is the same
-- key set as a model_profiles row without created_at/enabled, so a selection can
-- never be recorded for a binding the row does not carry. The credential
-- reference is deliberately excluded: it is a local deployment detail, bound in
-- the runtime by the full-profile comparison, and never archived here.
CREATE FUNCTION cap_ai.model_deployment_declaration(p cap_ai.model_profiles)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT jsonb_build_object(
    'id', p.id, 'revision', p.revision, 'purpose', p.purpose,
    'execution', p.execution, 'provider_id', p.provider_id,
    'model_id', p.model_id, 'dimension', to_jsonb(p.dimension),
    'limits', p.limits)
$$;

-- The single authority predicate: this exact immutable revision is the live
-- deployment selection for its purpose. An enabled boolean alone, a legacy slot
-- binding, a candidate import or an earlier selection never satisfies it.
CREATE FUNCTION cap_ai.model_deployment_current(p cap_ai.model_profiles)
RETURNS boolean
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT coalesce((
    SELECT p.enabled
        AND e.action = 'SELECT'
        AND e.profile_id = p.id
        AND e.profile_revision = p.revision
        AND e.binding IS NOT DISTINCT FROM cap_ai.model_deployment_declaration(p)
      FROM cap_ai.model_deployment_events e
      WHERE e.purpose = p.purpose
      ORDER BY e.selection_revision DESC
      LIMIT 1), false)
$$;

-- Coordinated normalization of historical enabled rows before the invariant is
-- enforced. Nothing is deleted: the profile history stays, and every withdrawn
-- row is simply no longer live. A purpose with no deployment selection therefore
-- denies until the deployment reconciles it.
UPDATE cap_ai.model_profiles SET enabled = false WHERE enabled;

-- At most one live profile per purpose. The reconciliation transaction holds the
-- deployment advisory lock, so a race can neither leave two enabled rows nor
-- leave the index and the audit trail disagreeing.
CREATE UNIQUE INDEX model_profiles_enabled_purpose
    ON cap_ai.model_profiles (purpose) WHERE enabled;

-- Record the exact selected deployment profiles, withdraw every other live
-- profile, and never touch an approval, policy, document or operation row.
--
-- Payload: {"profiles": [{...}]} is the *complete* installation declaration
-- (0..2 entries, one per purpose). A purpose that is absent is withdrawn: the
-- deployment switch for that purpose is off. Each entry is exactly
-- {id, revision, purpose, execution, provider_id, model_id, limits} plus an
-- optional dimension (required for EMBED, forbidden for GENERATE) and an
-- optional credential_ref. The immutable profile row is recorded if absent and
-- must match exactly if present; the routine never approves a processing policy.
CREATE FUNCTION cap_ai.reconcile_deployment_models(payload jsonb)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
#variable_conflict use_variable
DECLARE
  item jsonb;
  p jsonb;
  l jsonb;
  k text;
  normalized jsonb;
  stored jsonb;
  declared jsonb := '{}'::jsonb;
  entry jsonb;
  wanted text;
  current_profile text;
  current_revision bigint;
  latest_action text;
  latest_profile text;
  latest_revision bigint;
  selection bigint;
  created integer := 0;
  inserted integer;
  withdrawn text[] := ARRAY[]::text[];
  selections jsonb := '[]'::jsonb;
  changed boolean := false;
  purpose_changed boolean;
BEGIN
  -- SECURITY DEFINER changes current_user: authenticate the deployment session.
  IF session_user <> 'dxos_ai_model_catalog'
    OR current_setting('role') NOT IN ('none', 'dxos_ai_model_catalog')
    OR EXISTS(SELECT FROM pg_roles
      WHERE rolname = session_user AND (rolsuper OR rolbypassrls OR rolcreaterole)) THEN
    RAISE EXCEPTION 'model deployment session required' USING ERRCODE = '42501';
  END IF;
  IF payload IS NULL OR jsonb_typeof(payload) <> 'object'
    OR NOT (payload ? 'profiles') OR payload - 'profiles' <> '{}'::jsonb
    OR jsonb_typeof(payload -> 'profiles') IS DISTINCT FROM 'array'
    OR jsonb_array_length(payload -> 'profiles') > 2
    OR octet_length(payload::text) > 32768 THEN
    RAISE EXCEPTION 'invalid deployment declaration' USING ERRCODE = '22023';
  END IF;
  -- One deployment mutation at a time: reconciliation and suspension serialize
  -- on the same key, so the enabled index and the audit trail cannot disagree.
  PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai.model_deployment', 0));

  FOR item IN SELECT value FROM jsonb_array_elements(payload -> 'profiles') LOOP
    p := item;
    l := item -> 'limits';
    IF jsonb_typeof(item) IS DISTINCT FROM 'object'
      OR item - ARRAY['id', 'revision', 'purpose', 'execution', 'provider_id', 'model_id', 'dimension', 'credential_ref', 'limits'] <> '{}'::jsonb
      OR NOT (item ?& ARRAY['id', 'revision', 'purpose', 'execution', 'provider_id', 'model_id', 'limits'])
      OR jsonb_typeof(l) IS DISTINCT FROM 'object'
      OR l - ARRAY['max_input_tokens', 'max_output_tokens', 'max_bytes', 'timeout_ms'] <> '{}'::jsonb
      OR NOT (l ?& ARRAY['max_input_tokens', 'max_output_tokens', 'max_bytes', 'timeout_ms']) THEN
      RAISE EXCEPTION 'invalid deployment profile' USING ERRCODE = '22023';
    END IF;
    FOREACH k IN ARRAY ARRAY['id', 'provider_id', 'model_id'] LOOP
      IF jsonb_typeof(item -> k) IS DISTINCT FROM 'string' OR length(item ->> k) NOT BETWEEN 1 AND 200
        OR (item ->> k) !~ '^[a-zA-Z0-9][a-zA-Z0-9_.:/-]*$' THEN
        RAISE EXCEPTION 'invalid deployment profile identifier' USING ERRCODE = '22023';
      END IF;
    END LOOP;
    IF jsonb_typeof(item -> 'purpose') IS DISTINCT FROM 'string'
      OR (item ->> 'purpose') NOT IN ('EMBED', 'GENERATE')
      OR jsonb_typeof(item -> 'revision') IS DISTINCT FROM 'number'
      OR (item ->> 'revision') !~ '^[1-9][0-9]{0,17}$'
      OR jsonb_typeof(item -> 'execution') IS DISTINCT FROM 'string'
      OR (item ->> 'execution') NOT IN ('LOCAL', 'HOSTED')
      OR (item ? 'credential_ref' AND item -> 'credential_ref' <> 'null'::jsonb
        AND (jsonb_typeof(item -> 'credential_ref') <> 'string'
          OR (item ->> 'credential_ref') !~ '^[A-Za-z_][A-Za-z0-9_]{0,127}$')) THEN
      RAISE EXCEPTION 'invalid deployment profile' USING ERRCODE = '22023';
    END IF;
    IF item ->> 'purpose' = 'EMBED' THEN
      IF jsonb_typeof(item -> 'dimension') IS DISTINCT FROM 'number'
        OR (item ->> 'dimension') !~ '^[1-9][0-9]{0,4}$' OR (item ->> 'dimension')::integer > 16000 THEN
        RAISE EXCEPTION 'invalid deployment embedding dimension' USING ERRCODE = '22023';
      END IF;
    ELSIF item ? 'dimension' AND item -> 'dimension' <> 'null'::jsonb THEN
      RAISE EXCEPTION 'deployment generation dimension forbidden' USING ERRCODE = '22023';
    END IF;
    FOREACH k IN ARRAY ARRAY['max_input_tokens', 'max_output_tokens', 'max_bytes', 'timeout_ms'] LOOP
      IF jsonb_typeof(l -> k) IS DISTINCT FROM 'number' OR (l ->> k) !~ '^(0|[1-9][0-9]{0,17})$' THEN
        RAISE EXCEPTION 'invalid deployment model limit' USING ERRCODE = '22023';
      END IF;
    END LOOP;
    -- The deployment can never declare a bound the Gateway would refuse.
    IF (l ->> 'max_input_tokens')::numeric NOT BETWEEN 1 AND 1000000
      OR (l ->> 'max_output_tokens')::numeric NOT BETWEEN 1 AND 1000000
      OR (l ->> 'max_bytes')::numeric NOT BETWEEN 1 AND 1048576
      OR (l ->> 'timeout_ms')::numeric NOT BETWEEN 1 AND 120000 THEN
      RAISE EXCEPTION 'deployment model limits exceed the gateway ceiling' USING ERRCODE = '22023';
    END IF;
    -- A repeated purpose is ambiguous, so the whole declaration is rejected.
    IF declared ? (item ->> 'purpose') THEN
      RAISE EXCEPTION 'duplicate deployment purpose' USING ERRCODE = '22023';
    END IF;
    normalized := jsonb_build_object(
      'id', item ->> 'id', 'revision', (item ->> 'revision')::bigint,
      'purpose', item ->> 'purpose', 'execution', item ->> 'execution',
      'provider_id', item ->> 'provider_id', 'model_id', item ->> 'model_id',
      'dimension', CASE WHEN item -> 'dimension' IS NULL OR item -> 'dimension' = 'null'::jsonb
        THEN NULL ELSE to_jsonb((item ->> 'dimension')::integer) END,
      'credential_ref', CASE WHEN item -> 'credential_ref' IS NULL OR item -> 'credential_ref' = 'null'::jsonb
        THEN NULL ELSE to_jsonb(item ->> 'credential_ref') END,
      'limits', l);
    -- Immutable identity: create it once, then require the exact same revision.
    INSERT INTO cap_ai.model_profiles(id, revision, purpose, execution, provider_id, model_id, dimension, credential_ref, limits)
      VALUES(item ->> 'id', (item ->> 'revision')::bigint, item ->> 'purpose', item ->> 'execution',
        item ->> 'provider_id', item ->> 'model_id', (item ->> 'dimension')::integer,
        item ->> 'credential_ref', l)
      ON CONFLICT DO NOTHING;
    GET DIAGNOSTICS inserted = ROW_COUNT;
    created := created + inserted;
    SELECT to_jsonb(mp) - ARRAY['created_at', 'enabled'] INTO stored
      FROM cap_ai.model_profiles mp
      WHERE mp.id = item ->> 'id' AND mp.revision = (item ->> 'revision')::bigint
      FOR UPDATE;
    IF stored IS DISTINCT FROM normalized THEN
      RAISE EXCEPTION 'immutable deployment profile conflict' USING ERRCODE = '23505';
    END IF;
    declared := declared || jsonb_build_object(item ->> 'purpose', normalized);
  END LOOP;

  FOREACH wanted IN ARRAY ARRAY['EMBED', 'GENERATE'] LOOP
    entry := declared -> wanted;
    current_profile := NULL;
    current_revision := NULL;
    SELECT mp.id, mp.revision INTO current_profile, current_revision
      FROM cap_ai.model_profiles mp
      WHERE mp.purpose = wanted AND mp.enabled;
    latest_action := NULL;
    latest_profile := NULL;
    latest_revision := NULL;
    SELECT e.action, e.profile_id, e.profile_revision
      INTO latest_action, latest_profile, latest_revision
      FROM cap_ai.model_deployment_events e
      WHERE e.purpose = wanted
      ORDER BY e.selection_revision DESC
      LIMIT 1;
    purpose_changed := false;
    selection := NULL;

    IF entry IS NULL THEN
      -- The declaration omits this purpose: withdraw it (deny until reviewed).
      IF current_profile IS NOT NULL THEN
        UPDATE cap_ai.model_profiles mp SET enabled = false
          WHERE mp.purpose = wanted AND mp.enabled;
        withdrawn := withdrawn || (current_profile || '@' || current_revision);
        INSERT INTO cap_ai.model_deployment_events(purpose, action, actor_id, session_identity, review)
          VALUES(wanted, 'SUSPEND', NULL, session_user, jsonb_build_object(
            'reason', 'purpose omitted from deployment declaration',
            'previous_profile_id', current_profile, 'previous_revision', current_revision))
          RETURNING selection_revision INTO selection;
        purpose_changed := true;
        changed := true;
      END IF;
      selections := selections || jsonb_build_array(jsonb_build_object(
        'purpose', wanted, 'profile_id', NULL, 'revision', NULL, 'enabled', false,
        'selection_revision', selection, 'changed', purpose_changed));
      CONTINUE;
    END IF;

    -- Identical re-apply: same live revision, same recorded decision and a binding
    -- that still matches the stored revision, so nothing is written. A drifted row
    -- (only reachable by a privileged bypass of the immutable triggers) is never
    -- ratified by this fast path; it is re-declared explicitly below.
    IF current_profile = entry ->> 'id' AND current_revision = (entry ->> 'revision')::bigint
      AND latest_action = 'SELECT' AND latest_profile = current_profile AND latest_revision = current_revision
      AND EXISTS(SELECT FROM cap_ai.model_profiles mp
        WHERE mp.purpose = wanted AND mp.enabled AND cap_ai.model_deployment_current(mp)) THEN
      selections := selections || jsonb_build_array(jsonb_build_object(
        'purpose', wanted, 'profile_id', current_profile, 'revision', current_revision,
        'enabled', true, 'selection_revision', (SELECT max(e.selection_revision)
          FROM cap_ai.model_deployment_events e WHERE e.purpose = wanted), 'changed', false));
      CONTINUE;
    END IF;

    -- Withdraw whichever other revision is live for this purpose. The withdrawn
    -- row keeps its immutable history; only the live switch changes.
    IF current_profile IS NOT NULL AND (current_profile IS DISTINCT FROM entry ->> 'id'
      OR current_revision IS DISTINCT FROM (entry ->> 'revision')::bigint) THEN
      UPDATE cap_ai.model_profiles mp SET enabled = false
        WHERE mp.purpose = wanted AND mp.enabled
          AND NOT (mp.id = entry ->> 'id' AND mp.revision = (entry ->> 'revision')::bigint);
      withdrawn := withdrawn || (current_profile || '@' || current_revision);
    END IF;
    UPDATE cap_ai.model_profiles mp SET enabled = true
      WHERE mp.id = entry ->> 'id' AND mp.revision = (entry ->> 'revision')::bigint AND NOT mp.enabled;
    INSERT INTO cap_ai.model_deployment_events(purpose, profile_id, profile_revision, binding, binding_digest, action, actor_id, session_identity)
      SELECT wanted, mp.id, mp.revision, d.binding,
        encode(sha256(convert_to(d.binding::text, 'UTF8')), 'hex'), 'SELECT', NULL, session_user
        FROM cap_ai.model_profiles mp,
          LATERAL (SELECT cap_ai.model_deployment_declaration(mp) AS binding) d
        WHERE mp.id = entry ->> 'id' AND mp.revision = (entry ->> 'revision')::bigint
      RETURNING selection_revision INTO selection;
    selections := selections || jsonb_build_array(jsonb_build_object(
      'purpose', wanted, 'profile_id', entry ->> 'id', 'revision', (entry ->> 'revision')::bigint,
      'enabled', true, 'selection_revision', selection, 'changed', true));
    changed := true;
  END LOOP;

  RETURN jsonb_build_object(
    'profiles', selections, 'created', created, 'withdrawn', to_jsonb(withdrawn), 'changed', changed);
END
$$;

-- Atomic emergency withdrawal. The current administrator is authenticated before
-- any write, both authority rows are locked, both purposes are withdrawn in one
-- transaction, and one SUSPEND event per withdrawn purpose names the actor.
CREATE FUNCTION cap_ai.suspend_model_calls(actor uuid)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
#variable_conflict use_variable
DECLARE
  active boolean;
  target text;
  live text;
  live_revision bigint;
  suspended integer := 0;
  purposes jsonb := '[]'::jsonb;
  at timestamptz := now();
BEGIN
  IF session_user <> 'dxos_ai_model_catalog'
    OR current_setting('role') NOT IN ('none', 'dxos_ai_model_catalog')
    OR EXISTS(SELECT FROM pg_roles
      WHERE rolname = session_user AND (rolsuper OR rolbypassrls OR rolcreaterole))
    OR actor IS NULL THEN
    RAISE EXCEPTION 'model deployment session required' USING ERRCODE = '42501';
  END IF;
  -- Hold current administrative authority through the mutation: the same row
  -- locks a revoke must take, so a suspension cannot outlive its administrator.
  SELECT u.status = 'ACTIVE' INTO active FROM core.users u WHERE u.id = actor FOR SHARE;
  IF NOT FOUND OR NOT coalesce(active, false) THEN
    RAISE EXCEPTION 'platform administration authority required' USING ERRCODE = '42501';
  END IF;
  PERFORM 1 FROM core.user_roles ur
    WHERE ur.user_id = actor AND ur.role_name = 'role_operator' FOR SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'platform administration authority required' USING ERRCODE = '42501';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai.model_deployment', 0));
  FOR target IN SELECT capability FROM (VALUES ('EMBED'), ('GENERATE')) AS t(capability) LOOP
    live := NULL;
    live_revision := NULL;
    SELECT mp.id, mp.revision INTO live, live_revision
      FROM cap_ai.model_profiles mp
      WHERE mp.purpose = target AND mp.enabled;
    IF live IS NOT NULL THEN
      UPDATE cap_ai.model_profiles mp SET enabled = false
        WHERE mp.purpose = target AND mp.enabled;
      INSERT INTO cap_ai.model_deployment_events(purpose, action, actor_id, session_identity, review, occurred_at)
        VALUES(target, 'SUSPEND', actor, session_user, jsonb_build_object(
          'reason', 'model calls suspended', 'previous_profile_id', live,
          'previous_revision', live_revision), at);
      suspended := suspended + 1;
      purposes := purposes || jsonb_build_array(target);
    END IF;
  END LOOP;
  RETURN jsonb_build_object('suspended', suspended, 'purposes', purposes,
    'actor_id', actor, 'occurred_at', at);
END
$$;

-- Sanitized read-only deployment metadata for the deployment and administration
-- logins. It never returns a credential reference, an endpoint, a token, a
-- document or any content, and it never mutates state.
CREATE FUNCTION cap_ai.model_deployment_status()
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
DECLARE
  listed jsonb;
  live integer;
BEGIN
  IF NOT (cap_ai.model_admin_session()
    OR (session_user = 'dxos_ai_model_catalog'
      AND current_setting('role') IN ('none', 'dxos_ai_model_catalog')
      AND EXISTS(SELECT FROM pg_roles
        WHERE rolname = session_user AND NOT (rolsuper OR rolbypassrls OR rolcreaterole)))) THEN
    RAISE EXCEPTION 'model deployment authority required' USING ERRCODE = '42501';
  END IF;
  SELECT coalesce(jsonb_agg(jsonb_build_object(
      'purpose', t.purpose,
      'profile_id', mp.id,
      'revision', mp.revision,
      'execution', mp.execution,
      'provider_id', mp.provider_id,
      'model_id', mp.model_id,
      'dimension', mp.dimension,
      'limits', mp.limits,
      'selected', e.action = 'SELECT',
      'selection_revision', e.selection_revision,
      'selected_at', e.occurred_at,
      'enabled', coalesce(mp.enabled, false)) ORDER BY t.purpose), '[]'::jsonb) INTO listed
    FROM (VALUES ('EMBED'), ('GENERATE')) AS t(purpose)
    LEFT JOIN LATERAL (
      SELECT ev.*
        FROM cap_ai.model_deployment_events ev
        WHERE ev.purpose = t.purpose
        ORDER BY ev.selection_revision DESC
        LIMIT 1
    ) e ON true
    LEFT JOIN cap_ai.model_profiles mp
      ON mp.id = e.profile_id AND mp.revision = e.profile_revision;
  SELECT count(*) INTO live FROM cap_ai.model_profiles mp WHERE mp.enabled;
  RETURN jsonb_build_object('enabled', coalesce(live, 0) > 0, 'live_profiles', coalesce(live, 0),
    'profiles', listed);
END
$$;

-- ---------------------------------------------------------------------------
-- Rewritten authority routines: exact deployment profile instead of a slot.
-- Every pre-existing predicate (identity, scope, source READ proof, processing
-- policy, provider binding, PERSONAL owner/approval, lease fencing) is preserved
-- verbatim; only the binding source changes.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION cap_ai.model_policy(
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
  personal_approval bigint;
  source_approval bigint;
  source_personal boolean;
BEGIN
  IF scope_kind IS NULL OR scope_kind NOT IN ('PERSONAL', 'APP')
    OR jsonb_typeof(sources) IS DISTINCT FROM 'array' THEN
    RETURN '{}'::jsonb;
  END IF;
  IF jsonb_array_length(sources) > 50 THEN
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
    FROM cap_ai.model_profiles prof
    WHERE prof.id = profile AND prof.revision = profile_rev AND prof.enabled
      AND cap_ai.model_deployment_current(prof);
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
    source_personal := d.access_mode = 'PRIVATE' OR d.owner_user_id IS NOT NULL;
    personal := personal OR source_personal;
    SELECT * INTO STRICT p
      FROM cap_ai.processing_policies
      WHERE id = d.processing_policy_id AND version = d.processing_policy_version AND enabled;
    revision := greatest(revision, d.policy_revision);
    IF mp.execution = 'HOSTED' AND source_personal THEN
      source_approval := cap_ai.personal_processing_approval(profile, profile_rev, app, p.id, p.version);
      IF source_approval IS NULL THEN RETURN '{}'::jsonb; END IF;
      revision := greatest(revision, source_approval);
      CONTINUE;
    END IF;
    IF NOT EXISTS(SELECT FROM cap_ai.processing_policy_profiles
      WHERE policy_id = p.id AND policy_version = p.version AND model_profile_id = profile AND profile_revision = profile_rev)
      THEN
      RETURN '{}'::jsonb;
    END IF;
    IF mp.execution = 'HOSTED' AND(p.execution <> 'APPROVED_PROVIDER' OR p.classification <> 'NON_PERSONAL'
      OR p.approved_by IS NULL OR p.approved_at IS NULL OR NOT p.retention_approved OR p.provider_id IS DISTINCT FROM mp.provider_id)
      THEN
      RETURN '{}'::jsonb;
    END IF;
  END LOOP;
  IF mp.execution = 'HOSTED' AND personal THEN
    -- PERSONAL queries are classified by server scope/source authority, never the request.
    IF scope_kind = 'PERSONAL' AND app IS DISTINCT FROM cap_ai.library_app(actor, acting_role) THEN
      RETURN '{}'::jsonb;
    END IF;
    personal_approval := cap_ai.personal_processing_approval(profile, profile_rev, app, NULL, NULL);
    IF personal_approval IS NULL THEN RETURN '{}'::jsonb; END IF;
    classification := 'PERSONAL';
    revision := greatest(revision, personal_approval);
  END IF;
  IF mp.execution = 'HOSTED' AND coalesce(classification, 'UNKNOWN') NOT IN ('PERSONAL','NON_PERSONAL') THEN
    RETURN '{}'::jsonb;
  END IF;
  RETURN jsonb_build_object('personal_approved', personal_approval IS NOT NULL, 'allowed', true, 'profile', to_jsonb(mp), 'personal', personal, 'policy_revision', revision, 'query_classification', coalesce(classification, 'UNKNOWN'));
END
$$;

CREATE OR REPLACE FUNCTION cap_ai.worker_model_policy(
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
  personal_approval bigint;
  personal boolean;
  personal_source jsonb;
BEGIN
  IF actor::text <> worker OR NOT EXISTS(SELECT FROM core.users
    WHERE id = actor AND status = 'ACTIVE') OR NOT cap_ai.embedding_lease_valid(job, worker, fence, run) THEN
    RETURN '{}'::jsonb;
  END IF;
  scope := cap_ai.embedding_scope(run);
  IF scope IS NULL OR sources IS DISTINCT FROM jsonb_build_array(scope) THEN
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
    FROM cap_ai.model_profiles prof
    WHERE prof.id = profile AND prof.revision = rev AND prof.enabled AND prof.purpose = 'EMBED'
      AND cap_ai.model_deployment_current(prof);
  IF NOT FOUND THEN RETURN '{}'::jsonb; END IF;
  SELECT j.personal_embedding_source INTO personal_source
    FROM cap_ai.ingestion_jobs j
    WHERE j.id = job AND j.profile_key = cap_ai.embedding_profile_key(profile, rev, j.personal_embedding_source);
  IF NOT FOUND THEN RETURN '{}'::jsonb; END IF;
  personal := d.access_mode = 'PRIVATE' OR d.owner_user_id IS NOT NULL;
  IF personal THEN
    IF personal_source IS NULL OR personal_source->'scope' IS DISTINCT FROM scope
      OR personal_source IS DISTINCT FROM cap_ai.personal_embedding_source(run, profile, rev) THEN
      RETURN '{}'::jsonb;
    END IF;
    personal_approval := (personal_source->>'approval_revision')::bigint;
  ELSE
    IF personal_source IS NOT NULL THEN RETURN '{}'::jsonb; END IF;
    IF NOT EXISTS(SELECT FROM cap_ai.processing_policy_profiles WHERE policy_id=p.id AND policy_version=p.version
      AND model_profile_id=profile AND profile_revision=rev) THEN RETURN '{}'::jsonb; END IF;
    IF mp.execution = 'HOSTED' AND (p.execution <> 'APPROVED_PROVIDER' OR p.classification <> 'NON_PERSONAL'
      OR NOT p.retention_approved OR p.approved_by IS NULL OR p.approved_at IS NULL OR p.provider_id IS DISTINCT FROM mp.provider_id) THEN
      RETURN '{}'::jsonb;
    END IF;
  END IF;
  RETURN jsonb_build_object('allowed', true, 'profile', to_jsonb(mp), 'personal', personal,
    'personal_approved', personal_approval IS NOT NULL, 'policy_revision', greatest(d.policy_revision,coalesce(personal_approval,0)), 'query_classification', p.classification);
END
$$;

CREATE OR REPLACE FUNCTION cap_ai.queue_embeddings(
    actor uuid, acting_role text, profile text, rev bigint
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
BEGIN
  IF actor IS NULL AND session_user <> 'dxos_ai_worker' THEN
    RAISE EXCEPTION 'worker authority required' USING ERRCODE = '42501';
  END IF;
  INSERT INTO cap_ai.ingestion_jobs(version_id, kind, profile_key, personal_embedding_source)
    SELECT v.id, 'EMBED', cap_ai.embedding_profile_key(profile, rev, authority.source), authority.source
    FROM cap_ai.document_versions v
    JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
    JOIN cap_ai.processing_policies p ON p.id = d.processing_policy_id AND p.version = d.processing_policy_version
    JOIN cap_ai.model_profiles mp ON mp.id = profile AND mp.revision = rev
    CROSS JOIN LATERAL (
      SELECT CASE WHEN d.source_kind = 'UPLOAD' THEN cap_ai.personal_embedding_source(v.active_run_id, profile, rev) END AS source
    ) authority
    WHERE d.active_version_id = v.id AND d.desired_version_id = v.id AND v.active_run_id IS NOT NULL
      AND cap_ai.eligible_version(v.id) AND mp.purpose = 'EMBED' AND mp.enabled AND p.enabled
      AND cap_ai.model_deployment_current(mp)
      AND CASE WHEN d.source_kind = 'UPLOAD' THEN
        authority.source IS NOT NULL
      ELSE EXISTS (
        SELECT FROM cap_ai.processing_policy_profiles pp
        WHERE pp.policy_id = p.id AND pp.policy_version = p.version
          AND pp.model_profile_id = profile AND pp.profile_revision = rev
          AND (mp.execution = 'LOCAL' OR (d.access_mode <> 'PRIVATE' AND p.execution = 'APPROVED_PROVIDER'
            AND p.classification = 'NON_PERSONAL' AND p.retention_approved AND p.provider_id = mp.provider_id
            AND p.approved_at IS NOT NULL AND p.approved_by IS NOT NULL))
      ) END
      AND (actor IS NULL OR cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL)
    ON CONFLICT(version_id, kind, profile_key) DO NOTHING;
  RETURN true;
END
$$;

CREATE OR REPLACE FUNCTION cap_ai.embedding_work(
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
  pinned_source jsonb;
BEGIN
  SELECT personal_embedding_source INTO pinned_source
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
    FROM cap_ai.model_profiles prof
    WHERE prof.id = profile AND prof.revision = rev AND prof.enabled AND prof.purpose = 'EMBED'
      AND cap_ai.model_deployment_current(prof);
  INSERT INTO cap_ai.embedding_generations(run_id, model_profile_id, profile_revision, dimension, expected_count, job_id, fencing_token, personal_source)
    SELECT run, profile, rev, mp.dimension, count(*), job, fence, pinned_source
    FROM cap_ai.knowledge_chunks
    WHERE run_id = run ON CONFLICT DO NOTHING;
  SELECT generation.* INTO STRICT g
    FROM cap_ai.embedding_generations generation
    WHERE generation.run_id = run AND generation.model_profile_id = profile AND generation.profile_revision = rev
      AND generation.personal_source IS NOT DISTINCT FROM pinned_source
      AND (pinned_source IS NULL OR generation.job_id = job)
    FOR UPDATE;
  IF g.state IN('FAILED', 'RETIRED')
    OR g.dimension <> mp.dimension THEN
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

-- Claim only a job of the deployment profile the worker actually holds authority
-- for. A job queued for a superseded profile is never leased, and it does not
-- consume the worker's lease budget, so it cannot block the current profile.
CREATE FUNCTION cap_ai.claim_embedding(worker text, profile text, rev bigint)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  j cap_ai.ingestion_jobs;
  run uuid;
  mp cap_ai.model_profiles;
BEGIN
  IF NOT EXISTS(SELECT FROM core.users
    WHERE id = worker::uuid AND status = 'ACTIVE') THEN
    RAISE EXCEPTION 'worker unavailable' USING ERRCODE = 'P0404';
  END IF;
  IF profile IS NULL OR rev IS NULL THEN
    RETURN NULL;
  END IF;
  SELECT * INTO mp
    FROM cap_ai.model_profiles prof
    WHERE prof.id = profile AND prof.revision = rev AND prof.enabled AND prof.purpose = 'EMBED'
      AND cap_ai.model_deployment_current(prof);
  IF NOT FOUND THEN
    -- No current deployment profile for this purpose: claim nothing at all.
    RETURN NULL;
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
    AND job.profile_key = cap_ai.embedding_profile_key(profile, rev, job.personal_embedding_source)
    AND (d.source_kind <> 'UPLOAD' OR (job.personal_embedding_source IS NOT NULL
      AND job.personal_embedding_source = cap_ai.personal_embedding_source(v.active_run_id, profile, rev)))
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

-- The gateway reads a profile only while it is the live deployment selection.
CREATE OR REPLACE FUNCTION cap_ai.model_profile(profile text, rev bigint)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT to_jsonb(p)
  FROM cap_ai.model_profiles p
  WHERE id = profile AND revision = rev AND enabled
    AND cap_ai.model_deployment_current(p)
$$;

-- Agent generation is billed to one configured consumer and authorized by the
-- current deployment GENERATE profile. Every per-source READ, processing,
-- PERSONAL and query-review predicate below is unchanged from 0010; only the
-- slot lookup is replaced by the exact deployment binding.
CREATE FUNCTION cap_ai.agent_model_policy(
    actor uuid, billing_app text, profile text, revision bigint, proofs jsonb
)
RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE
  mp cap_ai.model_profiles;
  eligible_scopes jsonb;
  proof jsonb;
  read_scope jsonb;
  source_app text;
  source_role text;
  proof_kind text;
  doc uuid;
  source_decision jsonb;
  source_policy_id text;
  source_policy_version bigint;
  billing_query_review bigint;
  source_query_review bigint;
  source_review bigint;
  review_revisions jsonb;
BEGIN
  IF NOT cap_ai.agent_model_gateway_session() OR NOT cap_ai.agent_chat_user_active(actor)
    OR jsonb_typeof(proofs) IS DISTINCT FROM 'array'
    OR NOT EXISTS (SELECT FROM core.apps WHERE app_id = billing_app AND status = 'Ready') THEN
    RETURN '{}'::jsonb;
  END IF;
  IF jsonb_array_length(proofs) NOT BETWEEN 1 AND 50 THEN
    RETURN '{}'::jsonb;
  END IF;

  -- Each Gateway check must see the same live binding as the caller resolved.
  -- An enabled profile, a legacy slot binding or an earlier selection is not
  -- authority: the current deployment selection must name this exact revision.
  SELECT mp_row.* INTO mp
    FROM cap_ai.model_profiles mp_row
    WHERE mp_row.id = $3 AND mp_row.revision = $4
      AND mp_row.enabled AND mp_row.purpose = 'GENERATE'
      AND cap_ai.model_deployment_current(mp_row);
  IF NOT FOUND THEN
    RETURN '{}'::jsonb;
  END IF;
  -- A global question can contain PERSONAL terms even with entirely APP evidence.
  billing_query_review := cap_ai.personal_processing_approval(profile, revision, billing_app, NULL, NULL);
  IF billing_query_review IS NULL THEN
    RETURN '{}'::jsonb;
  END IF;
  review_revisions := jsonb_build_array(billing_query_review);
  eligible_scopes := cap_ai.agent_authorized_scopes(actor);

  FOR proof IN SELECT value FROM jsonb_array_elements(proofs) LOOP
    IF jsonb_typeof(proof) IS DISTINCT FROM 'object' THEN
      RETURN '{}'::jsonb;
    END IF;
    IF proof - ARRAY['scope_kind', 'app_id', 'acting_role', 'read_scope'] <> '{}'::jsonb
      OR jsonb_typeof(proof->'scope_kind') IS DISTINCT FROM 'string'
      OR jsonb_typeof(proof->'app_id') IS DISTINCT FROM 'string'
      OR jsonb_typeof(proof->'acting_role') IS DISTINCT FROM 'string'
      OR jsonb_typeof(proof->'read_scope') IS DISTINCT FROM 'object' THEN
      RETURN '{}'::jsonb;
    END IF;
    source_app := proof->>'app_id';
    source_role := proof->>'acting_role';
    proof_kind := proof->>'scope_kind';
    read_scope := proof->'read_scope';
    IF NOT eligible_scopes @> jsonb_build_array(
      jsonb_build_object('kind', proof_kind, 'app_id', source_app, 'acting_role', source_role)
    ) OR jsonb_typeof(read_scope->'document_id') IS DISTINCT FROM 'string'
      OR read_scope->>'document_id' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
      RETURN '{}'::jsonb;
    END IF;
    doc := (read_scope->>'document_id')::uuid;
    -- The document must actually belong to this app and kind, with usable parsed
    -- content. model_policy then compares the full current READ scope and
    -- applies the exact source and query processing predicates on that snapshot.
    SELECT d.processing_policy_id, d.processing_policy_version
      INTO source_policy_id, source_policy_version
    FROM cap_ai.knowledge_documents d
    JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
    JOIN cap_ai.document_versions v ON v.id = d.active_version_id AND v.state = 'READY'
    JOIN cap_ai.processing_runs r ON r.id = v.active_run_id AND r.state = 'COMPLETE'
    WHERE d.id = doc AND c.app_id = source_app AND c.kind = proof_kind;
    IF NOT FOUND THEN
      RETURN '{}'::jsonb;
    END IF;
    source_decision := cap_ai.model_policy(actor, source_role, source_app, proof_kind,
      profile, revision, jsonb_build_array(read_scope));
    IF source_decision->>'allowed' IS DISTINCT FROM 'true' THEN
      RETURN '{}'::jsonb;
    END IF;

    -- Record each approval separately. Taking the largest revision across all
    -- sources would hide changes to one source under a newer review elsewhere.
    source_review := NULL;
    source_query_review := NULL;
    IF proof_kind = 'PERSONAL' THEN
      source_review := cap_ai.personal_processing_approval(profile, revision, source_app,
        source_policy_id, source_policy_version);
      source_query_review := cap_ai.personal_processing_approval(profile, revision, source_app, NULL, NULL);
    END IF;
    review_revisions := review_revisions || jsonb_build_array(
      jsonb_build_object('policy', source_decision->'policy_revision', 'source', source_review, 'query', source_query_review)
    );
  END LOOP;

  RETURN jsonb_build_object('allowed', true, 'profile', to_jsonb(mp), 'personal', true,
    'personal_approved', true, 'query_classification', 'PERSONAL', 'policy_revision', billing_query_review,
    'authority_fingerprint', encode(sha256(convert_to(review_revisions::text, 'UTF8')), 'hex'));
END
$$;

-- Query-only authority for global retrieval, bound to the current deployment
-- EMBED profile instead of the legacy EMBEDDING slot revision. Every scope
-- predicate and the hosted personal query review are unchanged from 0010.
CREATE FUNCTION cap_ai.agent_query_model_policy(
    actor uuid, acting_role text, app text, scope_kind text, profile text, profile_rev bigint
)
RETURNS jsonb
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog, cap_ai AS $$
DECLARE
  decision jsonb;
  query_review bigint;
BEGIN
  IF NOT cap_ai.agent_model_gateway_session() OR NOT cap_ai.agent_chat_user_active(actor)
    OR scope_kind IS NULL OR scope_kind NOT IN ('APP', 'PERSONAL')
    OR app IS NULL OR acting_role IS NULL
    OR NOT EXISTS (
      SELECT FROM cap_ai.model_profiles prof
      WHERE prof.id = profile AND prof.revision = profile_rev
        AND prof.enabled AND prof.purpose = 'EMBED'
        AND cap_ai.model_deployment_current(prof)
    ) THEN
    RETURN '{}'::jsonb;
  END IF;
  IF NOT cap_ai.agent_authorized_scopes(actor) @> jsonb_build_array(
    jsonb_build_object('kind', scope_kind, 'app_id', app, 'acting_role', acting_role)
  ) THEN
    RETURN '{}'::jsonb;
  END IF;
  decision := cap_ai.model_policy(actor, acting_role, app, scope_kind, profile, profile_rev, '[]'::jsonb);
  IF decision->>'allowed' IS DISTINCT FROM 'true' OR decision #>> '{profile,purpose}' IS DISTINCT FROM 'EMBED' THEN
    RETURN '{}'::jsonb;
  END IF;
  IF decision #>> '{profile,execution}' = 'LOCAL' THEN
    RETURN decision;
  END IF;

  query_review := cap_ai.personal_processing_approval(profile, profile_rev, app, NULL, NULL);
  IF query_review IS NULL THEN
    RETURN '{}'::jsonb;
  END IF;
  RETURN decision || jsonb_build_object(
    'personal_approved', true, 'query_classification', 'PERSONAL', 'policy_revision', query_review
  );
END
$$;

-- ---------------------------------------------------------------------------
-- Legacy slot administration still records slot history for the CLI until T9,
-- but it is no longer model authority: only a deployment selection is. It is
-- re-emitted here for one reason: enabling a profile must keep the new
-- one-enabled-per-purpose invariant, so the previously enabled revision of the
-- same purpose is withdrawn in the same statement.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION cap_ai.model_admin_change_slot(
    actor uuid,
    slot text,
    expected_revision bigint,
    action text,
    profile text,
    rev bigint,
    reviewed_digest text,
    review jsonb
)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
DECLARE
  mp cap_ai.model_profiles;
  cand cap_ai.model_candidates;
  bound cap_ai.global_model_slots;
  active boolean;
  next_revision bigint;
  event_id uuid;
  reason text;
  qualification jsonb;
  attestation text;
BEGIN
  IF NOT coalesce(cap_ai.model_admin_actor(actor), false) THEN
    RAISE EXCEPTION 'model administration session required' USING ERRCODE = '42501';
  END IF;
  IF slot IS NULL OR slot NOT IN('EMBEDDING', 'LLM')
    OR action IS NULL OR action NOT IN('PROMOTE', 'ROLLBACK', 'DISABLE')
    OR expected_revision IS NULL OR expected_revision < 0 THEN
    RAISE EXCEPTION 'invalid slot administration request' USING ERRCODE = '22023';
  END IF;
  IF review IS NOT NULL AND octet_length(review::text) > 8192 THEN
    RAISE EXCEPTION 'review metadata too large' USING ERRCODE = '22023';
  END IF;

  IF action = 'DISABLE' THEN
    -- Withdrawing a binding needs no profile: it only records why.
    IF profile IS NOT NULL OR rev IS NOT NULL OR reviewed_digest IS NOT NULL
      OR review IS NULL OR jsonb_typeof(review) <> 'object'
      OR review - 'reason' <> '{}'::jsonb OR NOT(review ? 'reason')
      OR jsonb_typeof(review -> 'reason') <> 'string'
      OR length(btrim(review ->> 'reason')) NOT BETWEEN 1 AND 4000 THEN
      RAISE EXCEPTION 'invalid disable review' USING ERRCODE = '22023';
    END IF;
    reason := btrim(review ->> 'reason');
  ELSE
    IF profile IS NULL OR length(profile) NOT BETWEEN 1 AND 200 OR rev IS NULL OR rev < 1
      OR reviewed_digest IS NULL OR reviewed_digest !~ '^[0-9a-f]{64}$'
      OR review IS NULL OR jsonb_typeof(review) <> 'object'
      OR review - ARRAY['declaration_digest', 'qualification', 'attestation_ref'] <> '{}'::jsonb
      OR NOT(review ?& ARRAY['declaration_digest', 'qualification', 'attestation_ref'])
      OR jsonb_typeof(review -> 'declaration_digest') <> 'string'
      OR review ->> 'declaration_digest' IS DISTINCT FROM reviewed_digest
      OR jsonb_typeof(review -> 'attestation_ref') <> 'string'
      OR jsonb_typeof(review -> 'qualification') <> 'object' THEN
      RAISE EXCEPTION 'invalid slot review' USING ERRCODE = '22023';
    END IF;
    attestation := review ->> 'attestation_ref';
    -- An opaque human review reference: an identifier, never a URL or content.
    IF attestation !~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$' OR attestation LIKE '%://%' THEN
      RAISE EXCEPTION 'invalid attestation reference' USING ERRCODE = '22023';
    END IF;
    qualification := review -> 'qualification';
    IF qualification - ARRAY['profile_id', 'revision', 'declaration_digest', 'purpose', 'passed', 'checked_at', 'dimension', 'citation_ids', 'usage'] <> '{}'::jsonb
      OR NOT(qualification ?& ARRAY['profile_id', 'revision', 'declaration_digest', 'purpose', 'passed', 'checked_at'])
      OR jsonb_typeof(qualification -> 'profile_id') <> 'string'
      OR jsonb_typeof(qualification -> 'revision') <> 'number'
      OR jsonb_typeof(qualification -> 'declaration_digest') <> 'string'
      OR jsonb_typeof(qualification -> 'purpose') <> 'string'
      OR jsonb_typeof(qualification -> 'passed') <> 'boolean'
      OR jsonb_typeof(qualification -> 'checked_at') <> 'string'
      OR qualification ->> 'profile_id' IS DISTINCT FROM profile
      OR(qualification -> 'revision') IS DISTINCT FROM to_jsonb(rev)
      OR qualification ->> 'declaration_digest' IS DISTINCT FROM reviewed_digest
      OR(qualification -> 'passed') IS DISTINCT FROM 'true'::jsonb
      OR qualification ->> 'checked_at' !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$' THEN
      RAISE EXCEPTION 'invalid probe qualification' USING ERRCODE = '22023';
    END IF;

    SELECT * INTO mp
      FROM cap_ai.model_profiles p
      WHERE p.id = profile AND p.revision = rev;
    IF NOT FOUND OR mp.execution <> 'HOSTED' OR mp.purpose IS DISTINCT FROM(CASE slot WHEN 'EMBEDDING' THEN 'EMBED' ELSE 'GENERATE' END)
      OR qualification ->> 'purpose' IS DISTINCT FROM mp.purpose THEN
      RAISE EXCEPTION 'model profile does not match the slot' USING ERRCODE = '22023';
    END IF;
    -- Bounds must fit the Gateway's own ceilings before a binding is allowed.
    IF(mp.limits ->> 'max_input_tokens')::numeric > 1000000 OR(mp.limits ->> 'max_output_tokens')::numeric > 1000000
      OR(mp.limits ->> 'max_bytes')::numeric > 1048576 OR(mp.limits ->> 'timeout_ms')::numeric > 120000 THEN
      RAISE EXCEPTION 'model limits exceed the gateway ceiling' USING ERRCODE = '22023';
    END IF;

    SELECT * INTO cand
      FROM cap_ai.model_candidates c
      WHERE c.profile_id = profile AND c.revision = rev;
    IF NOT FOUND OR cand.adapter_key <> 'bounded-http-v1' THEN
      RAISE EXCEPTION 'unreviewed adapter candidate' USING ERRCODE = '22023';
    END IF;
    IF cand.declaration_digest IS DISTINCT FROM reviewed_digest
      OR cand.declaration - ARRAY['id', 'revision', 'purpose', 'execution', 'provider_id', 'model_id', 'dimension', 'credential_ref', 'limits'] <> '{}'::jsonb
      OR cand.declaration ->> 'id' IS DISTINCT FROM mp.id
      OR(cand.declaration -> 'revision') IS DISTINCT FROM to_jsonb(mp.revision)
      OR cand.declaration ->> 'purpose' IS DISTINCT FROM mp.purpose
      OR cand.declaration ->> 'execution' IS DISTINCT FROM mp.execution
      OR cand.declaration ->> 'provider_id' IS DISTINCT FROM mp.provider_id
      OR cand.declaration ->> 'model_id' IS DISTINCT FROM mp.model_id
      OR cand.declaration ->> 'credential_ref' IS DISTINCT FROM mp.credential_ref
      OR cand.declaration -> 'limits' IS DISTINCT FROM mp.limits
      OR coalesce(cand.declaration -> 'dimension', 'null'::jsonb) IS DISTINCT FROM coalesce(to_jsonb(mp.dimension), 'null'::jsonb) THEN
      RAISE EXCEPTION 'candidate declaration does not match the profile' USING ERRCODE = '22023';
    END IF;

    -- The probe report is evidence, never approval: identity, dimension, the one
    -- synthetic citation set and bounded usage must match the reviewed revision.
    IF mp.purpose = 'EMBED' THEN
      IF jsonb_typeof(qualification -> 'dimension') IS DISTINCT FROM 'number'
        OR(qualification -> 'dimension') IS DISTINCT FROM to_jsonb(mp.dimension)
        OR(qualification ? 'citation_ids' AND qualification -> 'citation_ids' <> '[]'::jsonb) THEN
        RAISE EXCEPTION 'invalid embedding qualification' USING ERRCODE = '22023';
      END IF;
    ELSE
      IF(qualification ? 'dimension' AND(jsonb_typeof(qualification -> 'dimension') <> 'number' OR(qualification -> 'dimension') <> '0'::jsonb))
        OR qualification -> 'citation_ids' IS DISTINCT FROM '["E1"]'::jsonb THEN
        RAISE EXCEPTION 'invalid generation qualification' USING ERRCODE = '22023';
      END IF;
    END IF;
    IF qualification ? 'usage' THEN
      -- Counters compare as JSON numbers, so a malformed value is rejected as
      -- invalid input instead of raising a cast error.
      IF jsonb_typeof(qualification -> 'usage') <> 'object'
        OR(qualification -> 'usage') - ARRAY['input_tokens', 'output_tokens'] <> '{}'::jsonb
        OR NOT((qualification -> 'usage') ?& ARRAY['input_tokens', 'output_tokens'])
        OR jsonb_typeof(qualification -> 'usage' -> 'input_tokens') <> 'number'
        OR jsonb_typeof(qualification -> 'usage' -> 'output_tokens') <> 'number'
        OR(qualification -> 'usage' -> 'input_tokens')::text !~ '^[0-9]+$'
        OR(qualification -> 'usage' -> 'output_tokens')::text !~ '^[0-9]+$'
        OR(qualification -> 'usage' -> 'input_tokens') > to_jsonb((mp.limits ->> 'max_input_tokens')::numeric)
        OR(qualification -> 'usage' -> 'output_tokens') > to_jsonb((mp.limits ->> 'max_output_tokens')::numeric)
        OR(mp.purpose = 'EMBED' AND(qualification -> 'usage' -> 'output_tokens') <> '0'::jsonb) THEN
        RAISE EXCEPTION 'invalid qualification usage' USING ERRCODE = '22023';
      END IF;
    END IF;

    -- A rollback restores a revision that was itself reviewed and bound before.
    IF action = 'ROLLBACK' AND NOT EXISTS(SELECT FROM cap_ai.model_slot_events e
      WHERE e.capability = slot AND e.profile_id = profile AND e.profile_revision = rev AND e.action IN('PROMOTE', 'ROLLBACK')) THEN
      RAISE EXCEPTION 'rollback target was never reviewed for this slot' USING ERRCODE = '22023';
    END IF;
  END IF;

  -- Serialize with authority administration: the same rows a revoke or an
  -- activation must change are locked immediately before the mutation.
  SELECT u.status = 'ACTIVE' INTO active
    FROM core.users u
    WHERE u.id = actor FOR SHARE;
  IF NOT FOUND OR NOT coalesce(active, false) THEN
    RAISE EXCEPTION 'model administration authority required' USING ERRCODE = '42501';
  END IF;
  PERFORM 1
    FROM core.user_roles ur
    WHERE ur.user_id = actor AND ur.role_name = 'role_operator' FOR SHARE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'model administration authority required' USING ERRCODE = '42501';
  END IF;

  SELECT * INTO bound
    FROM cap_ai.global_model_slots s
    WHERE s.capability = slot FOR UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'unknown model slot' USING ERRCODE = '22023';
  END IF;
  IF bound.slot_revision IS DISTINCT FROM expected_revision THEN
    RAISE EXCEPTION 'model slot revision conflict' USING ERRCODE = '40001';
  END IF;
  IF expected_revision >= 9223372036854775807 THEN
    RAISE EXCEPTION 'model slot revision exhausted' USING ERRCODE = '22003';
  END IF;
  next_revision := expected_revision + 1;

  IF action = 'DISABLE' THEN
    UPDATE cap_ai.global_model_slots s
    SET profile_id = NULL, profile_revision = NULL, slot_revision = next_revision
    WHERE s.capability = slot;
    INSERT INTO cap_ai.model_slot_events(capability, slot_revision, profile_id, profile_revision, actor_id, action, review)
      VALUES(slot, next_revision, NULL, NULL, actor, 'DISABLE', jsonb_build_object('reason', reason)) RETURNING id INTO event_id;
    RETURN jsonb_build_object('capability', slot, 'profile_id', NULL, 'profile_revision', NULL, 'slot_revision', next_revision, 'action', 'DISABLE', 'event_id', event_id);
  END IF;

  -- At most one enabled profile per purpose: a slot binding that enables a new
  -- revision withdraws the previous live one in the same transaction. The slot is
  -- bookkeeping only; a model call still requires a deployment selection.
  PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai.model_deployment', 0));
  UPDATE cap_ai.model_profiles p
  SET enabled = false
  WHERE p.purpose = (CASE slot WHEN 'EMBEDDING' THEN 'EMBED' ELSE 'GENERATE' END)
    AND p.enabled AND NOT(p.id = profile AND p.revision = rev);
  UPDATE cap_ai.model_profiles p
  SET enabled = true
  WHERE p.id = profile AND p.revision = rev AND NOT p.enabled;
  UPDATE cap_ai.global_model_slots s
  SET profile_id = profile, profile_revision = rev, slot_revision = next_revision
  WHERE s.capability = slot;
  INSERT INTO cap_ai.model_slot_events(capability, slot_revision, profile_id, profile_revision, actor_id, action, review)
    VALUES(slot, next_revision, profile, rev, actor, action, review) RETURNING id INTO event_id;
  RETURN jsonb_build_object('capability', slot, 'profile_id', profile, 'profile_revision', rev, 'slot_revision', next_revision, 'action', action, 'event_id', event_id);
END
$$;

-- Ownership and reachability. The deployment events table stays inside the owner
-- boundary; only the four narrow interfaces below are reachable, and the
-- superseded slot-revision interfaces lose their execute grants here (the
-- functions themselves stay until T9).
DO $$
DECLARE
  r record;
  g record;
BEGIN
  ALTER TABLE cap_ai.model_deployment_events OWNER TO dxos_ai_owner;
  EXECUTE 'REVOKE ALL ON cap_ai.model_deployment_events FROM PUBLIC, dxos_authz, dxos_ai_api, dxos_ai_worker, dxos_ai_gateway, dxos_ai_model_catalog, dxos_ai_model_admin';
  FOR r IN SELECT p.oid::regprocedure signature
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'cap_ai' AND p.proname IN (
      'model_deployment_declaration', 'model_deployment_current', 'reconcile_deployment_models',
      'suspend_model_calls', 'model_deployment_status', 'model_policy', 'worker_model_policy',
      'queue_embeddings', 'embedding_work', 'claim_embedding', 'model_profile',
      'agent_model_policy', 'agent_query_model_policy', 'model_admin_change_slot') LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner', r.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', r.signature);
  END LOOP;
  -- Only the deployment login may change live model authority.
  FOR g IN SELECT rolname FROM pg_roles
    WHERE rolname IN ('dxos_process', 'dxos_hasura', 'dxos_authz', 'dxos_hr', 'dxos_reader', 'dxos_caller',
      'dxos_module_test', 'dxos_ai_api', 'dxos_ai_worker', 'dxos_ai_dispatcher', 'dxos_ai_gateway',
      'dxos_ai_model_catalog', 'dxos_ai_model_admin') LOOP
    EXECUTE format('REVOKE ALL ON FUNCTION cap_ai.reconcile_deployment_models(jsonb) FROM %I', g.rolname);
    EXECUTE format('REVOKE ALL ON FUNCTION cap_ai.suspend_model_calls(uuid) FROM %I', g.rolname);
    EXECUTE format('REVOKE ALL ON FUNCTION cap_ai.model_deployment_status() FROM %I', g.rolname);
  END LOOP;
  -- Superseded slot-revision authority and the unbound claim interface are closed.
  EXECUTE 'REVOKE ALL ON FUNCTION cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb) FROM dxos_ai_gateway';
  EXECUTE 'REVOKE ALL ON FUNCTION cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint,bigint) FROM dxos_ai_gateway';
  EXECUTE 'REVOKE ALL ON FUNCTION cap_ai.claim_embedding(text) FROM dxos_ai_worker';
END
$$;

GRANT EXECUTE ON FUNCTION cap_ai.reconcile_deployment_models(jsonb),
    cap_ai.suspend_model_calls(uuid) TO dxos_ai_model_catalog;

GRANT EXECUTE ON FUNCTION cap_ai.model_deployment_status() TO dxos_ai_model_catalog,
    dxos_ai_model_admin;

GRANT EXECUTE ON FUNCTION cap_ai.claim_embedding(text, text, bigint) TO dxos_ai_worker;

GRANT EXECUTE ON FUNCTION cap_ai.agent_model_policy(uuid, text, text, bigint, jsonb),
    cap_ai.agent_query_model_policy(uuid, text, text, text, text, bigint) TO dxos_ai_gateway;
