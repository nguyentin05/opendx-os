# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Bounded transport/auth probes. Webhook qualification is a separate live test."""
import os
import socket
import sys
import xml.etree.ElementTree as ET
from urllib.error import HTTPError
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def response(url, method='GET'):
    parsed = urlsplit(url)
    if parsed.scheme not in ('http', 'https') or not parsed.netloc or parsed.username:
        raise ValueError('absolute HTTP endpoint required')
    try:
        result = build_opener(NoRedirect()).open(Request(url, method=method), timeout=4)
    except HTTPError as error:
        result = error
    with result:
        return result.status, result.read(8193)


def main():
    try:
        s3, clam, kestra = (os.environ[k] for k in ('S3_ENDPOINT', 'CLAMAV_ADDRESS', 'KESTRA_URL'))
        code, body = response(s3.rstrip('/') + '/')
        if code != 403 or ET.fromstring(body).findtext('Code') != 'AccessDenied':
            raise ValueError('S3 did not return the verified AccessDenied response')
        # Kestra Basic API rejects missing credentials with 401. A webhook key is
        # a distinct credential: never count its generic 404 as an auth result.
        code, _ = response(kestra)
        if code != 401:
            raise ValueError('Kestra API did not reject missing authentication with 401')
        host, port = clam.rsplit(':', 1)
        with socket.create_connection((host, int(port)), timeout=4) as stream:
            stream.settimeout(4)
            stream.sendall(b'zPING\0')
            answer = b''
            while len(answer) < 16 and not answer.endswith(b'\0'):
                chunk = stream.recv(16 - len(answer))
                if not chunk:
                    break
                answer += chunk
            if answer != b'PONG\0':
                raise ValueError('ClamAV PING did not return PONG')
    except (KeyError, OSError, ValueError, ET.ParseError):
        print('[test-supporting-services] FAIL/NOT_RUN: required bounded auth/readiness probe failed; endpoints withheld', file=sys.stderr)
        return 1
    print('[test-supporting-services] PASS: S3 AccessDenied, Kestra API 401, ClamAV PONG (webhook/effects separately qualified)')
    return 0


if __name__ == '__main__':
    sys.exit(main())
