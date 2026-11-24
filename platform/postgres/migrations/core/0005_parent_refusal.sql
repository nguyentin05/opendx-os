-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

-- Hasura hides insufficient_privilege behind "database query error". A check
-- violation passes the original message through as permission-error. The
-- dynamic status is at the end, after the stable prefix clients match.
CREATE OR REPLACE FUNCTION core.enforce_parent()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    v_parent UUID;
    v_status TEXT;
    v_found BOOLEAN;
BEGIN
    IF TG_OP = 'DELETE' THEN
        v_parent := (to_jsonb(OLD) ->> TG_ARGV[1])::uuid;
    ELSE
        v_parent := (to_jsonb(NEW) ->> TG_ARGV[1])::uuid;
        IF TG_OP = 'UPDATE' AND v_parent IS DISTINCT FROM (to_jsonb(OLD) ->> TG_ARGV[1])::uuid THEN
            RAISE EXCEPTION 'a row cannot move to another parent record'
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    EXECUTE format('SELECT true, to_jsonb(p) ->> ''status'' FROM %s p WHERE p.id = $1 FOR UPDATE', TG_ARGV[0]::regclass)
        INTO v_found, v_status USING v_parent;
    IF v_found IS NOT TRUE THEN
        RAISE EXCEPTION 'parent record % not found', v_parent USING ERRCODE = 'foreign_key_violation';
    END IF;
    IF TG_NARGS > 2 THEN
        v_found := false;
        FOR i IN 2 .. TG_NARGS - 1 LOOP
            v_found := v_found OR v_status = TG_ARGV[i];
        END LOOP;
        IF NOT v_found THEN
            RAISE EXCEPTION 'the parent record can no longer be changed (status %)', v_status
                USING ERRCODE = 'check_violation';
        END IF;
    END IF;

    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
