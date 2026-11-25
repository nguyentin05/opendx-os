#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Synthetic upstream for the coordinated model deployment tests.

One process stands in for a deployment-local inference endpoint behind the
private proxy. It answers the OpenAI-compatible shapes the pinned proxy forwards
and records only the purpose, path and requested model of each request: never a
prompt, an answer or a credential. The runner selects the answer through the
``mode`` file in ``--state-dir`` and reads ``requests.jsonl`` to count dispatch.

The synthetic answers are the fixed qualification values the runtime probe
expects (``BLUE-17`` and citation ``E1``), so a passing probe is the real probe
and not a stub.
"""

from __future__ import annotations

import argparse
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCK = threading.Lock()


class State:
    def __init__(self, directory: str, dimension: int) -> None:
        self.directory = directory
        self.dimension = dimension
        self.log = os.path.join(directory, "requests.jsonl")
        self.mode_path = os.path.join(directory, "mode")
        self.counts = {"EMBED": 0, "GENERATE": 0}

    def mode(self) -> str:
        try:
            with open(self.mode_path, encoding="utf-8") as handle:
                return handle.read().strip() or "normal"
        except OSError:
            return "normal"

    def record(self, purpose: str, path: str, model: str) -> None:
        with LOCK, open(self.log, "a", encoding="utf-8") as handle:
            handle.write(json.dumps({"purpose": purpose, "path": path, "model": model, "at": time.time()}) + "\n")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    state: State

    def log_message(self, *args):  # noqa: D102 - keep the fixture silent
        pass

    def reply(self, payload: dict, status: int = 200) -> None:
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):  # noqa: N802 - BaseHTTPRequestHandler API
        if self.path != "/stats":
            self.reply({}, 404)
            return
        with LOCK:
            self.reply({"counts": dict(self.state.counts)})

    def do_POST(self):  # noqa: N802 - BaseHTTPRequestHandler API
        length = int(self.headers.get("content-length") or 0)
        raw = self.rfile.read(length) if 0 < length <= 262144 else b""
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            self.reply({}, 400)
            return
        purpose = "EMBED" if self.path.endswith("/embeddings") else "GENERATE"
        model = str(body.get("model") or "")
        with LOCK:
            self.state.counts[purpose] += 1
        self.state.record(purpose, self.path, model)
        mode = self.state.mode()
        if mode == "hold":
            time.sleep(20)
        if mode == "unavailable" or (mode == "llm-fail" and purpose == "GENERATE") or (mode == "embed-fail" and purpose == "EMBED"):
            self.reply({"error": {"message": "synthetic upstream unavailable", "type": "server_error"}}, 503)
            return
        if purpose == "EMBED":
            vector = [round(0.25 * (index + 1), 6) for index in range(self.state.dimension)]
            self.reply({
                "object": "list",
                "model": model,
                "data": [{"object": "embedding", "index": 0, "embedding": vector}],
                "usage": {"prompt_tokens": 4, "total_tokens": 4},
            })
            return
        answer = {"text": "The synthetic project code is BLUE-17.", "citation_ids": ["E1"]}
        self.reply({
            "id": "chatcmpl-ai-up",
            "object": "chat.completion",
            "model": model,
            "choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps(answer)}}],
            "usage": {"prompt_tokens": 6, "completion_tokens": 3, "total_tokens": 9},
        })


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--state-dir", required=True)
    parser.add_argument("--dimension", type=int, default=8)
    arguments = parser.parse_args()
    state = State(arguments.state_dir, arguments.dimension)
    handler = type("BoundHandler", (Handler,), {"state": state})
    ThreadingHTTPServer(("0.0.0.0", 8081), handler).serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
