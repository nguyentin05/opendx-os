-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

-- Bound request-serving work. Background ingestion, the knowledge dispatcher,
-- the model gateway and Kestra legitimately run longer operations and keep no
-- limit. Roles may not exist in every installation. Existing connections keep
-- their settings until the services reconnect.
DO $$
DECLARE r text;
BEGIN
  FOREACH r IN ARRAY ARRAY['dxos_hasura','dxos_process','dxos_authz','dxos_hr','dxos_ai_api'] LOOP
    IF EXISTS (SELECT FROM pg_roles WHERE rolname=r) THEN
      EXECUTE format('ALTER ROLE %I SET statement_timeout = %L', r, '10s');
      IF r <> 'dxos_ai_api' THEN
        EXECUTE format('ALTER ROLE %I SET idle_in_transaction_session_timeout = %L', r, '30s');
      END IF;
    END IF;
  END LOOP;
END $$;
