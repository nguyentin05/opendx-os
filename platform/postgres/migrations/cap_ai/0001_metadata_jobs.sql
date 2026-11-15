-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Additive source/metadata foundation. Package sources remain dormant until Plan04.
DO $$ BEGIN
 IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='dxos_ai_owner') THEN CREATE ROLE dxos_ai_owner NOLOGIN NOSUPERUSER NOBYPASSRLS; END IF;
 IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='dxos_ai_api') THEN CREATE ROLE dxos_ai_api LOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT; END IF;
 IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='dxos_ai_worker') THEN CREATE ROLE dxos_ai_worker LOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT; END IF;
 IF NOT EXISTS(SELECT FROM pg_roles WHERE rolname='dxos_ai_dispatcher') THEN CREATE ROLE dxos_ai_dispatcher LOGIN NOSUPERUSER NOBYPASSRLS NOINHERIT; END IF;
END $$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA cap_ai AUTHORIZATION dxos_ai_owner;
REVOKE ALL ON SCHEMA cap_ai FROM PUBLIC;
CREATE TABLE cap_ai.processing_policies(
 id text NOT NULL,version bigint NOT NULL CHECK(version>0),execution text NOT NULL CHECK(execution IN('LOCAL_ONLY','APPROVED_PROVIDER')),
 classification text NOT NULL,approved_by uuid REFERENCES core.users(id),approved_at timestamptz,enabled boolean NOT NULL DEFAULT false,created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(id,version),CHECK(execution='LOCAL_ONLY' OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)));
INSERT INTO cap_ai.processing_policies(id,version,execution,classification,enabled) VALUES('local-only',1,'LOCAL_ONLY','PERSONAL',true);
CREATE TABLE cap_ai.knowledge_collections(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),app_id text NOT NULL REFERENCES core.apps(app_id),kind text NOT NULL CHECK(kind IN('PERSONAL','APP')),
 owner_user_id uuid REFERENCES core.users(id),status text NOT NULL DEFAULT 'ACTIVE' CHECK(status IN('ACTIVE','DISABLED')),revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),CHECK((kind='PERSONAL')=(owner_user_id IS NOT NULL)));
CREATE UNIQUE INDEX collection_personal ON cap_ai.knowledge_collections(app_id,owner_user_id) WHERE kind='PERSONAL';
CREATE UNIQUE INDEX collection_app ON cap_ai.knowledge_collections(app_id) WHERE kind='APP';
CREATE TABLE cap_ai.package_sources(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),app_id text NOT NULL REFERENCES core.apps(app_id),source_id text NOT NULL CHECK(source_id ~ '^[a-z][a-z0-9_-]*$'),incarnation uuid NOT NULL DEFAULT gen_random_uuid(),
 state text NOT NULL DEFAULT 'DECLARED' CHECK(state IN('DECLARED','REMOVED')),desired_revision_id uuid,authorization_generation bigint NOT NULL DEFAULT 1 CHECK(authorization_generation>0),
 declaration_hash text NOT NULL CHECK(declaration_hash ~ '^[0-9a-f]{64}$'),last_publication_generation bigint NOT NULL CHECK(last_publication_generation>0),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(app_id,source_id));
CREATE TABLE cap_ai.package_source_revisions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),package_source_id uuid NOT NULL REFERENCES cap_ai.package_sources(id),incarnation uuid NOT NULL,publication_generation bigint NOT NULL CHECK(publication_generation>0),
 package_digest text NOT NULL CHECK(package_digest ~ '^[0-9a-f]{64}$'),source_path text NOT NULL CHECK(source_path LIKE 'knowledge/%' AND source_path NOT LIKE '%..%' AND position(chr(92) in source_path)=0),
 sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),media_type text NOT NULL CHECK(media_type IN('text/markdown','application/pdf')),size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 20971520),artifact_ref text NOT NULL,
 safety_state text NOT NULL DEFAULT 'PENDING' CHECK(safety_state IN('PENDING','APPROVED','BLOCKED')),parser_profile text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(package_source_id,incarnation,package_digest,sha256,parser_profile),UNIQUE(package_source_id,id));
ALTER TABLE cap_ai.package_sources ADD FOREIGN KEY(id,desired_revision_id) REFERENCES cap_ai.package_source_revisions(package_source_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE cap_ai.package_publications(
 app_id text NOT NULL REFERENCES core.apps(app_id),publication_generation bigint NOT NULL CHECK(publication_generation>0),spec_digest text NOT NULL CHECK(spec_digest ~ '^[0-9a-f]{64}$'),
 state text NOT NULL DEFAULT 'PENDING' CHECK(state IN('PENDING','APPLIED','FAILED')),attempts bigint NOT NULL DEFAULT 0 CHECK(attempts>=0),next_attempt_at timestamptz NOT NULL DEFAULT now(),last_error_code text,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(app_id,publication_generation));
CREATE TABLE cap_ai.knowledge_documents(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),collection_id uuid NOT NULL REFERENCES cap_ai.knowledge_collections(id),source_kind text NOT NULL CHECK(source_kind IN('UPLOAD','PACKAGE')),
 owner_user_id uuid REFERENCES core.users(id),package_source_id uuid UNIQUE REFERENCES cap_ai.package_sources(id),initial_file_id uuid REFERENCES core.files(id),title text NOT NULL CHECK(length(title) BETWEEN 1 AND 512),
 state text NOT NULL DEFAULT 'LIVE' CHECK(state IN('LIVE','DELETED')),access_mode text NOT NULL CHECK(access_mode IN('PRIVATE','ROLE','RESTRICTED')),
 processing_policy_id text NOT NULL,processing_policy_version bigint NOT NULL,policy_revision bigint NOT NULL DEFAULT 1 CHECK(policy_revision>0),revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 authorization_generation bigint NOT NULL DEFAULT 1 CHECK(authorization_generation>0),active_version_id uuid,desired_version_id uuid,deleted_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(processing_policy_id,processing_policy_version) REFERENCES cap_ai.processing_policies(id,version),
 CHECK((source_kind='UPLOAD' AND owner_user_id IS NOT NULL AND initial_file_id IS NOT NULL AND package_source_id IS NULL AND access_mode='PRIVATE') OR(source_kind='PACKAGE' AND owner_user_id IS NULL AND initial_file_id IS NULL AND package_source_id IS NOT NULL AND access_mode IN('ROLE','RESTRICTED'))),
 CHECK((state='DELETED')=(deleted_at IS NOT NULL)),UNIQUE(collection_id,owner_user_id,initial_file_id));
