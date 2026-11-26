# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

from alembic import op

revision = "0002"
down_revision = "0001"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE FUNCTION cap_ai.holds_role(actor uuid, role text, lock_grant boolean)
        RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER
        SET search_path = pg_catalog, pg_temp AS $$
        BEGIN
            IF lock_grant THEN
                PERFORM 1 FROM core.users u WHERE u.id = actor AND u.status = 'ACTIVE' FOR SHARE;
                IF NOT FOUND THEN RETURN false; END IF;
                PERFORM 1 FROM core.user_roles ur
                WHERE ur.user_id = actor AND ur.role_name = role FOR SHARE;
                IF NOT FOUND THEN RETURN false; END IF;
            END IF;
            RETURN EXISTS (
                SELECT FROM core.users u
                JOIN core.user_roles ur ON ur.user_id = u.id
                JOIN core.roles r ON r.name = ur.role_name
                LEFT JOIN core.apps a ON a.app_id = r.app_id
                LEFT JOIN core.capabilities c ON c.cap_id = r.cap_id
                WHERE u.id = actor AND u.status = 'ACTIVE' AND r.name = role
                  AND (r.app_id IS NULL OR a.status = 'Ready')
                  AND (r.cap_id IS NULL OR c.enabled)
            );
        END $$;
        REVOKE ALL ON FUNCTION cap_ai.holds_role(uuid, text, boolean) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION cap_ai.holds_role(uuid, text, boolean)
            TO dxos_ai_api, dxos_ai_worker;

        CREATE TABLE cap_ai.model_providers (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            name text NOT NULL UNIQUE,
            execution text NOT NULL CHECK (execution IN ('LOCAL', 'HOSTED')),
            endpoint text NOT NULL,
            protocol text NOT NULL CHECK (protocol IN ('openai', 'cohere', 'infinity')),
            api_key_ciphertext bytea,
            enabled boolean NOT NULL DEFAULT true
        );

        CREATE TABLE cap_ai.models (
            revision uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            provider_id uuid NOT NULL REFERENCES cap_ai.model_providers,
            purpose text NOT NULL CHECK (purpose IN ('LLM', 'EMBEDDING', 'VLM', 'RERANKER')),
            name text NOT NULL,
            dimension int CHECK (dimension BETWEEN 1 AND 4096),
            input_chars int NOT NULL CHECK (input_chars BETWEEN 1 AND 128000),
            output_tokens int NOT NULL CHECK (output_tokens BETWEEN 1 AND 4096),
            CHECK ((dimension IS NOT NULL) = (purpose = 'EMBEDDING')),
            UNIQUE (purpose, revision)
        );

        CREATE TABLE cap_ai.model_slots (
            purpose text PRIMARY KEY CHECK (purpose IN ('LLM', 'EMBEDDING', 'VLM', 'RERANKER')),
            revision uuid,
            FOREIGN KEY (purpose, revision) REFERENCES cap_ai.models(purpose, revision)
        );
        INSERT INTO cap_ai.model_slots(purpose)
        VALUES ('LLM'), ('EMBEDDING'), ('VLM'), ('RERANKER');

        CREATE TABLE cap_ai.processing_policies (
            id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
            app_id text REFERENCES core.apps(app_id),
            data_class text NOT NULL CHECK (data_class IN ('QUERY', 'PERSONAL', 'APPLICATION')),
            purpose text NOT NULL CHECK (purpose IN ('LLM', 'EMBEDDING', 'VLM', 'RERANKER')),
            revision uuid REFERENCES cap_ai.models,
            decision text NOT NULL CHECK (decision IN ('ALLOW', 'DENY')),
            CHECK (decision = 'DENY' OR revision IS NOT NULL),
            CHECK (app_id IS NULL OR data_class = 'APPLICATION'),
            UNIQUE NULLS NOT DISTINCT (app_id, data_class, purpose, revision)
        );

        CREATE TABLE cap_ai.model_audit (
            id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
            actor_id uuid NOT NULL REFERENCES core.users(id),
            action text NOT NULL,
            target text NOT NULL,
            details jsonb NOT NULL DEFAULT '{}',
            created_at timestamptz NOT NULL DEFAULT now()
        );

        CREATE TABLE cap_ai.model_operations (
            operation_key text PRIMARY KEY,
            revision uuid NOT NULL,
            purpose text NOT NULL,
            state text NOT NULL CHECK (
                state IN ('MAY_HAVE_SENT', 'SUCCEEDED', 'FAILED_UNSENT', 'UNCERTAIN')
            ),
            input_tokens int,
            output_tokens int,
            created_at timestamptz NOT NULL DEFAULT now(),
            finished_at timestamptz,
            CHECK (state = 'SUCCEEDED' OR (input_tokens IS NULL AND output_tokens IS NULL))
        );

        CREATE FUNCTION cap_ai.begin_model_operation(k text, rev uuid, p text)
        RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
            WITH inserted AS (
                INSERT INTO cap_ai.model_operations(operation_key, revision, purpose, state)
                VALUES (k, rev, p, 'MAY_HAVE_SENT') ON CONFLICT DO NOTHING RETURNING 1
            ) SELECT EXISTS (SELECT FROM inserted)
        $$;
        CREATE FUNCTION cap_ai.finish_model_operation(k text, s text, i int, o int)
        RETURNS void LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
            UPDATE cap_ai.model_operations SET
                state=s, input_tokens=i, output_tokens=o, finished_at=clock_timestamp()
            WHERE operation_key=k AND state='MAY_HAVE_SENT' AND s<>'MAY_HAVE_SENT'
        $$;
        REVOKE ALL ON FUNCTION cap_ai.begin_model_operation(text, uuid, text),
            cap_ai.finish_model_operation(text, text, int, int) FROM PUBLIC;
        GRANT EXECUTE ON FUNCTION cap_ai.begin_model_operation(text, uuid, text),
            cap_ai.finish_model_operation(text, text, int, int) TO dxos_ai_api, dxos_ai_worker;
        GRANT SELECT ON cap_ai.model_providers, cap_ai.models, cap_ai.model_slots,
            cap_ai.processing_policies TO dxos_ai_api, dxos_ai_worker;
        GRANT INSERT, DELETE ON cap_ai.model_providers, cap_ai.models, cap_ai.processing_policies
            TO dxos_ai_api;
        GRANT UPDATE (name, api_key_ciphertext, enabled) ON cap_ai.model_providers TO dxos_ai_api;
        GRANT UPDATE (revision) ON cap_ai.model_slots TO dxos_ai_api;
        GRANT UPDATE (decision) ON cap_ai.processing_policies TO dxos_ai_api;
        GRANT SELECT ON cap_ai.model_operations, cap_ai.model_audit TO dxos_ai_api;
        GRANT INSERT ON cap_ai.model_audit TO dxos_ai_api;
    """)


def downgrade() -> None:
    raise RuntimeError("AI schema downgrades require an explicit deployment procedure")
