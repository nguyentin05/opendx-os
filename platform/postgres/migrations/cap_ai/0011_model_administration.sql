-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Additive audited slot administration. Requires 0004 and 0005. No password is
-- set here: the deployment owner provisions the login secret separately.

DO $$
BEGIN
  IF to_regclass('cap_ai.global_model_slots') IS NULL
    OR to_regclass('cap_ai.model_candidates') IS NULL
    OR to_regclass('cap_ai.model_slot_events') IS NULL THEN
    RAISE EXCEPTION 'model administration requires migrations 0004 and 0005';
  END IF;
  IF NOT EXISTS(SELECT FROM pg_roles
    WHERE rolname = 'dxos_ai_model_admin') THEN
    CREATE ROLE dxos_ai_model_admin LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT;
  END IF;
  ALTER ROLE dxos_ai_model_admin LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT;
END
$$;

-- Serializing a mutation against authority administration needs a row lock on
-- the actor's identity and membership rows, and a row lock needs UPDATE on at
-- least one column. Only a cosmetic column of each table is granted, so no
-- definer function owned by this role can rewrite identity, status or a role
-- assignment. The login itself still receives no table privilege at all.
GRANT UPDATE (display_name) ON core.users TO dxos_ai_owner;

GRANT UPDATE (granted_at) ON core.user_roles TO dxos_ai_owner;

-- Historical events stay valid with a NULL review; every new event carries the
-- bounded review object that authorized it.
ALTER TABLE cap_ai.model_slot_events ADD COLUMN IF NOT EXISTS review jsonb;

DO $$
BEGIN
  IF NOT EXISTS(SELECT FROM pg_constraint
    WHERE conrelid = 'cap_ai.model_slot_events'::regclass
      AND conname = 'model_slot_events_review_shape') THEN
    ALTER TABLE cap_ai.model_slot_events ADD CONSTRAINT model_slot_events_review_shape CHECK (
      review IS NULL OR jsonb_typeof(review) = 'object');
  END IF;
END
$$;

-- The deployment session is the only authority. SECURITY DEFINER changes
-- current_user, so the checks read session_user and the role setting instead.
CREATE FUNCTION cap_ai.model_admin_session()
RETURNS boolean
LANGUAGE sql STABLE
SET search_path = pg_catalog, cap_ai
AS $$
SELECT session_user = 'dxos_ai_model_admin'
  AND current_setting('role') IN ('none', 'dxos_ai_model_admin')
  AND EXISTS(SELECT FROM pg_roles
    WHERE rolname = session_user AND NOT(rolsuper OR rolbypassrls OR rolcreaterole))
$$;

-- Session identity plus a current platform operator. This is the read-only
-- authority check; the mutation additionally locks both authority rows.
CREATE FUNCTION cap_ai.model_admin_actor(actor uuid)
RETURNS boolean
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT actor IS NOT NULL AND cap_ai.model_admin_session()
  AND EXISTS(SELECT FROM core.users u
    JOIN core.user_roles ur ON ur.user_id = u.id
    WHERE u.id = actor AND u.status = 'ACTIVE' AND ur.role_name = 'role_operator')
$$;

-- Bounded administrative visibility. Only registry metadata is returned: never a
-- credential reference or value, a provider endpoint, a token or content.
CREATE FUNCTION cap_ai.model_admin_list(
    actor uuid,
    profile text,
    rev bigint,
    after_id text,
    after_rev bigint,
    page_size integer
)
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
DECLARE
  page integer;
  listed jsonb;
