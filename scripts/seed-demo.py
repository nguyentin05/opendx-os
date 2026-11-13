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

"""Demo organization for a running `docker compose up` stack.

    scripts/seed-demo.py seed     accounts, departments and roles (re-runnable)
    scripts/seed-demo.py scopes   sample purchase requests, then what each
                                  person sees through the gateway
    scripts/seed-demo.py flow     one request through the whole approval chain,
                                  including the refusals along the way

`seed` creates the Keycloak accounts below (password DEMO_PASSWORD), mirrors
them into core.users, places them in departments (cap_hr) and grants roles
(core.user_roles), as an administrator would (ADR 0007). Run it after the
operator has installed the sample applications.

`scopes` and `flow` sign in through a temporary Keycloak client that allows
scripted logins (the shipped client allows only the browser flow) and remove it
after. Everything else goes through the gateway as the UI would: quotes are
uploaded to cap-process and scanned by ClamAV, requests are filed through
GraphQL, and state changes are cap-process commands.

For local demos only: never point it at a stack holding real data.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import subprocess
import sys
import time
import uuid
import urllib.error
import urllib.parse
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]

DEPARTMENTS = [
    # code, name
    ("KD", "Phòng Kinh doanh"),
    ("KT", "Phòng Kỹ thuật"),
    ("TC", "Phòng Tài chính"),
]

# username, first name, last name, department, position, roles
PEOPLE = [
    ("an.nguyen", "Nguyễn Văn", "An", "KD", "Nhân viên kinh doanh",
     ["role_procurement_requester", "role_timesheet_employee"]),
    ("cuong.le", "Lê Minh", "Cường", "KD", "Trưởng phòng Kinh doanh",
     ["role_procurement_requester", "role_procurement_manager", "role_timesheet_manager"]),
    ("binh.tran", "Trần Thị", "Bình", "KT", "Kỹ sư bảo trì",
     ["role_procurement_requester", "role_maintenance_technician", "role_timesheet_employee"]),
    ("dung.pham", "Phạm Thu", "Dung", "KT", "Trưởng phòng Kỹ thuật",
     ["role_procurement_manager", "role_maintenance_manager", "role_timesheet_manager"]),
    ("giang.vo", "Võ Hoàng", "Giang", "TC", "Kế toán ngân sách",
     ["role_procurement_finance", "role_timesheet_employee"]),
    ("quan.tri", "Quản trị", "Hệ thống", "TC", "Quản trị hệ thống",
     ["role_operator", "role_auditor"]),
]

# requester, vendor, item, amount, submitted
REQUESTS = [
    ("an.nguyen", "FPT Shop", "Laptop cho nhân viên mới", 18500000, True),
    ("an.nguyen", "Thế Giới Di Động", "Máy chiếu phòng họp", 12900000, False),
    ("binh.tran", "Hioki Việt Nam", "Đồng hồ đo điện", 7400000, True),
]

TEMP_CLIENT = "dxos-demo-cli"


def load_env() -> dict[str, str]:
    env: dict[str, str] = {}
    path = ROOT / ".env"
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if line and not line.startswith("#") and "=" in line:
                key, value = line.split("=", 1)
                env[key.strip()] = value.strip()
    env.update({k: v for k, v in os.environ.items() if k.isupper()})
    return env


ENV = load_env()
KEYCLOAK = f"http://localhost:{ENV.get('KEYCLOAK_PORT', '8085')}"
GATEWAY = f"http://localhost:{ENV.get('APISIX_GATEWAY_PORT', '9080')}"
PASSWORD = ENV.get("DEMO_PASSWORD", "Demo@2026")


def http(url, data=None, token=None, form=False, method=None, headers=None):
    h = dict(headers or {})
    if token:
        h["Authorization"] = "Bearer " + token
    body = None
    if data is not None:
        if form:
            body = urllib.parse.urlencode(data).encode()
            h["Content-Type"] = "application/x-www-form-urlencoded"
        else:
            body = json.dumps(data).encode()
            h["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=body, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            raw = resp.read()
            return resp.status, json.loads(raw) if raw else None
    except urllib.error.HTTPError as err:
        raw = err.read()
        try:
            return err.code, json.loads(raw)
        except ValueError:
            return err.code, raw.decode(errors="replace")


def psql(sql: str) -> str:
    """Runs SQL in the platform database as the bootstrap administrator."""
    cmd = ["docker", "compose", "exec", "-T", "postgres", "sh", "-c",
           'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -qtA']
    res = subprocess.run(cmd, input=sql, text=True, capture_output=True, cwd=ROOT)
    if res.returncode != 0:
        sys.exit(f"psql failed: {res.stderr.strip()}")
    return res.stdout.strip()


def lit(value: str) -> str:
    return "'" + str(value).replace("'", "''") + "'"


def admin_token() -> str:
    status, body = http(f"{KEYCLOAK}/realms/master/protocol/openid-connect/token", {
        "grant_type": "password", "client_id": "admin-cli",
        "username": ENV.get("KEYCLOAK_ADMIN", "admin"),
        "password": ENV.get("KEYCLOAK_ADMIN_PASSWORD", "admin"),
    }, form=True)
    if status != 200:
        sys.exit(f"Keycloak admin sign-in failed ({status}); is the stack up? {body}")
    return body["access_token"]


def ensure_user(admin: str, username: str, first: str, last: str) -> str:
    base = f"{KEYCLOAK}/admin/realms/dxos/users"
    profile = {"username": username, "email": f"{username}@demo.opendx.local", "firstName": first,
               "lastName": last, "enabled": True, "emailVerified": True}
    _, found = http(f"{base}?exact=true&username={urllib.parse.quote(username)}", token=admin)
    if found:
        user_id = found[0]["id"]
        http(f"{base}/{user_id}", profile, admin, method="PUT")
    else:
        status, body = http(base, profile, admin)
        if status != 201:
            sys.exit(f"create {username}: {status} {body}")
        _, found = http(f"{base}?exact=true&username={urllib.parse.quote(username)}", token=admin)
        user_id = found[0]["id"]
    status, body = http(f"{base}/{user_id}/reset-password",
                        {"type": "password", "value": PASSWORD, "temporary": False}, admin, method="PUT")
    if status != 204:
        sys.exit(f"set password for {username}: {status} {body}")
    return user_id


def wait_for_roles(names: set[str]) -> None:
    listed = ",".join(lit(n) for n in sorted(names))
    deadline = time.time() + 180
    while True:
        have = set(filter(None, psql(f"SELECT name FROM core.roles WHERE name IN ({listed})").split("\n")))
        missing = names - have
        if not missing:
            return
        if time.time() > deadline:
            sys.exit("roles not registered yet: " + ", ".join(sorted(missing))
                     + "\nthe operator installs the sample apps; see: docker compose logs operator")
        time.sleep(3)


def seed() -> dict[str, str]:
    wait_for_roles({r for p in PEOPLE for r in p[5]})
    admin = admin_token()
    ids = {}
    for username, first, last, *_ in PEOPLE:
        ids[username] = ensure_user(admin, username, first, last)

    sql = ["BEGIN;"]
    for code, name in DEPARTMENTS:
        sql.append(f"INSERT INTO cap_hr.org_units (code, name) VALUES ({lit(code)}, {lit(name)}) "
                   f"ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name;")
    for username, first, last, dept, position, roles in PEOPLE:
        uid = lit(ids[username])
        # The mirror of the Keycloak account, as dxos-authz or the sync writes it.
        sql.append(f"INSERT INTO core.users (id, email, display_name) VALUES ({uid}, "
                   f"{lit(username + '@demo.opendx.local')}, {lit(first + ' ' + last)}) "
                   f"ON CONFLICT (id) DO UPDATE SET email = EXCLUDED.email, display_name = EXCLUDED.display_name;")
        sql.append(f"INSERT INTO cap_hr.staff (user_id, org_unit_id, position) SELECT {uid}, id, {lit(position)} "
                   f"FROM cap_hr.org_units WHERE code = {lit(dept)} "
                   f"ON CONFLICT (user_id) DO UPDATE SET org_unit_id = EXCLUDED.org_unit_id, position = EXCLUDED.position;")
        for role in roles:
            sql.append(f"INSERT INTO core.user_roles (user_id, role_name) VALUES ({uid}, {lit(role)}) ON CONFLICT DO NOTHING;")
    sql.append("COMMIT;")
    psql("\n".join(sql))

    print(f"Demo accounts (password: {PASSWORD}), sign in at http://localhost:{ENV.get('WORKSPACE_UI_PORT', '5173')}")
    print()
    rows = [("Username", "Name", "Department", "Roles")]
    depts = dict(DEPARTMENTS)
    for username, first, last, dept, _, roles in PEOPLE:
        rows.append((username, f"{first} {last}", depts[dept], ", ".join(roles)))
    table(rows)
    return ids


def table(rows) -> None:
    widths = [max(len(str(r[i])) for r in rows) for i in range(len(rows[0]))]
    for n, row in enumerate(rows):
        print("  ".join(str(c).ljust(w) for c, w in zip(row, widths)).rstrip())
        if n == 0:
            print("  ".join("-" * w for w in widths))


def with_demo_client(fn):
    admin = admin_token()
    clients = f"{KEYCLOAK}/admin/realms/dxos/clients"
    http(clients, {"clientId": TEMP_CLIENT, "publicClient": True, "directAccessGrantsEnabled": True,
                   "standardFlowEnabled": False, "protocolMappers": [{
                       "name": "dxos-runtime-audience", "protocol": "openid-connect",
                       "protocolMapper": "oidc-audience-mapper",
                       "config": {"included.custom.audience": "dxos-runtime", "access.token.claim": "true"}}]}, admin)
    try:
        return fn()
    finally:
        _, found = http(f"{clients}?clientId={TEMP_CLIENT}", token=admin_token())
        for c in found or []:
            http(f"{clients}/{c['id']}", token=admin_token(), method="DELETE")


def sign_in(username: str) -> str:
    status, body = http(f"{KEYCLOAK}/realms/dxos/protocol/openid-connect/token", {
        "grant_type": "password", "client_id": TEMP_CLIENT, "username": username,
        "password": PASSWORD, "scope": "openid"}, form=True)
    if status != 200:
        sys.exit(f"sign in as {username}: {status} {body}; run 'seed' first")
    return body["access_token"]


def graphql(token: str, role: str, query: str, variables=None):
    status, body = http(f"{GATEWAY}/v1/graphql", {"query": query, "variables": variables or {}},
                        token, headers={"X-Hasura-Role": role})
    if status != 200 or not isinstance(body, dict) or body.get("errors"):
        return None, body.get("errors") if isinstance(body, dict) else body
    return body["data"], None



REQUESTER = "role_procurement_requester"
REQUEST_FIELDS = "id item_description status version"


def upload_quote(token: str, name: str) -> str:
    """Uploads a quote through cap-process and waits until ClamAV marks it CLEAN."""
    boundary = uuid.uuid4().hex
    pdf = b"%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n"
    body = (f"--{boundary}\r\nContent-Disposition: form-data; name=\"app_id\"\r\n\r\nprocurement\r\n"
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{name}\"\r\n"
            "Content-Type: application/pdf\r\n\r\n").encode() + pdf + f"\r\n--{boundary}--\r\n".encode()
    req = urllib.request.Request(f"{GATEWAY}/api/cap/process/files/upload", data=body, method="POST", headers={
        "Authorization": "Bearer " + token, "X-Hasura-Role": REQUESTER,
        "Content-Type": "multipart/form-data; boundary=" + boundary})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            file_id = json.loads(resp.read())["file_id"]
    except urllib.error.HTTPError as err:
        sys.exit(f"upload failed: {err.code} {err.read().decode(errors='replace')}")
    deadline = time.time() + 60
    while time.time() < deadline:
        data, err = graphql(token, REQUESTER, "query($id: uuid!) { core_files(where: {id: {_eq: $id}}) { scan_status } }", {"id": file_id})
        status = (data or {}).get("core_files", [{}])[0].get("scan_status") if data and data["core_files"] else None
        if status == "CLEAN":
            return file_id
        if status == "INFECTED":
            sys.exit("the sample quote was flagged as infected")
        time.sleep(1)
    sys.exit("the quote was not scanned within a minute; see: docker compose logs cap-process clamav")


def file_request(token: str, vendor: str, item: str, amount: int) -> dict:
    quote = upload_quote(token, "bao-gia.pdf")
    data, err = graphql(token, REQUESTER,
                        "mutation($o: domain_procurement_requests_insert_input!) "
                        f"{{ insert_domain_procurement_requests_one(object: $o) {{ {REQUEST_FIELDS} }} }}",
                        {"o": {"vendor_name": vendor, "item_description": item, "quoted_amount": amount, "quote_file_id": quote}})
    if err:
        sys.exit(f"filing a request: {err}")
    return data["insert_domain_procurement_requests_one"]


def command(token: str, role: str, name: str, record: dict):
    """Runs a cap-process command; returns (HTTP status, new record state)."""
    status, body = http(f"{GATEWAY}/api/cap/process/commands/procurement/{name}",
                        {"entity_id": record["id"], "expected_version": record["version"], "payload": {}},
                        token, headers={"X-Hasura-Role": role, "Idempotency-Key": str(uuid.uuid4())})
    if status == 200:
        record = dict(record, status=body["status"], version=body["version"])
    return status, record


def scopes() -> None:
    def run():
        tokens = {p[0]: sign_in(p[0]) for p in PEOPLE if p[0] != "quan.tri"}
        list_query = "{ domain_procurement_requests(order_by: {created_at: asc}) { item_description status } }"

        # Each requester files their requests once, with a scanned quote, and
        # submits the ones marked so through cap-process.
        for username in sorted({r[0] for r in REQUESTS}):
            data, err = graphql(tokens[username], REQUESTER, list_query)
            if err:
                sys.exit(f"{username}: {err}")
            if data["domain_procurement_requests"]:
                continue
            for _, vendor, item, amount, submit in (r for r in REQUESTS if r[0] == username):
                record = file_request(tokens[username], vendor, item, amount)
                if submit:
                    status, _ = command(tokens[username], REQUESTER, "submit", record)
                    if status != 200:
                        sys.exit(f"{username} submits: HTTP {status}")

        depts = dict(DEPARTMENTS)
        people = {p[0]: p for p in PEOPLE}
        rows = [("Person", "Department", "Acting as", "Sees")]
        for username, role in [("an.nguyen", REQUESTER),
                               ("binh.tran", REQUESTER),
                               ("cuong.le", "role_procurement_manager"),
                               ("dung.pham", "role_procurement_manager"),
                               ("giang.vo", "role_procurement_finance"),
                               ("an.nguyen", "role_procurement_manager")]:
            p = people[username]
            data, err = graphql(tokens[username], role, list_query)
            if err:
                sees = "refused: " + "; ".join(e.get("message", str(e)) for e in err)
            else:
                sees = ", ".join(f"{r['item_description']} [{r['status']}]"
                                 for r in data["domain_procurement_requests"]) or "(nothing)"
            rows.append((f"{p[1]} {p[2]}", depts[p[3]], role, sees))
        print("The same GraphQL query, per person (domain_procurement_requests):")
        print()
        table(rows)

    with_demo_client(run)


def flow() -> None:
    """One request from filing to budget approval, with the refusals on the way."""
    def run():
        t = {p[0]: sign_in(p[0]) for p in PEOPLE if p[0] != "quan.tri"}
        people = {p[0]: f"{p[1]} {p[2]}" for p in PEOPLE}
        rows = [("Step", "Who", "Acting as", "Result")]
        record = file_request(t["an.nguyen"], "Phong Vũ", "Màn hình 27 inch", 6900000)
        rows.append(("file request + scanned quote", people["an.nguyen"], REQUESTER, record["status"]))

        def step(label, user, role, name, expect):
            nonlocal record
            status, after = command(t[user], role, name, record)
            outcome = {200: after["status"], 403: "refused (403)", 404: "not visible (404)", 409: "conflict (409)"}.get(status, f"HTTP {status}")
            mark = "ok" if status == expect else f"UNEXPECTED, wanted {expect}"
            rows.append((label, people[user], role, f"{outcome}  [{mark}]"))
            if status == 200:
                record = after

        step("manager acts on a draft", "cuong.le", "role_procurement_manager", "approve", 404)
        step("submit", "an.nguyen", REQUESTER, "submit", 200)
        step("other department approves", "dung.pham", "role_procurement_manager", "approve", 404)
        step("requester approves", "an.nguyen", REQUESTER, "approve", 403)
        step("department manager approves", "cuong.le", "role_procurement_manager", "approve", 200)
        step("finance approves the budget", "giang.vo", "role_procurement_finance", "approve_budget", 200)

        own = file_request(t["cuong.le"], "Phong Vũ", "Ghế công thái học", 4500000)
        status, own = command(t["cuong.le"], REQUESTER, "submit", own)
        status, _ = command(t["cuong.le"], "role_procurement_manager", "approve", own)
        rows.append(("approve one's own request", people["cuong.le"], "role_procurement_manager",
                     f"{'refused (403)' if status == 403 else f'HTTP {status}'}  [{'ok' if status == 403 else 'UNEXPECTED'}]"))
        print("Procurement approval chain through cap-process (POST /api/cap/process/commands/...):")
        print()
        table(rows)
        audit = psql(f"SELECT string_agg(action || ' by ' || coalesce(acting_role, '-'), ', ' ORDER BY seq) "
                     f"FROM core.audit_logs WHERE entity_id = {lit(record['id'])}")
        print()
        print("Audit trail of the first request:", audit)

    with_demo_client(run)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("command", choices=["seed", "scopes", "flow"])
    args = parser.parse_args()
    {"seed": seed, "scopes": scopes, "flow": flow}[args.command]()


if __name__ == "__main__":
    main()
