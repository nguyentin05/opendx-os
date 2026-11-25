#!/usr/bin/env bash
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

# Coordinated deployment of one reviewed model declaration on the existing
# Compose project. It reuses the knowledge bootstrap (`lib/stack.sh`, the same
# environment files, Keycloak clients and readiness checks), adds the model
# schema and the private proxy only when the declaration selects them, and moves
# the deployment snapshot in one coordinated step:
#
#   1. validate the declaration offline (a rejected declaration changes nothing);
#   2. render the private proxy configuration and derive its credential references;
#   3. provision the optional pgvector/model schema when the declaration needs it;
#   4. start the proxy (and a declared Ollama runtime) and probe every configured
#      route with the fixed synthetic input;
#   5. drain the model-calling API and worker, reconcile exactly the qualified
#      purposes in one transaction (a failed purpose is left disabled), then start
#      the runtime on that one verified snapshot and check its identity against the
#      database;
#   6. report failures in the exit status even when the installation continues to
#      serve authorized lexical search.
#
# It never runs `down -v`, drops a database, resets a migration checksum or prints
# a credential. A data volume initialized for another Compose project or without
# cap-ai is reported, never repaired by deleting data. The operator can correct the
# declaration and rerun at any point.
set -euo pipefail

# shellcheck source=scripts/lib/stack.sh
. "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/stack.sh"
cd "$STACK_ROOT"

STACK_LABEL=ai-up
export STACK_LABEL

CONFIG="models.yaml"
SECRET_ENV=""
INSTALL_ENV=".env.rag"
MODEL_ENV="${AI_UP_MODEL_ENV:-.env.models}"
STATE_DIR="${AI_UP_STATE_DIR:-build/ai-up}"
CLI="${DXOS_MODEL_BIN:-$STACK_ROOT/bin/dxos-model}"
CLI_TARGET="/run/dxos-deploy/dxos-model"

usage() {
    cat <<'EOF'
Usage: scripts/ai-up.sh [options]

Deploys the reviewed model declaration on this Compose project and moves the
API and worker to one verified snapshot. The default declaration is models.yaml.

Options:
  --config FILE       deployment declaration (default: models.yaml)
  --secret-env FILE   private owner-only environment file holding the values of
                      the credential references the declaration names
                      (for example OPENROUTER_API_KEY). Secret values never
                      appear in this script's output or arguments.
  --env-file FILE     installation environment file (default: .env.rag)
  --model-env FILE    generated model settings file (default: .env.models)
  --state-dir DIR     generated private files and the deployment lock
                      (default: build/ai-up)
  -h, --help          print this help

The installation environment file holds the generated stack secrets and keeps
every value it already has. The generated model settings live in .env.models
(override with --model-env or AI_UP_MODEL_ENV); the generated proxy
configuration and runtime credential live in the state directory (override with
--state-dir or AI_UP_STATE_DIR). Both overrides exist so a disposable or
parallel deployment never touches another installation's files.
A declaration that disables models, or that declares no route, runs this stack
FTS-only and starts no proxy.
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --config)
            [ "$#" -ge 2 ] || stack_fail "--config requires a file"
            CONFIG="$2"
            shift 2
            ;;
        --secret-env)
            [ "$#" -ge 2 ] || stack_fail "--secret-env requires a file"
            SECRET_ENV="$2"
            shift 2
            ;;
        --env-file)
            [ "$#" -ge 2 ] || stack_fail "--env-file requires a file"
            INSTALL_ENV="$2"
            shift 2
            ;;
        --model-env)
            [ "$#" -ge 2 ] || stack_fail "--model-env requires a file"
            MODEL_ENV="$2"
            shift 2
            ;;
        --state-dir)
            [ "$#" -ge 2 ] || stack_fail "--state-dir requires a directory"
            STATE_DIR="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            usage >&2
            stack_fail "unknown argument: $1"
            ;;
    esac
done

# ---------------------------------------------------------------------------
# Preconditions. Every check happens before a running deployment is touched.
# ---------------------------------------------------------------------------

command -v docker >/dev/null 2>&1 || stack_fail "docker is required"
docker compose version >/dev/null 2>&1 || stack_fail "the docker compose plugin is required"
command -v python3 >/dev/null 2>&1 || stack_fail "python3 is required"
python3 -c 'import yaml' 2>/dev/null || stack_fail "the python3 yaml module is required to read the generated proxy configuration"
command -v flock >/dev/null 2>&1 || stack_fail "flock is required for the deployment lock"

[ -f "$CONFIG" ] || stack_fail "the deployment declaration is not a file: $CONFIG"
CONFIG="$(cd "$(dirname "$CONFIG")" && pwd)/$(basename "$CONFIG")"
[ -r "$CONFIG" ] || stack_fail "the deployment declaration is not readable: $CONFIG"

