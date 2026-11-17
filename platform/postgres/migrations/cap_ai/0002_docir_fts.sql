-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

CREATE TABLE cap_ai.document_elements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES cap_ai.processing_runs (id),
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    parent_id uuid,
    kind text NOT NULL CHECK (
        kind IN (
            'heading', 'paragraph', 'list', 'code', 'table', 'figure', 'formula'
        )
    ),
    section_path jsonb NOT NULL CHECK (jsonb_typeof(section_path) = 'array'),
    page_no integer CHECK (page_no >= 1),
    bbox jsonb,
    text text NOT NULL,
    structured jsonb,
    confidence numeric NOT NULL CHECK (confidence BETWEEN 0 AND 1),
    provenance text NOT NULL CHECK (
        provenance IN ('EXTRACTED', 'OCR', 'SYNTHETIC')
    ),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, ordinal),
    UNIQUE (run_id, id),
    FOREIGN KEY (run_id, parent_id) REFERENCES cap_ai.document_elements (
        run_id, id
    ) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX elements_pages ON cap_ai.document_elements (
    run_id, page_no, ordinal
);

CREATE TABLE cap_ai.document_assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES cap_ai.processing_runs (id),
    element_id uuid,
    kind text NOT NULL CHECK (kind IN ('PAGE', 'FIGURE', 'TABLE')),
    artifact_ref text NOT NULL,
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    media_type text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 209715200),
    width integer CHECK (width > 0),
    height integer CHECK (height > 0),
    state text NOT NULL CHECK (state IN ('STAGED', 'AVAILABLE', 'PURGED')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, id),
    UNIQUE NULLS NOT DISTINCT (run_id, sha256, kind, element_id),
    FOREIGN KEY (run_id, element_id) REFERENCES cap_ai.document_elements (
        run_id, id
    )
);

CREATE TABLE cap_ai.knowledge_chunks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id uuid NOT NULL REFERENCES cap_ai.processing_runs (id),
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    text text NOT NULL CHECK (length(trim(text)) > 0),
    section_path jsonb NOT NULL,
    page_start integer CHECK (page_start >= 1),
    page_end integer CHECK (page_end >= page_start),
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    token_count integer NOT NULL CHECK (token_count BETWEEN 1 AND 768),
    search_vector tsvector GENERATED ALWAYS AS (
        to_tsvector('simple'::regconfig, normalize(text, nfc))
    ) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, ordinal),
    UNIQUE (run_id, id)
);

CREATE INDEX chunks_fts ON cap_ai.knowledge_chunks USING gin (search_vector);

CREATE TABLE cap_ai.chunk_elements (
    run_id uuid NOT NULL,
    chunk_id uuid NOT NULL,
    element_id uuid NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (chunk_id, element_id),
    FOREIGN KEY (run_id, chunk_id) REFERENCES cap_ai.knowledge_chunks (
        run_id, id
    ),
    FOREIGN KEY (run_id, element_id) REFERENCES cap_ai.document_elements (
        run_id, id
    )
);

CREATE INDEX chunks_elements_reverse ON cap_ai.chunk_elements (
    element_id, chunk_id
);

CREATE FUNCTION cap_ai.guard_derived_write()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  target uuid;
BEGIN
  target := CASE WHEN TG_OP = 'DELETE' THEN OLD.run_id ELSE NEW.run_id END;
  IF NOT EXISTS(SELECT FROM cap_ai.processing_runs r
    JOIN cap_ai.ingestion_jobs j ON j.id = r.ingestion_job_id
    WHERE r.id = target AND r.state = 'BUILDING' AND cap_ai.lease_valid(j.id, j.lease_owner, r.fencing_token, r.id, statement_timestamp()))
    THEN
    RAISE EXCEPTION 'stale derived write' USING ERRCODE = 'P0410';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.run_id <> OLD.run_id THEN
    RAISE EXCEPTION 'immutable run identity';
  END IF;
  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  ELSE RETURN NEW;
END IF;
END
$$;

CREATE TRIGGER elements_fence BEFORE INSERT OR UPDATE OR DELETE ON cap_ai.document_elements FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_derived_write();

CREATE TRIGGER assets_fence BEFORE INSERT OR UPDATE OR DELETE ON cap_ai.document_assets FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_derived_write();

CREATE TRIGGER chunks_fence BEFORE INSERT OR UPDATE OR DELETE ON cap_ai.knowledge_chunks FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_derived_write();

