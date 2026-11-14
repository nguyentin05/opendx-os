# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
import unittest
import rag_sources

class RevokedMetadataTest(unittest.TestCase):
    def test_revoked_authority_denial_is_safe(self):
        for status, raw in [(403, b'{}'), (200, b'{"errors":[{"extensions":{"code":"access-denied"}}]}'), (200, b'{"data":{"core_files_by_pk":null}}')]:
            rag_sources.assert_revoked_metadata(status, raw)

    def test_visible_metadata_and_unrelated_errors_fail(self):
        for status, raw in [(200, b'{"data":{"core_files_by_pk":{"id":"hidden"}}}'), (200, b'{"errors":[{"extensions":{"code":"validation-failed"}}]}'), (503, b'{}')]:
            with self.assertRaises(RuntimeError):
                rag_sources.assert_revoked_metadata(status, raw)

if __name__ == '__main__':
    unittest.main()
