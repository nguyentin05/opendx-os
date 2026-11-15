#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Test-only bounded adapter spy. No provider credentials or external network use.

Run only in a disposable model qualification stack. Control and statistics must
remain private to that stack. Request bodies are never logged or retained.
"""
import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class State:
    lock = threading.Lock()
    release = threading.Event()
    mode = 'normal'
    counts = {'EMBED': 0, 'GENERATE': 0, 'RERANK': 0, 'OCR': 0}
    pending = 0


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def reply(self, status, data):
        body = json.dumps(data).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def do_GET(self):
        if self.path != '/stats':
            return self.reply(404, {})
        with State.lock:
            self.reply(200, {'counts': dict(State.counts), 'pending': State.pending})

    def do_POST(self):
        size = int(self.headers.get('Content-Length', '0'))
        if not 0 < size <= 65536:
            return self.reply(413, {})
        try:
            body = json.loads(self.rfile.read(size))
        except (ValueError, UnicodeError):
            return self.reply(400, {})
        if self.path == '/control':
            mode = body.get('mode', 'normal')
            if mode not in ('normal', 'hold', 'timeout', 'fabricate', 'tools'):
                return self.reply(400, {})
            with State.lock:
                State.mode = mode
                if mode == 'hold':
                    State.release.clear()
                else:
                    State.release.set()
            return self.reply(200, {})
        if self.path != '/call' or body.get('purpose') not in State.counts:
            return self.reply(400, {})
        purpose = body['purpose']
        with State.lock:
            State.counts[purpose] += 1
            mode = State.mode
            if purpose == 'GENERATE':
                State.pending += 1
        if purpose == 'GENERATE':
            try:
                if mode == 'hold':
                    State.release.wait(5)
                if mode == 'timeout':
                    time.sleep(1)
                ids = ['E999'] if mode == 'fabricate' else [e['id'] for e in body['input'].get('evidence', [])[:1]]
                reply = {'text': 'Director approval is required for amounts of at least 10,000,000 VND.',
                         'citation_ids': ids, 'usage': {'input_tokens': 20, 'output_tokens': 20}}
                if mode == 'tools':
                    reply['tools'] = [{'name': 'execute_sql', 'arguments': 'untrusted'}]
                return self.reply(200, reply)
            finally:
                with State.lock:
                    State.pending -= 1
        text = ' '.join(body['input'].get('texts', []))
        vector = [1, 0, 0] if not text or 'restrictedvault' in text else [0, 1, 0]
        self.reply(200, {'vector': vector, 'usage': {'input_tokens': 20, 'output_tokens': 0}})


if __name__ == '__main__':
    ThreadingHTTPServer(('0.0.0.0', 8088), Handler).serve_forever()