CREATE INDEX documents_collection ON cap_ai.knowledge_documents(collection_id,state,id);
CREATE TABLE cap_ai.document_allowed_roles(document_id uuid NOT NULL REFERENCES cap_ai.knowledge_documents(id),role_name text NOT NULL REFERENCES core.roles(name),created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(document_id,role_name));
CREATE INDEX allowed_role_document ON cap_ai.document_allowed_roles(role_name,document_id);
CREATE TABLE cap_ai.document_grants(document_id uuid NOT NULL REFERENCES cap_ai.knowledge_documents(id),authorization_generation bigint NOT NULL CHECK(authorization_generation>0),user_id uuid NOT NULL REFERENCES core.users(id),granted_by uuid NOT NULL REFERENCES core.users(id),granted_at timestamptz NOT NULL DEFAULT now(),revoked_at timestamptz,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(document_id,authorization_generation,user_id));
CREATE TABLE cap_ai.document_versions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),document_id uuid NOT NULL REFERENCES cap_ai.knowledge_documents(id),version_no bigint NOT NULL CHECK(version_no>0),source_kind text NOT NULL CHECK(source_kind IN('UPLOAD','PACKAGE')),
 source_file_id uuid REFERENCES core.files(id),package_revision_id uuid REFERENCES cap_ai.package_source_revisions(id),sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),
 media_type text NOT NULL CHECK(media_type IN('application/pdf','image/png','image/jpeg','text/markdown')),size_bytes bigint NOT NULL CHECK(size_bytes BETWEEN 1 AND 20971520),parser_profile text NOT NULL,
 state text NOT NULL DEFAULT 'QUEUED' CHECK(state IN('QUEUED','PROCESSING','READY','FAILED','CANCELLED')),active_run_id uuid,error_code text,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(document_id,id),UNIQUE(document_id,version_no),
 CHECK((source_kind='UPLOAD' AND source_file_id IS NOT NULL AND package_revision_id IS NULL) OR(source_kind='PACKAGE' AND source_file_id IS NULL AND package_revision_id IS NOT NULL)));
CREATE UNIQUE INDEX version_upload_recipe ON cap_ai.document_versions(document_id,source_file_id,parser_profile) WHERE source_kind='UPLOAD';
CREATE UNIQUE INDEX version_package_recipe ON cap_ai.document_versions(document_id,package_revision_id,parser_profile) WHERE source_kind='PACKAGE';
ALTER TABLE cap_ai.knowledge_documents ADD FOREIGN KEY(id,active_version_id) REFERENCES cap_ai.document_versions(document_id,id) DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE cap_ai.knowledge_documents ADD FOREIGN KEY(id,desired_version_id) REFERENCES cap_ai.document_versions(document_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE cap_ai.ingestion_jobs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),version_id uuid NOT NULL REFERENCES cap_ai.document_versions(id),kind text NOT NULL CHECK(kind IN('PARSE','EMBED','PURGE')),profile_key text NOT NULL,
 state text NOT NULL DEFAULT 'PENDING' CHECK(state IN('PENDING','LEASED','RETRY_WAIT','SUCCEEDED','FAILED','CANCELLED')),attempts bigint NOT NULL DEFAULT 0 CHECK(attempts>=0),max_attempts bigint NOT NULL DEFAULT 5 CHECK(max_attempts BETWEEN 1 AND 5),
 lease_owner text,lease_until timestamptz,fencing_token bigint NOT NULL DEFAULT 0 CHECK(fencing_token>=0),next_attempt_at timestamptz NOT NULL DEFAULT now(),checkpoint jsonb NOT NULL DEFAULT '{}',error_code text,
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(version_id,kind,profile_key),UNIQUE(version_id,id),
 CHECK((state='LEASED')=(lease_owner IS NOT NULL AND lease_until IS NOT NULL)));
CREATE INDEX jobs_due ON cap_ai.ingestion_jobs(next_attempt_at,id) WHERE state IN('PENDING','RETRY_WAIT');
CREATE INDEX jobs_expiry ON cap_ai.ingestion_jobs(lease_until) WHERE state='LEASED';
CREATE TABLE cap_ai.processing_runs(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),version_id uuid NOT NULL REFERENCES cap_ai.document_versions(id),ingestion_job_id uuid NOT NULL,attempt_no bigint NOT NULL CHECK(attempt_no>0),fencing_token bigint NOT NULL CHECK(fencing_token>0),
 parser_name text NOT NULL DEFAULT 'unqualified',parser_version text NOT NULL DEFAULT 'unqualified',chunker_version text NOT NULL DEFAULT 'unqualified',ir_version bigint NOT NULL DEFAULT 1 CHECK(ir_version=1),
 state text NOT NULL DEFAULT 'BUILDING' CHECK(state IN('BUILDING','COMPLETE','ABANDONED')),element_count bigint NOT NULL DEFAULT 0 CHECK(element_count>=0),chunk_count bigint NOT NULL DEFAULT 0 CHECK(chunk_count>=0),content_digest text CHECK(content_digest ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now(),UNIQUE(ingestion_job_id,fencing_token),UNIQUE(version_id,id),
 FOREIGN KEY(version_id,ingestion_job_id) REFERENCES cap_ai.ingestion_jobs(version_id,id));
ALTER TABLE cap_ai.document_versions ADD FOREIGN KEY(id,active_run_id) REFERENCES cap_ai.processing_runs(version_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE cap_ai.job_attempts(job_id uuid NOT NULL REFERENCES cap_ai.ingestion_jobs(id),attempt_no bigint NOT NULL CHECK(attempt_no>0),fencing_token bigint NOT NULL CHECK(fencing_token>0),worker_id text NOT NULL,started_at timestamptz NOT NULL DEFAULT now(),finished_at timestamptz,outcome text,error_code text,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(job_id,attempt_no),UNIQUE(job_id,fencing_token));
CREATE TABLE cap_ai.request_receipts(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),actor_id uuid NOT NULL REFERENCES core.users(id),authority_app_id text NOT NULL REFERENCES core.apps(app_id),action text NOT NULL,idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 200),payload_hash text NOT NULL CHECK(payload_hash ~ '^[0-9a-f]{64}$'),
 document_id uuid REFERENCES cap_ai.knowledge_documents(id),version_id uuid,job_id uuid REFERENCES cap_ai.ingestion_jobs(id),response_code integer NOT NULL,revision bigint NOT NULL CHECK(revision>0),created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(actor_id,authority_app_id,action,idempotency_key),FOREIGN KEY(document_id,version_id) REFERENCES cap_ai.document_versions(document_id,id));
