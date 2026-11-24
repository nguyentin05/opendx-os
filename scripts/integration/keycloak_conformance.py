#!/usr/bin/env python3
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

"""Conformance checks for the shipped Keycloak realm against a disposable server.

The browser flow only needs a public client with PKCE, so the shipped
dxos-portal client disables the password grant. This check enables it on the
disposable server to obtain a token that exercises the real client, its
dxos-runtime audience mapper, and the realm's signing keys.
"""

from __future__ import annotations

import base64
import hashlib
import html
import http.cookiejar
import json
import os
import re
import secrets
import sys
import urllib.error
import urllib.parse
import urllib.request

BASE = os.environ.get("KEYCLOAK_BASE_URL", "http://127.0.0.1:18085").rstrip("/")
ADMIN_USER = os.environ.get("KEYCLOAK_ADMIN", "admin")
ADMIN_PASSWORD = os.environ.get("KEYCLOAK_ADMIN_PASSWORD", "")
REALM = "dxos"
CLIENT_ID = "dxos-portal"
USERNAME = "conformance-user"
PASSWORD = "conformance-password"
WORKSPACE_ID = "11111111-1111-4111-8111-111111111111"
SERVICE_CLIENTS = [
    ("dxos-rag-worker", "33333333-3333-4333-8333-000000000001"),
    ("dxos-file-service", "33333333-3333-4333-8333-000000000002"),
    ("dxos-rag-dispatcher", "33333333-3333-4333-8333-000000000003"),
]
REDIRECT_URI = "http://localhost:5173/"


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class LoopbackCookies(http.cookiejar.DefaultCookiePolicy):
    """Send Secure cookies over plain http, as browsers do for a loopback host."""

    def return_ok_secure(self, cookie, request):
        return True


def browser_opener():
    return urllib.request.build_opener(
        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar(LoopbackCookies())),
        NoRedirect(),
    )


def browser_request(opener, url, data=None):
    headers = {"Accept": "text/html,application/json"}
    body = None
    if data is not None:
        body = urllib.parse.urlencode(data).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    req = urllib.request.Request(url, data=body, headers=headers)
    try:
        return opener.open(req, timeout=30)
    except urllib.error.HTTPError as error:
        return error


def response_body(response):
    try:
        return response.read().decode("utf-8", "replace")
    finally:
        try:
            response.close()
        except Exception:
            pass


def attributes(tag):
    return {match.group(1).lower(): html.unescape(match.group(2))
            for match in re.finditer(r'([A-Za-z0-9:_-]+)\s*=\s*"([^"]*)"', tag)}


def login_form(page):
    for tag in re.findall(r"<form\b[^>]*>", page, re.IGNORECASE):
        form = attributes(tag)
        if form.get("id") != "kc-form-login":
            continue
        hidden = {}
        for input_tag in re.findall(r"<input\b[^>]*>", page, re.IGNORECASE):
            field = attributes(input_tag)
            if field.get("type", "").lower() == "hidden" and field.get("name"):
                hidden[field["name"]] = field.get("value", "")
        return form.get("action"), hidden
    return None, {}


def browser_login(discovery):
    """Establish a real authorization-code session and return its id token."""
    opener = browser_opener()
    verifier = secrets.token_urlsafe(64)
    challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()
    state = secrets.token_urlsafe(16)
    auth_url = discovery["authorization_endpoint"] + "?" + urllib.parse.urlencode({
        "client_id": CLIENT_ID,
        "redirect_uri": REDIRECT_URI,
        "response_type": "code",
        "scope": "openid profile email",
        "state": state,
        "code_challenge": challenge,
        "code_challenge_method": "S256",
    })
    response = browser_request(opener, auth_url)
    if response.code != 200:
        return None, f"authorization page returned {response.code}"
    page = response_body(response)
    action, hidden = login_form(page)
    if not action:
        return None, "authorization page has no login form"
    fields = dict(hidden, username=USERNAME, password=PASSWORD)
    response = browser_request(opener, urllib.parse.urljoin(auth_url, action), fields)
    location = response.headers.get("Location", "") if response.code in (301, 302, 303, 307, 308) else ""
    if not location:
        return None, f"login returned {response.code} without a redirect"
    code = urllib.parse.parse_qs(urllib.parse.urlparse(location).query).get("code", [None])[0]
    if not code:
        return None, f"login redirect has no code: {location}"
    token = request(discovery["token_endpoint"], {
        "grant_type": "authorization_code",
        "client_id": CLIENT_ID,
        "redirect_uri": REDIRECT_URI,
        "code": code,
        "code_verifier": verifier,
    }, form=True)
    id_token = token.get("id_token")
    if not id_token:
        return None, "authorization-code token response has no id_token"
    return (opener, id_token, auth_url), None


