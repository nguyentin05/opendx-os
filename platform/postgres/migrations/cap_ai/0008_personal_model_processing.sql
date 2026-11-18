-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Optional, explicit model/registry prerequisites; no enabled approval is seeded.
DO $$ BEGIN
  IF to_regclass('cap_ai.model_profiles') IS NULL OR to_regclass('cap_ai.global_model_slots') IS NULL THEN
    RAISE EXCEPTION 'personal processing requires migrations 0004 and 0005';
  END IF;
END $$;

CREATE TABLE cap_ai.agent_processing_approvals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    revision bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    review_digest bytea NOT NULL CHECK (octet_length(review_digest) = 32),
    profile_id text NOT NULL,
    profile_revision bigint NOT NULL,
    provider_id text NOT NULL,
    purpose text NOT NULL CHECK (purpose IN ('EMBED','GENERATE')),
    source_policy_id text NOT NULL,
    source_policy_version bigint NOT NULL,
    source_classification text NOT NULL CHECK (source_classification = 'PERSONAL'),
    query_app_id text NOT NULL REFERENCES core.apps(app_id),
    query_classification text NOT NULL CHECK (query_classification = 'PERSONAL'),
    retention_evidence text NOT NULL CHECK (length(btrim(retention_evidence)) BETWEEN 1 AND 4000),
    training_use_evidence text NOT NULL CHECK (length(btrim(training_use_evidence)) BETWEEN 1 AND 4000),
    region_evidence text NOT NULL CHECK (length(btrim(region_evidence)) BETWEEN 1 AND 4000),
    approved_by uuid NOT NULL REFERENCES core.users(id),
    approved_at timestamptz NOT NULL DEFAULT now(),
    approved_session text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    FOREIGN KEY (profile_id,profile_revision) REFERENCES cap_ai.model_profiles(id,revision),
    FOREIGN KEY (source_policy_id,source_policy_version) REFERENCES cap_ai.processing_policies(id,version)
);
CREATE UNIQUE INDEX agent_processing_active_review ON cap_ai.agent_processing_approvals (review_digest)
WHERE enabled;

CREATE TABLE cap_ai.agent_processing_approval_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    approval_id uuid NOT NULL REFERENCES cap_ai.agent_processing_approvals(id),
    action text NOT NULL CHECK (action IN ('APPROVE','DISABLE')),
    actor_id uuid NOT NULL REFERENCES core.users(id),
    session_identity text NOT NULL,
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 4000),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (approval_id,action)
);
CREATE FUNCTION cap_ai.guard_agent_processing_approval() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
  IF TG_OP = 'DELETE' OR (to_jsonb(NEW)-'enabled') IS DISTINCT FROM (to_jsonb(OLD)-'enabled')
    OR NOT OLD.enabled OR NEW.enabled THEN
    RAISE EXCEPTION 'immutable processing approval; create a new reviewed revision' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER agent_processing_approval_history BEFORE UPDATE OR DELETE ON cap_ai.agent_processing_approvals
FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_agent_processing_approval();
CREATE TRIGGER agent_processing_event_history BEFORE UPDATE OR DELETE ON cap_ai.agent_processing_approval_events
FOR EACH ROW EXECUTE FUNCTION cap_ai.registry_guard_history();

CREATE FUNCTION cap_ai.agent_processing_admin(actor uuid) RETURNS void
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = pg_catalog,cap_ai AS $$
BEGIN
  IF session_user <> 'dxos_authz' OR current_setting('role') NOT IN ('none','dxos_authz')
    OR EXISTS (SELECT FROM pg_roles WHERE rolname=session_user AND (rolsuper OR rolbypassrls OR rolcreaterole))
    OR NOT EXISTS (SELECT FROM core.users u JOIN core.user_roles ur ON ur.user_id=u.id
      WHERE u.id=actor AND u.status='ACTIVE' AND ur.role_name='role_operator') THEN
    RAISE EXCEPTION 'platform administration authority required' USING ERRCODE='42501';
  END IF;
END $$;

