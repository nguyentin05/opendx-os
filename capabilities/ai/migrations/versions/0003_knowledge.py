# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from alembic import op

revision = "0003"
down_revision = "0002"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE cap_ai.documents (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            app_id text NOT NULL REFERENCES core.apps(app_id),
            owner_id uuid REFERENCES core.users(id),
            file_id uuid REFERENCES core.files(id),
            source_id text,
            title text NOT NULL,
            revision bigint NOT NULL DEFAULT 1,
            deleted_at timestamptz,
            active_version uuid,
            desired_version uuid,
            CHECK ((owner_id IS NOT NULL AND file_id IS NOT NULL AND source_id IS NULL)
                OR (owner_id IS NULL AND file_id IS NULL AND source_id IS NOT NULL)),
            UNIQUE (owner_id, file_id),
            UNIQUE (app_id, source_id)
        );
        CREATE TABLE cap_ai.document_versions (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            document_id uuid NOT NULL REFERENCES cap_ai.documents(id),
            publication_generation bigint NOT NULL DEFAULT 0,
            incarnation uuid,
            authority_generation bigint,
            declaration_hash text,
            artifact_ref text,
            sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
            size_bytes int NOT NULL CHECK (size_bytes BETWEEN 1 AND 20971520),
            media_type text NOT NULL CHECK (media_type IN ('application/pdf','text/markdown')),
            status text NOT NULL DEFAULT 'PENDING'
                CHECK (status IN ('PENDING','INDEXING','READY','FAILED')),
            UNIQUE (document_id, id),
            UNIQUE (document_id, publication_generation)
        );
        ALTER TABLE cap_ai.documents
            ADD FOREIGN KEY (id, active_version)
                REFERENCES cap_ai.document_versions(document_id, id),
            ADD FOREIGN KEY (id, desired_version)
                REFERENCES cap_ai.document_versions(document_id, id);
        CREATE TABLE cap_ai.document_grants (
            document_id uuid NOT NULL REFERENCES cap_ai.documents(id),
            user_id uuid NOT NULL REFERENCES core.users(id),
            incarnation uuid NOT NULL,
            authority_generation bigint NOT NULL,
            granted_by uuid NOT NULL REFERENCES core.users(id),
            PRIMARY KEY (document_id, user_id)
        );
        CREATE TABLE cap_ai.ingestion_jobs (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            version_id uuid NOT NULL UNIQUE REFERENCES cap_ai.document_versions(id),
            state text NOT NULL DEFAULT 'PENDING'
                CHECK (state IN ('PENDING','INDEXING','READY','FAILED')),
            attempts int NOT NULL DEFAULT 0,
            publish_attempts int NOT NULL DEFAULT 0,
            next_dispatch_at timestamptz NOT NULL DEFAULT now(),
            dispatched_at timestamptz,
            lease_token uuid,
            lease_until timestamptz,
            delegation_hash text,
            error_code text
        );

        CREATE FUNCTION cap_ai.ready_app(app text)
        RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT a.spec FROM core.apps a
            WHERE a.app_id=app AND a.status='Ready'
              AND a.spec #>> '{knowledge,version}'='1'
              AND EXISTS (
                  SELECT FROM core.app_capabilities ac JOIN core.capabilities c USING (cap_id)
                  WHERE ac.app_id=app AND c.cap_id='cap-ai' AND c.enabled
              )
              AND NOT EXISTS (
                  SELECT FROM core.app_capabilities ac JOIN core.capabilities c USING (cap_id)
                  WHERE ac.app_id=app AND NOT c.enabled
              )
        $$;

        CREATE FUNCTION cap_ai.publication(app text, gen bigint, digest text)
        RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT jsonb_build_object('status', a.status, 'spec', a.spec)
            FROM core.apps a WHERE a.app_id=app AND a.status IN ('Ready','Removed')
              AND a.publication_generation=gen
              AND a.spec #>> '{_publication,generation}'=gen::text
              AND a.spec #>> '{_publication,spec_digest}'=digest
        $$;

        CREATE FUNCTION cap_ai.package_source(app text, source text)
        RETURNS jsonb LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT s.value || jsonb_build_object(
                'declaration', d.value, 'generation', a.publication_generation,
                'policies', a.spec #> '{knowledge,grantPolicies}'
            )
            FROM core.apps a
            CROSS JOIN LATERAL jsonb_array_elements(a.spec #> '{_publication,sources}') s
            CROSS JOIN LATERAL jsonb_array_elements(a.spec #> '{knowledge,sources}') d
            WHERE a.app_id=app AND cap_ai.ready_app(app) IS NOT NULL
              AND a.spec #>> '{_publication,generation}'=a.publication_generation::text
              AND s.value->>'source_id'=source AND d.value->>'id'=source
              AND s.value->>'safety_state'='APPROVED'
              AND s.value->>'source_path'=d.value->>'path'
              AND s.value->>'media_type'=d.value->>'mediaType'
              AND s.value->>'parser_profile'=d.value->>'indexingProfile'
              AND s.value->>'parser_profile'='standard-text-v1'
        $$;

        CREATE FUNCTION cap_ai.personal_role(member uuid, app text, selected text)
        RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT EXISTS (
                SELECT FROM core.users u JOIN core.user_roles ur ON ur.user_id=u.id
                JOIN core.roles r ON r.name=ur.role_name
                CROSS JOIN LATERAL cap_ai.ready_app(app) s
                WHERE u.id=member AND u.status='ACTIVE' AND r.app_id=app
                  AND (selected IS NULL OR r.name=selected)
                  AND s #>> '{knowledge,personalLibrary,enabled}'='true'
                  AND (s #> '{knowledge,personalLibrary,allowedRoles}') ? r.name
            )
        $$;

        CREATE FUNCTION cap_ai.private_file(member uuid, role text, file uuid)
        RETURNS TABLE (id uuid, app_id text, file_name text, sha256 text, size_bytes bigint,
            mime_type text, scan_status text)
        LANGUAGE sql STABLE SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
            SELECT f.id, f.app_id, f.file_name, f.sha256_hash, f.size_bytes,
                f.mime_type, f.scan_status FROM core.files f
            WHERE f.id=file AND f.uploaded_by=member AND f.read_policy='OWNER_ONLY'
              AND cap_ai.personal_role(member, f.app_id, role)
        $$;

        CREATE FUNCTION cap_ai.source_current(document uuid, version uuid)
        RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT EXISTS (
                SELECT FROM cap_ai.documents d JOIN cap_ai.document_versions v ON v.document_id=d.id
                LEFT JOIN LATERAL cap_ai.package_source(d.app_id, d.source_id) s ON true
                WHERE d.id=document AND v.id=version AND d.deleted_at IS NULL AND (
                    (d.owner_id IS NOT NULL AND EXISTS (
                        SELECT FROM cap_ai.private_file(d.owner_id, NULL, d.file_id) f
                        WHERE f.scan_status='CLEAN' AND f.app_id=d.app_id AND f.sha256=v.sha256
                          AND f.size_bytes=v.size_bytes AND f.mime_type=v.media_type
                    ))
                    OR
                    (d.source_id IS NOT NULL AND s IS NOT NULL
                        AND s->>'generation'=v.publication_generation::text
                        AND s->>'incarnation'=v.incarnation::text
                        AND s->>'authorization_generation'=v.authority_generation::text
                        AND s->>'declaration_hash'=v.declaration_hash
                        AND s->>'sha256'=v.sha256 AND s->>'artifact_ref'=v.artifact_ref
                        AND s->>'size_bytes'=v.size_bytes::text AND s->>'media_type'=v.media_type)
                )
            )
        $$;

        CREATE FUNCTION cap_ai.source_access(
            actor uuid, role text, document uuid, version uuid, manage boolean
        ) RETURNS boolean LANGUAGE sql STABLE SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
            SELECT EXISTS (
                SELECT FROM cap_ai.documents d
                LEFT JOIN LATERAL cap_ai.package_source(d.app_id,d.source_id) s ON true
                WHERE d.id=document AND d.deleted_at IS NULL
                  AND cap_ai.holds_role(actor, role, false)
                  AND cap_ai.source_current(d.id, version)
                  AND (
                    (d.owner_id=actor AND NOT manage)
                    OR
                    (d.source_id IS NOT NULL AND EXISTS (
                        SELECT FROM core.roles r WHERE r.name=role AND r.app_id=d.app_id
                    ) AND CASE
                        WHEN manage THEN (
                            s #>> '{declaration,access,mode}'='RESTRICTED' AND
                            (s->'policies'->(s #>> '{declaration,access,grantsManagedBy}')
                                ->'allowedRoles') ? role
                        )
                        ELSE (
                            (s #> '{declaration,access,allowedRoles}') ? role AND (
                                s #>> '{declaration,access,mode}'='ROLE'
                                OR (
                                    s #>> '{declaration,access,mode}'='RESTRICTED'
                                    AND EXISTS (
                                        SELECT FROM cap_ai.document_grants g
                                        WHERE g.document_id=d.id AND g.user_id=actor
                                          AND g.incarnation::text=s->>'incarnation'
                                          AND g.authority_generation::text=
                                            s->>'authorization_generation'
                                    )
                                )
                            )
                        )
                    END)
                  )
            )
        $$;

        REVOKE ALL ON FUNCTION cap_ai.ready_app(text), cap_ai.publication(text,bigint,text),
            cap_ai.package_source(text,text), cap_ai.personal_role(uuid,text,text),
            cap_ai.private_file(uuid,text,uuid), cap_ai.source_current(uuid,uuid),
            cap_ai.source_access(uuid,text,uuid,uuid,boolean) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION cap_ai.ready_app(text), cap_ai.publication(text,bigint,text),
            cap_ai.package_source(text,text), cap_ai.personal_role(uuid,text,text),
            cap_ai.private_file(uuid,text,uuid) TO dxos_ai_api;
        GRANT EXECUTE ON FUNCTION cap_ai.source_current(uuid,uuid),
            cap_ai.source_access(uuid,text,uuid,uuid,boolean) TO dxos_ai_api, dxos_ai_worker;
        GRANT SELECT ON cap_ai.documents, cap_ai.document_versions, cap_ai.ingestion_jobs
            TO dxos_ai_api, dxos_ai_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON cap_ai.document_grants TO dxos_ai_api;
        GRANT INSERT ON cap_ai.documents, cap_ai.document_versions, cap_ai.ingestion_jobs
            TO dxos_ai_api;
        GRANT UPDATE (desired_version, revision, deleted_at) ON cap_ai.documents TO dxos_ai_api;
        GRANT UPDATE (active_version) ON cap_ai.documents TO dxos_ai_worker;
        GRANT UPDATE (status) ON cap_ai.document_versions TO dxos_ai_worker;
        GRANT UPDATE ON cap_ai.ingestion_jobs TO dxos_ai_api, dxos_ai_worker;
    """)


def downgrade() -> None:
    raise RuntimeError("AI schema downgrades require an explicit deployment procedure")