CREATE TABLE cap_ai.source_read_delegations(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),token_hash text NOT NULL UNIQUE CHECK(token_hash ~ '^[0-9a-f]{64}$'),job_id uuid NOT NULL,fencing_token bigint NOT NULL,run_id uuid NOT NULL REFERENCES cap_ai.processing_runs(id),source_kind text NOT NULL CHECK(source_kind IN('UPLOAD','PACKAGE')),
 source_identity text NOT NULL,sha256 text NOT NULL CHECK(sha256 ~ '^[0-9a-f]{64}$'),owner_user_id uuid REFERENCES core.users(id),processing_policy_id text NOT NULL,processing_policy_version bigint NOT NULL,worker_id text NOT NULL,expires_at timestamptz NOT NULL,revoked_at timestamptz,created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(job_id,fencing_token) REFERENCES cap_ai.job_attempts(job_id,fencing_token),FOREIGN KEY(processing_policy_id,processing_policy_version) REFERENCES cap_ai.processing_policies(id,version));

-- All source identities are immutable. SQL constraints supplement API admission.
CREATE FUNCTION cap_ai.enforce_identity() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE c cap_ai.knowledge_collections;d cap_ai.knowledge_documents;f core.files;v cap_ai.document_versions;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF TG_TABLE_NAME='knowledge_collections' AND (to_jsonb(NEW)-ARRAY['status','revision','updated_at']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['status','revision','updated_at']) THEN RAISE EXCEPTION 'immutable collection'; END IF;
  IF TG_TABLE_NAME='knowledge_documents' THEN
   IF (to_jsonb(NEW)-ARRAY['state','deleted_at','title','access_mode','processing_policy_id','processing_policy_version','policy_revision','revision','authorization_generation','active_version_id','desired_version_id','updated_at']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['state','deleted_at','title','access_mode','processing_policy_id','processing_policy_version','policy_revision','revision','authorization_generation','active_version_id','desired_version_id','updated_at']) OR (OLD.state='DELETED' AND NEW.state<>'DELETED') OR(OLD.access_mode='PRIVATE' AND NEW.access_mode<>'PRIVATE') THEN RAISE EXCEPTION 'immutable document source'; END IF;
  END IF;
  IF TG_TABLE_NAME='document_versions' AND (to_jsonb(NEW)-ARRAY['state','active_run_id','error_code','updated_at']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['state','active_run_id','error_code','updated_at']) THEN RAISE EXCEPTION 'immutable version source'; END IF;
 END IF;
 IF TG_TABLE_NAME='knowledge_documents' THEN
  SELECT * INTO STRICT c FROM cap_ai.knowledge_collections WHERE id=NEW.collection_id;
  IF NEW.source_kind='UPLOAD' THEN
   SELECT * INTO STRICT f FROM core.files WHERE id=NEW.initial_file_id;
   IF c.kind<>'PERSONAL' OR c.owner_user_id<>NEW.owner_user_id OR f.uploaded_by<>NEW.owner_user_id OR f.app_id<>c.app_id OR f.read_policy<>'OWNER_ONLY' THEN RAISE EXCEPTION 'private source mismatch'; END IF;
   IF NOT EXISTS(SELECT FROM cap_ai.processing_policies WHERE id=NEW.processing_policy_id AND version=NEW.processing_policy_version AND execution='LOCAL_ONLY') THEN RAISE EXCEPTION 'private egress forbidden'; END IF;
  ELSIF c.kind<>'APP' OR NOT EXISTS(SELECT FROM cap_ai.package_sources WHERE id=NEW.package_source_id AND app_id=c.app_id) THEN RAISE EXCEPTION 'package source mismatch'; END IF;
 END IF;
 IF TG_TABLE_NAME='document_versions' THEN
  SELECT * INTO STRICT d FROM cap_ai.knowledge_documents WHERE id=NEW.document_id;
  SELECT * INTO STRICT c FROM cap_ai.knowledge_collections WHERE id=d.collection_id;
  IF NEW.source_kind<>d.source_kind THEN RAISE EXCEPTION 'version kind mismatch'; END IF;
  IF NEW.source_kind='UPLOAD' THEN
   SELECT * INTO STRICT f FROM core.files WHERE id=NEW.source_file_id;
   IF f.uploaded_by<>d.owner_user_id OR f.app_id<>c.app_id OR f.read_policy<>'OWNER_ONLY' OR f.sha256_hash<>NEW.sha256 OR f.size_bytes<>NEW.size_bytes OR f.mime_type<>NEW.media_type THEN RAISE EXCEPTION 'version source mismatch'; END IF;
  ELSIF NOT EXISTS(SELECT FROM cap_ai.package_source_revisions WHERE id=NEW.package_revision_id AND package_source_id=d.package_source_id AND sha256=NEW.sha256 AND media_type=NEW.media_type AND size_bytes=NEW.size_bytes) THEN RAISE EXCEPTION 'package version mismatch'; END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER collection_identity BEFORE INSERT OR UPDATE ON cap_ai.knowledge_collections FOR EACH ROW EXECUTE FUNCTION cap_ai.enforce_identity();
CREATE TRIGGER document_identity BEFORE INSERT OR UPDATE ON cap_ai.knowledge_documents FOR EACH ROW EXECUTE FUNCTION cap_ai.enforce_identity();
CREATE TRIGGER version_identity BEFORE INSERT OR UPDATE ON cap_ai.document_versions FOR EACH ROW EXECUTE FUNCTION cap_ai.enforce_identity();
CREATE FUNCTION cap_ai.guard_package_acl() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE d cap_ai.knowledge_documents;c cap_ai.knowledge_collections;
BEGIN
 SELECT * INTO STRICT d FROM cap_ai.knowledge_documents WHERE id=NEW.document_id;
 SELECT * INTO STRICT c FROM cap_ai.knowledge_collections WHERE id=d.collection_id;
 IF d.source_kind<>'PACKAGE' THEN RAISE EXCEPTION 'personal grants forbidden'; END IF;
 IF TG_TABLE_NAME='document_allowed_roles' THEN IF NOT EXISTS(SELECT FROM core.roles WHERE name=NEW.role_name AND app_id=c.app_id) THEN RAISE EXCEPTION 'cross-app role'; END IF; END IF;
 IF TG_TABLE_NAME='document_grants' THEN IF d.access_mode<>'RESTRICTED' OR NEW.authorization_generation<>d.authorization_generation THEN RAISE EXCEPTION 'invalid grant generation'; END IF; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER allowed_role_guard BEFORE INSERT OR UPDATE ON cap_ai.document_allowed_roles FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_package_acl();
