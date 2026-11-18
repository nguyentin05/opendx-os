-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

CREATE FUNCTION cap_ai.agent_model_gateway_session() RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog AS $$
SELECT session_user = 'dxos_ai_gateway'
  AND current_setting('role') IN ('none', 'dxos_ai_gateway')
  AND EXISTS (SELECT FROM pg_roles WHERE rolname = session_user
    AND NOT (rolsuper OR rolbypassrls OR rolcreaterole))
$$;

-- Query-only authority for global retrieval. Keep the ordinary scope and
-- profile checks and classify every hosted global query as potentially PERSONAL.
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
    OR app IS NULL OR acting_role IS NULL THEN
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

-- Bill a single generation to the configured consumer, retaining each source's
-- current READ and processing decision under the role that gave it access.
CREATE FUNCTION cap_ai.agent_model_policy(
    actor uuid, billing_app text, profile text, revision bigint, slot_revision bigint, proofs jsonb
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

  -- Each Gateway check must see the same active binding as the caller resolved.
  -- An enabled profile alone may remain usable elsewhere after slot withdrawal.
  SELECT mp_row.* INTO mp FROM cap_ai.global_model_slots slot
    JOIN cap_ai.model_profiles mp_row ON mp_row.id = slot.profile_id AND mp_row.revision = slot.profile_revision
    WHERE slot.capability = 'LLM' AND slot.slot_revision = $5 AND $5 > 0
      AND mp_row.id = $3 AND mp_row.revision = $4 AND mp_row.enabled AND mp_row.purpose = 'GENERATE';
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

ALTER FUNCTION cap_ai.agent_model_gateway_session() OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint) OWNER TO dxos_ai_owner;
ALTER FUNCTION cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.agent_model_gateway_session(),
  cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint),
  cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION cap_ai.agent_query_model_policy(uuid,text,text,text,text,bigint),
  cap_ai.agent_model_policy(uuid,text,text,bigint,bigint,jsonb) TO dxos_ai_gateway;
