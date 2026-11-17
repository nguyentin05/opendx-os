-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Optional. The migration runners gate 0008 and later behind the Agent model flag.

DO $$ BEGIN
  IF to_regclass('cap_ai.embedding_generations') IS NULL
     OR to_regclass('cap_ai.global_model_slots') IS NULL
     OR to_regprocedure('cap_ai.personal_processing_approval(text,bigint,text,text,bigint)') IS NULL THEN
    RAISE EXCEPTION 'personal hybrid search requires migrations 0004, 0005 and 0008';
  END IF;
END $$;

-- APP generations keep NULL here. A legacy PERSONAL generation cannot attest
-- which source authority governed its writes and is ineligible for retrieval.
ALTER TABLE cap_ai.embedding_generations ADD COLUMN personal_source jsonb;

-- Bind source authority to the owner, library, active run, current processing
-- policy and, for hosted execution, the exact admin review.
CREATE FUNCTION cap_ai.personal_embedding_source(run uuid, profile text, revision bigint)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT jsonb_build_object('owner_id', d.owner_user_id, 'app_id', c.app_id,
                          'scope', authority.scope, 'dimension', mp.dimension,
                          'approval_revision', approval.revision)
FROM cap_ai.processing_runs r
JOIN cap_ai.document_versions v ON v.id = r.version_id AND v.active_run_id = r.id
JOIN cap_ai.knowledge_documents d ON d.id = v.document_id AND d.active_version_id = v.id
JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
JOIN cap_ai.model_profiles mp ON mp.id = profile AND mp.revision = $3
JOIN cap_ai.processing_policies p ON p.id = d.processing_policy_id AND p.version = d.processing_policy_version
CROSS JOIN LATERAL (
  SELECT cap_ai.read_upload_document(d.owner_user_id, ur.role_name, d.id) AS scope
  FROM core.user_roles ur
  WHERE ur.user_id = d.owner_user_id
    AND cap_ai.read_upload_document(d.owner_user_id, ur.role_name, d.id) IS NOT NULL
  LIMIT 1
) authority
CROSS JOIN LATERAL (
  SELECT cap_ai.personal_processing_approval(profile, $3, c.app_id, p.id, p.version) AS revision
) approval
WHERE r.id = run AND r.state = 'COMPLETE' AND v.state = 'READY'
  AND d.source_kind = 'UPLOAD' AND c.kind = 'PERSONAL' AND d.owner_user_id = c.owner_user_id
  AND p.enabled AND p.execution = 'LOCAL_ONLY' AND p.classification = 'PERSONAL'
  AND mp.enabled AND mp.purpose = 'EMBED' AND mp.dimension > 0
  AND ((mp.execution = 'HOSTED' AND approval.revision IS NOT NULL)
       OR (mp.execution = 'LOCAL' AND EXISTS (
         SELECT FROM cap_ai.processing_policy_profiles pp
         WHERE pp.policy_id = p.id AND pp.policy_version = p.version
           AND pp.model_profile_id = profile AND pp.profile_revision = $3
       )))
$$;

-- Until the first work call creates the generation, the current eligible source
-- is sufficient. Every subsequent lease/work/dispatch must match its pinned basis.
CREATE FUNCTION cap_ai.personal_embedding_lease_source(run uuid, profile text, revision bigint)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT current_source
FROM cap_ai.personal_embedding_source(run, profile, revision) current_source
WHERE current_source IS NOT NULL AND NOT EXISTS (
  SELECT FROM cap_ai.embedding_generations g
  WHERE g.run_id = run AND g.model_profile_id = profile AND g.profile_revision = revision
    AND (g.personal_source IS DISTINCT FROM current_source OR g.state IN ('FAILED', 'RETIRED'))
)
$$;

