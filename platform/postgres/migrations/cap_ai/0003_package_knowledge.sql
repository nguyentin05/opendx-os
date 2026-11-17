-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Core remains the authority even while package notification delivery is stopped.

CREATE FUNCTION cap_ai.package_snapshot(
    app text, generation bigint, spec_digest text
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('status', a.status, 'spec', a.spec)
  FROM core.apps a
  WHERE a.app_id = app AND a.status IN('Ready', 'Removed') AND a.publication_generation = generation AND(a.spec #>> '{_publication,generation}')::bigint = generation
  AND a.spec #>> '{_publication,spec_digest}' = spec_digest
$$;

CREATE FUNCTION cap_ai.current_package_source(app text, source text)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT s.value || jsonb_build_object('access', decl.value->'access', 'publication_generation', a.publication_generation)
  FROM core.apps a
  CROSS JOIN LATERAL jsonb_array_elements(a.spec #> '{_publication,sources}') s
  CROSS JOIN LATERAL jsonb_array_elements(a.spec #> '{knowledge,sources}') decl
  WHERE a.app_id = app AND a.status = 'Ready' AND a.spec #>> '{knowledge,version}' = '1' AND(a.spec #>> '{_publication,generation}')::bigint = a.publication_generation
  AND s.value->>'source_id' = source AND decl.value->>'id' = source AND s.value->>'safety_state' = 'APPROVED' AND s.value->>'source_path' = decl.value->>'path'
  AND s.value->>'parser_profile' = decl.value->>'indexingProfile' AND s.value->>'media_type' = decl.value->>'mediaType'
  AND EXISTS(SELECT FROM core.app_capabilities ac
  JOIN core.capabilities c ON c.cap_id = ac.cap_id
  WHERE ac.app_id = app AND c.cap_id = 'cap-ai' AND c.enabled) AND NOT EXISTS(SELECT FROM core.app_capabilities ac
  JOIN core.capabilities c ON c.cap_id = ac.cap_id
  WHERE ac.app_id = app AND NOT c.enabled)
$$;

CREATE FUNCTION cap_ai.package_version_current(ver uuid, desired boolean)
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
  AND r.size_bytes = v.size_bytes AND p.enabled AND p.execution = 'LOCAL_ONLY' AND(NOT desired OR(d.desired_version_id = v.id
  AND v.sha256 = current_source->>'sha256' AND v.parser_profile = current_source->>'parser_profile')))
$$;

CREATE FUNCTION cap_ai.reconcile_package(
    app text, generation bigint, wanted_digest text
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  a core.apps;
  pub jsonb;
  source jsonb;
  decl jsonb;
  c uuid;
  s cap_ai.package_sources;
  r cap_ai.package_source_revisions;
  d cap_ai.knowledge_documents;
  v cap_ai.document_versions;
  new_incarnation boolean;
  changed boolean;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtext('dxos.operator.publication'));
  SELECT * INTO a
    FROM core.apps
    WHERE app_id = app;
  IF NOT FOUND OR a.status NOT IN('Ready', 'Removed') OR a.publication_generation <> generation OR(a.spec #>> '{_publication,spec_digest}') IS DISTINCT FROM wanted_digest
    OR(a.spec #>> '{_publication,generation}')::bigint IS DISTINCT FROM generation THEN
    RAISE EXCEPTION 'stale publication' USING ERRCODE = 'P0409';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai.package:' || app, 0));
  IF EXISTS(SELECT FROM cap_ai.package_publications
    WHERE app_id = app AND publication_generation = generation AND spec_digest = wanted_digest AND state = 'APPLIED')
    THEN
    RETURN true;
  END IF;
  pub := a.spec->'_publication';
  INSERT INTO cap_ai.knowledge_collections(app_id, kind)
    VALUES(app, 'APP') ON CONFLICT(app_id)
    WHERE kind = 'APP' DO NOTHING;
  SELECT id INTO STRICT c
    FROM cap_ai.knowledge_collections
    WHERE app_id = app AND kind = 'APP';
  UPDATE cap_ai.package_sources SET state = 'REMOVED', last_publication_generation = generation, updated_at = now()
    WHERE app_id = app AND(a.status = 'Removed' OR NOT EXISTS(SELECT FROM jsonb_array_elements(pub->'sources') x WHERE x->>'source_id' = source_id));
  IF a.status = 'Ready' THEN
    FOR source IN SELECT value
      FROM jsonb_array_elements(pub->'sources') LOOP
      SELECT value INTO decl
        FROM jsonb_array_elements(a.spec #> '{knowledge,sources}')
        WHERE value->>'id' = source->>'source_id';
      IF decl IS NULL OR cap_ai.current_package_source(app, source->>'source_id') IS NULL THEN
        RAISE EXCEPTION 'source unavailable' USING ERRCODE = 'P0404';
      END IF;
      SELECT * INTO s
        FROM cap_ai.package_sources
        WHERE app_id = app AND source_id = source->>'source_id' FOR UPDATE;
      new_incarnation := NOT FOUND OR s.incarnation <> (source->>'incarnation')::uuid;
      changed := new_incarnation OR s.authorization_generation IS DISTINCT FROM(source->>'authorization_generation')::bigint;
      INSERT INTO cap_ai.package_sources(app_id, source_id, incarnation, authorization_generation, declaration_hash, last_publication_generation)
        VALUES(app, source->>'source_id', (source->>'incarnation')::uuid, (source->>'authorization_generation')::bigint, source->>'declaration_hash', generation)
        ON CONFLICT(app_id, source_id) DO UPDATE SET incarnation = EXCLUDED.incarnation, authorization_generation = EXCLUDED.authorization_generation, declaration_hash = EXCLUDED.declaration_hash, last_publication_generation = generation, state = 'DECLARED', updated_at = now()
        RETURNING * INTO s;
      INSERT INTO cap_ai.package_source_revisions(package_source_id, incarnation, publication_generation, package_digest, source_path, sha256, media_type, size_bytes, artifact_ref, safety_state, parser_profile)
        VALUES(s.id, s.incarnation, generation, pub->>'package_digest', source->>'source_path', source->>'sha256', source->>'media_type', (source->>'size_bytes')::bigint, source->>'artifact_ref', 'APPROVED', source->>'parser_profile')
        ON CONFLICT(package_source_id, incarnation, package_digest, sha256, parser_profile) DO NOTHING;
      SELECT * INTO STRICT r
        FROM cap_ai.package_source_revisions
        WHERE package_source_id = s.id AND incarnation = s.incarnation AND package_digest = pub->>'package_digest'
        AND sha256 = source->>'sha256' AND parser_profile = source->>'parser_profile';
      UPDATE cap_ai.package_sources SET desired_revision_id = r.id
        WHERE id = s.id;
      INSERT INTO cap_ai.knowledge_documents(collection_id, source_kind, package_source_id, title, access_mode, processing_policy_id, processing_policy_version, authorization_generation)
        VALUES(c, 'PACKAGE', s.id, source->>'source_id', decl #>> '{access,mode}', 'local-only', 1, s.authorization_generation)
        ON CONFLICT(package_source_id) DO NOTHING;
      SELECT * INTO STRICT d
        FROM cap_ai.knowledge_documents
        WHERE package_source_id = s.id FOR UPDATE;
      UPDATE cap_ai.knowledge_documents SET access_mode = decl #>> '{access,mode}', authorization_generation = s.authorization_generation, policy_revision = policy_revision + CASE
        WHEN changed THEN 1 ELSE 0 END, revision = revision + CASE WHEN changed THEN 1 ELSE 0 END, active_version_id = CASE
        WHEN new_incarnation THEN NULL ELSE active_version_id END, updated_at = now()
        WHERE id = d.id;
      DELETE
        FROM cap_ai.document_allowed_roles
        WHERE document_id = d.id;
      INSERT INTO cap_ai.document_allowed_roles(document_id, role_name) SELECT d.id, value
        FROM jsonb_array_elements_text(decl #> '{access,allowedRoles}');
      SELECT * INTO v
        FROM cap_ai.document_versions
        WHERE document_id = d.id AND package_revision_id = r.id AND parser_profile = r.parser_profile;
      IF NOT FOUND THEN
        INSERT INTO cap_ai.document_versions(document_id, version_no, source_kind, package_revision_id, sha256, media_type, size_bytes, parser_profile)
          VALUES(d.id, (SELECT coalesce(max(version_no), 0) + 1 FROM cap_ai.document_versions WHERE document_id = d.id), 'PACKAGE', r.id, r.sha256, r.media_type, r.size_bytes, r.parser_profile)
          RETURNING * INTO v;
      END IF;
      UPDATE cap_ai.knowledge_documents SET desired_version_id = v.id, revision = revision + CASE WHEN desired_version_id IS DISTINCT FROM v.id
        THEN 1 ELSE 0 END
        WHERE id = d.id;
      -- Re-selecting historical content needs a new fenced job when a different
      -- version is active. Keep canonical source/version identity and prior attempt
      -- history; never reset a terminal job or mutate its immutable COMPLETE run.
      IF d.desired_version_id IS DISTINCT FROM v.id AND d.active_version_id IS DISTINCT FROM v.id AND EXISTS(SELECT FROM cap_ai.ingestion_jobs
        WHERE version_id = v.id AND kind = 'PARSE') THEN
        INSERT INTO cap_ai.ingestion_jobs(version_id, kind, profile_key)
          VALUES(v.id, 'PARSE', r.parser_profile || ':publication:' || generation::text) ON CONFLICT(version_id, kind, profile_key) DO NOTHING;
      ELSE INSERT INTO cap_ai.ingestion_jobs(version_id, kind, profile_key)
        VALUES(v.id, 'PARSE', r.parser_profile) ON CONFLICT(version_id, kind, profile_key) DO NOTHING;
    END IF;
  END LOOP;
END IF;
UPDATE cap_ai.ingestion_jobs j SET state = 'CANCELLED', lease_owner = NULL, lease_until = NULL, updated_at = now()
  FROM cap_ai.document_versions ver_row
  JOIN cap_ai.knowledge_documents doc_row ON doc_row.id = ver_row.document_id
  JOIN cap_ai.package_sources source_row ON source_row.id = doc_row.package_source_id
  WHERE j.version_id = ver_row.id AND source_row.app_id = app AND j.state IN('PENDING', 'LEASED', 'RETRY_WAIT')
  AND(source_row.state = 'REMOVED' OR doc_row.desired_version_id <> ver_row.id);
UPDATE cap_ai.source_read_delegations sd SET revoked_at = now()
  FROM cap_ai.ingestion_jobs j
  WHERE sd.job_id = j.id AND j.state = 'CANCELLED' AND sd.revoked_at IS NULL;
INSERT INTO cap_ai.package_publications(app_id, publication_generation, spec_digest, state)
  VALUES(app, generation, wanted_digest, 'APPLIED') ON CONFLICT(app_id, publication_generation) DO UPDATE SET state = 'APPLIED', updated_at = now();
RETURN true;
END
$$;

-- Preserve the qualified upload implementation while adding package branches.

ALTER FUNCTION cap_ai.read_document(
    uuid, text, uuid
) RENAME TO read_upload_document;

CREATE FUNCTION cap_ai.read_document(actor uuid, acting_role text, doc uuid)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT cap_ai.read_upload_document(actor, acting_role, doc)
  WHERE cap_ai.read_upload_document(actor, acting_role, doc) IS NOT NULL
  UNION ALL SELECT jsonb_build_object('document_id', d.id, 'version_id', coalesce(d.active_version_id, d.desired_version_id), 'run_id', v.active_run_id, 'policy_revision', d.policy_revision, 'authorization_generation', (current_source->>'authorization_generation')::bigint, 'source_incarnation', current_source->>'incarnation', 'app_publication_generation', (current_source->>'publication_generation')::bigint, 'processing_policy_id', d.processing_policy_id, 'processing_policy_version', d.processing_policy_version)
  FROM cap_ai.knowledge_documents d
  JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
  JOIN cap_ai.package_sources s ON s.id = d.package_source_id
  JOIN cap_ai.document_versions v ON v.id = coalesce(d.active_version_id, d.desired_version_id)
  CROSS JOIN LATERAL cap_ai.current_package_source(c.app_id, s.source_id) current_source
  JOIN core.users u ON u.id = actor AND u.status = 'ACTIVE'
  JOIN core.user_roles ur ON ur.user_id = u.id AND ur.role_name = acting_role
  JOIN core.roles role_owner ON role_owner.name = acting_role AND role_owner.app_id = c.app_id
  WHERE d.id = doc AND cap_ai.package_version_current(v.id, false) AND(current_source #> '{access,allowedRoles}') ? acting_role
  AND(current_source #>> '{access,mode}' = 'ROLE' OR(current_source #>> '{access,mode}' = 'RESTRICTED' AND EXISTS(SELECT FROM cap_ai.document_grants g WHERE g.document_id = d.id
  AND g.user_id = actor AND g.revoked_at IS NULL AND g.authorization_generation = (current_source->>'authorization_generation')::bigint)))
  AND cap_ai.read_upload_document(actor, acting_role, doc) IS NULL
  LIMIT 1
$$;

ALTER FUNCTION cap_ai.eligible_version(uuid) RENAME TO eligible_upload_version;

CREATE FUNCTION cap_ai.eligible_version(ver uuid)
RETURNS boolean
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT cap_ai.eligible_upload_version(ver) OR cap_ai.package_version_current(ver, true)
$$;

CREATE OR REPLACE FUNCTION cap_ai.lease_source(
    job uuid, worker text, fence bigint, run uuid
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('source_kind', v.source_kind, 'source_id', coalesce(v.source_file_id, v.package_revision_id), 'sha256', v.sha256, 'media_type', v.media_type, 'recipe', v.parser_profile)
  FROM cap_ai.ingestion_jobs j
  JOIN cap_ai.document_versions v ON v.id = j.version_id
  WHERE j.id = job AND cap_ai.lease_valid(job, worker, fence, run, statement_timestamp())
$$;

CREATE OR REPLACE FUNCTION cap_ai.issue_delegation(
    job uuid,
    worker text,
    fence bigint,
    run uuid,
    at_time timestamptz,
    token_digest text
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
BEGIN
  PERFORM 1
    FROM cap_ai.ingestion_jobs
    WHERE id = job FOR UPDATE;
  IF NOT cap_ai.lease_valid(job, worker, fence, run, at_time) THEN
    RAISE EXCEPTION 'stale lease' USING ERRCODE = 'P0410';
  END IF;
  INSERT INTO cap_ai.source_read_delegations(token_hash, job_id, fencing_token, run_id, source_kind, source_identity, sha256, owner_user_id, processing_policy_id, processing_policy_version, worker_id, expires_at) SELECT token_digest, j.id, fence, run, v.source_kind, coalesce(v.source_file_id, v.package_revision_id)::text, v.sha256, d.owner_user_id, d.processing_policy_id, d.processing_policy_version, worker, least(j.lease_until, at_time + interval '60 seconds')
    FROM cap_ai.ingestion_jobs j
    JOIN cap_ai.document_versions v ON v.id = j.version_id
    JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
    WHERE j.id = job;
  RETURN true;
END
$$;

CREATE OR REPLACE FUNCTION cap_ai.validate_delegation(
    token_digest text, worker text, source text
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('source_kind', s.source_kind, 'source_id', s.source_identity, 'sha256', s.sha256, 'owner_id', s.owner_user_id, 'expires_at', s.expires_at)
  FROM cap_ai.source_read_delegations s
  JOIN cap_ai.ingestion_jobs j ON j.id = s.job_id
  JOIN cap_ai.document_versions v ON v.id = j.version_id
  JOIN cap_ai.knowledge_documents d ON d.id = v.document_id
  WHERE s.token_hash = token_digest AND s.worker_id = worker AND s.source_identity = source AND s.revoked_at IS NULL
  AND s.expires_at > statement_timestamp() AND s.sha256 = v.sha256 AND s.owner_user_id IS NOT DISTINCT FROM d.owner_user_id
  AND s.processing_policy_id = d.processing_policy_id AND s.processing_policy_version = d.processing_policy_version
  AND cap_ai.lease_valid(s.job_id, worker, s.fencing_token, s.run_id, statement_timestamp())
$$;

CREATE FUNCTION cap_ai.package_artifact(source uuid)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('artifact_ref', r.artifact_ref, 'sha256', r.sha256, 'size_bytes', r.size_bytes)
  FROM cap_ai.package_source_revisions r
  JOIN cap_ai.document_versions v ON v.package_revision_id = r.id
  WHERE r.id = source AND cap_ai.package_version_current(v.id, false)
  LIMIT 1
$$;

CREATE FUNCTION cap_ai.search_app(
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
  LIMIT 50), diversified AS(SELECT *, row_number() OVER(PARTITION BY document_id ORDER BY score DESC, chunk_id) doc_rank
  FROM candidates), chosen AS(SELECT *
  FROM diversified
  WHERE doc_rank <= 4
  ORDER BY score DESC, document_id, chunk_id
  LIMIT least(greatest(page_size, 1), 20)) SELECT coalesce(jsonb_agg(jsonb_build_object('hit', cap_ai.citation_data(actor, acting_role, document_id, version_id, chunk_id), 'scope', cap_ai.read_document(actor, acting_role, document_id)) ORDER BY score DESC, document_id, chunk_id), '[]'::jsonb)
  FROM chosen
$$;

DO $$
DECLARE
  fn record;
BEGIN
  FOR fn IN SELECT p.oid::regprocedure signature
    FROM pg_proc p
    JOIN pg_namespace n ON n.oid = p.pronamespace
    WHERE n.nspname = 'cap_ai' AND p.proname IN('package_snapshot', 'current_package_source', 'package_version_current', 'reconcile_package', 'read_document', 'eligible_version', 'package_artifact', 'search_app') LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner', fn.signature);
    EXECUTE format('REVOKE ALL ON FUNCTION %s FROM PUBLIC', fn.signature);
  END LOOP;
END
$$;

GRANT EXECUTE ON FUNCTION cap_ai.package_snapshot(text, bigint, text),
cap_ai.reconcile_package(text, bigint, text) TO dxos_ai_dispatcher;

GRANT EXECUTE ON FUNCTION cap_ai.read_document(uuid, text, uuid),
cap_ai.search_app(uuid, text, text, text, integer, uuid[]) TO dxos_ai_api;

GRANT EXECUTE ON FUNCTION cap_ai.package_artifact(uuid) TO dxos_ai_worker;

CREATE OR REPLACE FUNCTION cap_ai.activate_job(
    job uuid, worker text, fence bigint, run uuid, at_time timestamptz
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  d cap_ai.knowledge_documents;
  v cap_ai.document_versions;
  r cap_ai.processing_runs;
  app text;
BEGIN
  PERFORM 1
    FROM cap_ai.ingestion_jobs
    WHERE id = job FOR UPDATE;
  IF NOT cap_ai.lease_valid(job, worker, fence, run, statement_timestamp()) THEN
    RAISE EXCEPTION 'stale lease' USING ERRCODE = 'P0410';
  END IF;
  SELECT v1.* INTO STRICT v
    FROM cap_ai.document_versions v1
    JOIN cap_ai.ingestion_jobs j ON j.version_id = v1.id
    WHERE j.id = job;
  SELECT * INTO STRICT d
    FROM cap_ai.knowledge_documents
    WHERE id = v.document_id FOR UPDATE;
  SELECT * INTO STRICT r
    FROM cap_ai.processing_runs
    WHERE id = run;
  IF d.state <> 'LIVE' OR d.desired_version_id IS DISTINCT FROM v.id OR r.state <> 'COMPLETE' OR r.chunk_count < 1
    OR r.element_count < 1 OR r.content_digest IS NULL OR r.chunk_count <> (SELECT count(*)
    FROM cap_ai.knowledge_chunks
    WHERE run_id = run) OR r.element_count <> (SELECT count(*)
    FROM cap_ai.document_elements
    WHERE run_id = run) OR EXISTS(SELECT FROM cap_ai.knowledge_chunks k
    WHERE k.run_id = run AND NOT EXISTS(SELECT FROM cap_ai.chunk_elements ce WHERE ce.run_id = run AND ce.chunk_id = k.id))
    THEN
    RAISE EXCEPTION 'incomplete or undesired run' USING ERRCODE = 'P0410';
  END IF;
  UPDATE cap_ai.document_versions SET state = 'READY', active_run_id = run, updated_at = now()
    WHERE id = v.id;
  UPDATE cap_ai.knowledge_documents SET active_version_id = v.id, revision = revision + 1, updated_at = now()
    WHERE id = d.id
    RETURNING * INTO d;
  SELECT app_id INTO app
    FROM cap_ai.knowledge_collections
    WHERE id = d.collection_id;
  IF d.source_kind = 'PACKAGE' AND NOT EXISTS(SELECT FROM core.users
    WHERE id = worker::uuid AND status = 'ACTIVE') THEN
    RAISE EXCEPTION 'service actor unavailable' USING ERRCODE = 'P0410';
  END IF;
  INSERT INTO core.audit_logs(app_id, actor_id, action, entity_type, entity_id, entity_version, payload)
    VALUES(app, CASE WHEN d.source_kind = 'PACKAGE' THEN worker::uuid ELSE d.owner_user_id END, 'knowledge.activate', 'cap_ai.knowledge_documents', d.id, d.revision, jsonb_build_object('version_id', v.id, 'run_id', run));
  IF NOT cap_ai.lease_valid(job, worker, fence, run, statement_timestamp()) THEN
    RAISE EXCEPTION 'source revoked' USING ERRCODE = 'P0410';
  END IF;
  UPDATE cap_ai.ingestion_jobs SET state = 'SUCCEEDED', lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE id = job;
  UPDATE cap_ai.job_attempts SET finished_at = now(), outcome = 'SUCCEEDED'
    WHERE job_id = job AND fencing_token = fence;
  UPDATE cap_ai.source_read_delegations SET revoked_at = now()
    WHERE job_id = job AND revoked_at IS NULL;
  RETURN true;
END
$$;

CREATE OR REPLACE FUNCTION cap_ai.original_source(
    actor uuid, acting_role text, doc uuid
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT CASE WHEN d.source_kind = 'PACKAGE' THEN jsonb_build_object('artifact', jsonb_build_object('artifact_ref', r.artifact_ref, 'sha256', r.sha256, 'size_bytes', r.size_bytes))
  ELSE jsonb_build_object('file_id', coalesce(v.source_file_id, d.initial_file_id)) END
  FROM cap_ai.knowledge_documents d
  LEFT JOIN cap_ai.document_versions v ON v.id = coalesce(d.active_version_id, d.desired_version_id)
  LEFT JOIN cap_ai.package_source_revisions r ON r.id = v.package_revision_id
  WHERE d.id = doc AND cap_ai.read_document(actor, acting_role, doc) IS NOT NULL
$$;

CREATE FUNCTION cap_ai.management_scope(actor uuid, acting_role text, doc uuid)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('document_id', d.id, 'revision', d.revision, 'authorization_generation', (source->>'authorization_generation')::bigint, 'ceiling', source->'access', 'pending_publication', d.authorization_generation <> (source->>'authorization_generation')::bigint)
  FROM cap_ai.knowledge_documents d
  JOIN cap_ai.knowledge_collections c ON c.id = d.collection_id
  JOIN cap_ai.package_sources ps ON ps.id = d.package_source_id
  JOIN core.apps a ON a.app_id = c.app_id
  CROSS JOIN LATERAL cap_ai.current_package_source(c.app_id, ps.source_id) source
  JOIN core.users u ON u.id = actor AND u.status = 'ACTIVE'
  JOIN core.user_roles ur ON ur.user_id = u.id AND ur.role_name = acting_role
  JOIN core.roles ro ON ro.name = ur.role_name AND ro.app_id = a.app_id
  WHERE d.id = doc AND d.state = 'LIVE' AND d.source_kind = 'PACKAGE' AND c.status = 'ACTIVE' AND c.kind = 'APP'
  AND ps.state = 'DECLARED' AND source IS NOT NULL AND ps.incarnation = (source->>'incarnation')::uuid AND source #>> '{access,mode}' = 'RESTRICTED'
  AND(a.spec #> ARRAY['knowledge', 'grantPolicies', source #>> '{access,grantsManagedBy}', 'allowedRoles']) ? acting_role
$$;

CREATE FUNCTION cap_ai.grant_management(
    actor uuid, acting_role text, doc uuid, after_user uuid
)
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  scope jsonb;
  grants jsonb;
BEGIN
  scope := cap_ai.management_scope(actor, acting_role, doc);
  IF scope IS NULL THEN
    RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
  END IF;
  SELECT coalesce(jsonb_agg(jsonb_build_object('user_id', user_id, 'status', CASE WHEN revoked_at IS NULL THEN 'ACTIVE'
    ELSE 'REVOKED' END) ORDER BY user_id), '[]'::jsonb) INTO grants
    FROM(SELECT user_id, revoked_at
    FROM cap_ai.document_grants
    WHERE document_id = doc AND authorization_generation = (scope->>'authorization_generation')::bigint AND(after_user IS NULL
    OR user_id > after_user)
    ORDER BY user_id
    LIMIT 51) page;
  RETURN scope || jsonb_build_object('grants', grants);
END
$$;

CREATE FUNCTION cap_ai.mutate_read_grant(
    actor uuid,
    acting_role text,
    request_key text,
    doc uuid,
    target uuid,
    expected_revision bigint,
    revoke boolean
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  scope jsonb;
  d cap_ai.knowledge_documents;
  app text;
  action_name text;
  payload_hash text;
  receipt cap_ai.request_receipts;
  generation bigint;
BEGIN
  IF length(request_key) NOT BETWEEN 1 AND 200 OR expected_revision < 1 THEN
    RAISE EXCEPTION 'invalid grant request' USING ERRCODE = 'P0400';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtext('dxos.operator.publication'));
  scope := cap_ai.management_scope(actor, acting_role, doc);
  IF scope IS NULL THEN
    RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
  END IF;
  SELECT * INTO STRICT d
    FROM cap_ai.knowledge_documents
    WHERE id = doc FOR UPDATE;
  SELECT app_id INTO STRICT app
    FROM cap_ai.knowledge_collections
    WHERE id = d.collection_id;
  action_name := CASE WHEN revoke THEN 'revoke-read' ELSE 'grant-read' END;
  payload_hash := encode(digest(doc::text || ':' || target::text || ':' || action_name, 'sha256'), 'hex');
  SELECT * INTO receipt
    FROM cap_ai.request_receipts
    WHERE actor_id = actor AND authority_app_id = app AND action = action_name AND idempotency_key = request_key;
  IF FOUND THEN
    IF receipt.payload_hash <> payload_hash THEN
      RAISE EXCEPTION 'receipt conflict' USING ERRCODE = 'P0409';
    END IF;
    IF cap_ai.management_scope(actor, acting_role, doc) IS NULL THEN
      RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
    END IF;
    RETURN true;
  END IF;
  generation := (scope->>'authorization_generation')::bigint;
  IF d.revision <> expected_revision OR d.authorization_generation <> generation THEN
    RAISE EXCEPTION 'revision conflict' USING ERRCODE = 'P0409';
  END IF;
  IF NOT revoke AND NOT EXISTS(SELECT FROM core.users u
    JOIN core.user_roles ur ON ur.user_id = u.id
    JOIN core.roles r ON r.name = ur.role_name AND r.app_id = app
    WHERE u.id = target AND u.status = 'ACTIVE' AND(scope #> '{ceiling,allowedRoles}') ? r.name) THEN
    RAISE EXCEPTION 'target unavailable' USING ERRCODE = 'P0404';
  END IF;
  IF revoke THEN
    UPDATE cap_ai.document_grants SET revoked_at = now()
      WHERE document_id = doc AND authorization_generation = generation AND user_id = target;
  ELSE INSERT INTO cap_ai.document_grants(document_id, authorization_generation, user_id, granted_by)
    VALUES(doc, generation, target, actor) ON CONFLICT(document_id, authorization_generation, user_id) DO UPDATE
    SET granted_by = actor, granted_at = now(), revoked_at = NULL;
END IF;
UPDATE cap_ai.knowledge_documents SET revision = revision + 1, policy_revision = policy_revision + 1, updated_at = now()
  WHERE id = doc
  RETURNING * INTO d;
INSERT INTO core.audit_logs(app_id, actor_id, acting_role, action, entity_type, entity_id, entity_version, payload)
  VALUES(app, actor, acting_role, 'knowledge.' || action_name, 'cap_ai.knowledge_documents', doc, d.revision, jsonb_build_object('user_id', target, 'authorization_generation', generation));
INSERT INTO cap_ai.request_receipts(actor_id, authority_app_id, action, idempotency_key, payload_hash, document_id, response_code, revision)
  VALUES(actor, app, action_name, request_key, payload_hash, doc, 204, d.revision);
IF cap_ai.management_scope(actor, acting_role, doc) IS NULL THEN
  RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
END IF;
RETURN true;
END
$$;

ALTER FUNCTION cap_ai.management_scope(uuid, text, uuid) OWNER TO dxos_ai_owner;

ALTER FUNCTION cap_ai.grant_management(
    uuid, text, uuid, uuid
) OWNER TO dxos_ai_owner;

ALTER FUNCTION cap_ai.mutate_read_grant(
    uuid, text, text, uuid, uuid, bigint, boolean
) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.management_scope(uuid, text, uuid),
cap_ai.grant_management(uuid, text, uuid, uuid),
cap_ai.mutate_read_grant(uuid, text, text, uuid, uuid, bigint, boolean)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.management_scope(uuid, text, uuid),
cap_ai.grant_management(uuid, text, uuid, uuid),
cap_ai.mutate_read_grant(
    uuid, text, text, uuid, uuid, bigint, boolean
) TO dxos_ai_api;

CREATE FUNCTION cap_ai.app_knowledge(actor uuid, acting_role text, app text)
RETURNS jsonb
LANGUAGE plpgsql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  result jsonb;
BEGIN
  IF NOT EXISTS(SELECT FROM core.apps a
    JOIN core.roles r ON r.app_id = a.app_id AND r.name = acting_role
    JOIN core.user_roles ur ON ur.role_name = r.name AND ur.user_id = actor
    JOIN core.users u ON u.id = actor AND u.status = 'ACTIVE'
    WHERE a.app_id = app AND a.status = 'Ready') THEN
    RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
  END IF;
  WITH declared AS(SELECT a.spec, source, cap_ai.current_package_source(app, source->>'source_id') current_source
    FROM core.apps a
    CROSS JOIN LATERAL jsonb_array_elements(a.spec #> '{_publication,sources}') source
    WHERE a.app_id = app), scoped AS(SELECT x.*, ps.incarnation, d.id document_id, d.title, d.active_version_id, d.desired_version_id, v.sha256 desired_hash, v.state desired_status, active.state active_status, cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL readable, coalesce((x.spec #> ARRAY['knowledge', 'grantPolicies', current_source #>> '{access,grantsManagedBy}', 'allowedRoles']) ? acting_role, false)
    AND current_source #>> '{access,mode}' = 'RESTRICTED' manager
    FROM declared x
    LEFT JOIN cap_ai.package_sources ps ON ps.app_id = app AND ps.source_id = x.source->>'source_id'
    LEFT JOIN cap_ai.knowledge_documents d ON d.package_source_id = ps.id
    LEFT JOIN cap_ai.document_versions v ON v.id = d.desired_version_id
    LEFT JOIN cap_ai.document_versions active ON active.id = d.active_version_id
    WHERE x.current_source IS NOT NULL) SELECT coalesce(jsonb_agg(jsonb_strip_nulls(jsonb_build_object('source_id', source->>'source_id', 'document_id', document_id, 'title', CASE
    WHEN readable THEN title ELSE NULL END, 'readable', readable, 'manageable', manager AND document_id IS NOT NULL, 'status', CASE
    WHEN document_id IS NULL OR incarnation IS DISTINCT FROM(current_source->>'incarnation')::uuid OR desired_hash IS DISTINCT FROM current_source->>'sha256'
    THEN 'PENDING' WHEN desired_status = 'FAILED' THEN 'FAILED' WHEN desired_status = 'READY' AND active_version_id = desired_version_id
    THEN 'READY' ELSE 'INDEXING' END, 'active_version_available', coalesce(readable AND active_status = 'READY', false))) ORDER BY source->>'source_id'), '[]'::jsonb) INTO result
    FROM scoped
    WHERE readable OR manager OR(current_source #>> '{access,mode}' = 'ROLE' AND(current_source #> '{access,allowedRoles}') ? acting_role);
  RETURN jsonb_build_object('sources', result);
END
$$;

ALTER FUNCTION cap_ai.app_knowledge(uuid, text, text) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.app_knowledge(uuid, text, text)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.app_knowledge(uuid, text, text) TO dxos_ai_api;