def check_logout(discovery, opener, id_token, auth_url):
    endpoint = discovery.get("end_session_endpoint")
    if not endpoint:
        return "discovery document lacks end_session_endpoint"
    query = urllib.parse.urlencode({
        "id_token_hint": id_token,
        "client_id": CLIENT_ID,
        "post_logout_redirect_uri": REDIRECT_URI,
    })
    response = browser_request(opener, endpoint + "?" + query)
    location = response.headers.get("Location", "") if response.code in (301, 302, 303, 307, 308) else ""
    if not location:
        return f"logout returned {response.code}, expected a redirect"
    if not urllib.parse.urljoin(endpoint, location).startswith(REDIRECT_URI):
        return f"logout redirected to {location!r}"
    response = browser_request(opener, auth_url)
    page = response_body(response)
    if response.code != 200 or "kc-form-login" not in page:
        return f"authorization after logout returned {response.code} without the login form"
    return None


def request(path, data=None, token=None, form=False, method=None):
    url = path if path.startswith("http") else BASE + path
    headers = {"Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    body = None
    if data is not None:
        if form:
            body = urllib.parse.urlencode(data).encode()
            headers["Content-Type"] = "application/x-www-form-urlencoded"
        else:
            body = json.dumps(data).encode()
            headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=30) as response:
        raw = response.read()
        return json.loads(raw) if raw else None


def decode_claims(token):
    payload = token.split(".")[1]
    payload += "=" * (-len(payload) % 4)
    return json.loads(base64.urlsafe_b64decode(payload))