-- API requests are always actor-scoped. The worker can schedule eligible owners.
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
  INSERT INTO cap_ai.ingestion_jobs(version_id, kind, profile_key) SELECT v.id, 'EMBED', profile || '@' || rev
    FROM cap_ai.document_versions v
    JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
    JOIN cap_ai.processing_policies p ON p.id = d.processing_policy_id AND p.version = d.processing_policy_version
    JOIN cap_ai.model_profiles mp ON mp.id = profile AND mp.revision = rev
    WHERE d.active_version_id = v.id AND d.desired_version_id = v.id AND v.active_run_id IS NOT NULL
      AND cap_ai.eligible_version(v.id) AND mp.purpose = 'EMBED' AND mp.enabled AND p.enabled
      AND CASE WHEN d.source_kind = 'UPLOAD' THEN
        cap_ai.personal_embedding_lease_source(v.active_run_id, profile, rev) IS NOT NULL
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

CREATE OR REPLACE FUNCTION cap_ai.claim_embedding(worker text)
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
    AND (d.source_kind <> 'UPLOAD' OR EXISTS (
      SELECT FROM cap_ai.model_profiles mp
      WHERE job.profile_key = mp.id || '@' || mp.revision
        AND cap_ai.personal_embedding_lease_source(v.active_run_id, mp.id, mp.revision) IS NOT NULL
    ))
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
    FROM cap_ai.model_profiles
    WHERE id = profile AND revision = rev AND enabled AND purpose = 'EMBED';
  IF NOT FOUND OR NOT EXISTS(SELECT FROM cap_ai.ingestion_jobs
    WHERE id = job AND profile_key = profile || '@' || rev) THEN
    RETURN '{}'::jsonb;
  END IF;
  personal := d.access_mode = 'PRIVATE' OR d.owner_user_id IS NOT NULL;
  IF personal THEN
    personal_source := cap_ai.personal_embedding_lease_source(run, profile, rev);
    IF personal_source IS NULL OR personal_source->'scope' IS DISTINCT FROM scope THEN
      RETURN '{}'::jsonb;
    END IF;
    personal_approval := (personal_source->>'approval_revision')::bigint;
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
  personal_source jsonb;
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
  IF scope->>'source_incarnation' = '' THEN
    personal_source := cap_ai.personal_embedding_lease_source(run, profile, rev);
    IF personal_source IS NULL OR personal_source->'scope' IS DISTINCT FROM scope THEN
      RAISE EXCEPTION 'embedding authority unavailable' USING ERRCODE = 'P0410';
    END IF;
  END IF;
  INSERT INTO cap_ai.embedding_generations(run_id, model_profile_id, profile_revision, dimension, expected_count, job_id, fencing_token, personal_source)
    SELECT run, profile, rev, mp.dimension, count(*), job, fence, personal_source
    FROM cap_ai.knowledge_chunks
    WHERE run_id = run ON CONFLICT(run_id, model_profile_id, profile_revision) DO NOTHING;
  SELECT * INTO STRICT g
    FROM cap_ai.embedding_generations
    WHERE run_id = run AND model_profile_id = profile AND profile_revision = rev FOR UPDATE;
  IF g.state IN('FAILED', 'RETIRED') OR g.personal_source IS DISTINCT FROM personal_source
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

-- A fresh source decision is required after each model response and again
-- before a complete generation can transition to READY.
CREATE FUNCTION cap_ai.embedding_generation_authorized(g cap_ai.embedding_generations)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT coalesce((
  SELECT CASE WHEN v.source_kind = 'UPLOAD' THEN
    g.dimension = (g.personal_source->>'dimension')::integer AND g.personal_source =
      cap_ai.personal_embedding_source(g.run_id, g.model_profile_id, g.profile_revision)
  ELSE true END
  FROM cap_ai.processing_runs r
  JOIN cap_ai.document_versions v ON v.id = r.version_id
  WHERE r.id = g.run_id
), false)
$$;

