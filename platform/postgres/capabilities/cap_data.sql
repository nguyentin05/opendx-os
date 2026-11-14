-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--     http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- Tier 2 capability: cap-data ([D])
-- Equipment master data, telemetry ingestion, lakehouse export and lineage.
-- An equipment alert that needs action becomes a cap_work work item whose
-- source is the device (source_type 'cap_data.device').

CREATE SCHEMA IF NOT EXISTS cap_data;

-- 1. Devices (shared master data)
CREATE TABLE IF NOT EXISTS cap_data.devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code TEXT NOT NULL UNIQUE CHECK (length(trim(device_code)) > 0),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    location TEXT,
    status TEXT NOT NULL DEFAULT 'NORMAL' CHECK (status IN ('NORMAL', 'WARNING', 'ALERT', 'OFFLINE')),
    last_reading JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. OT events with deduplication key (S02)
CREATE TABLE IF NOT EXISTS cap_data.ot_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES cap_data.devices(id),
    event_dedup_key TEXT NOT NULL CHECK (length(trim(event_dedup_key)) > 0),
    metric_name TEXT NOT NULL CHECK (length(trim(metric_name)) > 0),
    metric_value NUMERIC NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, event_dedup_key)
);

-- 3. Reconciled export batches and source-key lineage (S07, S08)
CREATE TABLE IF NOT EXISTS cap_data.export_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parquet_uri TEXT NOT NULL CHECK (length(trim(parquet_uri)) > 0),
    row_count INTEGER NOT NULL CHECK (row_count >= 0),
    checksum_sha256 TEXT NOT NULL CHECK (length(trim(checksum_sha256)) = 64),
    publication_status TEXT NOT NULL DEFAULT 'PENDING' CHECK (publication_status IN ('PENDING', 'PUBLISHED', 'FAILED')),
    reconciliation_summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS cap_data.data_lineage (
    batch_id UUID NOT NULL REFERENCES cap_data.export_batches(id),
    entity_type TEXT NOT NULL CHECK (length(trim(entity_type)) > 0),
    entity_id UUID NOT NULL,
    entity_version INTEGER NOT NULL CHECK (entity_version > 0),
    content_hash TEXT NOT NULL CHECK (length(trim(content_hash)) = 64),
    metric_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    PRIMARY KEY (batch_id, entity_type, entity_id, entity_version)
);

-- 4. Register cap-data
INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled)
VALUES ('cap-data', 'IoT OT Telemetry & Lakehouse BI', 'D', '/api/cap/data', true)
ON CONFLICT (cap_id) DO UPDATE SET enabled = true, updated_at = now();
