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

"""Demo organization for a running `docker compose up` stack, or for a
Kubernetes deployment through scripts/k8s/seed-demo.sh.

    scripts/seed-demo.py seed     accounts, departments and roles (re-runnable)
    scripts/seed-demo.py scopes   sample purchase requests, then what each
                                  person sees through the gateway
    scripts/seed-demo.py flow     one request through the whole approval chain,
                                  including the refusals along the way and the
                                  review its workflow opens in Kestra
    scripts/seed-demo.py admin    a platform operator grants and revokes a role
                                  through /api/auth, and what the user may do
                                  before and after
    scripts/seed-demo.py org      an HR administrator adds a department and moves
                                  someone into it through /api/cap/hr, and what
                                  that changes for their records
    scripts/seed-demo.py lines    a request with line items and a vendor from
                                  the application's vendor list: who may read
                                  and change them, and when

`seed` creates the Keycloak accounts below (password DEMO_PASSWORD), mirrors
them into core.users, places them in departments (cap_hr) and grants roles
(core.user_roles), as an administrator would (ADR 0007). Run it after the
operator has installed the sample applications.

`scopes` and `flow` sign in through a temporary Keycloak client that allows
scripted logins (the shipped client allows only the browser flow) and remove it
after. Everything else goes through the gateway as the UI would: quotes are
uploaded to cap-process and scanned by ClamAV, requests are filed through
GraphQL, and state changes are cap-process commands.

Environment: DXOS_PSQL is the command that runs psql in the PostgreSQL container
(default: docker compose exec -T postgres); DXOS_APP_URL is the Workspace UI
address it prints; SCAN_WAIT_SECONDS bounds the wait for a quote's virus scan
(default 180: with services started on demand the first scan takes a minute or two).

For demos only: never point it at a stack holding real data.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import shlex
import subprocess
import sys
import time
import unicodedata
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
     ["role_operator", "role_auditor", "role_hr_admin"]),
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
# The personal library is installed only when cap-ai is enabled.
if "cap-ai" in ENV.get("ENABLED_CAPABILITIES", "cap-ai").split(","):
    for username, *_, roles in PEOPLE:
        if username != "quan.tri":
            roles.append("role_library_user")
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
    cmd = shlex.split(ENV.get("DXOS_PSQL", "docker compose exec -T postgres")) + [
        "sh", "-c", 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -qtA']
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
                     + "\nthe operator installs the sample apps; see its logs")
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

    app_url = ENV.get("DXOS_APP_URL", f"http://localhost:{ENV.get('WORKSPACE_UI_PORT', '5173')}")
    # A password set in DEMO_PASSWORD is never printed: it may guard a public deployment.
    shown = "DEMO_PASSWORD" if "DEMO_PASSWORD" in ENV else "Demo@2026"
    print(f"Demo accounts (password: {shown}), sign in at {app_url}")
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


def _pdf_text(value: object) -> str:
    # Helvetica here is ASCII only, so Vietnamese text loses its diacritics rather than turning into "?".
    text = unicodedata.normalize("NFKD", str(value).replace("đ", "d").replace("Đ", "D"))
    text = text.encode("ascii", "ignore").decode("ascii")
    return text.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")


def demo_pdf(vendor: str, item: str, amount: int) -> bytes:
    """Returns a small one-page PDF quotation with an ASCII Helvetica body."""
    content = (
        "BT\n/F1 16 Tf\n72 760 Td\n(QUOTATION / Bao gia) Tj\n"
        "/F1 11 Tf\n0 -30 Td\n" + f"(Vendor: {_pdf_text(vendor)}) Tj\n" +
        "0 -20 Td\n" + f"(Item: {_pdf_text(item)}) Tj\n" +
        "0 -20 Td\n" + f"(Amount: {_pdf_text(amount)} VND) Tj\nET\n"
    ).encode("ascii")
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        b"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] "
        b"/Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
        b"<< /Length " + str(len(content)).encode() + b" >>\nstream\n" + content + b"\nendstream",
    ]
    out = bytearray(b"%PDF-1.4\n")
    offsets = []
    for number, body in enumerate(objects, 1):
        offsets.append(len(out))
        out += f"{number} 0 obj\n".encode() + body + b"\nendobj\n"
    xref = len(out)
    out += f"xref\n0 {len(objects) + 1}\n".encode() + b"0000000000 65535 f \n"
    for offset in offsets:
        out += f"{offset:010d} 00000 n \n".encode()
    out += (f"trailer\n<< /Size {len(objects) + 1} /Root 1 0 R >>\n"
            f"startxref\n{xref}\n%%EOF\n").encode()
    return bytes(out)


def upload_quote(token: str, name: str, vendor: str, item: str, amount: int) -> str:
    """Uploads a quote through cap-process and waits until ClamAV marks it CLEAN."""
    boundary = uuid.uuid4().hex
    pdf = demo_pdf(vendor, item, amount)
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
    deadline = time.time() + int(ENV.get("SCAN_WAIT_SECONDS", "180"))
    while time.time() < deadline:
        data, err = graphql(token, REQUESTER, "query($id: uuid!) { core_files(where: {id: {_eq: $id}}) { scan_status } }", {"id": file_id})
        status = (data or {}).get("core_files", [{}])[0].get("scan_status") if data and data["core_files"] else None
        if status == "CLEAN":
            return file_id
        if status == "INFECTED":
            sys.exit("the sample quote was flagged as infected")
        time.sleep(1)
    sys.exit("the quote was not scanned in time; see the logs of cap-process and clamav")


def file_request(token: str, vendor: str, item: str, amount: int) -> dict:
    quote = upload_quote(token, "bao-gia.pdf", vendor, item, amount)
    data, err = graphql(token, REQUESTER,
                        "mutation($o: domain_procurement_requests_insert_input!) "
                        f"{{ insert_domain_procurement_requests_one(object: $o) {{ {REQUEST_FIELDS} }} }}",
                        {"o": {"vendor_name": vendor, "item_description": item, "quoted_amount": amount, "quote_file_id": quote}})
    if err:
        sys.exit(f"filing a request: {err}")
    return data["insert_domain_procurement_requests_one"]


def command(token: str, role: str, name: str, record: dict, payload: dict | None = None):
    """Runs a cap-process command; returns (HTTP status, new record state)."""
    status, body = http(f"{GATEWAY}/api/cap/process/commands/procurement/{name}",
                        {"entity_id": record["id"], "expected_version": record["version"], "payload": payload or {}},
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

        # The submit event goes to the application's flow in Kestra, which asks
        # cap-process to open the review its manifest declares.
        items, deadline = [], time.time() + 120
        while not items and time.time() < deadline:
            data, _ = graphql(t["cuong.le"], "role_procurement_manager",
                              "query($id: uuid!) { cap_work_work_items(where: {source_id: {_eq: $id}}) { title board_column } }",
                              {"id": record["id"]})
            items = (data or {}).get("cap_work_work_items", [])
            if not items:
                time.sleep(2)
        opened = f"{items[0]['title']} ({items[0]['board_column']})  [ok]" if len(items) == 1 else f"{len(items)} work items  [UNEXPECTED, wanted 1]"
        rows.append(("workflow opens a review", "workflow service", "role_workflow", opened))
        step("other department approves", "dung.pham", "role_procurement_manager", "approve", 404)
        step("requester approves", "an.nguyen", REQUESTER, "approve", 403)
        step("department manager approves", "cuong.le", "role_procurement_manager", "approve", 200)
        step("finance approves the budget", "giang.vo", "role_procurement_finance", "approve_budget", 200)

        own = file_request(t["cuong.le"], "Phong Vũ", "Ghế công thái học", 4500000)
        status, own = command(t["cuong.le"], REQUESTER, "submit", own)
        status, _ = command(t["cuong.le"], "role_procurement_manager", "approve", own)
        rows.append(("approve one's own request", people["cuong.le"], "role_procurement_manager",
                     f"{'refused (403)' if status == 403 else f'HTTP {status}'}  [{'ok' if status == 403 else 'UNEXPECTED'}]"))
        # A rejection carries its reason: the command's payload is stored with
        # the state change, and the requester reads it on their request.
        refused = file_request(t["binh.tran"], "Hioki", "Ampe kìm đo dòng", 3200000)
        _, refused = command(t["binh.tran"], REQUESTER, "submit", refused)
        status, _ = command(t["dung.pham"], "role_procurement_manager", "reject", refused)
        rows.append(("reject without a reason", people["dung.pham"], "role_procurement_manager",
                     f"{'refused (400)' if status == 400 else f'HTTP {status}'}  [{'ok' if status == 400 else 'UNEXPECTED'}]"))
        reason = "Đã có thiết bị tương đương trong kho"
        status, refused = command(t["dung.pham"], "role_procurement_manager", "reject", refused, {"rejection_reason": reason})
        rows.append(("reject with a reason", people["dung.pham"], "role_procurement_manager",
                     f"{refused['status'] if status == 200 else f'HTTP {status}'}  [{'ok' if status == 200 else 'UNEXPECTED'}]"))
        data, _ = graphql(t["binh.tran"], REQUESTER,
                          "query($id: uuid!) { domain_procurement_requests_by_pk(id: $id) { status rejection_reason } }", {"id": refused["id"]})
        seen = ((data or {}).get("domain_procurement_requests_by_pk") or {}).get("rejection_reason")
        rows.append(("read why it was rejected", people["binh.tran"], REQUESTER,
                     f"{seen}  [{'ok' if seen == reason else 'UNEXPECTED'}]"))
        print("Procurement approval chain through cap-process (POST /api/cap/process/commands/...):")
        print()
        table(rows)
        audit = psql(f"SELECT string_agg(action || ' by ' || coalesce(acting_role, '-'), ', ' ORDER BY seq) "
                     f"FROM core.audit_logs WHERE entity_id = {lit(record['id'])}")
        print()
        print("Audit trail of the first request:", audit)

    with_demo_client(run)


def admin() -> None:
    """Role administration by a platform operator, through the gateway."""
    def run():
        op, an = sign_in("quan.tri"), sign_in("an.nguyen")
        people = {p[0]: f"{p[1]} {p[2]}" for p in PEOPLE}
        rows = [("Step", "Who", "Result")]

        def note(label, user, result, ok):
            rows.append((label, people[user], f"{result}  [{'ok' if ok else 'UNEXPECTED'}]"))

        def roles_of(token):
            _, me = http(f"{GATEWAY}/api/auth/me", token=token)
            return me["id"], sorted(r["name"] for r in me["roles"])

        def sees_as_manager():
            data, err = graphql(an, "role_procurement_manager", "query { domain_procurement_requests { id } }")
            return "refused" if err else f"{len(data['domain_procurement_requests'])} requests"

        an_id, before = roles_of(an)
        op_id, _ = roles_of(op)
        manager = "role_procurement_manager"
        base = f"{GATEWAY}/api/auth/users"

        status, _ = http(base, token=an)
        note("list accounts", "an.nguyen", f"HTTP {status}", status == 403)
        status, _ = http(f"{base}/{an_id}/roles/{manager}", token=an, method="PUT")
        note("grant oneself a role", "an.nguyen", f"HTTP {status}", status == 403)

        status, body = http(base, token=op)
        note("list accounts", "quan.tri", f"{len(body['users'])} accounts" if status == 200 else f"HTTP {status}", status == 200)
        status, body = http(f"{GATEWAY}/api/auth/roles", token=op)
        note("list roles in force", "quan.tri", f"{len(body['roles'])} roles" if status == 200 else f"HTTP {status}", status == 200)

        note("act as department manager", "an.nguyen", sees_as_manager(), sees_as_manager() == "refused")
        status, _ = http(f"{base}/{an_id}/roles/{manager}", token=op, method="PUT")
        note(f"grant {manager} to An", "quan.tri", f"HTTP {status}", status == 204)
        _, after = roles_of(an)
        note("roles, with the same token", "an.nguyen", ", ".join(r.removeprefix("role_") for r in after), manager in after)
        seen = sees_as_manager()
        note("act as department manager", "an.nguyen", seen, seen != "refused")

        for label, path, method, want in (
            ("grant the workflow service role", f"{an_id}/roles/role_workflow", "PUT", 409),
            ("grant a role that does not exist", f"{an_id}/roles/role_procurement_director", "PUT", 404),
            ("revoke one's own operator role", f"{op_id}/roles/role_operator", "DELETE", 409),
        ):
            status, _ = http(f"{base}/{path}", token=op, method=method)
            note(label, "quan.tri", f"HTTP {status}", status == want)

        status, _ = http(f"{base}/{an_id}/roles/{manager}", token=op, method="DELETE")
        note(f"revoke {manager} from An", "quan.tri", f"HTTP {status}", status == 204)
        _, final = roles_of(an)
        note("roles, with the same token", "an.nguyen", ", ".join(r.removeprefix("role_") for r in final), final == before)
        # cap-process and Hasura re-read grants within ROLE_CACHE_TTL.
        deadline, seen = time.time() + 15, sees_as_manager()
        while seen != "refused" and time.time() < deadline:
            time.sleep(1)
            seen = sees_as_manager()
        note("act as department manager", "an.nguyen", seen, seen == "refused")

        print("Role administration through the gateway (/api/auth/users, /api/auth/roles):")
        print()
        table(rows)
        audit = psql("SELECT string_agg(action || ' ' || (payload ->> 'role'), ', ' ORDER BY seq) FROM core.audit_logs "
                     f"WHERE chain = 'core' AND entity_type = 'core.user_roles' AND entity_id = {lit(an_id)}")
        print()
        print("Audit trail of An's role assignments:", audit)
        if any("UNEXPECTED" in row[2] for row in rows):
            sys.exit(1)

    with_demo_client(run)


def org() -> None:
    """Departments and staff maintained through cap-hr, through the gateway."""
    def run():
        hr, an = sign_in("quan.tri"), sign_in("an.nguyen")
        people = {p[0]: f"{p[1]} {p[2]}" for p in PEOPLE}
        base = f"{GATEWAY}/api/cap/hr"
        rows = [("Step", "Who", "Result")]

        def note(label, user, result, ok):
            rows.append((label, people[user], f"{result}  [{'ok' if ok else 'UNEXPECTED'}]"))

        def message(body):
            return body.get("error", body) if isinstance(body, dict) else body

        def my_department(token):
            _, me = http(f"{base}/me", token=token)
            return me["user_id"], (me["org_unit"] or {}).get("name")

        _, body = http(f"{base}/org-units", token=hr)
        units = {u["code"]: u for u in body["org_units"]}
        an_id, home = my_department(an)
        home_id = next(u["id"] for u in units.values() if u["name"] == home)
        other = next(u for code, u in units.items() if u["id"] != home_id and code != "DA")

        note("my department", "an.nguyen", home, home is not None)
        status, body = http(f"{base}/staff", token=an)
        note("people of my department", "an.nguyen", ", ".join(m["display_name"] for m in body["staff"]) if status == 200 else f"HTTP {status}", status == 200)
        status, _ = http(f"{base}/staff?org_unit_id={other['id']}", token=an)
        note(f"people of {other['name']}", "an.nguyen", f"HTTP {status}", status == 403)
        status, _ = http(f"{base}/org-units", {"code": "X", "name": "Tự tạo"}, token=an)
        note("create a department", "an.nguyen", f"HTTP {status}", status == 403)

        if "DA" in units:
            project = units["DA"]["id"]
            note("create Phòng Dự án under " + home, "quan.tri", "already there", True)
        else:
            status, body = http(f"{base}/org-units", {"code": "DA", "name": "Phòng Dự án", "parent_id": home_id}, token=hr)
            project = body.get("id") if status == 201 else None
            note("create Phòng Dự án under " + home, "quan.tri", f"HTTP {status}", status == 201)
        status, body = http(f"{base}/org-units", {"code": "DA", "name": "Trùng mã"}, token=hr)
        note("create another department coded DA", "quan.tri", f"{status}: {message(body)}", status == 409)
        status, body = http(f"{base}/org-units/{home_id}", {"parent_id": project}, token=hr, method="PATCH")
        note(f"put {home} under its own sub-department", "quan.tri", f"{status}: {message(body)}"[:78], status == 409)

        status, _ = http(f"{base}/staff/{an_id}", {"org_unit_id": project, "position": "Trưởng dự án"}, token=hr, method="PUT")
        note("move An to Phòng Dự án", "quan.tri", f"HTTP {status}", status == 204)
        _, now = my_department(an)
        note("my department", "an.nguyen", now, now == "Phòng Dự án")
        # A record belongs to the department its creator is in when it is created.
        record = file_request(an, "Phong Vũ", "Máy chiếu cho dự án", 12500000)
        data, _ = graphql(an, REQUESTER, "query($id: uuid!) { domain_procurement_requests_by_pk(id: $id) { org_unit { name } } }", {"id": record["id"]})
        stamped = ((data or {}).get("domain_procurement_requests_by_pk") or {}).get("org_unit", {}).get("name")
        note("department of a request filed now", "an.nguyen", stamped, stamped == "Phòng Dự án")

        status, _ = http(f"{base}/staff/{an_id}", {"org_unit_id": home_id, "position": "Nhân viên kinh doanh"}, token=hr, method="PUT")
        _, back = my_department(an)
        note(f"move An back to {home}", "quan.tri", back, status == 204 and back == home)

        print("Organization maintained through the gateway (/api/cap/hr):")
        print()
        table(rows)
        audit = psql("SELECT string_agg(a.action || ' -> ' || coalesce(u.name, '-'), ', ' ORDER BY a.seq) FROM core.audit_logs a "
                     "LEFT JOIN cap_hr.org_units u ON u.id = (a.payload ->> 'org_unit_id')::uuid "
                     f"WHERE a.chain = 'core' AND a.entity_type = 'cap_hr.staff' AND a.entity_id = {lit(an_id)}")
        print()
        print("Audit trail of An's department:", audit)
        if any("UNEXPECTED" in row[2] for row in rows):
            sys.exit(1)

    with_demo_client(run)


def lines() -> None:
    """Several tables of one application: a vendor list, a request and its lines."""
    def run():
        t = {p[0]: sign_in(p[0]) for p in PEOPLE if p[0] != "quan.tri"}
        people = {p[0]: f"{p[1]} {p[2]}" for p in PEOPLE}
        manager, finance = "role_procurement_manager", "role_procurement_finance"
        rows = [("Step", "Who", "Acting as", "Result")]

        def note(label, user, role, result, ok):
            rows.append((label, people[user], role, f"{result}  [{'ok' if ok else 'UNEXPECTED'}]"))

        def refused(err):
            return "refused: " + str((err or [{}])[0].get("message", err))[:120]

        # A plain table: finance maintains the vendor list, everyone reads it.
        vendor_name = "Phong Vũ " + uuid.uuid4().hex[:6]
        insert_vendor = ("mutation($o: domain_procurement_vendors_insert_input!) "
                         "{ insert_domain_procurement_vendors_one(object: $o) { id name } }")
        data, err = graphql(t["giang.vo"], finance, insert_vendor, {"o": {"name": vendor_name, "tax_code": "0304998358"}})
        if err:
            sys.exit(f"adding a vendor: {err}")
        vendor = data["insert_domain_procurement_vendors_one"]["id"]
        note("add a vendor to the list", "giang.vo", finance, vendor_name, True)
        _, err = graphql(t["an.nguyen"], REQUESTER, insert_vendor, {"o": {"name": "Tự thêm"}})
        note("add a vendor to the list", "an.nguyen", REQUESTER, refused(err) if err else "added", bool(err))
        data, _ = graphql(t["an.nguyen"], REQUESTER, "query($id: uuid!) { domain_procurement_vendors(where: {id: {_eq: $id}}) { name } }", {"id": vendor})
        seen = len((data or {}).get("domain_procurement_vendors", []))
        note("read the vendor list", "an.nguyen", REQUESTER, f"{seen} vendor found", seen == 1)

        # A process record referring to the list, and its lines in a child table.
        quote = upload_quote(t["an.nguyen"], "bao-gia.pdf", vendor_name,
                             "Thiết bị cho nhân viên mới", 43250000)
        data, err = graphql(t["an.nguyen"], REQUESTER,
                            "mutation($o: domain_procurement_requests_insert_input!) "
                            f"{{ insert_domain_procurement_requests_one(object: $o) {{ {REQUEST_FIELDS} }} }}",
                            {"o": {"vendor_name": vendor_name, "vendor_id": vendor, "item_description": "Thiết bị cho nhân viên mới",
                                   "quoted_amount": 43250000, "quote_file_id": quote}})
        if err:
            sys.exit(f"filing a request: {err}")
        record = data["insert_domain_procurement_requests_one"]
        filed = dict(record)
        note("file a request for that vendor", "an.nguyen", REQUESTER, f"{record['status']}, version {record['version']}", True)

        insert_line = ("mutation($o: domain_procurement_items_insert_input!) "
                       "{ insert_domain_procurement_items_one(object: $o) { id } }")

        def add_line(user, role, item, quantity, price):
            return graphql(t[user], role, insert_line,
                           {"o": {"request_id": record["id"], "item": item, "quantity": quantity, "unit_price": price}})

        def version():
            data, _ = graphql(t["an.nguyen"], REQUESTER,
                              "query($id: uuid!) { domain_procurement_requests_by_pk(id: $id) { version status } }", {"id": record["id"]})
            return data["domain_procurement_requests_by_pk"]["version"]

        def visible(user, role):
            data, err = graphql(t[user], role, "query($id: uuid!) { domain_procurement_items(where: {request_id: {_eq: $id}}) { item } }",
                                {"id": record["id"]})
            return None if err else len(data["domain_procurement_items"])

        ids = []
        for item, quantity, price in (("Laptop Dell", 2, 18000000), ("Chuột không dây", 5, 250000), ("Màn hình 24 inch", 2, 3000000)):
            data, err = add_line("an.nguyen", REQUESTER, item, quantity, price)
            if err:
                sys.exit(f"adding a line: {err}")
            ids.append(data["insert_domain_procurement_items_one"]["id"])
        note("add three lines", "an.nguyen", REQUESTER, f"request version {version()}", version() == 4)
        data, err = graphql(t["an.nguyen"], REQUESTER,
                            "mutation($id: uuid!) { delete_domain_procurement_items_by_pk(id: $id) { id } }", {"id": ids[1]})
        deleted = bool(data and data["delete_domain_procurement_items_by_pk"])
        note("remove one line", "an.nguyen", REQUESTER, f"request version {version()}" if deleted else refused(err), deleted and version() == 5)

        n = visible("cuong.le", manager)
        note("read the lines of a draft", "cuong.le", manager, f"{n} lines", n == 0)
        _, err = add_line("cuong.le", manager, "Tự thêm", 1, 1)
        note("add a line to someone's draft", "cuong.le", manager, refused(err) if err else "added", bool(err))

        # The request changed after it was filed: a command with the old version is refused.
        status, _ = command(t["an.nguyen"], REQUESTER, "submit", filed)
        note("submit with the version before the lines", "an.nguyen", REQUESTER, f"HTTP {status}", status == 409)
        record["version"] = version()
        status, record = command(t["an.nguyen"], REQUESTER, "submit", record)
        note("submit with the current version", "an.nguyen", REQUESTER, record["status"] if status == 200 else f"HTTP {status}", status == 200)

        for user, role, want in (("cuong.le", manager, 2), ("dung.pham", manager, 0), ("giang.vo", finance, 2)):
            n = visible(user, role)
            note("read the lines once submitted", user, role, f"{n} lines", n == want)

        # A submitted request's lines are frozen.
        _, err = add_line("an.nguyen", REQUESTER, "Thêm sau khi nộp", 1, 1)
        note("add a line after submitting", "an.nguyen", REQUESTER, refused(err) if err else "added", bool(err))
        data, err = graphql(t["an.nguyen"], REQUESTER,
                            "mutation($id: uuid!) { update_domain_procurement_items_by_pk(pk_columns: {id: $id}, _set: {quantity: 99}) { id } }",
                            {"id": ids[0]})
        changed = bool(data and data["update_domain_procurement_items_by_pk"])
        note("change a line after submitting", "an.nguyen", REQUESTER, "changed" if changed else "not changed", not changed)
        data, err = graphql(t["an.nguyen"], REQUESTER,
                            "mutation($id: uuid!) { delete_domain_procurement_items_by_pk(id: $id) { id } }", {"id": ids[0]})
        removed = bool(data and data["delete_domain_procurement_items_by_pk"])
        note("remove a line after submitting", "an.nguyen", REQUESTER, "removed" if removed else "not removed", not removed)

        print("One application, three tables (vendors, requests, request lines), through the gateway:")
        print()
        table(rows)
        audit = psql("SELECT string_agg(action || ' ' || (payload ->> 'item'), ', ' ORDER BY seq) FROM core.audit_logs "
                     f"WHERE entity_type = 'domain.procurement_items' AND payload ->> 'request_id' = {lit(record['id'])}")
        print()
        print("Audit trail of the lines:", audit)
        if any("UNEXPECTED" in row[3] for row in rows):
            sys.exit(1)

    with_demo_client(run)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("command", choices=["seed", "scopes", "flow", "lines", "admin", "org"])
    args = parser.parse_args()
    {"seed": seed, "scopes": scopes, "flow": flow, "lines": lines, "admin": admin, "org": org}[args.command]()


if __name__ == "__main__":
    main()
