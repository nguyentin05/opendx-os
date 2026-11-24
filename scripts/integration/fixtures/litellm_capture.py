#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Disposable upstream capture for the private LiteLLM qualification.

One process serves the synthetic upstream twice: a plain-HTTP listener that
stands in for a deployment-local inference endpoint, and a TLS listener that
stands in for a hosted provider endpoint. Neither listener is a provider, a
fixture of real data or a service: it records exactly what the proxy sends and
answers with a synthetic body selected by the ``mode`` file in ``--state-dir``.

Everything the runner needs travels through ``--state-dir``, so the runner never
has to reach into the container or publish a port: it writes ``mode`` (and, for
``redirect``, ``redirect``) and reads ``requests.jsonl``. Each request is one JSON
line with the listener, the path, the authorization header and the body.

Every mode below is exercised by ``test_litellm_deployment.py``; none is dead:

* ``ok`` — the reviewed synthetic answer;
* ``status503`` and ``status429`` — one request, no retry;
* ``slow`` — a one-second caller deadline expires against a six-second answer,
  and a retry would appear as a second request;
* ``redirect`` — a 307 to the ``redirect`` file's target, which is then answered
  normally under ``/redirected`` so a followed redirect costs exactly one extra
  request and the capture cannot loop;
* ``wrong-dimension``, ``unknown-citation``, ``tool-call`` and ``truncated`` —
  defective answers the proxy forwards unchanged, so the strict reading that
  refuses them is provably the Go transport's.
"""

from __future__ import annotations

import argparse
import json
import os
import ssl
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# The synthetic embedding width the qualification declaration declares.
DIMENSION = 8
# REDIRECT_PREFIX is the path a "redirect" answer sends the caller to. A request
# under it is answered normally, so following the 307 is observable exactly once.
REDIRECT_PREFIX = "/redirected"
# slow mode holds the answer long enough for a one-second caller deadline to
# expire, so a retry would be visible as a second request.
SLOW_SECONDS = 6.0
LOCK = threading.Lock()


class State:
    """The runner-controlled state of one capture process."""

    def __init__(self, directory: str) -> None:
        self.directory = directory
        self.log = os.path.join(directory, "requests.jsonl")
        self.mode_path = os.path.join(directory, "mode")

    def mode(self) -> str:
        try:
            with open(self.mode_path, encoding="utf-8") as handle:
                return handle.read().strip() or "ok"
        except OSError:
            return "ok"

    def record(self, listener: str, request: dict) -> None:
        request["listener"] = listener
        request["mode"] = self.mode()
        with LOCK, open(self.log, "a", encoding="utf-8") as handle:
            handle.write(json.dumps(request) + "\n")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    state: State
    listener = "local"

    def log_message(self, *args):  # noqa: D102 - silence the default stderr log
        pass

    def do_POST(self):  # noqa: N802 - BaseHTTPRequestHandler API
        length = int(self.headers.get("content-length") or 0)
        body = self.rfile.read(length).decode("utf-8", "replace")
        self.state.record(self.listener, {
            "path": self.path,
            "authorization": self.headers.get("authorization"),
            "content_type": self.headers.get("content-type"),
            "body": body,
        })
        mode = self.state.mode()
        if mode == "status503":
            self._send({"error": {"message": "capture upstream unavailable", "type": "server_error"}}, 503)
            return
        if mode == "status429":
            self._send({"error": {"message": "capture rate limit", "type": "rate_limit"}}, 429)
            return
        if mode == "redirect" and not self.path.startswith(REDIRECT_PREFIX):
            # The reviewed path answers 307 once; the redirected path below answers
            # normally, so a followed redirect is exactly one extra recorded request
            # and the capture cannot loop.
            target = ""
            try:
                with open(os.path.join(self.state.directory, "redirect"), encoding="utf-8") as handle:
                    target = handle.read().strip()
            except OSError:
                pass
            self.send_response(307)
            self.send_header("location", target)
            self.send_header("content-length", "0")
            self.end_headers()
            return
        if mode == "slow":
            time.sleep(SLOW_SECONDS)
        if self.path.endswith("/embeddings") or self.path.endswith("/api/embed"):
            self._send(self._embedding(mode))
            return
        self._send(self._generation(mode))

    def _embedding(self, mode: str) -> dict:
        width = DIMENSION + 1 if mode == "wrong-dimension" else DIMENSION
        # The synthetic vector is finite and fixed, so a dimension mismatch is the
        # only thing a wrong answer can express.
        vector = [round(0.125 * (index + 1), 6) for index in range(width)]
        if self.path.endswith("/api/embed"):
            return {"model": "capture", "embeddings": [vector], "total_duration": 1}
        return {
            "object": "list",
            "model": "capture",
            "data": [{"object": "embedding", "index": 0, "embedding": vector}],
            "usage": {"prompt_tokens": 4, "total_tokens": 4},
        }

    def _generation(self, mode: str) -> dict:
        answer = {"text": "synthetic grounded answer", "citation_ids": ["doc-a"]}
        if mode == "unknown-citation":
            answer["citation_ids"] = ["doc-not-supplied"]
        if mode == "tool-call":
            return {
                "id": "chatcmpl-capture",
                "object": "chat.completion",
                "model": "capture",
                "choices": [{"index": 0, "finish_reason": "stop", "message": {
                    "role": "assistant",
                    "content": json.dumps(answer),
                    "tool_calls": [{"id": "call-capture", "type": "function", "function": {"name": "f", "arguments": "{}"}}],
                }}],
                "usage": {"prompt_tokens": 6, "completion_tokens": 3, "total_tokens": 9},
            }
        finish = "length" if mode == "truncated" else "stop"
        return {
            "id": "chatcmpl-capture",
            "object": "chat.completion",
            "model": "capture",
            "choices": [{"index": 0, "finish_reason": finish, "message": {
                "role": "assistant",
                "content": json.dumps(answer),
            }}],
            "usage": {"prompt_tokens": 6, "completion_tokens": 3, "total_tokens": 9},
        }

    def _send(self, payload: dict, status: int = 200) -> None:
        blob = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(blob)))
        self.end_headers()
        self.wfile.write(blob)


def serve(port: int, state: State, listener: str, context: ssl.SSLContext | None) -> ThreadingHTTPServer:
    handler = type("BoundHandler", (Handler,), {"state": state, "listener": listener})
    server = ThreadingHTTPServer(("0.0.0.0", port), handler)
    if context is not None:
        server.socket = context.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--state-dir", required=True)
    parser.add_argument("--http-port", type=int, default=8081)
    parser.add_argument("--https-port", type=int, default=8082)
    parser.add_argument("--cert", default="")
    parser.add_argument("--key", default="")
    arguments = parser.parse_args()
    state = State(arguments.state_dir)
    serve(arguments.http_port, state, "local", None)
    if arguments.cert and arguments.key:
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(arguments.cert, arguments.key)
        serve(arguments.https_port, state, "hosted", context)
    while True:
        time.sleep(3600)


if __name__ == "__main__":
    raise SystemExit(main())
