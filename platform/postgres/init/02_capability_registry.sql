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

-- Tier 1 Kernel Extension Registry: Pluggable H-P-D-I Capabilities
CREATE TABLE IF NOT EXISTS core.capability_registry (
    cap_id TEXT PRIMARY KEY CHECK (length(trim(cap_id)) > 0),
    display_name TEXT NOT NULL CHECK (length(trim(display_name)) > 0),
    category_hpdi TEXT NOT NULL CHECK (category_hpdi IN ('H', 'P', 'D', 'I')),
    base_route TEXT NOT NULL CHECK (length(trim(base_route)) > 0),
    enabled BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