CREATE TRIGGER grants_guard BEFORE INSERT OR UPDATE ON cap_ai.document_grants FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_package_acl();

CREATE FUNCTION cap_ai.library_app(actor uuid,acting_role text) RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
 SELECT a.app_id FROM core.apps a JOIN core.roles r ON r.app_id=a.app_id AND r.name=acting_role JOIN core.user_roles ur ON ur.role_name=r.name AND ur.user_id=actor JOIN core.users u ON u.id=actor AND u.status='ACTIVE'
 WHERE a.status='Ready' AND a.spec#>>'{knowledge,version}'='1' AND a.spec#>>'{knowledge,personalLibrary,enabled}'='true'
 AND (a.spec#>'{knowledge,personalLibrary,allowedRoles}') ? acting_role
 AND NOT EXISTS(SELECT FROM core.app_capabilities ac JOIN core.capabilities c ON c.cap_id=ac.cap_id WHERE ac.app_id=a.app_id AND NOT c.enabled)
 ORDER BY a.app_id LIMIT 1
$$;
CREATE FUNCTION cap_ai.read_document(actor uuid,acting_role text,doc uuid) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
 SELECT jsonb_build_object('document_id',d.id,'version_id',coalesce(d.active_version_id,d.desired_version_id),'run_id',v.active_run_id,'policy_revision',d.policy_revision,'authorization_generation',d.authorization_generation,'source_incarnation','','app_publication_generation',0,'processing_policy_id',d.processing_policy_id,'processing_policy_version',d.processing_policy_version)
 FROM cap_ai.knowledge_documents d JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id JOIN cap_ai.processing_policies p ON p.id=d.processing_policy_id AND p.version=d.processing_policy_version
 LEFT JOIN cap_ai.document_versions v ON v.id=coalesce(d.active_version_id,d.desired_version_id)
 JOIN core.files f ON f.id=coalesce(v.source_file_id,d.initial_file_id)
 WHERE d.id=doc AND d.state='LIVE' AND d.source_kind='UPLOAD' AND d.access_mode='PRIVATE' AND d.owner_user_id=actor AND c.kind='PERSONAL' AND c.owner_user_id=actor AND c.status='ACTIVE'
 AND c.app_id=cap_ai.library_app(actor,acting_role) AND f.app_id=c.app_id AND f.uploaded_by=actor AND f.read_policy='OWNER_ONLY' AND f.scan_status='CLEAN'
 AND(v.id IS NULL OR f.sha256_hash=v.sha256) AND p.enabled AND p.execution='LOCAL_ONLY'
$$;
CREATE FUNCTION cap_ai.register_document(actor uuid,acting_role text,request_key text,request_hash text,req jsonb,existing_doc uuid,expected_revision bigint) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE app text;c cap_ai.knowledge_collections;d cap_ai.knowledge_documents;v cap_ai.document_versions;j cap_ai.ingestion_jobs;f core.files;r cap_ai.request_receipts;action_name text;new_doc boolean:=false;result jsonb;
BEGIN
 app:=cap_ai.library_app(actor,acting_role);IF app IS NULL THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 IF length(request_key) NOT BETWEEN 1 AND 200 OR length(coalesce(req->>'title',''))>512 THEN RAISE EXCEPTION 'invalid' USING ERRCODE='P0400';END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai:'||app||':'||actor::text,0));
 IF app IS DISTINCT FROM cap_ai.library_app(actor,acting_role) THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 SELECT * INTO f FROM core.files WHERE id=(req->>'file_id')::uuid AND app_id=app AND uploaded_by=actor AND read_policy='OWNER_ONLY';
 IF NOT FOUND THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 IF f.scan_status<>'CLEAN' THEN RAISE EXCEPTION 'unclean' USING ERRCODE='P0422';END IF;
 IF f.mime_type<>'application/pdf' OR req->>'parser_profile' IS DISTINCT FROM 'standard-text-v1' THEN RAISE EXCEPTION 'unsupported media or recipe' USING ERRCODE='P0415';END IF;
 action_name:=CASE WHEN existing_doc IS NULL THEN 'register' ELSE 'replace:'||existing_doc::text END;
 SELECT * INTO r FROM cap_ai.request_receipts WHERE actor_id=actor AND authority_app_id=app AND action=action_name AND idempotency_key=request_key;
 IF FOUND THEN
  IF cap_ai.read_document(actor,acting_role,r.document_id) IS NULL THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
  IF r.payload_hash<>request_hash THEN RAISE EXCEPTION 'conflict' USING ERRCODE='P0409';END IF;
  RETURN jsonb_build_object('document_id',r.document_id,'version_id',r.version_id,'job_id',r.job_id,'revision',r.revision,'status','QUEUED');
 END IF;
 IF existing_doc IS NULL THEN
  INSERT INTO cap_ai.knowledge_collections(app_id,kind,owner_user_id) VALUES(app,'PERSONAL',actor) ON CONFLICT(app_id,owner_user_id) WHERE kind='PERSONAL' DO UPDATE SET updated_at=cap_ai.knowledge_collections.updated_at RETURNING * INTO c;
  IF c.status<>'ACTIVE' OR(nullif(req->>'collection_id','') IS NOT NULL AND c.id<>(req->>'collection_id')::uuid) THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
  SELECT * INTO d FROM cap_ai.knowledge_documents WHERE collection_id=c.id AND owner_user_id=actor AND initial_file_id=f.id FOR UPDATE;
  IF NOT FOUND THEN
   INSERT INTO cap_ai.knowledge_documents(collection_id,source_kind,owner_user_id,initial_file_id,title,access_mode,processing_policy_id,processing_policy_version) VALUES(c.id,'UPLOAD',actor,f.id,coalesce(nullif(trim(req->>'title'),''),left(f.file_name,512)),'PRIVATE','local-only',1) RETURNING * INTO d;
   new_doc:=true;
  END IF;
 ELSE
  IF cap_ai.read_document(actor,acting_role,existing_doc) IS NULL THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
  SELECT * INTO STRICT d FROM cap_ai.knowledge_documents WHERE id=existing_doc FOR UPDATE;
  IF expected_revision<>d.revision THEN RAISE EXCEPTION 'revision conflict' USING ERRCODE='P0409';END IF;
  SELECT * INTO STRICT c FROM cap_ai.knowledge_collections WHERE id=d.collection_id;
  IF nullif(req->>'collection_id','') IS NOT NULL AND c.id<>(req->>'collection_id')::uuid THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 END IF;
 IF d.state<>'LIVE' THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 SELECT * INTO v FROM cap_ai.document_versions WHERE document_id=d.id AND source_file_id=f.id AND parser_profile=req->>'parser_profile';
 IF NOT FOUND THEN
  INSERT INTO cap_ai.document_versions(document_id,version_no,source_kind,source_file_id,sha256,media_type,size_bytes,parser_profile)
  SELECT d.id,coalesce(max(version_no),0)+1,'UPLOAD',f.id,f.sha256_hash,f.mime_type,f.size_bytes,req->>'parser_profile' FROM cap_ai.document_versions WHERE document_id=d.id RETURNING * INTO v;
  INSERT INTO cap_ai.ingestion_jobs(version_id,kind,profile_key) VALUES(v.id,'PARSE',v.parser_profile) RETURNING * INTO j;
  UPDATE cap_ai.knowledge_documents SET desired_version_id=v.id,revision=revision+CASE WHEN new_doc THEN 0 ELSE 1 END,updated_at=now() WHERE id=d.id RETURNING * INTO d;
  -- Superseded leases no longer delegate; old active data is preserved.
  UPDATE cap_ai.ingestion_jobs SET state='CANCELLED',lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE version_id IN(SELECT id FROM cap_ai.document_versions WHERE document_id=d.id AND id<>v.id) AND state IN('PENDING','LEASED','RETRY_WAIT');
  UPDATE cap_ai.source_read_delegations SET revoked_at=now() WHERE job_id IN(SELECT id FROM cap_ai.ingestion_jobs WHERE state='CANCELLED') AND revoked_at IS NULL;
 ELSE
  IF d.desired_version_id IS DISTINCT FROM v.id THEN RAISE EXCEPTION 'superseded source recipe' USING ERRCODE='P0409';END IF;
  SELECT * INTO STRICT j FROM cap_ai.ingestion_jobs WHERE version_id=v.id AND kind='PARSE' AND profile_key=v.parser_profile;
  IF j.state IN('FAILED','CANCELLED') THEN RAISE EXCEPTION 'terminal source recipe' USING ERRCODE='P0409';END IF;
 END IF;
 INSERT INTO core.audit_logs(app_id,actor_id,acting_role,action,entity_type,entity_id,entity_version,payload) VALUES(app,actor,acting_role,'knowledge.'||action_name,'cap_ai.knowledge_documents',d.id,d.revision,jsonb_build_object('version_id',v.id,'job_id',j.id));
 INSERT INTO cap_ai.request_receipts(actor_id,authority_app_id,action,idempotency_key,payload_hash,document_id,version_id,job_id,response_code,revision) VALUES(actor,app,action_name,request_key,request_hash,d.id,v.id,j.id,202,d.revision);
 IF cap_ai.read_document(actor,acting_role,d.id) IS NULL THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 RETURN jsonb_build_object('document_id',d.id,'version_id',v.id,'job_id',j.id,'revision',d.revision,'status','QUEUED');
END $$;

CREATE FUNCTION cap_ai.eligible_version(ver uuid) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
 SELECT EXISTS(SELECT FROM cap_ai.document_versions v JOIN cap_ai.knowledge_documents d ON d.id=v.document_id JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id JOIN core.files f ON f.id=v.source_file_id JOIN cap_ai.processing_policies p ON p.id=d.processing_policy_id AND p.version=d.processing_policy_version
 WHERE v.id=ver AND d.source_kind='UPLOAD' AND d.access_mode='PRIVATE' AND d.state='LIVE' AND d.desired_version_id=v.id AND c.status='ACTIVE' AND f.app_id=c.app_id AND f.uploaded_by=d.owner_user_id AND f.read_policy='OWNER_ONLY' AND f.scan_status='CLEAN' AND f.sha256_hash=v.sha256 AND p.enabled AND p.execution='LOCAL_ONLY'
 AND EXISTS(SELECT FROM core.user_roles ur WHERE ur.user_id=d.owner_user_id AND cap_ai.library_app(d.owner_user_id,ur.role_name)=c.app_id))
$$;
CREATE FUNCTION cap_ai.lease_valid(job uuid,worker text,fence bigint,run uuid,at_time timestamptz) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
 SELECT EXISTS(SELECT FROM cap_ai.ingestion_jobs j JOIN cap_ai.processing_runs r ON r.ingestion_job_id=j.id AND r.fencing_token=j.fencing_token WHERE j.id=job AND j.state='LEASED' AND j.lease_owner=worker AND j.fencing_token=fence AND j.lease_until>at_time AND r.id=run AND r.state IN('BUILDING','COMPLETE') AND cap_ai.eligible_version(j.version_id))
$$;
CREATE FUNCTION cap_ai.claim_job(worker text,at_time timestamptz) RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE j cap_ai.ingestion_jobs;r cap_ai.processing_runs;
BEGIN
 IF length(worker) NOT BETWEEN 1 AND 200 THEN RAISE EXCEPTION 'invalid worker' USING ERRCODE='P0400';END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai.worker:'||worker,0));
 IF (SELECT count(*) FROM cap_ai.ingestion_jobs WHERE state='LEASED' AND lease_owner=worker AND lease_until>at_time)>=2 THEN RETURN NULL;END IF;
 UPDATE cap_ai.ingestion_jobs SET state='FAILED',error_code='RETRY_EXHAUSTED',lease_owner=NULL,lease_until=NULL,updated_at=at_time WHERE attempts>=max_attempts AND state IN('PENDING','RETRY_WAIT','LEASED') AND (lease_until IS NULL OR lease_until<=at_time);
 SELECT * INTO j FROM cap_ai.ingestion_jobs WHERE kind='PARSE' AND attempts<max_attempts AND ((state IN('PENDING','RETRY_WAIT') AND next_attempt_at<=at_time) OR(state='LEASED' AND lease_until<=at_time)) AND cap_ai.eligible_version(version_id) ORDER BY next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
 IF NOT FOUND THEN RETURN NULL;END IF;
 UPDATE cap_ai.processing_runs SET state='ABANDONED',updated_at=at_time WHERE ingestion_job_id=j.id AND state='BUILDING';
 UPDATE cap_ai.source_read_delegations SET revoked_at=at_time WHERE job_id=j.id AND revoked_at IS NULL;
 UPDATE cap_ai.job_attempts SET finished_at=at_time,outcome='LEASE_LOST',error_code='LEASE_EXPIRED' WHERE job_id=j.id AND finished_at IS NULL;
 UPDATE cap_ai.ingestion_jobs SET state='LEASED',lease_owner=worker,lease_until=at_time+interval '60 seconds',attempts=attempts+1,fencing_token=fencing_token+1,updated_at=at_time WHERE id=j.id RETURNING * INTO j;
 INSERT INTO cap_ai.job_attempts(job_id,attempt_no,fencing_token,worker_id,started_at) VALUES(j.id,j.attempts,j.fencing_token,worker,at_time);
 INSERT INTO cap_ai.processing_runs(version_id,ingestion_job_id,attempt_no,fencing_token) VALUES(j.version_id,j.id,j.attempts,j.fencing_token) RETURNING * INTO r;
 UPDATE cap_ai.document_versions SET state='PROCESSING',updated_at=at_time WHERE id=j.version_id;
 RETURN jsonb_build_object('job_id',j.id,'version_id',j.version_id,'run_id',r.id,'worker_id',worker,'fence',j.fencing_token,'expires_at',j.lease_until);
END $$;
CREATE FUNCTION cap_ai.heartbeat_job(job uuid,worker text,fence bigint,run uuid,at_time timestamptz) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
BEGIN
 PERFORM 1 FROM cap_ai.ingestion_jobs WHERE id=job FOR UPDATE;
 IF NOT cap_ai.lease_valid(job,worker,fence,run,at_time) THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='P0410';END IF;
 UPDATE cap_ai.ingestion_jobs SET lease_until=at_time+interval '60 seconds',updated_at=at_time WHERE id=job;
 RETURN true;
END $$;
CREATE FUNCTION cap_ai.checkpoint_job(job uuid,worker text,fence bigint,run uuid,at_time timestamptz,value jsonb) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
BEGIN
 PERFORM 1 FROM cap_ai.ingestion_jobs WHERE id=job FOR UPDATE;
 IF NOT cap_ai.lease_valid(job,worker,fence,run,at_time) THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='P0410';END IF;
 IF jsonb_typeof(value)<>'object' OR octet_length(value::text)>65536 THEN RAISE EXCEPTION 'invalid checkpoint' USING ERRCODE='P0400';END IF;
 UPDATE cap_ai.ingestion_jobs SET checkpoint=value,updated_at=at_time WHERE id=job;
 RETURN true;
END $$;
CREATE FUNCTION cap_ai.activate_job(job uuid,worker text,fence bigint,run uuid,at_time timestamptz) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
BEGIN
 -- Plan02 has no derived-artifact completion writer: activation cannot be qualified yet.
 -- Still check the exact fence so stale worker requests are never silently accepted.
 PERFORM 1 FROM cap_ai.ingestion_jobs WHERE id=job FOR UPDATE;
 IF NOT cap_ai.lease_valid(job,worker,fence,run,at_time) THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='P0410';END IF;
 RAISE EXCEPTION 'derived pipeline unavailable' USING ERRCODE='P0400';
END $$;
CREATE FUNCTION cap_ai.retry_job(job uuid,worker text,fence bigint,run uuid,at_time timestamptz,retry_at timestamptz,safe_code text,permanent boolean) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE exhausted boolean;
BEGIN
 PERFORM 1 FROM cap_ai.ingestion_jobs WHERE id=job FOR UPDATE;
 IF NOT cap_ai.lease_valid(job,worker,fence,run,at_time) THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='P0410';END IF;
 IF safe_code NOT IN('TEMPORARY_UNAVAILABLE','PARSER_UNAVAILABLE','OCR_UNAVAILABLE','SOURCE_UNAVAILABLE') OR retry_at<at_time OR retry_at>at_time+interval '5 minutes' THEN RAISE EXCEPTION 'invalid retry' USING ERRCODE='P0400';END IF;
 SELECT attempts>=max_attempts OR permanent INTO exhausted FROM cap_ai.ingestion_jobs WHERE id=job;
 UPDATE cap_ai.ingestion_jobs SET state=CASE WHEN exhausted THEN 'FAILED' ELSE 'RETRY_WAIT' END,lease_owner=NULL,lease_until=NULL,next_attempt_at=retry_at,error_code=safe_code,updated_at=at_time WHERE id=job;
 UPDATE cap_ai.job_attempts SET finished_at=at_time,outcome=CASE WHEN exhausted THEN 'FAILED' ELSE 'RETRY_WAIT' END,error_code=safe_code WHERE job_id=job AND fencing_token=fence;
 UPDATE cap_ai.processing_runs SET state='ABANDONED',updated_at=at_time WHERE id=run;
 UPDATE cap_ai.source_read_delegations SET revoked_at=at_time WHERE job_id=job AND revoked_at IS NULL;
 IF exhausted THEN UPDATE cap_ai.document_versions SET state='FAILED',error_code=safe_code,updated_at=at_time WHERE id=(SELECT version_id FROM cap_ai.ingestion_jobs WHERE id=job);END IF;
 RETURN true;
END $$;
CREATE FUNCTION cap_ai.issue_delegation(job uuid,worker text,fence bigint,run uuid,at_time timestamptz,token_digest text) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
BEGIN
 PERFORM 1 FROM cap_ai.ingestion_jobs WHERE id=job FOR UPDATE;
 IF NOT cap_ai.lease_valid(job,worker,fence,run,at_time) THEN RAISE EXCEPTION 'stale lease' USING ERRCODE='P0410';END IF;
 INSERT INTO cap_ai.source_read_delegations(token_hash,job_id,fencing_token,run_id,source_kind,source_identity,sha256,owner_user_id,processing_policy_id,processing_policy_version,worker_id,expires_at)
 SELECT token_digest,j.id,fence,run,v.source_kind,v.source_file_id::text,v.sha256,d.owner_user_id,d.processing_policy_id,d.processing_policy_version,worker,least(j.lease_until,at_time+interval '60 seconds')
 FROM cap_ai.ingestion_jobs j JOIN cap_ai.document_versions v ON v.id=j.version_id JOIN cap_ai.knowledge_documents d ON d.id=v.document_id WHERE j.id=job;
 RETURN true;
END $$;
CREATE FUNCTION cap_ai.validate_delegation(token_digest text,worker text,source text) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
 SELECT jsonb_build_object('source_kind',s.source_kind,'source_id',s.source_identity,'sha256',s.sha256,'owner_id',s.owner_user_id,'expires_at',s.expires_at)
 FROM cap_ai.source_read_delegations s JOIN cap_ai.ingestion_jobs j ON j.id=s.job_id JOIN cap_ai.document_versions v ON v.id=j.version_id JOIN cap_ai.knowledge_documents d ON d.id=v.document_id
 WHERE s.token_hash=token_digest AND s.worker_id=worker AND s.source_identity=source AND s.revoked_at IS NULL AND s.expires_at>statement_timestamp()
 AND s.sha256=v.sha256 AND s.owner_user_id=d.owner_user_id AND s.processing_policy_id=d.processing_policy_id AND s.processing_policy_version=d.processing_policy_version
 AND cap_ai.lease_valid(s.job_id,worker,s.fencing_token,s.run_id,statement_timestamp())
$$;

CREATE FUNCTION cap_ai.document_metadata(actor uuid,acting_role text,doc uuid) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
 SELECT jsonb_build_object('document_id',d.id,'collection_id',d.collection_id,'title',d.title,'revision',d.revision,'status',d.state,'active_version_id',d.active_version_id,'desired_version_id',d.desired_version_id,'active_version_status',a.state,'desired_version_status',v.state,'warnings',CASE WHEN v.error_code IS NULL THEN '[]'::jsonb ELSE jsonb_build_array(v.error_code) END,'created_at',d.created_at,'updated_at',d.updated_at)
 FROM cap_ai.knowledge_documents d LEFT JOIN cap_ai.document_versions a ON a.id=d.active_version_id LEFT JOIN cap_ai.document_versions v ON v.id=d.desired_version_id
 WHERE d.id=doc AND cap_ai.read_document(actor,acting_role,doc) IS NOT NULL
$$;
CREATE FUNCTION cap_ai.list_documents(actor uuid,acting_role text,after_id uuid,page_size integer) RETURNS jsonb LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE app text;result jsonb;
BEGIN
 app:=cap_ai.library_app(actor,acting_role);IF app IS NULL THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 IF page_size<1 OR page_size>100 THEN RAISE EXCEPTION 'invalid page' USING ERRCODE='P0400';END IF;
 SELECT coalesce(jsonb_agg(q.metadata ORDER BY q.id),'[]'::jsonb) INTO result FROM(
  SELECT d.id,cap_ai.document_metadata(actor,acting_role,d.id) metadata FROM cap_ai.knowledge_documents d JOIN cap_ai.knowledge_collections c ON c.id=d.collection_id
  WHERE d.owner_user_id=actor AND c.app_id=app AND d.state='LIVE' AND (after_id IS NULL OR d.id>after_id) AND cap_ai.read_document(actor,acting_role,d.id) IS NOT NULL ORDER BY d.id LIMIT page_size+1
 )q;
 RETURN jsonb_build_object('app_id',app,'documents',result);
END $$;
CREATE FUNCTION cap_ai.get_collections(actor uuid,acting_role text) RETURNS jsonb LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE app text;result jsonb;
BEGIN
 app:=cap_ai.library_app(actor,acting_role);IF app IS NULL THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 SELECT coalesce(jsonb_agg(jsonb_build_object('collection_id',id,'app_id',app_id,'kind',kind,'revision',revision) ORDER BY id),'[]'::jsonb) INTO result FROM cap_ai.knowledge_collections WHERE app_id=app AND owner_user_id=actor AND kind='PERSONAL' AND status='ACTIVE';
 RETURN result;
END $$;
CREATE FUNCTION cap_ai.ingestion_metadata(actor uuid,acting_role text,job uuid) RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
 SELECT jsonb_build_object('job_id',j.id,'document_id',v.document_id,'version_id',v.id,'status',j.state,'attempts',j.attempts,'error_code',j.error_code)
 FROM cap_ai.ingestion_jobs j JOIN cap_ai.document_versions v ON v.id=j.version_id WHERE j.id=job AND cap_ai.read_document(actor,acting_role,v.document_id) IS NOT NULL
$$;
CREATE FUNCTION cap_ai.delete_document(actor uuid,acting_role text,request_key text,doc uuid,expected_revision bigint) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE app text;d cap_ai.knowledge_documents;r cap_ai.request_receipts;
BEGIN
 app:=cap_ai.library_app(actor,acting_role);IF app IS NULL THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 IF length(request_key) NOT BETWEEN 1 AND 200 THEN RAISE EXCEPTION 'invalid key' USING ERRCODE='P0400';END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('cap_ai:'||app||':'||actor::text,0));
 IF app IS DISTINCT FROM cap_ai.library_app(actor,acting_role) THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 SELECT d1.* INTO d FROM cap_ai.knowledge_documents d1 JOIN cap_ai.knowledge_collections c ON c.id=d1.collection_id WHERE d1.id=doc AND d1.owner_user_id=actor AND c.app_id=app AND c.status='ACTIVE' FOR UPDATE OF d1;
 IF NOT FOUND THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 SELECT * INTO r FROM cap_ai.request_receipts WHERE actor_id=actor AND authority_app_id=app AND action='delete' AND idempotency_key=request_key;
 IF FOUND THEN IF r.document_id<>doc THEN RAISE EXCEPTION 'conflict' USING ERRCODE='P0409';END IF;IF app IS DISTINCT FROM cap_ai.library_app(actor,acting_role) THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;RETURN true;END IF;
 IF d.state<>'LIVE' THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 IF d.revision<>expected_revision THEN RAISE EXCEPTION 'revision conflict' USING ERRCODE='P0409';END IF;
 UPDATE cap_ai.knowledge_documents SET state='DELETED',deleted_at=now(),revision=revision+1,policy_revision=policy_revision+1,updated_at=now() WHERE id=doc RETURNING * INTO d;
 UPDATE cap_ai.ingestion_jobs SET state='CANCELLED',lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE version_id IN(SELECT id FROM cap_ai.document_versions WHERE document_id=doc) AND state IN('PENDING','LEASED','RETRY_WAIT');
 UPDATE cap_ai.source_read_delegations SET revoked_at=now() WHERE job_id IN(SELECT j.id FROM cap_ai.ingestion_jobs j JOIN cap_ai.document_versions v ON v.id=j.version_id WHERE v.document_id=doc) AND revoked_at IS NULL;
 UPDATE cap_ai.processing_runs SET state='ABANDONED',updated_at=now() WHERE version_id IN(SELECT id FROM cap_ai.document_versions WHERE document_id=doc) AND state='BUILDING';
 INSERT INTO core.audit_logs(app_id,actor_id,acting_role,action,entity_type,entity_id,entity_version,payload) VALUES(app,actor,acting_role,'knowledge.delete','cap_ai.knowledge_documents',doc,d.revision,'{}');
 INSERT INTO cap_ai.request_receipts(actor_id,authority_app_id,action,idempotency_key,payload_hash,document_id,response_code,revision) VALUES(actor,app,'delete',request_key,encode(digest(doc::text,'sha256'),'hex'),doc,204,d.revision);
 IF app IS DISTINCT FROM cap_ai.library_app(actor,acting_role) THEN RAISE EXCEPTION 'unavailable' USING ERRCODE='P0404';END IF;
 RETURN true;