CREATE FUNCTION cap_ai.approve_agent_processing(actor uuid, review jsonb) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog,cap_ai AS $$
DECLARE approval uuid; field text; review_key bytea;
BEGIN
  PERFORM cap_ai.agent_processing_admin(actor);
  IF review IS NULL OR jsonb_typeof(review) <> 'object'
    OR NOT review ?& ARRAY['profile_id','profile_revision','provider_id','purpose','source_policy_id','source_policy_version','source_classification','query_app_id','query_classification','retention_evidence','training_use_evidence','region_evidence']
    OR review - ARRAY['profile_id','profile_revision','provider_id','purpose','source_policy_id','source_policy_version','source_classification','query_app_id','query_classification','retention_evidence','training_use_evidence','region_evidence'] <> '{}'::jsonb THEN
    RAISE EXCEPTION 'invalid processing review' USING ERRCODE='22023';
  END IF;
  FOREACH field IN ARRAY ARRAY['profile_id','provider_id','purpose','source_policy_id','source_classification','query_app_id','query_classification','retention_evidence','training_use_evidence','region_evidence'] LOOP
    IF jsonb_typeof(review->field) IS DISTINCT FROM 'string' OR length(btrim(review->>field)) NOT BETWEEN 1 AND 4000 THEN
      RAISE EXCEPTION 'missing reviewed evidence or classification' USING ERRCODE='22023';
    END IF;
  END LOOP;
  IF jsonb_typeof(review->'profile_revision') IS DISTINCT FROM 'number'
    OR jsonb_typeof(review->'source_policy_version') IS DISTINCT FROM 'number'
    OR (review->>'profile_revision') !~ '^[1-9][0-9]{0,17}$'
    OR (review->>'source_policy_version') !~ '^[1-9][0-9]{0,17}$'
    OR review->>'source_classification' <> 'PERSONAL' OR review->>'query_classification' <> 'PERSONAL' THEN
    RAISE EXCEPTION 'invalid processing review' USING ERRCODE='22023';
  END IF;
  -- A committed review can be recovered even if its dependencies have since
  -- stopped qualifying for processing. JSONB normalizes the field order.
  review_key := sha256(convert_to(review::text, 'UTF8'));
  SELECT id INTO approval FROM cap_ai.agent_processing_approvals
    WHERE review_digest=review_key AND enabled FOR SHARE;
  IF FOUND THEN RETURN approval; END IF;

  IF NOT EXISTS (SELECT FROM cap_ai.model_profiles mp WHERE mp.id=review->>'profile_id'
      AND mp.revision=(review->>'profile_revision')::bigint AND mp.provider_id=review->>'provider_id'
      AND mp.purpose=review->>'purpose' AND mp.purpose IN ('EMBED','GENERATE') AND mp.execution='HOSTED')
    OR NOT EXISTS (SELECT FROM cap_ai.processing_policies p WHERE p.id=review->>'source_policy_id'
      AND p.version=(review->>'source_policy_version')::bigint AND p.execution='LOCAL_ONLY' AND p.classification='PERSONAL' AND p.enabled)
    OR NOT EXISTS (SELECT FROM core.apps WHERE app_id=review->>'query_app_id' AND status='Ready') THEN
    RAISE EXCEPTION 'processing review does not match authority' USING ERRCODE='22023';
  END IF;
  INSERT INTO cap_ai.agent_processing_approvals(review_digest,profile_id,profile_revision,provider_id,purpose,source_policy_id,source_policy_version,source_classification,query_app_id,query_classification,retention_evidence,training_use_evidence,region_evidence,approved_by,approved_session)
  VALUES(review_key,review->>'profile_id',(review->>'profile_revision')::bigint,review->>'provider_id',review->>'purpose',review->>'source_policy_id',(review->>'source_policy_version')::bigint,review->>'source_classification',review->>'query_app_id',review->>'query_classification',review->>'retention_evidence',review->>'training_use_evidence',review->>'region_evidence',actor,session_user)
  ON CONFLICT (review_digest) WHERE enabled DO NOTHING
  RETURNING id INTO approval;
  IF NOT FOUND THEN
    -- A concurrent identical request won the insert and recorded the audit.
    SELECT id INTO approval FROM cap_ai.agent_processing_approvals
      WHERE review_digest=review_key AND enabled FOR SHARE;
    IF FOUND THEN RETURN approval; END IF;
    RAISE EXCEPTION 'processing review changed concurrently' USING ERRCODE='40001';
  END IF;
  INSERT INTO cap_ai.agent_processing_approval_events(approval_id,action,actor_id,session_identity,reason)
  VALUES(approval,'APPROVE',actor,session_user,'reviewed retention, training use and processing region');
  RETURN approval;
END $$;
CREATE FUNCTION cap_ai.disable_agent_processing(actor uuid, approval uuid, reason text) RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog,cap_ai AS $$
BEGIN
  PERFORM cap_ai.agent_processing_admin(actor);
  IF reason IS NULL OR length(btrim(reason)) NOT BETWEEN 1 AND 4000 THEN
    RAISE EXCEPTION 'disable reason required' USING ERRCODE='22023';
  END IF;
  UPDATE cap_ai.agent_processing_approvals SET enabled=false WHERE id=approval AND enabled;
  IF NOT FOUND THEN RETURN false; END IF;
  INSERT INTO cap_ai.agent_processing_approval_events(approval_id,action,actor_id,session_identity,reason)
  VALUES(approval,'DISABLE',actor,session_user,reason);
  RETURN true;
END $$;

