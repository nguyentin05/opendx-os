-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors

-- Work items created by a workflow effect record the source event version, so
-- a later completion can close them and an older creation can be superseded.
ALTER TABLE IF EXISTS cap_work.work_items ADD COLUMN IF NOT EXISTS source_version BIGINT;