END $$;
CREATE FUNCTION cap_ai.recover_jobs(at_time timestamptz) RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,cap_ai,public AS $$
DECLARE j cap_ai.ingestion_jobs;terminal boolean;
BEGIN
 FOR j IN SELECT * FROM cap_ai.ingestion_jobs WHERE state='LEASED' AND lease_until<=at_time ORDER BY id FOR UPDATE SKIP LOCKED LOOP
  terminal:=j.attempts>=j.max_attempts;
  UPDATE cap_ai.ingestion_jobs SET state=CASE WHEN terminal THEN 'FAILED' ELSE 'RETRY_WAIT' END,lease_owner=NULL,lease_until=NULL,error_code=CASE WHEN terminal THEN 'RETRY_EXHAUSTED' ELSE 'LEASE_EXPIRED' END,next_attempt_at=at_time+random()*least(300,5*power(2,j.attempts-1))*interval '1 second',updated_at=at_time WHERE id=j.id;
  UPDATE cap_ai.processing_runs SET state='ABANDONED',updated_at=at_time WHERE ingestion_job_id=j.id AND state='BUILDING';
  UPDATE cap_ai.source_read_delegations SET revoked_at=at_time WHERE job_id=j.id AND revoked_at IS NULL;
  UPDATE cap_ai.job_attempts SET finished_at=at_time,outcome='LEASE_LOST',error_code='LEASE_EXPIRED' WHERE job_id=j.id AND finished_at IS NULL;
  IF terminal THEN UPDATE cap_ai.document_versions SET state='FAILED',error_code='RETRY_EXHAUSTED',updated_at=at_time WHERE id=j.version_id;END IF;
 END LOOP;
 -- No original, history or referenced staging deletion is authorized by this operation.
 RETURN true;