-- Internal authority only. A NULL source policy means query-only classification;
-- source callers must additionally match their exact LOCAL_ONLY policy revision.
CREATE FUNCTION cap_ai.personal_processing_approval(profile text, rev bigint, app text, policy text, policy_version bigint)
RETURNS bigint LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog,cap_ai AS $$
SELECT max(a.revision) FROM cap_ai.agent_processing_approvals a
JOIN cap_ai.model_profiles mp ON mp.id=a.profile_id AND mp.revision=a.profile_revision
JOIN cap_ai.processing_policies p ON p.id=a.source_policy_id AND p.version=a.source_policy_version
JOIN core.apps app_row ON app_row.app_id=a.query_app_id AND app_row.status='Ready'
WHERE a.enabled AND mp.enabled AND p.enabled AND mp.execution='HOSTED' AND mp.purpose IN ('EMBED','GENERATE')
AND a.profile_id=profile AND a.profile_revision=rev AND a.provider_id=mp.provider_id AND a.purpose=mp.purpose
AND a.query_app_id=app AND a.query_classification='PERSONAL' AND a.source_classification=p.classification
AND p.classification='PERSONAL' AND p.execution='LOCAL_ONLY'
AND (policy IS NULL OR (a.source_policy_id=policy AND a.source_policy_version=policy_version))
$$;

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
    FROM cap_ai.model_profiles
    WHERE id = profile AND revision = rev AND enabled AND purpose = 'EMBED';
  IF NOT FOUND OR NOT EXISTS(SELECT FROM cap_ai.ingestion_jobs
    WHERE id = job AND profile_key = profile || '@' || rev) THEN
    RETURN '{}'::jsonb;
  END IF;
  personal := d.access_mode = 'PRIVATE' OR d.owner_user_id IS NOT NULL;
  IF mp.execution = 'HOSTED' AND personal THEN
    personal_approval := cap_ai.personal_processing_approval(profile, rev, app, p.id, p.version);
    -- The worker lease delegates processing only while the owner still has READ.
    IF personal_approval IS NULL OR NOT EXISTS (SELECT FROM core.user_roles ur
      WHERE ur.user_id=d.owner_user_id AND cap_ai.read_document(d.owner_user_id,ur.role_name,d.id)=scope) THEN
      RETURN '{}'::jsonb;
    END IF;
  ELSE
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
  INSERT INTO cap_ai.ingestion_jobs(version_id, kind, profile_key) SELECT v.id, 'EMBED', profile || '@' || rev
    FROM cap_ai.document_versions v
    JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
    JOIN cap_ai.processing_policies p ON p.id = d.processing_policy_id AND p.version = d.processing_policy_version
    LEFT JOIN cap_ai.processing_policy_profiles pp ON pp.policy_id = p.id AND pp.policy_version = p.version
    JOIN cap_ai.model_profiles mp ON mp.id = profile AND mp.revision = rev
    JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id
    WHERE d.active_version_id = v.id AND d.desired_version_id = v.id AND v.active_run_id IS NOT NULL AND cap_ai.eligible_version(v.id)
    AND mp.id = profile AND mp.revision = rev AND mp.purpose = 'EMBED' AND mp.enabled AND p.enabled AND((mp.execution = 'LOCAL' AND pp.model_profile_id=profile AND pp.profile_revision=rev)
    OR(mp.execution='HOSTED' AND d.access_mode='PRIVATE' AND cap_ai.personal_processing_approval(profile,rev,c.app_id,p.id,p.version) IS NOT NULL)
    OR(pp.model_profile_id=profile AND pp.profile_revision=rev AND d.access_mode <> 'PRIVATE' AND p.execution = 'APPROVED_PROVIDER' AND p.classification = 'NON_PERSONAL' AND p.retention_approved
    AND p.provider_id = mp.provider_id AND p.approved_at IS NOT NULL AND p.approved_by IS NOT NULL)) AND(actor IS NULL
    OR cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL) ON CONFLICT(version_id, kind, profile_key) DO NOTHING;
  RETURN true;
END
$$;

ALTER TABLE cap_ai.agent_processing_approvals OWNER TO dxos_ai_owner;
ALTER TABLE cap_ai.agent_processing_approval_events OWNER TO dxos_ai_owner;
REVOKE ALL ON cap_ai.agent_processing_approvals,cap_ai.agent_processing_approval_events FROM PUBLIC,dxos_authz,dxos_ai_api,dxos_ai_worker,dxos_ai_gateway,dxos_ai_model_catalog;
DO $$ DECLARE f record; BEGIN
  FOR f IN SELECT p.oid::regprocedure signature FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE n.nspname='cap_ai' AND p.proname IN ('guard_agent_processing_approval','agent_processing_admin','approve_agent_processing','disable_agent_processing','personal_processing_approval') LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner',f.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC',f.signature);
  END LOOP;
END $$;
GRANT USAGE ON SCHEMA cap_ai TO dxos_authz;
GRANT EXECUTE ON FUNCTION cap_ai.approve_agent_processing(uuid,jsonb),cap_ai.disable_agent_processing(uuid,uuid,text) TO dxos_authz;