CREATE OR REPLACE FUNCTION cap_ai.save_embedding(
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
  IF NOT cap_ai.embedding_lease_valid(job, worker, fence, run) OR g.run_id IS DISTINCT FROM run
    OR g.job_id IS DISTINCT FROM job OR g.fencing_token IS DISTINCT FROM fence
    OR g.state <> 'BUILDING' OR public.vector_dims(value) IS DISTINCT FROM g.dimension
    OR NOT cap_ai.embedding_generation_authorized(g) OR NOT EXISTS(SELECT FROM cap_ai.knowledge_chunks
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

CREATE OR REPLACE FUNCTION cap_ai.finish_embedding(
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
  IF NOT cap_ai.embedding_lease_valid(job, worker, fence, run) OR g.run_id IS DISTINCT FROM run
    OR g.job_id IS DISTINCT FROM job OR g.fencing_token IS DISTINCT FROM fence
    THEN
    RAISE EXCEPTION 'stale generation' USING ERRCODE = 'P0410';
  END IF;
  IF success THEN
    IF NOT cap_ai.embedding_generation_authorized(g) THEN
      RAISE EXCEPTION 'embedding authority unavailable' USING ERRCODE = 'P0410';
    END IF;
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

-- HYBRID asks for up to 50 lexical candidates and applies the per-document
-- cap after fusion. Ordinary FTS callers keep their existing page semantics.
CREATE OR REPLACE FUNCTION cap_ai.search_personal(
    actor uuid,
    acting_role text,
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
  WHERE d.owner_user_id = actor AND d.state = 'LIVE' AND c.kind = 'PERSONAL' AND c.status = 'ACTIVE' AND v.state = 'READY'
  AND r.state = 'COMPLETE' AND(coalesce(cardinality(collections), 0) = 0 OR c.id = ANY(collections)) AND cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL), candidates AS(SELECT a.*, k.id chunk_id, ts_rank_cd(k.search_vector, websearch_to_tsquery('simple', normalize(query, NFC))) score
  FROM authorized_versions a
  JOIN cap_ai.knowledge_chunks k ON k.run_id = a.run_id
  WHERE k.search_vector @@ websearch_to_tsquery('simple', normalize(query, NFC))
  ORDER BY score DESC, a.document_id, k.id
  LIMIT 50), diversified AS(SELECT *, row_number() OVER(PARTITION BY document_id ORDER BY score DESC, chunk_id) doc_rank
  FROM candidates), chosen AS(SELECT *
  FROM diversified
  WHERE page_size > 20 OR doc_rank <= 4
  ORDER BY score DESC, document_id, chunk_id
  LIMIT least(greatest(page_size, 1), 50)) SELECT coalesce(jsonb_agg(jsonb_build_object('hit', cap_ai.citation_data(actor, acting_role, document_id, version_id, chunk_id), 'scope', cap_ai.read_document(actor, acting_role, document_id)) ORDER BY score DESC, document_id, chunk_id), '[]'::jsonb)
  FROM chosen
$$;

CREATE FUNCTION cap_ai.personal_vector_query_revision(actor uuid, acting_role text, profile text, revision bigint)
RETURNS bigint
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT CASE WHEN mp.execution = 'HOSTED' THEN
         cap_ai.personal_processing_approval(profile, revision, library.app_id, NULL, NULL)
       ELSE 0 END
FROM cap_ai.model_profiles mp
CROSS JOIN LATERAL (SELECT cap_ai.library_app(actor, acting_role) AS app_id) library
WHERE mp.id = profile AND mp.revision = $4 AND mp.enabled AND mp.purpose = 'EMBED'
  AND mp.dimension > 0 AND library.app_id IS NOT NULL
$$;

CREATE FUNCTION cap_ai.personal_vector_generations(actor uuid, acting_role text, profile text, revision bigint, collections uuid[])
RETURNS TABLE(document_id uuid, version_id uuid, run_id uuid, generation_id uuid, dimension integer, scope jsonb)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT d.id, v.id, r.id, g.id, g.dimension, authority.scope
FROM cap_ai.knowledge_documents d
JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
JOIN cap_ai.document_versions v ON v.id = d.active_version_id
JOIN cap_ai.processing_runs r ON r.id = v.active_run_id
JOIN cap_ai.embedding_generations g ON g.run_id = r.id
JOIN cap_ai.model_profiles mp ON mp.id = g.model_profile_id AND mp.revision = g.profile_revision
CROSS JOIN LATERAL (SELECT cap_ai.read_upload_document(actor, acting_role, d.id) AS scope) authority
WHERE d.owner_user_id = actor AND c.owner_user_id = actor AND c.kind = 'PERSONAL'
  AND (coalesce(cardinality(collections), 0) = 0 OR c.id = ANY(collections))
  AND authority.scope IS NOT NULL AND g.personal_source->'scope' = authority.scope
  AND v.state = 'READY' AND r.state = 'COMPLETE' AND g.state = 'READY'
  AND g.completed_count = g.expected_count AND g.expected_count = r.chunk_count
  AND g.model_profile_id = profile AND g.profile_revision = $4 AND g.dimension = mp.dimension
  AND cap_ai.embedding_generation_authorized(g)
$$;

-- NULL means the request has no authorized READY generation or query authority.
-- A local profile uses 0. A hosted profile uses its current query review revision.
CREATE FUNCTION cap_ai.personal_vector_readiness(actor uuid, acting_role text, profile text, revision bigint, collections uuid[])
RETURNS bigint
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, cap_ai
AS $$
SELECT cap_ai.personal_vector_query_revision(actor, acting_role, profile, revision)
WHERE EXISTS (SELECT FROM cap_ai.personal_vector_generations(actor, acting_role, profile, revision, collections))
$$;

CREATE FUNCTION cap_ai.search_personal_vectors(actor uuid, acting_role text, profile text, revision bigint, query public.vector, collections uuid[])
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
WITH authorized AS MATERIALIZED (
  SELECT *
  FROM cap_ai.personal_vector_generations(actor, acting_role, profile, revision, collections) g
  WHERE cap_ai.personal_vector_query_revision(actor, acting_role, profile, revision) IS NOT NULL
    AND g.dimension = public.vector_dims(query) AND public.vector_norm(query) > 0
), vectors AS MATERIALIZED (
  SELECT a.document_id, a.version_id, a.scope, e.chunk_id, e.embedding
  FROM authorized a
  JOIN cap_ai.chunk_embeddings e ON e.generation_id = a.generation_id AND e.run_id = a.run_id
  JOIN cap_ai.knowledge_chunks k ON k.id = e.chunk_id AND k.run_id = e.run_id AND k.content_hash = e.content_hash
  WHERE public.vector_dims(e.embedding) = a.dimension AND public.vector_norm(e.embedding) > 0
), ranked AS (
  SELECT document_id, version_id, scope, chunk_id, embedding OPERATOR(public.<=>) query AS distance
  FROM vectors
  ORDER BY distance, document_id, chunk_id
  LIMIT 50
)
SELECT coalesce((
  SELECT jsonb_agg(jsonb_build_object(
    'hit', cap_ai.citation_data(actor, acting_role, document_id, version_id, chunk_id), 'scope', scope)
    ORDER BY distance, document_id, chunk_id)
  FROM ranked
), '[]'::jsonb)
WHERE EXISTS (SELECT FROM authorized)
$$;

DO $$ DECLARE f record; BEGIN
  FOR f IN SELECT p.oid::regprocedure signature FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'cap_ai' AND p.proname IN (
      'personal_embedding_source', 'personal_embedding_lease_source', 'embedding_generation_authorized',
      'personal_vector_query_revision', 'personal_vector_generations', 'personal_vector_readiness', 'search_personal_vectors'
    ) LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner', f.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', f.signature);
  END LOOP;
END $$;
GRANT EXECUTE ON FUNCTION cap_ai.personal_vector_readiness(uuid, text, text, bigint, uuid[]),
  cap_ai.search_personal_vectors(uuid, text, text, bigint, public.vector, uuid[]) TO dxos_ai_api;