END $$;
CREATE FUNCTION cap_ai.guard_immutable_metadata() RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog,cap_ai,public AS $$
BEGIN
 IF TG_TABLE_NAME='package_source_revisions' AND(to_jsonb(NEW)-'safety_state') IS DISTINCT FROM(to_jsonb(OLD)-'safety_state') THEN RAISE EXCEPTION 'immutable package revision';END IF;
 IF TG_TABLE_NAME='package_sources' THEN IF NEW.id IS DISTINCT FROM OLD.id OR NEW.app_id IS DISTINCT FROM OLD.app_id OR NEW.source_id IS DISTINCT FROM OLD.source_id THEN RAISE EXCEPTION 'immutable package source';END IF;END IF;
 IF TG_TABLE_NAME='processing_runs' THEN IF OLD.state='COMPLETE' OR NEW.version_id IS DISTINCT FROM OLD.version_id OR NEW.ingestion_job_id IS DISTINCT FROM OLD.ingestion_job_id OR NEW.attempt_no IS DISTINCT FROM OLD.attempt_no OR NEW.fencing_token IS DISTINCT FROM OLD.fencing_token THEN RAISE EXCEPTION 'immutable processing run';END IF;END IF;
 IF TG_TABLE_NAME='source_read_delegations' AND(to_jsonb(NEW)-'revoked_at') IS DISTINCT FROM(to_jsonb(OLD)-'revoked_at') THEN RAISE EXCEPTION 'immutable source delegation';END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER package_revision_immutable BEFORE UPDATE ON cap_ai.package_source_revisions FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_immutable_metadata();