CREATE TRIGGER links_fence BEFORE INSERT OR UPDATE OR DELETE ON cap_ai.chunk_elements FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_derived_write();

DO $$
DECLARE
  r record;
BEGIN
  FOR r IN SELECT tablename
    FROM pg_tables
    WHERE schemaname = 'cap_ai' LOOP
    EXECUTE format('ALTER TABLE cap_ai.%I OWNER TO dxos_ai_owner', r.tablename);
  END LOOP;
END
$$;

ALTER FUNCTION cap_ai.guard_derived_write() OWNER TO dxos_ai_owner;

REVOKE ALL ON ALL TABLES IN SCHEMA cap_ai
FROM public;

REVOKE ALL ON FUNCTION cap_ai.guard_derived_write()
FROM public;

ALTER TABLE cap_ai.knowledge_chunks ADD COLUMN warnings jsonb NOT NULL DEFAULT '[]';

CREATE FUNCTION cap_ai.save_run(
    job uuid,
    worker text,
    fence bigint,
    run uuid,
    result jsonb,
    chunks jsonb,
    payload_digest text
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  v cap_ai.document_versions;
  r cap_ai.processing_runs;
  e jsonb;
  c jsonb;
  ids jsonb := '{}';
  eid uuid;
  cid uuid;
  ref text;
  position_no integer;
BEGIN
  PERFORM 1
    FROM cap_ai.ingestion_jobs
    WHERE id = job FOR UPDATE;
  IF NOT cap_ai.lease_valid(job, worker, fence, run, statement_timestamp()) THEN
    RAISE EXCEPTION 'stale lease' USING ERRCODE = 'P0410';
  END IF;
  SELECT * INTO STRICT r
    FROM cap_ai.processing_runs
    WHERE id = run;
  IF r.state = 'COMPLETE' THEN
    IF r.content_digest = payload_digest THEN
      RETURN true;
    END IF;
    RAISE EXCEPTION 'run conflict' USING ERRCODE = 'P0409';
  END IF;
  SELECT v1.* INTO STRICT v
    FROM cap_ai.document_versions v1
    JOIN cap_ai.ingestion_jobs j ON j.version_id = v1.id
    WHERE j.id = job;
  IF result->>'source_sha256' <> v.sha256 OR result #>> '{parser,recipe}' <> v.parser_profile OR result->>'ir_version' <> '1'
    OR jsonb_array_length(result->'elements') NOT BETWEEN 1 AND 50000 OR jsonb_array_length(chunks) NOT BETWEEN 1
    AND 20000 OR jsonb_array_length(result->'assets') <> 0 THEN
    RAISE EXCEPTION 'invalid output' USING ERRCODE = 'P0400';
  END IF;
  FOR e IN SELECT value
    FROM jsonb_array_elements(result->'elements') LOOP
    eid := gen_random_uuid();
    IF ids ? (e->>'local_id') THEN
      RAISE EXCEPTION 'duplicate element';
    END IF;
    ids := ids || jsonb_build_object(e->>'local_id', eid);
    INSERT INTO cap_ai.document_elements(id, run_id, ordinal, kind, section_path, page_no, bbox, text, structured, confidence, provenance)
      VALUES(eid, run, (e->>'ordinal')::integer, e->>'kind', e->'section_path', nullif(e->>'page_no', '0')::integer, e->'bbox', e->>'text', e->'structured', (e->>'confidence')::numeric, e->>'provenance');
  END LOOP;
  FOR e IN SELECT value
    FROM jsonb_array_elements(result->'elements') LOOP
    IF nullif(e->>'parent_local_id', '') IS NOT NULL THEN
      IF NOT ids ? (e->>'parent_local_id') THEN
        RAISE EXCEPTION 'foreign parent';
      END IF;
      UPDATE cap_ai.document_elements SET parent_id = (ids->>(e->>'parent_local_id'))::uuid
        WHERE id = (ids->>(e->>'local_id'))::uuid;
    END IF;
  END LOOP;
  FOR c IN SELECT value
    FROM jsonb_array_elements(chunks) LOOP
    cid := gen_random_uuid();
    INSERT INTO cap_ai.knowledge_chunks(id, run_id, ordinal, text, section_path, page_start, page_end, content_hash, token_count, warnings)
      VALUES(cid, run, (c->>'ordinal')::integer, c->>'text', c->'section_path', nullif(c->>'page_start', '0')::integer, nullif(c->>'page_end', '0')::integer, c->>'content_hash', (c->>'token_count')::integer, coalesce(c->'warnings', '[]'::jsonb));
    IF jsonb_array_length(c->'element_local_ids') < 1 THEN
      RAISE EXCEPTION 'missing provenance';
    END IF;
    position_no := 0;
    FOR ref IN SELECT jsonb_array_elements_text(c->'element_local_ids') LOOP
      IF NOT ids ? ref THEN
        RAISE EXCEPTION 'foreign element';
      END IF;
      INSERT INTO cap_ai.chunk_elements(run_id, chunk_id, element_id, position)
        VALUES(run, cid, (ids->>ref)::uuid, position_no);
      position_no := position_no + 1;
    END LOOP;
  END LOOP;
  IF NOT cap_ai.lease_valid(job, worker, fence, run, statement_timestamp()) THEN
    RAISE EXCEPTION 'stale lease' USING ERRCODE = 'P0410';
  END IF;
  UPDATE cap_ai.processing_runs SET parser_name = result #>> '{parser,name}', parser_version = result #>> '{parser,version}', chunker_version = 'chunk-v1/unicode-rune-v1', element_count = jsonb_array_length(result->'elements'), chunk_count = jsonb_array_length(chunks), content_digest = payload_digest, state = 'COMPLETE', updated_at = now()
    WHERE id = run;
  RETURN true;
END
$$;

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
  INSERT INTO core.audit_logs(app_id, actor_id, action, entity_type, entity_id, entity_version, payload)
    VALUES(app, d.owner_user_id, 'knowledge.activate', 'cap_ai.knowledge_documents', d.id, d.revision, jsonb_build_object('version_id', v.id, 'run_id', run));
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

CREATE FUNCTION cap_ai.lease_source(
    job uuid, worker text, fence bigint, run uuid
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('source_id', v.source_file_id, 'sha256', v.sha256, 'media_type', v.media_type, 'recipe', v.parser_profile)
  FROM cap_ai.ingestion_jobs j
  JOIN cap_ai.document_versions v ON v.id = j.version_id
  WHERE j.id = job AND cap_ai.lease_valid(job, worker, fence, run, statement_timestamp())
$$;

ALTER FUNCTION cap_ai.save_run(
    uuid, text, bigint, uuid, jsonb, jsonb, text
) OWNER TO dxos_ai_owner;

ALTER FUNCTION cap_ai.activate_job(
    uuid, text, bigint, uuid, timestamptz
) OWNER TO dxos_ai_owner;

ALTER FUNCTION cap_ai.lease_source(
    uuid, text, bigint, uuid
) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.save_run(
    uuid, text, bigint, uuid, jsonb, jsonb, text
),
cap_ai.lease_source(uuid, text, bigint, uuid)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.save_run(
    uuid, text, bigint, uuid, jsonb, jsonb, text
),
cap_ai.lease_source(uuid, text, bigint, uuid) TO dxos_ai_worker;

CREATE FUNCTION cap_ai.citation_data(
    actor uuid, acting_role text, doc uuid, ver uuid, chunk uuid
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('document_id', d.id, 'version_id', v.id, 'run_id', r.id, 'chunk_id', k.id, 'title', d.title, 'snippet', k.text, 'section_path', k.section_path, 'page_start', k.page_start, 'page_end', k.page_end, 'warnings', k.warnings, 'element_ids', (SELECT jsonb_agg(ce.element_id ORDER BY ce.position) FROM cap_ai.chunk_elements ce WHERE ce.run_id = r.id
  AND ce.chunk_id = k.id), 'positions', (SELECT jsonb_agg(jsonb_build_object('element_id', e.id, 'page_no', e.page_no, 'bbox', e.bbox, 'section_path', e.section_path, 'provenance', e.provenance) ORDER BY ce.position) FROM cap_ai.chunk_elements ce JOIN cap_ai.document_elements e
  ON e.run_id = ce.run_id AND e.id = ce.element_id WHERE ce.run_id = r.id AND ce.chunk_id = k.id))
  FROM cap_ai.knowledge_documents d
  JOIN cap_ai.document_versions v ON v.id = d.active_version_id
  JOIN cap_ai.processing_runs r ON r.id = v.active_run_id
  JOIN cap_ai.knowledge_chunks k ON k.run_id = r.id
  WHERE d.id = doc AND v.id = ver AND k.id = chunk AND d.state = 'LIVE' AND v.state = 'READY' AND r.state = 'COMPLETE'
  AND cap_ai.read_document(actor, acting_role, d.id) IS NOT NULL
$$;

CREATE FUNCTION cap_ai.search_personal(
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
  WHERE doc_rank <= 4
  ORDER BY score DESC, document_id, chunk_id
  LIMIT least(greatest(page_size, 1), 20)) SELECT coalesce(jsonb_agg(jsonb_build_object('hit', cap_ai.citation_data(actor, acting_role, document_id, version_id, chunk_id), 'scope', cap_ai.read_document(actor, acting_role, document_id)) ORDER BY score DESC, document_id, chunk_id), '[]'::jsonb)
  FROM chosen
$$;

CREATE FUNCTION cap_ai.original_source(actor uuid, acting_role text, doc uuid)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('file_id', coalesce(v.source_file_id, d.initial_file_id))
  FROM cap_ai.knowledge_documents d
  LEFT JOIN cap_ai.document_versions v ON v.id = coalesce(d.active_version_id, d.desired_version_id)
  WHERE d.id = doc AND cap_ai.read_document(actor, acting_role, doc) IS NOT NULL
$$;

ALTER FUNCTION cap_ai.citation_data(
    uuid, text, uuid, uuid, uuid
) OWNER TO dxos_ai_owner;

ALTER FUNCTION cap_ai.search_personal(
    uuid, text, text, integer, uuid[]
) OWNER TO dxos_ai_owner;

ALTER FUNCTION cap_ai.original_source(uuid, text, uuid) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.citation_data(uuid, text, uuid, uuid, uuid),
cap_ai.search_personal(uuid, text, text, integer, uuid[]),
cap_ai.original_source(uuid, text, uuid)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.citation_data(uuid, text, uuid, uuid, uuid),
cap_ai.search_personal(uuid, text, text, integer, uuid[]),
cap_ai.original_source(uuid, text, uuid) TO dxos_ai_api;

CREATE FUNCTION cap_ai.asset_data(
    actor uuid, acting_role text, doc uuid, asset uuid
)
RETURNS jsonb
LANGUAGE sql STABLE
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
SELECT jsonb_build_object('artifact_ref', a.artifact_ref, 'sha256', a.sha256, 'size_bytes', a.size_bytes, 'run_id', a.run_id)
  FROM cap_ai.document_assets a
  JOIN cap_ai.processing_runs r ON r.id = a.run_id
  JOIN cap_ai.document_versions v ON v.active_run_id = r.id
  JOIN cap_ai.knowledge_documents d ON d.active_version_id = v.id
  WHERE a.id = asset AND d.id = doc AND a.state = 'AVAILABLE' AND r.state = 'COMPLETE' AND cap_ai.read_document(actor, acting_role, doc) IS NOT NULL
$$;

ALTER FUNCTION cap_ai.asset_data(uuid, text, uuid, uuid) OWNER TO dxos_ai_owner;

REVOKE ALL ON FUNCTION cap_ai.asset_data(uuid, text, uuid, uuid)
FROM public;

GRANT EXECUTE ON FUNCTION cap_ai.asset_data(
    uuid, text, uuid, uuid
) TO dxos_ai_api;

CREATE OR REPLACE FUNCTION cap_ai.register_document(
    actor uuid,
    acting_role text,
    request_key text,
    request_hash text,
    req jsonb,
    existing_doc uuid,
    expected_revision bigint
)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  app text;
  c cap_ai.knowledge_collections;
  d cap_ai.knowledge_documents;
  v cap_ai.document_versions;
  j cap_ai.ingestion_jobs;
  f core.files;
  r cap_ai.request_receipts;
  action_name text;
  new_doc boolean := false;
  result jsonb;
BEGIN
  app := cap_ai.library_app(actor, acting_role);
  IF app IS NULL THEN
    RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
  END IF;
  IF length(request_key) NOT BETWEEN 1 AND 200 OR length(coalesce(req->>'title', '')) > 512 THEN
    RAISE EXCEPTION 'invalid' USING ERRCODE = 'P0400';
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai:' || app || ':' || actor::text, 0));
  IF app IS DISTINCT FROM cap_ai.library_app(actor, acting_role) THEN
    RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
  END IF;
  SELECT * INTO f
    FROM core.files
    WHERE id = (req->>'file_id')::uuid AND app_id = app AND uploaded_by = actor AND read_policy = 'OWNER_ONLY';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
  END IF;
  IF f.scan_status <> 'CLEAN' THEN
    RAISE EXCEPTION 'unclean' USING ERRCODE = 'P0422';
  END IF;
  IF f.mime_type NOT IN('application/pdf', 'text/markdown') OR req->>'parser_profile' IS DISTINCT FROM 'standard-text-v1'
    THEN
    RAISE EXCEPTION 'unsupported media or recipe' USING ERRCODE = 'P0415';
  END IF;
  action_name := CASE WHEN existing_doc IS NULL THEN 'register' ELSE 'replace:' || existing_doc::text END;
  SELECT * INTO r
    FROM cap_ai.request_receipts
    WHERE actor_id = actor AND authority_app_id = app AND action = action_name AND idempotency_key = request_key;
  IF FOUND THEN
    IF cap_ai.read_document(actor, acting_role, r.document_id) IS NULL THEN
      RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
    END IF;
    IF r.payload_hash <> request_hash THEN
      RAISE EXCEPTION 'conflict' USING ERRCODE = 'P0409';
    END IF;
    RETURN jsonb_build_object('document_id', r.document_id, 'version_id', r.version_id, 'job_id', r.job_id, 'revision', r.revision, 'status', 'QUEUED');
  END IF;
  IF existing_doc IS NULL THEN
    INSERT INTO cap_ai.knowledge_collections(app_id, kind, owner_user_id)
      VALUES(app, 'PERSONAL', actor) ON CONFLICT(app_id, owner_user_id)
      WHERE kind = 'PERSONAL' DO UPDATE SET updated_at = cap_ai.knowledge_collections.updated_at
      RETURNING * INTO c;
    IF c.status <> 'ACTIVE' OR(nullif(req->>'collection_id', '') IS NOT NULL AND c.id <> (req->>'collection_id')::uuid)
      THEN
      RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
    END IF;
    SELECT * INTO d
      FROM cap_ai.knowledge_documents
      WHERE collection_id = c.id AND owner_user_id = actor AND initial_file_id = f.id FOR UPDATE;
    IF NOT FOUND THEN
      INSERT INTO cap_ai.knowledge_documents(collection_id, source_kind, owner_user_id, initial_file_id, title, access_mode, processing_policy_id, processing_policy_version)
        VALUES(c.id, 'UPLOAD', actor, f.id, coalesce(nullif(trim(req->>'title'), ''), left(f.file_name, 512)), 'PRIVATE', 'local-only', 1)
        RETURNING * INTO d;
      new_doc := true;
    END IF;
  ELSE IF cap_ai.read_document(actor, acting_role, existing_doc) IS NULL THEN RAISE EXCEPTION 'unavailable'
    USING ERRCODE = 'P0404';
END IF;
SELECT * INTO STRICT d
  FROM cap_ai.knowledge_documents
  WHERE id = existing_doc FOR UPDATE;
IF expected_revision <> d.revision THEN
  RAISE EXCEPTION 'revision conflict' USING ERRCODE = 'P0409';
END IF;
SELECT * INTO STRICT c
  FROM cap_ai.knowledge_collections
  WHERE id = d.collection_id;
IF nullif(req->>'collection_id', '') IS NOT NULL AND c.id <> (req->>'collection_id')::uuid THEN
  RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
END IF;
END IF;
IF d.state <> 'LIVE' THEN
  RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
END IF;
SELECT * INTO v
  FROM cap_ai.document_versions
  WHERE document_id = d.id AND source_file_id = f.id AND parser_profile = req->>'parser_profile';
IF NOT FOUND THEN
  INSERT INTO cap_ai.document_versions(document_id, version_no, source_kind, source_file_id, sha256, media_type, size_bytes, parser_profile) SELECT d.id, coalesce(max(version_no), 0) + 1, 'UPLOAD', f.id, f.sha256_hash, f.mime_type, f.size_bytes, req->>'parser_profile'
    FROM cap_ai.document_versions
    WHERE document_id = d.id
    RETURNING * INTO v;
  INSERT INTO cap_ai.ingestion_jobs(version_id, kind, profile_key)
    VALUES(v.id, 'PARSE', v.parser_profile)
    RETURNING * INTO j;
  UPDATE cap_ai.knowledge_documents SET desired_version_id = v.id, revision = revision + CASE WHEN new_doc THEN 0
    ELSE 1 END, updated_at = now()
    WHERE id = d.id
    RETURNING * INTO d;
  -- Superseded leases no longer delegate; old active data is preserved.
  UPDATE cap_ai.ingestion_jobs SET state = 'CANCELLED', lease_owner = NULL, lease_until = NULL, updated_at = now()
    WHERE version_id IN(SELECT id
    FROM cap_ai.document_versions
    WHERE document_id = d.id AND id <> v.id) AND state IN('PENDING', 'LEASED', 'RETRY_WAIT');
  UPDATE cap_ai.source_read_delegations SET revoked_at = now()
    WHERE job_id IN(SELECT id
    FROM cap_ai.ingestion_jobs
    WHERE state = 'CANCELLED') AND revoked_at IS NULL;
ELSE IF d.desired_version_id IS DISTINCT FROM v.id THEN RAISE EXCEPTION 'superseded source recipe' USING ERRCODE = 'P0409';
END IF;
SELECT * INTO STRICT j
  FROM cap_ai.ingestion_jobs
  WHERE version_id = v.id AND kind = 'PARSE' AND profile_key = v.parser_profile;
IF j.state IN('FAILED', 'CANCELLED') THEN
  RAISE EXCEPTION 'terminal source recipe' USING ERRCODE = 'P0409';
END IF;
END IF;
INSERT INTO core.audit_logs(app_id, actor_id, acting_role, action, entity_type, entity_id, entity_version, payload)
  VALUES(app, actor, acting_role, 'knowledge.' || action_name, 'cap_ai.knowledge_documents', d.id, d.revision, jsonb_build_object('version_id', v.id, 'job_id', j.id));
INSERT INTO cap_ai.request_receipts(actor_id, authority_app_id, action, idempotency_key, payload_hash, document_id, version_id, job_id, response_code, revision)
  VALUES(actor, app, action_name, request_key, request_hash, d.id, v.id, j.id, 202, d.revision);
IF cap_ai.read_document(actor, acting_role, d.id) IS NULL THEN
  RAISE EXCEPTION 'unavailable' USING ERRCODE = 'P0404';
END IF;
RETURN jsonb_build_object('document_id', d.id, 'version_id', v.id, 'job_id', j.id, 'revision', d.revision, 'status', 'QUEUED');
END
$$;

ALTER FUNCTION cap_ai.register_document(
    uuid, text, text, text, jsonb, uuid, bigint
) OWNER TO dxos_ai_owner;

-- A COMPLETE attempt remains immutable when activation must retry.

CREATE OR REPLACE FUNCTION cap_ai.retry_job(
    job uuid,
    worker text,
    fence bigint,
    run uuid,
    at_time timestamptz,
    retry_at timestamptz,
    safe_code text,
    permanent boolean
)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, cap_ai, public
AS $$
DECLARE
  exhausted boolean;
BEGIN
  PERFORM 1
    FROM cap_ai.ingestion_jobs
    WHERE id = job FOR UPDATE;
  IF NOT cap_ai.lease_valid(job, worker, fence, run, at_time) THEN
    RAISE EXCEPTION 'stale lease' USING ERRCODE = 'P0410';
  END IF;
  IF safe_code NOT IN('TEMPORARY_UNAVAILABLE', 'PARSER_UNAVAILABLE', 'OCR_UNAVAILABLE', 'SOURCE_UNAVAILABLE') OR retry_at < at_time
    OR retry_at > at_time + interval '5 minutes' THEN
    RAISE EXCEPTION 'invalid retry' USING ERRCODE = 'P0400';
  END IF;
  SELECT attempts >= max_attempts OR permanent INTO exhausted
    FROM cap_ai.ingestion_jobs
    WHERE id = job;
  UPDATE cap_ai.ingestion_jobs SET state = CASE WHEN exhausted THEN 'FAILED' ELSE 'RETRY_WAIT' END, lease_owner = NULL, lease_until = NULL, next_attempt_at = retry_at, error_code = safe_code, updated_at = at_time
    WHERE id = job;
  UPDATE cap_ai.job_attempts SET finished_at = at_time, outcome = CASE WHEN exhausted THEN 'FAILED' ELSE 'RETRY_WAIT' END, error_code = safe_code
    WHERE job_id = job AND fencing_token = fence;
  UPDATE cap_ai.processing_runs SET state = 'ABANDONED', updated_at = at_time
    WHERE id = run AND state = 'BUILDING';
  UPDATE cap_ai.source_read_delegations SET revoked_at = at_time
    WHERE job_id = job AND revoked_at IS NULL;
  IF exhausted THEN
    UPDATE cap_ai.document_versions SET state = 'FAILED', error_code = safe_code, updated_at = at_time
      WHERE id = (SELECT version_id
      FROM cap_ai.ingestion_jobs
      WHERE id = job);
  END IF;
  RETURN true;
END
$$;
