-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Execute after core bootstrap or upgrade in a disposable database.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
       WHERE table_schema='core' AND table_name='files' AND column_name='read_policy'
         AND is_nullable='NO' AND column_default LIKE '%APP_ATTACHMENT%') THEN
        RAISE EXCEPTION 'private-file policy contract is absent';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_constraint c
       JOIN pg_class target ON target.oid=c.confrelid
       JOIN pg_namespace ns ON ns.oid=target.relnamespace
       WHERE c.conrelid='core.files'::regclass AND ns.nspname='cap_ai') THEN
        RAISE EXCEPTION 'Core must not depend on cap_ai';
    END IF;
END $$;