CREATE TRIGGER package_source_immutable BEFORE UPDATE ON cap_ai.package_sources FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_immutable_metadata();
CREATE TRIGGER run_immutable BEFORE UPDATE ON cap_ai.processing_runs FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_immutable_metadata();
CREATE TRIGGER delegation_immutable BEFORE UPDATE ON cap_ai.source_read_delegations FOR EACH ROW EXECUTE FUNCTION cap_ai.guard_immutable_metadata();

-- No capability role has unscoped table reads, Core identity writes, or a schema-owner membership.
GRANT USAGE ON SCHEMA core TO dxos_ai_owner,dxos_ai_api;
GRANT SELECT ON core.users,core.roles,core.user_roles,core.apps,core.app_capabilities,core.capabilities,core.files TO dxos_ai_owner,dxos_ai_api;
GRANT SELECT,INSERT ON core.audit_logs TO dxos_ai_owner;
GRANT USAGE,SELECT ON SEQUENCE core.audit_logs_id_seq TO dxos_ai_owner;
GRANT USAGE ON SCHEMA cap_ai TO dxos_ai_api,dxos_ai_worker,dxos_ai_dispatcher;
DO $$ DECLARE r record; BEGIN
 FOR r IN SELECT tablename FROM pg_tables WHERE schemaname='cap_ai' LOOP EXECUTE format('ALTER TABLE cap_ai.%I OWNER TO dxos_ai_owner',r.tablename); END LOOP;
 FOR r IN SELECT p.oid::regprocedure signature FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='cap_ai' LOOP EXECUTE format('ALTER FUNCTION %s OWNER TO dxos_ai_owner',r.signature); END LOOP;
