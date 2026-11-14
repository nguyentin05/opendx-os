# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""scripts/init-env.sh replaces every development secret and never clobbers."""

from __future__ import annotations

import pathlib
import re
import stat
import subprocess
import tempfile
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "init-env.sh"
SECRETS = ("POSTGRES_PASSWORD", "HASURA_ADMIN_SECRET", "KEYCLOAK_ADMIN_PASSWORD", "KESTRA_PASSWORD",
           "KESTRA_API_TOKEN", "KESTRA_DATABASE_PASSWORD", "HASURA_DATABASE_PASSWORD",
           "PROCESS_DATABASE_PASSWORD", "AUTHZ_DATABASE_PASSWORD")


def parse(path: pathlib.Path) -> dict[str, str]:
    out = {}
    for line in path.read_text().splitlines():
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            out[key] = value
    return out


class InitEnvTest(unittest.TestCase):
    def test_generates_random_secrets_and_refuses_to_overwrite(self) -> None:
        example = parse(ROOT / ".env.example")
        with tempfile.TemporaryDirectory() as tmp:
            first, second = pathlib.Path(tmp) / "a.env", pathlib.Path(tmp) / "b.env"
            for target in (first, second):
                subprocess.run(["bash", str(SCRIPT), str(target)], check=True, capture_output=True)
            a, b = parse(first), parse(second)
            for key in SECRETS:
                self.assertRegex(a[key], re.compile(r"^[A-Za-z0-9]{32}$"), key)
                self.assertNotEqual(a[key], example[key], key)
                self.assertNotEqual(a[key], b[key], key)
            # Everything else is the example's.
            self.assertEqual({k: v for k, v in a.items() if k not in SECRETS},
                             {k: v for k, v in example.items() if k not in SECRETS})
            self.assertEqual(stat.S_IMODE(first.stat().st_mode), 0o600)
            again = subprocess.run(["bash", str(SCRIPT), str(first)], capture_output=True)
            self.assertNotEqual(again.returncode, 0)
            self.assertEqual(parse(first), a)


if __name__ == "__main__":
    unittest.main()
