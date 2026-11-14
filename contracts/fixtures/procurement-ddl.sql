-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Generated from apps/procurement/manifest.yaml; capability tests install it.
-- Regenerate: go -C operator run ./cmd/dxos-operator plan -format sql ../apps/procurement/manifest.yaml
-- Auto-generated DDL by dxos-operator. DO NOT EDIT DIRECTLY.
CREATE SCHEMA IF NOT EXISTS domain;

CREATE TABLE IF NOT EXISTS "domain"."procurement_requests" (
    "id" uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    "created_by" uuid NOT NULL REFERENCES "core"."users"("id"),
    "org_unit_id" uuid NOT NULL REFERENCES "cap_hr"."org_units"("id"),
    "status" text NOT NULL DEFAULT 'DRAFT',
    "version" bigint NOT NULL DEFAULT 1 CHECK ("version" > 0),
    "created_at" timestamptz NOT NULL DEFAULT now(),
    "updated_at" timestamptz NOT NULL DEFAULT now(),
    "vendor_name" text NOT NULL CHECK (length(trim(vendor_name)) > 0),
    "item_description" text NOT NULL CHECK (length(trim(item_description)) > 0),
    "quoted_amount" numeric(14,2) NOT NULL CHECK (quoted_amount > 0),
    "quote_file_id" uuid,
    "rejection_reason" text
);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = '"domain"."procurement_requests"'::regclass AND conname = 'fk_procurement_requests_quote_file_id') THEN
        ALTER TABLE "domain"."procurement_requests" ADD CONSTRAINT "fk_procurement_requests_quote_file_id" FOREIGN KEY ("quote_file_id") REFERENCES "core"."files"("id") ON DELETE RESTRICT;
    END IF;
END $$;

ALTER TABLE "domain"."procurement_requests" DROP CONSTRAINT IF EXISTS "ck_procurement_requests_status";
ALTER TABLE "domain"."procurement_requests" ADD CONSTRAINT "ck_procurement_requests_status" CHECK ("status" IN ('DRAFT', 'SUBMITTED', 'BUDGET_REVIEW', 'APPROVED', 'REJECTED', 'CANCELLED'));

CREATE INDEX IF NOT EXISTS "idx_procurement_requests_created_by" ON "domain"."procurement_requests" ("created_by");
CREATE INDEX IF NOT EXISTS "idx_procurement_requests_status" ON "domain"."procurement_requests" ("status");
CREATE INDEX IF NOT EXISTS "idx_procurement_requests_org_unit_id" ON "domain"."procurement_requests" ("org_unit_id");

DROP TRIGGER IF EXISTS "trg_procurement_requests_10_stamp_owner" ON "domain"."procurement_requests";
CREATE TRIGGER "trg_procurement_requests_10_stamp_owner"
BEFORE INSERT ON "domain"."procurement_requests"
FOR EACH ROW EXECUTE FUNCTION core.stamp_record_owner();

DROP TRIGGER IF EXISTS "trg_procurement_requests_20_stamp_org_unit" ON "domain"."procurement_requests";
CREATE TRIGGER "trg_procurement_requests_20_stamp_org_unit"
BEFORE INSERT ON "domain"."procurement_requests"
FOR EACH ROW EXECUTE FUNCTION cap_hr.stamp_record_org_unit();

DROP TRIGGER IF EXISTS "trg_procurement_requests_30_bump_version" ON "domain"."procurement_requests";
CREATE TRIGGER "trg_procurement_requests_30_bump_version"
BEFORE UPDATE ON "domain"."procurement_requests"
FOR EACH ROW EXECUTE FUNCTION core.bump_domain_record_version();

DROP TRIGGER IF EXISTS "trg_procurement_requests_90_audit" ON "domain"."procurement_requests";
CREATE TRIGGER "trg_procurement_requests_90_audit"
AFTER INSERT OR UPDATE ON "domain"."procurement_requests"
FOR EACH ROW EXECUTE FUNCTION core.record_domain_audit('procurement');

GRANT SELECT, INSERT, UPDATE ON "domain"."procurement_requests" TO dxos_hasura, dxos_process;
GRANT SELECT ON "domain"."procurement_requests" TO dxos_reader;
GRANT USAGE ON SCHEMA "cap_hr" TO dxos_hasura;
GRANT SELECT ON "cap_hr"."org_units" TO dxos_hasura;
GRANT SELECT ON "cap_hr"."staff" TO dxos_hasura;
GRANT USAGE ON SCHEMA "core" TO dxos_hasura;
GRANT SELECT ON "core"."files" TO dxos_hasura;
GRANT SELECT ON "core"."users" TO dxos_hasura;