if [ -n "$SECRET_ENV" ]; then
    [ -f "$SECRET_ENV" ] || stack_fail "the secret environment file is not a file: $SECRET_ENV"
    SECRET_ENV="$(cd "$(dirname "$SECRET_ENV")" && pwd)/$(basename "$SECRET_ENV")"
    mode="$(stat -c '%a' "$SECRET_ENV")"
    if [ $((8#$mode & 8#077)) -ne 0 ]; then
        stack_fail "the secret environment file must be owner-only (chmod 600): $SECRET_ENV"
    fi
fi

mkdir -p "$STATE_DIR"
chmod 700 "$STATE_DIR"
STATE_DIR="$(cd "$STATE_DIR" && pwd)"
case "$MODEL_ENV" in
    /*) ;;
    *) MODEL_ENV="$STACK_ROOT/$MODEL_ENV" ;;
esac
PROXY_CONFIG="$STATE_DIR/litellm-config.yaml"
PROXY_ENV="$STATE_DIR/proxy.env"
RUNTIME_ENV="$STATE_DIR/runtime.env"
VALIDATE_JSON="$STATE_DIR/validate.json"
PROBE_JSON="$STATE_DIR/verify.json"
STATUS_JSON="$STATE_DIR/status.json"

# One deployment at a time for this Compose project: a second invocation waits
# for no one, because a concurrent cutover could reconcile a different snapshot
# while the first is starting services. The lock is keyed on the Compose project
# name, so two checkouts deploying the same project are serialized while two
# different projects (for example a disposable one) never block each other.
STACK_VALUE_FILES=("$INSTALL_ENV" .env)
lock_project="$(stack_env_value COMPOSE_PROJECT_NAME '')"
lock_project="${lock_project:-$(basename "$STACK_ROOT")}"
lock_project="$(printf '%s' "$lock_project" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9_-')"
LOCK="$STATE_DIR/ai-up.${lock_project:-default}.lock"
exec 9>"$LOCK"
chmod 600 "$LOCK"
flock -n 9 || stack_fail "another ai-up deployment holds $LOCK for the Compose project ${lock_project:-default}"

# The CLI is the one owner of declaration parsing and proxy rendering. It is a
# static binary so the same one runs on the host and inside the private network.
build_cli() {
    if [ -x "$CLI" ] && [ -z "${DXOS_MODEL_FORCE_BUILD:-}" ] && [ "${CLI}" != "$STACK_ROOT/bin/dxos-model" ]; then
        return 0
    fi
    if command -v go >/dev/null 2>&1; then
        ( cd "$STACK_ROOT/capabilities" && CGO_ENABLED=0 go build -o "$CLI" ./cmd/dxos-model ) \
            || stack_fail "the deployment CLI could not be built"
    elif [ ! -x "$CLI" ]; then
        stack_fail "a Go toolchain is required to build $CLI"
    fi
}
build_cli

# Validation is offline and first: an invalid declaration must not replace or
# interrupt a running valid deployment.
if ! "$CLI" validate --config "$CONFIG" --json >"$VALIDATE_JSON"; then
    stack_fail "the deployment declaration is invalid; the running deployment is unchanged"
fi

# Derive the deployment facts from the validated report. The declaration is never
# re-parsed here: this reads the CLI's own report.
read -r MODELS_SELECTED LOCAL_ROUTES OLLAMA_ROUTES < <(python3 - "$VALIDATE_JSON" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
purposes = report.get("purposes") or []
declared = [p for p in purposes if p.get("declared")]
models = "1" if report.get("enabled") and declared else "0"
local = "1" if any(p.get("mode") == "LOCAL_ONLY" for p in declared) else "0"
ollama = "1" if any(p.get("provider") == "ollama" for p in declared) else "0"
print(models, local, ollama)
PY
)
[ -n "${MODELS_SELECTED:-}" ] || stack_fail "the declaration report could not be read"

# Render the proxy configuration from the same declaration. Rendering happens
# before anything starts, so a declaration that cannot render never reaches the
# proxy or the database.
if ! "$CLI" deployment render --config "$CONFIG" --out "$PROXY_CONFIG" >/dev/null; then
    stack_fail "the deployment declaration cannot render a proxy configuration"
fi
chmod 644 "$PROXY_CONFIG"

# ---------------------------------------------------------------------------
# Installation environment. Existing values are kept: this never rotates a
# running installation's secrets.
# ---------------------------------------------------------------------------

stack_env_file_path "$INSTALL_ENV"
STACK_VALUE_FILES=("$INSTALL_ENV" .env)
stack_ensure_knowledge_env "$INSTALL_ENV"
STACK_VALUE_FILES=("$INSTALL_ENV" .env)

# A caller's exported capability list wins; otherwise use the installation's own.
ENABLED_CAPABILITIES="$(stack_env_value ENABLED_CAPABILITIES 'cap-process,cap-hr,cap-work,cap-data,cap-ai')"
export ENABLED_CAPABILITIES

stack_env_file_path "$MODEL_ENV"
# The deployment owns these switches. They are rewritten on every run so a
# corrected declaration takes effect and a disabled one cannot leave an old
# model selected.
stack_env_set "$MODEL_ENV" RAG_MODELS_ENABLED "$([ "$MODELS_SELECTED" = 1 ] && echo true || echo false)"
# The model schema (pgvector and the cap_ai model tables) belongs to a
# declaration that selects models. A disabled or route-less declaration must not
# install it, so the switches follow the declaration instead of being hard-coded
# on: initializing a new volume with this deployment installs no model schema.
# A volume that already carries the schema is left exactly as it is (nothing is
# dropped); `rag-up` remains the path that never installs it at all.
for schema_switch in \
    RAG_MODELS_SCHEMA_ENABLED \
    RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED \
    RAG_AGENT_MODELS_SCHEMA_ENABLED
do
    stack_env_set "$MODEL_ENV" "$schema_switch" "$([ "$MODELS_SELECTED" = 1 ] && echo true || echo false)"
done
stack_env_set "$MODEL_ENV" RAG_LOCAL_MODELS_QUALIFIED "$([ "$LOCAL_ROUTES" = 1 ] && echo true || echo false)"
stack_env_set "$MODEL_ENV" RAG_MODEL_CATALOG_FILE "$CONFIG"
stack_env_set "$MODEL_ENV" RAG_MODEL_CREDENTIAL_ENV_FILE "$RUNTIME_ENV"
stack_env_set "$MODEL_ENV" RAG_MODEL_LITELLM_CONFIG_FILE "$PROXY_CONFIG"
stack_env_set "$MODEL_ENV" RAG_MODEL_LITELLM_SECRET_ENV_FILE "$PROXY_ENV"
stack_env_set "$MODEL_ENV" RAG_MODEL_LITELLM_CREDENTIAL_ENV_FILE "$RUNTIME_ENV"
stack_env_set "$MODEL_ENV" RAG_MODEL_LITELLM_ENDPOINT "http://litellm:4000"
# No agent processing until the personal library is ready, as in the FTS path.
stack_env_ensure "$MODEL_ENV" RAG_AGENT_ENABLED false
stack_env_ensure "$MODEL_ENV" AI_AGENT_PROCESSING_APP_ID ""

# ---------------------------------------------------------------------------
# Private generated credentials. Only values the declaration references are
# copied, and no value is ever printed.
# ---------------------------------------------------------------------------

master_key=""
if [ -n "$SECRET_ENV" ]; then
    if grep -q '^LITELLM_MASTER_KEY=' "$SECRET_ENV"; then
        master_key="$(grep -m1 '^LITELLM_MASTER_KEY=' "$SECRET_ENV" | cut -d= -f2-)"
        # An empty assignment is the operator naming the credential and leaving it
        # blank. Generating a replacement here would silently rotate a proxy
        # credential the operator believes they control, so it is refused with the
        # two ways forward instead.
        if [ -z "$master_key" ]; then
            stack_fail "LITELLM_MASTER_KEY is present but empty in the secret environment file: set it to at least 24 characters, or remove the line to let this deployment generate one"
        fi
    fi
fi
if [ -z "$master_key" ]; then
    stack_env_ensure "$INSTALL_ENV" LITELLM_MASTER_KEY "$(stack_random_secret)"
    master_key="$(grep -m1 '^LITELLM_MASTER_KEY=' "$INSTALL_ENV" | cut -d= -f2-)"
    stack_env_set "$INSTALL_ENV" LITELLM_MASTER_KEY "$master_key"
fi
case "$master_key" in
    *$'\n'*|'') stack_fail "the proxy credential is empty or malformed" ;;
esac
if [ "${#master_key}" -lt 24 ]; then
    stack_fail "the proxy credential is shorter than 24 characters"
fi

umask 077
printf 'LITELLM_MASTER_KEY=%s\n' "$master_key" >"$RUNTIME_ENV"
printf 'LITELLM_MASTER_KEY=%s\n' "$master_key" >"$PROXY_ENV"
chmod 600 "$RUNTIME_ENV" "$PROXY_ENV"

# The generated proxy configuration names every upstream credential by reference.
# Each referenced value comes from the operator's private environment file; an
# absent one is named (never its value) and the deployment stops before starting.
provider_refs="$(python3 - "$PROXY_CONFIG" <<'PY'
import sys, yaml
document = yaml.safe_load(open(sys.argv[1], encoding="utf-8")) or {}
names = set()
for entry in document.get("model_list") or []:
    reference = (entry.get("litellm_params") or {}).get("api_key")
    if isinstance(reference, str) and reference.startswith("os.environ/"):
        names.add(reference[len("os.environ/"):])
print("\n".join(sorted(names)))
PY
)"
if [ -n "$provider_refs" ]; then
    while IFS= read -r name; do
        [ -n "$name" ] || continue
        [ -n "$SECRET_ENV" ] || stack_fail "the declaration references $name; pass --secret-env with its value"
        line="$(grep -m1 "^${name}=" "$SECRET_ENV" || true)"
        [ -n "$line" ] || stack_fail "the declaration references $name; it is absent from the secret environment file"
        value="${line#*=}"
        case "$value" in
            *$'\n'*|'') stack_fail "the secret environment file has an empty or malformed $name" ;;
        esac
        printf '%s=%s\n' "$name" "$value" >>"$PROXY_ENV"
    done <<<"$provider_refs"
fi

# Compose hashes a file secret by its mount name, not by the source path or its
# contents: a changed declaration, a re-rendered proxy configuration or a rotated
# credential leaves the service configuration hash identical, so `up` would keep
# the containers and their stale mount. The runtime refuses a cache of the wrong
# snapshot (it verifies the mounted proxy configuration), which is why a
# deployment that changed any of these inputs must recreate the model-calling
# services explicitly. The digest is recorded only after a deployment that ended
# on the verified snapshot, so an interrupted or failed cutover always recreates
# on the next run.
runtime_digest="$(cat "$CONFIG" "$PROXY_CONFIG" "$PROXY_ENV" "$RUNTIME_ENV" | sha256sum | cut -d' ' -f1)"
recreate_runtime=0
if [ ! -f "$STATE_DIR/runtime.digest" ] || [ "$(cat "$STATE_DIR/runtime.digest")" != "$runtime_digest" ]; then
    recreate_runtime=1
fi

# ---------------------------------------------------------------------------
# Compose project. The existing project and environment are reused; an explicit
# COMPOSE_FILE is honoured so a documented disposable stack (different project,
# ports and volumes) can run the same deployment path.
# ---------------------------------------------------------------------------

if [ -n "${COMPOSE_FILE:-}" ]; then
    IFS=':' read -r -a STACK_COMPOSE_FILES <<<"$COMPOSE_FILE"
else
    STACK_COMPOSE_FILES=(compose.yaml compose.rag.yaml)
    if [ "$MODELS_SELECTED" = 1 ]; then
        STACK_COMPOSE_FILES+=(compose.models.yaml compose.litellm.yaml)
    fi
    if [ "$MODELS_SELECTED" = 1 ] && [ "$OLLAMA_ROUTES" = 1 ]; then
        STACK_COMPOSE_FILES+=(compose.ollama.yaml)
    fi
fi

stack_compose_init .env "$INSTALL_ENV" "$MODEL_ENV"
trap stack_cleanup EXIT

compose_services="$(stack_compose config --services 2>/dev/null || true)"
[ -n "$compose_services" ] || stack_fail "the Compose project does not render with this declaration"
for required in postgres cap-ai cap-ai-worker; do
    grep -qx "$required" <<<"$compose_services" || stack_fail "the Compose project has no $required service"
done
if [ "$MODELS_SELECTED" = 1 ] && ! grep -qx litellm <<<"$compose_services"; then
    stack_fail "the declaration selects models but the Compose project has no litellm service"
fi
if [ "$OLLAMA_ROUTES" = 1 ] && [ "$MODELS_SELECTED" = 1 ] && ! grep -qx ollama <<<"$compose_services"; then
    stack_fail "the declaration selects a local Ollama route but the Compose project has no ollama service"
fi

# ---------------------------------------------------------------------------
# Helpers that run the deployment CLI against the private network. The catalog
# login is the restricted deployment authority; the runtime roles are never used
# for a deployment decision.
# ---------------------------------------------------------------------------

catalog_dsn() {
    # The deployment authority is the restricted catalog login, never the
    # bootstrap/superuser login and never a runtime role.
    python3 - "dxos_ai_model_catalog" \
        "$(stack_env_value AI_MODEL_CATALOG_DATABASE_PASSWORD '')" \
        "$(stack_env_value POSTGRES_DB dxos_db)" <<'PY'
import sys
from urllib.parse import quote
user, password, database = sys.argv[1:4]
print("postgres://%s:%s@postgres:5432/%s?sslmode=disable" % (quote(user, safe=""), quote(password, safe=""), quote(database, safe="")))
PY
}

model_cli() {
    local service="$1" with_db="$2"
    shift 2
    local arguments=(
        run --rm --no-deps -T
        --entrypoint "$CLI_TARGET"
        -v "$CLI:$CLI_TARGET:ro"
    )
    if [ "$with_db" = 1 ]; then
        arguments+=(-e "AI_MODEL_CATALOG_DATABASE_URL=$(catalog_dsn)")
    fi
    stack_compose "${arguments[@]}" "$service" "$@"
}

running_services() {
    stack_compose ps --status running -q cap-ai cap-ai-worker 2>/dev/null || true
}

# Degrade to lexical search instead of leaving mismatched services dispatching:
# the model switch is turned off, the model-calling services are recreated with
# that snapshot, and the command still reports failure.
degrade_and_fail() {
    local message="$1"
    printf '[ai-up] FAIL: %s\n' "$message" >&2
    stack_env_set "$MODEL_ENV" RAG_MODELS_ENABLED false
    stack_compose stop -t 20 cap-ai cap-ai-worker >/dev/null 2>&1 || true
    if stack_compose up -d --no-deps cap-ai cap-ai-worker >/dev/null 2>&1; then
        printf '[ai-up] the API and worker run with model calls off; authorized lexical search stays available\n' >&2
    else
        printf '[ai-up] the API and worker are stopped; correct the declaration and rerun\n' >&2
    fi
    exit 1
}

on_signal() {
    printf '\n[ai-up] interrupted; no partial cutover continues. Rerun the same command.\n' >&2
    exit 130
}
trap on_signal INT TERM

# ---------------------------------------------------------------------------
# Database bootstrap and the optional model/pgvector schema.
# ---------------------------------------------------------------------------

stack_say "starting PostgreSQL"
stack_compose up -d postgres
stack_wait_healthy postgres 300
stack_wait_postgres 300
stack_require_cap_ai

# The optional model/pgvector schema is provisioned only for a declaration that
# selects models. A disabled or route-less declaration runs on the plain FTS
# schema: it installs no pgvector extension and no cap_ai model table, exactly as
# `rag-up.sh` does, whatever the Compose file list happens to contain.
model_schema_present="$(stack_psql_value "SELECT count(*) FROM information_schema.tables WHERE table_schema='cap_ai' AND table_name='model_profiles'")"
[ "$model_schema_present" = "1" ] || model_schema_present=0
if [ "$MODELS_SELECTED" = 1 ]; then
    vector_extension="$(stack_psql_value "SELECT count(*) FROM pg_extension WHERE extname='vector'")"
    model_schema="$model_schema_present"
    if [ "$vector_extension" != "1" ] || [ "$model_schema" != "1" ]; then
        stack_say "provisioning the model/pgvector schema through the migration authority"
        migration_dsn="$(python3 - \
            "$(stack_env_value BIND_ADDRESS 127.0.0.1)" \
            "$(stack_env_value POSTGRES_PORT 5432)" \
            "$(stack_env_value POSTGRES_USER dxos_admin)" \
            "$(stack_env_value POSTGRES_PASSWORD dxos_secret)" \
            "$(stack_env_value POSTGRES_DB dxos_db)" <<'PY'
import sys
from urllib.parse import quote
host, port, user, password, database = sys.argv[1:6]
print("postgres://%s:%s@%s:%s/%s?sslmode=disable" % (quote(user, safe=""), quote(password, safe=""), host, port, quote(database, safe="")))
PY
)"
        if ! AI_UP_MIGRATION_DATABASE_URL="$migration_dsn" \
            RAG_MODELS_SCHEMA_ENABLED=true \
            RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true \
            RAG_AGENT_MODELS_SCHEMA_ENABLED=true \
            "$STACK_ROOT/scripts/migrate-rag.sh" --database-url-env AI_UP_MIGRATION_DATABASE_URL --scope cap-ai --through latest; then
            stack_fail "the model schema could not be provisioned; the existing database and its checksums are unchanged"
        fi
        vector_extension="$(stack_psql_value "SELECT count(*) FROM pg_extension WHERE extname='vector'")"
        model_schema="$(stack_psql_value "SELECT count(*) FROM information_schema.tables WHERE table_schema='cap_ai' AND table_name='model_profiles'")"
        if [ "$vector_extension" != "1" ] || [ "$model_schema" != "1" ]; then
            stack_fail "this database volume was not initialized for models and cannot be repaired in place; keep the data and follow docs/operations/installation.md#rebuilding-an-existing-model-installation"
        fi
    fi
    # The restricted deployment logins cannot log in without a password on a
    # Compose network. They are set by the bootstrap migration authority; neither
    # is a runtime role, and neither is a globally privileged application login.
    stack_role_exists dxos_ai_gateway || stack_fail "the model schema has no dxos_ai_gateway login"
    stack_role_exists dxos_ai_model_catalog || stack_fail "the model schema has no dxos_ai_model_catalog login"
    stack_set_role_password dxos_ai_gateway "$(stack_env_value AI_GATEWAY_DATABASE_PASSWORD '')"
    stack_set_role_password dxos_ai_model_catalog "$(stack_env_value AI_MODEL_CATALOG_DATABASE_PASSWORD '')"
fi

# ---------------------------------------------------------------------------
# Proxy and optional local runtime. A declaration with no route starts neither.
# ---------------------------------------------------------------------------

if [ "$MODELS_SELECTED" = 1 ]; then
    stack_say "building the model-calling runtime"
    stack_compose build cap-ai
    if [ "$OLLAMA_ROUTES" = 1 ]; then
        [ -n "$(stack_env_value RAG_MODEL_OLLAMA_IMAGE '')" ] \
            || stack_fail "the declaration selects Ollama; set RAG_MODEL_OLLAMA_IMAGE to the pinned image reference"
    fi
    stack_say "starting the private model proxy"
    # A rotated upstream credential or a changed generated configuration is not
    # a Compose configuration change, so the proxy is recreated when the
    # deployment's private files differ from the last run.
    proxy_digest="$(cat "$PROXY_CONFIG" "$PROXY_ENV" | sha256sum | cut -d' ' -f1)"
    if [ -f "$STATE_DIR/proxy.digest" ] && [ "$(cat "$STATE_DIR/proxy.digest")" != "$proxy_digest" ]; then
        stack_compose up -d --force-recreate litellm
    else
        stack_compose up -d litellm
    fi
    printf '%s\n' "$proxy_digest" >"$STATE_DIR/proxy.digest"
    chmod 600 "$STATE_DIR/proxy.digest"
    stack_wait_healthy litellm 240
    if [ "$OLLAMA_ROUTES" = 1 ]; then
        stack_say "starting the operator-managed local runtime"
        stack_compose up -d ollama
        stack_wait_healthy ollama 240
    fi
fi

# ---------------------------------------------------------------------------
# Probe every configured route with the fixed synthetic input. Each purpose is
# independent: the measurement reports all routes in one run, and nothing is
# retried or resent.
# ---------------------------------------------------------------------------

qualified_embed=0
qualified_llm=0
probe_reason_embed=""
probe_reason_llm=""
if [ "$MODELS_SELECTED" = 1 ]; then
    stack_say "probing the configured routes with synthetic input"
    probe_status=0
    model_cli cap-ai 0 verify --config /run/secrets/model-catalog --json >"$PROBE_JSON" 2>"$STATE_DIR/verify.err" || probe_status=$?
    if [ -s "$PROBE_JSON" ]; then
        read -r qualified_embed qualified_llm probe_reason_embed probe_reason_llm < <(python3 - "$PROBE_JSON" <<'PY'
import json, sys
try:
    reports = json.load(open(sys.argv[1]))
except (ValueError, OSError):
    reports = []
passed = {"EMBED": 0, "GENERATE": 0}
reason = {"EMBED": "not reported", "GENERATE": "not reported"}
for report in reports if isinstance(reports, list) else []:
    purpose = report.get("purpose")
    if purpose in passed:
        passed[purpose] = 1 if report.get("passed") else 0
        reason[purpose] = (report.get("reason") or "").replace(" ", "-")
print(passed["EMBED"], passed["GENERATE"], reason["EMBED"] or "-", reason["GENERATE"] or "-")
PY
)
    else
        qualified_embed=0
        qualified_llm=0
        probe_reason_embed="probe-failed"
        probe_reason_llm="probe-failed"
    fi
    if [ "$probe_status" -ne 0 ]; then
        stack_say "one or more configured routes did not pass the synthetic probe"
    fi
fi

# Only purposes the probe qualified are reconciled. A failed purpose is withdrawn
# in the same transaction rather than left describing an unverified route.
disabled_purposes=()
declared_embed=0
declared_llm=0
expected_embed=0
expected_llm=0
if [ "$MODELS_SELECTED" = 1 ]; then
    read -r declared_embed declared_llm < <(python3 - "$VALIDATE_JSON" <<'PY'
import json, sys
report = json.load(open(sys.argv[1]))
declared = {p.get("purpose") for p in report.get("purposes") or [] if p.get("declared")}
print("1" if "EMBED" in declared else "0", "1" if "GENERATE" in declared else "0")
PY
)
    if [ "$declared_embed" = 1 ]; then
        if [ "$qualified_embed" = 1 ]; then expected_embed=1; else disabled_purposes+=(--disable embed); fi
    fi
    if [ "$declared_llm" = 1 ]; then
        if [ "$qualified_llm" = 1 ]; then expected_llm=1; else disabled_purposes+=(--disable llm); fi
    fi
fi

# ---------------------------------------------------------------------------
# Coordinated cutover: stop the model-calling services before the selection
# changes, reconcile once, then start them on that snapshot.
# ---------------------------------------------------------------------------

# There is a selection to move only when this run selects a model or the volume
# already carries the model schema (a previous deployment selected something that
# this declaration may withdraw). A models-disabled declaration on a volume
# without the schema has no model state at all: it reconciles nothing, exactly as
# `rag-up.sh` does, and the catalog login it would need does not exist.
reconcile_selection=0
if [ "$MODELS_SELECTED" = 1 ] || [ "$model_schema_present" = 1 ]; then
    reconcile_selection=1
fi

selected_before=0
if [ "$reconcile_selection" = 1 ] && model_cli cap-ai 1 status --config /run/secrets/model-catalog --json >"$STATUS_JSON" 2>/dev/null; then
    # The decision (declared-and-matched, and nothing selected that this run does
    # not qualify to keep) is one small pure function so it can be tested without
    # a Compose project: scripts/lib/ai_up_snapshot.py.
    read -r selected_before < <(python3 "$STACK_ROOT/scripts/lib/ai_up_snapshot.py" \
        "$STATUS_JSON" "$VALIDATE_JSON" "$expected_embed" "$expected_llm")
fi
[ -n "${selected_before:-}" ] || selected_before=0

if [ "$(running_services)" != "" ]; then
    if [ "$selected_before" != 1 ]; then
        stack_say "stopping the model-calling API and worker before the snapshot changes"
        stack_compose stop -t 20 cap-ai cap-ai-worker >/dev/null 2>&1 || true
    fi
fi

# The old worker is stopped before this point; its lease can no longer be
# renewed, and the first claim of the new runtime advances the fencing token, so
# a stale worker cannot write after the cutover. No job is deleted or requeued.
if [ "$(running_services)" != "" ] && [ "$selected_before" != 1 ]; then
    deadline=$((SECONDS + 60))
    while [ "$(running_services)" != "" ]; do
        if [ "$SECONDS" -ge "$deadline" ]; then
            stack_fail "the previous API and worker did not stop; nothing was reconciled"
        fi
        sleep 2
    done
fi

zero_selected=0
[ "$MODELS_SELECTED" = 1 ] || zero_selected=1
[ "$expected_embed" = 1 ] || [ "$expected_llm" = 1 ] || zero_selected=1
if [ "$reconcile_selection" = 1 ]; then
    stack_say "reconciling the qualified deployment selection"
    if ! model_cli cap-ai 1 deployment reconcile --config /run/secrets/model-catalog --json "${disabled_purposes[@]}" >"$STATE_DIR/reconcile.json" 2>"$STATE_DIR/reconcile.err"; then
        degrade_and_fail "the deployment selection could not be reconciled; no mismatched service was started"
    fi
fi
if [ "$zero_selected" = 1 ]; then
    stack_say "no model route is selected; the installation serves authorized lexical search only"
fi

# ---------------------------------------------------------------------------
# Start the one verified snapshot on both services and check the identity the
# runtime actually bound against the database.
# ---------------------------------------------------------------------------

stack_say "starting the knowledge stack on the verified snapshot"
# Everything from here to the identity check runs on the failure path too: a
# service that cannot start, a service client that cannot be enabled or an app
# that never becomes Ready after the selection moved must not leave the API and
# worker running on a snapshot nobody verified. The helpers are called in a
# subshell where they call `stack_fail` (which exits), so their failure becomes a
# plain status this script can degrade on instead of an immediate exit.
if ! ( stack_compose up -d --build keycloak ); then
    degrade_and_fail "Keycloak could not be started after the cutover; no service was left dispatching to an unverified route"
fi
if ! ( stack_configure_service_clients ); then
    degrade_and_fail "the knowledge service clients could not be enabled after the cutover; no service was left dispatching to an unverified route"
fi
if [ "$recreate_runtime" = 1 ] && [ "$MODELS_SELECTED" = 1 ]; then
    # The private inputs changed, which Compose cannot see: recreate only the two
    # model-calling services so they mount the declaration and generated
    # configuration this run verified. Everything else keeps running.
    stack_say "recreating the model-calling services with the current private inputs"
    if ! ( stack_compose up -d --build --no-deps --force-recreate cap-ai cap-ai-worker >/dev/null ); then
        degrade_and_fail "the model-calling services could not be recreated with the current private inputs"
    fi
fi
if ! ( stack_compose up -d --build ); then
    degrade_and_fail "the knowledge stack could not be started on the verified snapshot"
fi
if ! ( stack_wait_app personal_library 300 ); then
    degrade_and_fail "the personal library did not become ready on the verified snapshot"
fi
stack_env_set "$MODEL_ENV" AI_AGENT_PROCESSING_APP_ID personal_library
stack_env_set "$MODEL_ENV" RAG_AGENT_ENABLED true
if ! ( stack_compose up -d ); then
    degrade_and_fail "the agent processing setting could not be applied on the verified snapshot"
fi

identity_failure=""
if [ "$MODELS_SELECTED" = 1 ] && { [ "$expected_embed" = 1 ] || [ "$expected_llm" = 1 ]; }; then
    if ! model_cli cap-ai 1 status --config /run/secrets/model-catalog --json >"$STATUS_JSON" 2>/dev/null; then
        identity_failure="the deployment status could not be read after startup"
    else
        read -r identity_failure < <(python3 - "$STATUS_JSON" "$VALIDATE_JSON" "$expected_embed" "$expected_llm" <<'PY'
import json, sys
status = json.load(open(sys.argv[1]))
declaration = json.load(open(sys.argv[2]))
expected = {"EMBED": sys.argv[3] == "1", "GENERATE": sys.argv[4] == "1"}
declared = {p.get("purpose"): p for p in declaration.get("purposes") or [] if p.get("declared")}
selected = {p.get("purpose"): p for p in status.get("purposes") or [] if p.get("qualified")}
problems = []
for purpose, wanted in expected.items():
    if not wanted:
        continue
    profile = declared.get(purpose) or {}
    active = selected.get(purpose) or {}
    if not active:
        problems.append(purpose.lower() + "-not-selected")
    elif active.get("profile_id") != profile.get("profile_id") or active.get("revision") != profile.get("revision"):
        problems.append(purpose.lower() + "-identity-mismatch")
print(",".join(problems) if problems else "")
PY
)
    fi
fi

if [ -z "$identity_failure" ] && [ -n "$(running_services)" ]; then
    if [ "$expected_embed" = 1 ] && stack_compose logs --no-log-prefix --since 10m cap-ai-worker 2>/dev/null | grep -q "embedding model unavailable"; then
        identity_failure="worker-embedding-unavailable"
    fi
    if { [ "$expected_embed" = 1 ] || [ "$expected_llm" = 1 ]; } && \
        stack_compose logs --no-log-prefix --since 10m cap-ai 2>/dev/null | grep -q "model calls unavailable"; then
        identity_failure="api-model-unavailable"
    fi
fi

if [ -n "$identity_failure" ]; then
    degrade_and_fail "the started runtime does not hold the verified snapshot ($identity_failure)"
fi

# The deployment ended on the verified snapshot, so this is the state the private
# inputs describe. Recording it here rather than before the start is what makes a
# rerun of an unchanged declaration restart nothing while a corrected declaration
# (or a cutover interrupted before this point) always recreates the services.
printf '%s\n' "$runtime_digest" >"$STATE_DIR/runtime.digest"
chmod 600 "$STATE_DIR/runtime.digest"

# ---------------------------------------------------------------------------
# Report. A route the probe did not qualify is a visible failure even while the
# installation keeps serving lexical search; a corrected declaration can be
# deployed by rerunning this command.
# ---------------------------------------------------------------------------

if [ "$MODELS_SELECTED" = 1 ]; then
    if [ "$expected_embed" = 1 ]; then
        stack_say "embedding route verified and selected"
    elif [ "$declared_embed" = 1 ]; then
        stack_say "embedding route unavailable (${probe_reason_embed:-failed}); lexical search stays available"
    fi
    if [ "$expected_llm" = 1 ]; then
        stack_say "generation route verified and selected"
    elif [ "$declared_llm" = 1 ]; then
        stack_say "generation route unavailable (${probe_reason_llm:-failed}); search-only answers stay available"
    fi
    if model_cli cap-ai 1 status --config /run/secrets/model-catalog --json >"$STATUS_JSON" 2>/dev/null; then
        python3 - "$STATUS_JSON" <<'PY'
import json, sys
status = json.load(open(sys.argv[1]))
index = status.get("index") or {}
print("[ai-up] embedding jobs pending=%s leased=%s failed=%s; embeddings ready=%s building=%s failed=%s"
      % (index.get("embedding_jobs_pending", 0), index.get("embedding_jobs_leased", 0),
         index.get("embedding_jobs_failed", 0), index.get("embeddings_ready", 0),
         index.get("embeddings_building", 0), index.get("embeddings_failed", 0)))
PY
    fi
fi

cat <<EOF
[ai-up] UI: http://localhost:$(stack_env_value WORKSPACE_UI_PORT 5173)
[ai-up] declaration: $(basename "$CONFIG")
EOF

failed_declared=0
if [ "$declared_embed" = 1 ] && [ "$expected_embed" != 1 ]; then failed_declared=1; fi
if [ "$declared_llm" = 1 ] && [ "$expected_llm" != 1 ]; then failed_declared=1; fi
if [ "$failed_declared" = 1 ]; then
    stack_fail "a requested model capability is unavailable; no alternate model was selected and authorized lexical search remains available"
fi
stack_say "deployment complete"
