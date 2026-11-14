-- SPDX-License-Identifier: Apache-2.0
-- Copyright 2026 DX-OS Open-Core Contributors
-- Atomic constraint replacement; existing identities/policies/bytes remain unchanged.
ALTER TABLE core.files DROP CONSTRAINT files_mime_type_check;
ALTER TABLE core.files ADD CONSTRAINT files_mime_type_check CHECK (
 mime_type IN ('application/pdf', 'image/png', 'image/jpeg')
 OR (mime_type='text/markdown' AND read_policy='OWNER_ONLY'));