END $$;
REVOKE ALL ON ALL TABLES IN SCHEMA cap_ai FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA cap_ai FROM PUBLIC;
GRANT EXECUTE ON FUNCTION cap_ai.read_document(uuid,text,uuid),cap_ai.library_app(uuid,text) TO dxos_ai_api;

GRANT EXECUTE ON FUNCTION cap_ai.register_document(uuid,text,text,text,jsonb,uuid,bigint) TO dxos_ai_api;

GRANT EXECUTE ON FUNCTION cap_ai.claim_job(text,timestamptz),cap_ai.heartbeat_job(uuid,text,bigint,uuid,timestamptz),cap_ai.checkpoint_job(uuid,text,bigint,uuid,timestamptz,jsonb),cap_ai.activate_job(uuid,text,bigint,uuid,timestamptz),cap_ai.retry_job(uuid,text,bigint,uuid,timestamptz,timestamptz,text,boolean),cap_ai.issue_delegation(uuid,text,bigint,uuid,timestamptz,text),cap_ai.validate_delegation(text,text,text) TO dxos_ai_worker;

GRANT EXECUTE ON FUNCTION cap_ai.document_metadata(uuid,text,uuid),cap_ai.list_documents(uuid,text,uuid,integer),cap_ai.get_collections(uuid,text),cap_ai.ingestion_metadata(uuid,text,uuid),cap_ai.delete_document(uuid,text,text,uuid,bigint) TO dxos_ai_api;
GRANT EXECUTE ON FUNCTION cap_ai.recover_jobs(timestamptz) TO dxos_ai_worker;
