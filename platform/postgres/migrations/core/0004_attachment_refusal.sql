-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

-- Hasura hides insufficient_privilege behind "database query error". A check
-- violation passes the original message through as permission-error, while the
-- message text clients match stays unchanged.
CREATE OR REPLACE FUNCTION core.enforce_attachments()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, core, public AS $$
DECLARE
    v_actor UUID := core.session_actor_id();
    v_new JSONB := to_jsonb(NEW);
    v_old JSONB;
    v_column TEXT;
    v_file TEXT;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        v_old := to_jsonb(OLD);
    END IF;
    FOR i IN 1 .. TG_NARGS - 1 LOOP
        v_column := TG_ARGV[i];
        v_file := v_new ->> v_column;
        IF v_file IS NULL OR (v_old IS NOT NULL AND (v_old ->> v_column) IS NOT DISTINCT FROM v_file) THEN
            CONTINUE;
        END IF;
        IF v_actor IS NULL OR NOT EXISTS (
            SELECT 1 FROM core.files f
             WHERE f.id = v_file::uuid AND f.app_id = TG_ARGV[0] AND f.uploaded_by = v_actor
        ) THEN
            RAISE EXCEPTION 'attachment %: only a file you uploaded to this application can be attached', v_column
                USING ERRCODE = 'check_violation';
        END IF;
    END LOOP;
    RETURN NEW;
END;
$$;

REVOKE ALL ON FUNCTION core.enforce_attachments() FROM PUBLIC;
