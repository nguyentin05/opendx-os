-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Login for the KEDA PostgreSQL scaler. It may only call keda.files_waiting_for_scan(),
-- which counts uploads ClamAV has not scanned yet; it reads no table itself.
-- Idempotent: the setup Job runs it on every new Job.
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'dxos_keda') THEN
    CREATE ROLE dxos_keda LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;
  END IF;
END $$;
ALTER ROLE dxos_keda LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOINHERIT;
ALTER ROLE dxos_keda PASSWORD :'password';
ALTER ROLE dxos_keda SET statement_timeout = '5s';

CREATE SCHEMA IF NOT EXISTS keda;
REVOKE ALL ON SCHEMA keda FROM PUBLIC;
GRANT USAGE ON SCHEMA keda TO dxos_keda;

CREATE OR REPLACE FUNCTION keda.files_waiting_for_scan() RETURNS bigint
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, pg_temp
  AS $$ SELECT count(*) FROM core.files WHERE scan_status = 'QUARANTINED' $$;
REVOKE ALL ON FUNCTION keda.files_waiting_for_scan() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION keda.files_waiting_for_scan() TO dxos_keda;

-- Work cap-process does in the background: outbox events it still has to
-- deliver to the workflow engine, and uploads waiting for a scan.
CREATE OR REPLACE FUNCTION keda.process_work_waiting() RETURNS bigint
  LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, pg_temp
  AS $$ SELECT (SELECT count(*) FROM core.outbox_events
                 WHERE delivery_status <> 'delivered' AND event_type <> 'knowledge.publication')
             + (SELECT count(*) FROM core.files WHERE scan_status = 'QUARANTINED') $$;
REVOKE ALL ON FUNCTION keda.process_work_waiting() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION keda.process_work_waiting() TO dxos_keda;
