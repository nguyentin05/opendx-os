# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Phase seed boundary. Live orchestration is gated on identity/publication work."""
import argparse
import json
import os
from pathlib import Path


class Refused(RuntimeError):
    pass


def seed_phase(phase, values, ready_apps, create):
    if values.get('TEST_DISPOSABLE_DATABASE') != 'yes':
        raise Refused('TEST_DISPOSABLE_DATABASE=yes is required')
    if phase not in ('manual', 'automatic', 'files'):
        raise Refused('unknown phase')
    if not {'procurement', 'process_fixture'}.issubset(set(ready_apps)):
        raise Refused('both apps require verified Ready snapshots before seeding')
    raise Refused('live phase creation NOT_RUN: identity and publication prerequisites pending; no records created')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--phase', required=True, choices=['manual', 'automatic', 'files'])
    args = parser.parse_args()
    # Do not accept caller-declared READY as evidence. Tasks 3/4 wire actual registry reads.
    try:
        seed_phase(args.phase, os.environ, ready_apps=[], create=None)
    except Refused as exc:
        parser.exit(1, str(exc) + '\n')


if __name__ == '__main__':
    main()
