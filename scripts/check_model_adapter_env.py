#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Check the two adapter credential env files before an opt-in deployment starts.

``RAG_MODEL_ADAPTER_CREDENTIAL_ENV_FILE`` holds the adapter-only provider
credentials; ``RAG_MODEL_CREDENTIAL_ENV_FILE`` holds the deployment's internal
adapter token. The adapter loads both files, so a provider file that repeats a
reserved name would shadow the deployment's own token or inject an unrelated
runtime setting into the adapter.

Compose cannot reject that collision: ``env_file`` entries are merged. This check
runs before ``docker compose up`` and rejects it. It prints variable names and
static reasons only, never a value, so a rejected file cannot leak a credential
into a terminal transcript or a log.

    scripts/check_model_adapter_env.py \
      --provider-env .env.providers --internal-env .env.models

Exit codes: 0 the pair is usable, 2 the pair or an argument was rejected.
"""

from __future__ import annotations

import argparse
import os
import re
import stat
import sys

# max_bytes bounds one env file. A credential file is a handful of lines; a larger
# document is not the reviewed declaration.
MAX_BYTES = 65536

# Reserved exact names belong to another deployment secret or to the runtime.
RESERVED_NAMES = frozenset({
    "DATABASE_URL",
    "PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE",
    "PGSERVICEFILE", "PGSSLMODE",
    "SSL_CERT_FILE", "SSL_CERT_DIR",
})

# Reserved prefixes name adapter settings and platform credentials. The provider
# file supplies upstream vendor keys only, so it may not enter these namespaces.
RESERVED_PREFIXES = ("MODEL_ADAPTER_", "AI_MODEL_", "AI_", "RAG_", "POSTGRES_", "OIDC_")

# TOKEN_NAME is the only name the internal token file may define.
TOKEN_NAME = "MODEL_ADAPTER_TOKEN"

NAME_PATTERN = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*$")


class Rejected(Exception):
    """A rejected credential file or argument, carrying a static reason."""


def read_pairs(path, role):
    """Return the (name, value) pairs of one env file, in declaration order.

    The file must be an owner-only regular file and must be plain ``NAME=VALUE``
    lines. Rejections name the role, the line and the variable, never the value.
    """
    try:
        info = os.stat(path)
    except OSError:
        raise Rejected("the %s env file is not readable" % role)
    if not stat.S_ISREG(info.st_mode):
        raise Rejected("the %s env file is not a regular file" % role)
    if info.st_size > MAX_BYTES:
        raise Rejected("the %s env file exceeds the size limit" % role)
    if info.st_mode & 0o077:
        raise Rejected("the %s env file must not grant group or other access" % role)

    pairs = []
    seen = set()
    with open(path, "r", encoding="utf-8", errors="strict") as handle:
        for number, line in enumerate(handle, start=1):
            line = line.rstrip("\r\n")
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            if "=" not in line:
                raise Rejected("the %s env file line %d is not NAME=VALUE" % (role, number))
            name, value = line.split("=", 1)
            name = name.strip()
            if not NAME_PATTERN.match(name):
                raise Rejected("the %s env file line %d is not a variable name" % (role, number))
            if name in seen:
                raise Rejected("the %s env file repeats %s" % (role, name))
            seen.add(name)
            pairs.append((name, value))
    return pairs


def check(provider, internal):
    """Validate the pair. Returns a static success summary or raises Rejected."""
    for name, _ in provider:
        if name == TOKEN_NAME:
            raise Rejected("the provider env file must not define " + TOKEN_NAME)
        if name in RESERVED_NAMES or name.startswith(RESERVED_PREFIXES):
            raise Rejected("the provider env file must not define the reserved name " + name)
    if len(internal) != 1 or internal[0][0] != TOKEN_NAME:
        raise Rejected("the internal env file must define only " + TOKEN_NAME)
    if not internal[0][1].strip():
        raise Rejected("the internal env file must set a nonempty " + TOKEN_NAME)
    return "provider references=%d internal tokens=1" % len(provider)


def main(argv):
    parser = argparse.ArgumentParser(
        description="Check the adapter provider and internal-token env files.",
        add_help=True,
    )
    parser.add_argument("--provider-env", required=True, help="adapter-only provider credential env file")
    parser.add_argument("--internal-env", required=True, help="env file holding only MODEL_ADAPTER_TOKEN")
    arguments = parser.parse_args(argv)
    try:
        summary = check(read_pairs(arguments.provider_env, "provider"),
                        read_pairs(arguments.internal_env, "internal"))
    except Rejected as rejected:
        sys.stderr.write("check-model-adapter-env: %s\n" % rejected)
        return 2
    sys.stdout.write("check-model-adapter-env: %s\n" % summary)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
