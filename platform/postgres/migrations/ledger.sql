-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

-- Both entry points execute this inside the migration transaction and lock.
-- Existing unscoped Tier 1 versions belong to Core; retain their checksums.
CREATE TABLE IF NOT EXISTS core.schema_migrations (
    scope TEXT NOT NULL DEFAULT 'core',
    version TEXT NOT NULL,
    checksum TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, version)
);
DO $$
DECLARE legacy_primary_key TEXT;
BEGIN
    IF NOT EXISTS (
        SELECT FROM information_schema.columns
        WHERE table_schema='core' AND table_name='schema_migrations' AND column_name='scope'
    ) THEN
        ALTER TABLE core.schema_migrations ADD COLUMN scope TEXT NOT NULL DEFAULT 'core';
        SELECT conname INTO legacy_primary_key FROM pg_constraint
        WHERE conrelid='core.schema_migrations'::regclass AND contype='p';
        IF legacy_primary_key IS NOT NULL THEN
            EXECUTE format('ALTER TABLE core.schema_migrations DROP CONSTRAINT %I', legacy_primary_key);
        END IF;
        ALTER TABLE core.schema_migrations ADD PRIMARY KEY (scope, version);
    END IF;
END $$;
