-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

ALTER TABLE core.files ADD COLUMN IF NOT EXISTS read_policy TEXT NOT NULL DEFAULT 'APP_ATTACHMENT' CHECK (read_policy IN ('APP_ATTACHMENT', 'OWNER_ONLY'));

CREATE OR REPLACE FUNCTION core.enforce_file_immutability()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.app_id IS DISTINCT FROM OLD.app_id
       OR NEW.bucket IS DISTINCT FROM OLD.bucket
       OR NEW.object_key IS DISTINCT FROM OLD.object_key
       OR NEW.file_name IS DISTINCT FROM OLD.file_name
       OR NEW.sha256_hash IS DISTINCT FROM OLD.sha256_hash
       OR NEW.read_policy IS DISTINCT FROM OLD.read_policy
       OR NEW.uploaded_by IS DISTINCT FROM OLD.uploaded_by
       OR NEW.mime_type IS DISTINCT FROM OLD.mime_type
       OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'core.files identity and content metadata are immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_files_immutable ON core.files;
CREATE TRIGGER trg_files_immutable
BEFORE UPDATE ON core.files
FOR EACH ROW EXECUTE FUNCTION core.enforce_file_immutability();
