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

-- Tier 2 Pluggable Capability Schema: cap-ai ([I])
-- Manages: OCR of clean files and policy knowledge RAG

-- Register cap-ai in capability registry
INSERT INTO core.capabilities (cap_id, display_name, category_hpdi, base_route, enabled)
VALUES ('cap-ai', 'AI OCR & Policy Knowledge RAG', 'I', '/api/cap/ai', true)
ON CONFLICT (cap_id) DO UPDATE SET enabled = true, updated_at = now();