def main():
    errors = []

    def check(condition, message):
        if not condition:
            errors.append(message)

    discovery = request(f"/realms/{REALM}/.well-known/openid-configuration")
    issuer = f"{BASE}/realms/{REALM}"
    check(discovery.get("issuer") == issuer, f"issuer is {discovery.get('issuer')!r}, expected {issuer!r}")
    for key in ("authorization_endpoint", "token_endpoint", "end_session_endpoint", "jwks_uri"):
        check(bool(discovery.get(key)), f"discovery document lacks {key}")

    jwks = request(discovery["jwks_uri"])
    keys = jwks.get("keys") or []
    check(bool(keys), "JWKS has no keys")
    check(all(key.get("kid") and key.get("kty") for key in keys), "a JWKS key lacks kid or kty")

    admin = request(
        "/realms/master/protocol/openid-connect/token",
        {"grant_type": "password", "client_id": "admin-cli", "username": ADMIN_USER, "password": ADMIN_PASSWORD},
        form=True,
    )["access_token"]

    realm = request(f"/admin/realms/{REALM}", token=admin)
    check(realm.get("bruteForceProtected") is True, "brute-force protection is off")
    check("length(" in (realm.get("passwordPolicy") or ""), "the realm has no password length policy")
    # The Workspace UI refreshes tokens, so access tokens stay short-lived.
    check(realm.get("accessTokenLifespan") == 300, "access tokens must last 300 seconds")

    clients = request(f"/admin/realms/{REALM}/clients?clientId={CLIENT_ID}", token=admin)
    check(len(clients) == 1, f"{CLIENT_ID} client is missing")
    if clients:
        client = clients[0]
        mappers = request(f"/admin/realms/{REALM}/clients/{client['id']}/protocol-mappers/models", token=admin)
        audience = [mapper for mapper in mappers if mapper.get("name") == "dxos-runtime-audience"]
        check(bool(audience), "dxos-runtime audience mapper is missing")
        if audience:
            custom = audience[0].get("config", {}).get("included.custom.audience")
            check(custom == "dxos-runtime", f"audience mapper targets {custom!r}, expected 'dxos-runtime'")
        client["directAccessGrantsEnabled"] = True
        request(f"/admin/realms/{REALM}/clients/{client['id']}", client, token=admin, method="PUT")

    # Confidential service clients ship disabled with a generated secret, so a
    # stack without the knowledge overlay cannot mint service tokens. rag-up
    # enables them with its own secrets; the check below does the same.
    for client_id, subject in SERVICE_CLIENTS:
        clients = request(f"/admin/realms/{REALM}/clients?clientId={client_id}", token=admin)
        check(len(clients) == 1, f"{client_id} client is missing")
        if not clients:
            continue
        client = clients[0]
        check(client.get("enabled") is False, f"{client_id} must be imported disabled")
        check(client.get("publicClient") is False, f"{client_id} must be confidential")
        check(client.get("serviceAccountsEnabled") is True, f"{client_id} has no service account")
        check(client.get("standardFlowEnabled") is False, f"{client_id} must not use the browser flow")
        check(client.get("directAccessGrantsEnabled") is False, f"{client_id} must not use the password grant")
        installed = request(f"/admin/realms/{REALM}/clients/{client['id']}/client-secret", token=admin).get("value") or ""
        check(len(installed) >= 16 and "${" not in installed, f"{client_id} imported without a generated secret")
        for guess in ("", "${RAG_WORKER_CLIENT_SECRET}"):
            try:
                request(f"/realms/{REALM}/protocol/openid-connect/token",
                        {"grant_type": "client_credentials", "client_id": client_id, "client_secret": guess}, form=True)
                errors.append(f"{client_id} issued a token for a guessed secret")
            except urllib.error.HTTPError:
                pass
        secret = f"conformance-{client_id}-secret"
        # A partial representation recreates the service account; send it whole.
        client.update(enabled=True, secret=secret)
        request(f"/admin/realms/{REALM}/clients/{client['id']}", client, token=admin, method="PUT")
        try:
            token = request(
                f"/realms/{REALM}/protocol/openid-connect/token",
                {"grant_type": "client_credentials", "client_id": client_id, "client_secret": secret},
                form=True,
            )["access_token"]
            claims = decode_claims(token)
            check(claims.get("sub") == subject, f"{client_id} subject is {claims.get('sub')!r}, expected {subject!r}")
            check(claims.get("azp") == client_id, f"{client_id} authorized party is {claims.get('azp')!r}")
            audience = claims.get("aud")
            audiences = audience if isinstance(audience, list) else [audience]
            check("dxos-runtime" in audiences, f"{client_id} audience is {audience!r}, expected dxos-runtime")
        except urllib.error.HTTPError as error:
            errors.append(f"{client_id} client-credentials grant returned {error.code}")

    user_id = None
    try:
        users = request(f"/admin/realms/{REALM}/users?username={USERNAME}&exact=true", token=admin)
        if users:
            user_id = users[0]["id"]
            request(f"/admin/realms/{REALM}/users/{user_id}", {"enabled": True}, token=admin, method="PUT")
        else:
            request(
                f"/admin/realms/{REALM}/users",
                {
                    "username": USERNAME,
                    "enabled": True,
                    "email": f"{USERNAME}@conformance.invalid",
                    "emailVerified": True,
                    "firstName": "Conformance",
                    "lastName": "User",
                    "attributes": {
                        "workspace_id": [WORKSPACE_ID],
                        "default_role": ["role_requester"],
                        "allowed_roles": ["role_requester"],
                    },
                },
                token=admin,
                method="POST",
            )
            user_id = request(f"/admin/realms/{REALM}/users?username={USERNAME}&exact=true", token=admin)[0]["id"]

        request(
            f"/admin/realms/{REALM}/users/{user_id}/reset-password",
            {"type": "password", "value": PASSWORD, "temporary": False},
            token=admin,
            method="PUT",
        )

        token = request(
            f"/realms/{REALM}/protocol/openid-connect/token",
            {
                "grant_type": "password",
                "client_id": CLIENT_ID,
                "username": USERNAME,
                "password": PASSWORD,
                "scope": "openid",
            },
            form=True,
        )["access_token"]
        claims = decode_claims(token)
        check(claims.get("iss") == issuer, f"token issuer is {claims.get('iss')!r}")
        check(claims.get("azp") == CLIENT_ID, f"authorized party is {claims.get('azp')!r}")
        check(claims.get("sub") == user_id, "token subject does not match the provisioned user")
        check(
            isinstance(claims.get("iat"), int) and isinstance(claims.get("exp"), int),
            "token lacks iat/exp",
        )
        audience = claims.get("aud")
        audiences = audience if isinstance(audience, list) else [audience]
        check("dxos-runtime" in audiences, f"token audience is {audience!r}, expected dxos-runtime")
        check(claims.get("email_verified") is True, "token lacks email_verified: true")

        # dxos-authz trusts the email and name in the token, so a user must not
        # edit them in the account console.
        try:
            request(
                f"/realms/{REALM}/account/",
                {"username": USERNAME, "email": "operator@conformance.invalid",
                 "firstName": "Someone", "lastName": "Else"},
                token=token,
            )
        except urllib.error.HTTPError:
            pass
        account = request(f"/admin/realms/{REALM}/users/{user_id}", token=admin)
        check(
            account.get("email") == f"{USERNAME}@conformance.invalid" and account.get("firstName") == "Conformance",
            "a user changed their own email or name in the account console",
        )

        try:
            session, error = browser_login(discovery)
            if error:
                errors.append(f"RP-initiated logout check: {error}")
            else:
                opener, id_token, auth_url = session
                error = check_logout(discovery, opener, id_token, auth_url)
                if error:
                    errors.append(f"RP-initiated logout check: {error}")
        except Exception as error:
            errors.append(f"RP-initiated logout check failed: {error}")

        try:
            request(
                f"/realms/{REALM}/protocol/openid-connect/token",
                {
                    "grant_type": "password",
                    "client_id": CLIENT_ID,
                    "username": USERNAME,
                    "password": "wrong-password",
                    "scope": "openid",
                },
                form=True,
            )
            errors.append("a wrong password was accepted")
        except urllib.error.HTTPError as error:
            check(error.code in (400, 401), f"a wrong password returned {error.code}, expected 400 or 401")
    finally:
        if user_id:
            try:
                request(f"/admin/realms/{REALM}/users/{user_id}", token=admin, method="DELETE")
            except urllib.error.HTTPError:
                pass

    if errors:
        for error in errors:
            print(f"[keycloak-conformance] FAIL: {error}", file=sys.stderr)
        return 1

    print(
        "[keycloak-conformance] PASS: realm discovery, JWKS, audience mapper, portal and service-client token claims, "
        "brute-force and password policy, admin-only profile edits, wrong-password rejection, and RP-initiated logout verified."
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
