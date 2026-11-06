-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- REVIEW ONLY: B owns execution/reset on disposable services. No registry/DDL provisioning.
BEGIN;
INSERT INTO core.workspaces (id,code,name) VALUES ('11111111-1111-4111-8111-111111111111','a6_alpha','A6 alpha');
INSERT INTO core.employees (id,workspace_id,full_name,email,department) VALUES ('22222222-2222-4222-8222-222222222222','11111111-1111-4111-8111-111111111111','A6 requester','requester@example.test','Demo');
INSERT INTO core.employees (id,workspace_id,full_name,email,department) VALUES ('22222222-2222-4222-8222-000000000002','11111111-1111-4111-8111-111111111111','A6 manager','manager@example.test','Demo');
INSERT INTO core.employees (id,workspace_id,full_name,email,department) VALUES ('22222222-2222-4222-8222-000000000003','11111111-1111-4111-8111-111111111111','A6 selfapprover','selfapprover@example.test','Demo');
INSERT INTO core.employees (id,workspace_id,full_name,email,department) VALUES ('22222222-2222-4222-8222-000000000004','11111111-1111-4111-8111-111111111111','A6 noaccess','noaccess@example.test','Demo');
INSERT INTO core.employees (id,workspace_id,full_name,email,department) VALUES ('22222222-2222-4222-8222-000000000005','11111111-1111-4111-8111-111111111111','A6 workflow','workflow@example.test','Demo');
INSERT INTO core.workspaces (id,code,name) VALUES ('11111111-1111-4111-8111-111111111112','a6_beta','A6 beta');
-- Beta identity is distinct because core.employees.id is globally unique.
INSERT INTO core.employees (id,workspace_id,full_name,email,department) VALUES ('22222222-2222-4222-8222-222222222223','11111111-1111-4111-8111-111111111112','Beta requester','beta@example.test','Demo');
COMMIT;