BEGIN
  IF NOT coalesce(cap_ai.model_admin_actor(actor), false) THEN
    RAISE EXCEPTION 'model administration session required' USING ERRCODE = '42501';
  END IF;
  IF(profile IS NULL) <> (rev IS NULL) OR(after_id IS NULL) <> (after_rev IS NULL) THEN
    RAISE EXCEPTION 'invalid model administration selector' USING ERRCODE = '22023';
  END IF;
  IF profile IS NOT NULL THEN
    IF length(profile) NOT BETWEEN 1 AND 200 OR rev < 1 THEN
      RAISE EXCEPTION 'invalid model profile identity' USING ERRCODE = '22023';
    END IF;
    SELECT coalesce(jsonb_agg(jsonb_build_object('profile_id', mp.id, 'revision', mp.revision, 'purpose', mp.purpose, 'execution', mp.execution, 'provider_id', mp.provider_id, 'model_id', mp.model_id, 'dimension', mp.dimension, 'enabled', mp.enabled, 'adapter_key', mc.adapter_key, 'declaration_digest', mc.declaration_digest, 'limits', mp.limits)), '[]'::jsonb) INTO listed
      FROM cap_ai.model_profiles mp
      LEFT JOIN cap_ai.model_candidates mc ON mc.profile_id = mp.id AND mc.revision = mp.revision
      WHERE mp.id = profile AND mp.revision = rev;
    RETURN listed;
  END IF;
  IF after_id IS NOT NULL AND length(after_id) NOT BETWEEN 1 AND 200 THEN
    RAISE EXCEPTION 'invalid model administration cursor' USING ERRCODE = '22023';
  END IF;
  page := coalesce(page_size, 50);
  IF page NOT BETWEEN 1 AND 100 THEN
    RAISE EXCEPTION 'invalid administration page size' USING ERRCODE = '22023';
  END IF;
  SELECT coalesce(jsonb_agg(q.meta ORDER BY q.id, q.revision), '[]'::jsonb) INTO listed
    FROM(SELECT mp.id, mp.revision, jsonb_build_object('profile_id', mp.id, 'revision', mp.revision, 'purpose', mp.purpose, 'execution', mp.execution, 'provider_id', mp.provider_id, 'model_id', mp.model_id, 'dimension', mp.dimension, 'enabled', mp.enabled, 'adapter_key', mc.adapter_key, 'declaration_digest', mc.declaration_digest, 'limits', mp.limits) meta
    FROM cap_ai.model_profiles mp
    LEFT JOIN cap_ai.model_candidates mc ON mc.profile_id = mp.id AND mc.revision = mp.revision
    WHERE after_id IS NULL OR(mp.id, mp.revision) >(after_id, after_rev)
    ORDER BY mp.id, mp.revision
    LIMIT page) q;
  RETURN listed;
END
$$;

-- Read-only view of the four global slots. An unbound slot, or one whose profile
-- is missing or disabled, reports enabled=false.
CREATE FUNCTION cap_ai.model_admin_slots(actor uuid)
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
DECLARE
  listed jsonb;
BEGIN
  IF NOT coalesce(cap_ai.model_admin_actor(actor), false) THEN
    RAISE EXCEPTION 'model administration session required' USING ERRCODE = '42501';
  END IF;
  SELECT coalesce(jsonb_agg(jsonb_build_object('capability', s.capability, 'slot_revision', s.slot_revision, 'profile_id', s.profile_id, 'profile_revision', s.profile_revision, 'enabled', coalesce(p.enabled, false)) ORDER BY s.capability), '[]'::jsonb) INTO listed
    FROM cap_ai.global_model_slots s
    LEFT JOIN cap_ai.model_profiles p ON p.id = s.profile_id AND p.revision = s.profile_revision
    WHERE s.capability IN('EMBEDDING', 'LLM', 'VLM', 'RERANKING');
  RETURN listed;
END
$$;

-- One audited slot transition. Every check runs before any write, the actor and
-- the slot are locked, and the profile enable, the binding and the event commit
-- in the same transaction.
CREATE FUNCTION cap_ai.model_admin_change_slot(
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

-- Only the dedicated login executes the interfaces; every runtime session and
-- PUBLIC stays closed, and the login receives no direct relation rights.
DO $$
DECLARE
  f record;
  g record;
BEGIN
  FOR f IN SELECT p.oid::regprocedure signature
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'cap_ai' AND p.proname IN('model_admin_session', 'model_admin_actor', 'model_admin_list', 'model_admin_slots', 'model_admin_change_slot') LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner', f.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', f.signature);
    FOR g IN SELECT rolname
      FROM pg_roles
      WHERE rolname = ANY(ARRAY['dxos_process', 'dxos_hasura', 'dxos_authz', 'dxos_hr', 'dxos_reader', 'dxos_caller', 'dxos_module_test', 'dxos_ai_api', 'dxos_ai_worker', 'dxos_ai_dispatcher', 'dxos_ai_gateway', 'dxos_ai_model_catalog', 'dxos_ai_model_admin']) LOOP
      EXECUTE format('REVOKE ALL ON FUNCTION %s FROM %I', f.signature, g.rolname);
    END LOOP;
  END LOOP;
END
$$;

GRANT USAGE ON SCHEMA cap_ai TO dxos_ai_model_admin;

GRANT EXECUTE ON FUNCTION cap_ai.model_admin_session(),
cap_ai.model_admin_actor(uuid),
cap_ai.model_admin_list(uuid, text, bigint, text, bigint, integer),
cap_ai.model_admin_slots(uuid),
cap_ai.model_admin_change_slot(uuid, text, bigint, text, text, bigint, text, jsonb) TO dxos_ai_model_admin;
