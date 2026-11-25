#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Cutover decision for ``scripts/ai-up.sh``.

The deployment may leave the model-calling API and worker running only when the
live selection is already exactly the one this run would keep. Two things count
as "the selection is about to change", and both require the drain:

* a purpose this run wants selected is not selected on the declared revision;
* a purpose the runtime holds now is not among the purposes this run qualifies
  to keep. That is a withdrawal: ``deployment reconcile --disable`` is about to
  remove it while the worker still holds its lease, so stopping first is what
  keeps a stale worker from writing under a selection nobody verified.

The rule lives in this file rather than in an inline heredoc so it can be tested
without Docker. The script calls it as

    python3 scripts/lib/ai_up_snapshot.py STATUS_JSON DECLARATION_JSON EMBED LLM

and prints ``1`` (leave the runtime running) or ``0`` (drain first). ``EMBED`` and
``LLM`` are ``1``/``0``: whether the probe qualified that purpose in this run.
"""

from __future__ import annotations

import json
import sys


def selection_matches(status: dict, declaration: dict, expected: dict[str, bool]) -> bool:
    """Report whether the live selection is exactly the one this run keeps."""
    declared = {
        purpose.get("purpose"): purpose
        for purpose in declaration.get("purposes") or []
        if purpose.get("declared")
    }
    current = {
        purpose.get("purpose"): purpose
        for purpose in status.get("purposes") or []
        if purpose.get("qualified")
    }
    wanted = {purpose for purpose, value in expected.items() if value}
    for purpose in wanted:
        profile = declared.get(purpose) or {}
        active = current.get(purpose) or {}
        if (
            not active
            or active.get("profile_id") != profile.get("profile_id")
            or active.get("revision") != profile.get("revision")
        ):
            return False
    for purpose in current:
        if purpose not in wanted:
            return False
    return True


def main(argv: list[str]) -> int:
    if len(argv) != 5:
        print(__doc__, file=sys.stderr)
        return 2
    with open(argv[1], encoding="utf-8") as status:
        live = json.load(status)
    with open(argv[2], encoding="utf-8") as declaration:
        reviewed = json.load(declaration)
    expected = {"EMBED": argv[3] == "1", "GENERATE": argv[4] == "1"}
    print("1" if selection_matches(live, reviewed, expected) else "0")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
