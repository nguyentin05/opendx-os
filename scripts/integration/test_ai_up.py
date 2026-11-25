#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Coordinated deployment tests for ``make ai-up`` and the FTS-only path.

These cases drive the real ``scripts/ai-up.sh`` and ``scripts/rag-up.sh`` on a
disposable Compose project with the real pinned proxy, the real PostgreSQL/
pgvector schema and a synthetic upstream. They assert the deployment contract:

* a second deployment of the same declaration creates no profile, generation or
  job and restarts no service;
* an LLM-only change leaves the embedding identity and every completed vector
  untouched;
* an embedding change creates a new profile, keeps old generations isolated, and
  re-embeds the same parsed run without a reparse;
* an interrupted cutover leaves the model-calling services stopped and a rerun
  completes safely;
* a purpose the runtime holds but this run does not qualify is withdrawn only
  after the API and worker are drained;
* a failure after the cutover degrades to lexical search and exits non-zero;
* an unavailable route is reported, is not replaced by an alternate model, and
  lexical search keeps answering;
* a disabled declaration starts no proxy, makes no model call and installs no
  model schema, even on a fresh volume;
* ``rag-up`` boots an FTS-only installation without the model/pgvector schema.

Scope: these cases pin the *deployment* contract on a stack with no parsed
documents. A deployment must leave every parsed run, chunk, job and vector
untouched; the generation and vector counts it compares are therefore
deployment-only invariants (see the comment in ``test_ai_up_embedding_change``).
Vector-level generation, generation isolation and fence behaviour need real
chunks and are pinned against real PostgreSQL 17/pgvector by
``TestWorkerDeploymentReembed`` and ``TestWorkerRejectsObsoleteLease`` in
``capabilities/internal/ai/knowledge``.

State isolation: the suite never uses the checkout's ``.env.models``,
``build/ai-up`` or its lock. Every deployment runs with ``AI_UP_STATE_DIR``,
``AI_UP_MODEL_ENV`` and ``AI_UP_LOCK_DIR`` pointing into this run's temporary
directory, and ``test_suite_is_hermetic`` asserts that the checkout paths are
byte-for-byte unchanged afterwards.

Prerequisites (a missing one is NOT_RUN, never a pass): Docker with the Compose
plugin, the stack images, and ``TEST_DISPOSABLE_DATABASE=yes`` with a project
name other than ``opendx-os``. The suite never prints a rendered secret.
"""

from __future__ import annotations

import json
import os
import secrets
import shutil
import subprocess
import tempfile
import threading
import time
import unittest
from importlib import util as importlib_util
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "ai-up.sh"
RAG_SCRIPT = ROOT / "scripts" / "rag-up.sh"
FAKE_SCRIPT = ROOT / "scripts" / "integration" / "fixtures" / "ai_up_fake.py"
FAKE_OVERLAY = ROOT / "scripts" / "integration" / "fixtures" / "ai-up-fake.yaml"
# The cutover decision is a pure function so its rule is pinned without Docker.
# AI_UP_SNAPSHOT_MODULE exists to load the previous revision of the rule when
# recording RED; the suite uses the shipped file by default.
SNAPSHOT_MODULE = Path(os.environ.get(
    "AI_UP_SNAPSHOT_MODULE", str(ROOT / "scripts" / "lib" / "ai_up_snapshot.py")))


def _load_snapshot_module():
    spec = importlib_util.spec_from_file_location("ai_up_snapshot", SNAPSHOT_MODULE)
    if spec is None or spec.loader is None:
        raise RuntimeError("cannot load the cutover rule from " + str(SNAPSHOT_MODULE))
    module = importlib_util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


ai_up_snapshot = _load_snapshot_module()

# Nothing in this suite may touch these: they belong to the operator's live
# installation and a test run must not rewrite or lock them.
CHECKOUT_PRIVATE_PATHS = (ROOT / ".env.models", ROOT / "build" / "ai-up")

# Every run gets its own Compose project. A PostgreSQL data volume is initialized
# once, with the runtime role passwords generated into that run's installation
# environment file; reusing a volume across runs would leave the roles holding a
# previous run's passwords and make every runtime login fail. The project name is
# also the guard for teardown: only a disposable project this suite created is
# ever removed, and never the development project.
RUN_TOKEN = os.environ.get("AI_UP_TEST_TOKEN") or secrets.token_hex(3)
MODEL_PROJECT = os.environ.get("AI_UP_TEST_PROJECT", "opendx-aiup-test-" + RUN_TOKEN)
FTS_PROJECT = os.environ.get("AI_UP_TEST_FTS_PROJECT", "opendx-aiup-fts-" + RUN_TOKEN)
OFF_PROJECT = os.environ.get("AI_UP_TEST_OFF_PROJECT", "opendx-aiup-off-" + RUN_TOKEN)
DISPOSABLE_PROJECT_PREFIX = "opendx-aiup"
DISPOSABLE = os.environ.get("TEST_DISPOSABLE_DATABASE") == "yes"


def declaration(embed_revision: str = "embed-r1", llm_revision: str = "llm-r1",
                embed_dimension: int = 8, enabled: bool = True, llm: bool = True) -> str:
    """One reviewed declaration pointing at the synthetic local endpoint."""
    lines = [f"enabled: {'true' if enabled else 'false'}"]
    if enabled:
        lines += [
            "embedding:",
            "  model: hosted_vllm/fake-embed-v1",
            "  execution: local",
            "  api_base: http://model-route-fake:8081/v1",
            f"  revision: {embed_revision}",
            f"  dimension: {embed_dimension}",
            "  limits: {max_input_tokens: 4096, max_output_tokens: 1, max_bytes: 65536, timeout_ms: 20000}",
        ]
        if llm:
            lines += [
                "llm:",
                "  model: hosted_vllm/fake-llm-v1",
                "  execution: local",
                "  api_base: http://model-route-fake:8081/v1",
                f"  revision: {llm_revision}",
                "  limits: {max_input_tokens: 4096, max_output_tokens: 256, max_bytes: 65536, timeout_ms: 20000}",
            ]
    return "\n".join(lines) + "\n"


def port_map(offset: int) -> dict[str, str]:
    return {
        "POSTGRES_PORT": str(15532 + offset),
        "HASURA_PORT": str(28080 + offset),
        "KEYCLOAK_PORT": str(28085 + offset),
        "CAP_PROCESS_PORT": str(28091 + offset * 10),
        "APISIX_GATEWAY_PORT": str(29080 + offset * 10),
        "WORKSPACE_UI_PORT": str(25173 + offset * 10),
        "KESTRA_PORT": str(28082 + offset * 10),
        "SEAWEEDFS_S3_PORT": str(28333 + offset),
        "CLAMAV_PORT": str(23310 + offset),
    }


def docker_available() -> bool:
    if shutil.which("docker") is None:
        return False
    probe = subprocess.run(["docker", "compose", "version"], capture_output=True, text=True)
    return probe.returncode == 0


def checkout_state() -> dict[str, str]:
    """Fingerprint the checkout paths a deployment run must never touch."""
    state = {}
    for path in CHECKOUT_PRIVATE_PATHS:
        if not path.exists():
            state[str(path)] = "absent"
        elif path.is_dir():
            state[str(path)] = "dir:" + ",".join(sorted(entry.name for entry in path.rglob("*")))
        else:
            stat = path.stat()
            state[str(path)] = "file:%d:%d" % (stat.st_size, stat.st_mtime_ns)
    return state


class DeploymentHarness:
    """One disposable Compose project and the private files a deployment needs."""

    def __init__(self, project: str, workspace: Path, ports: dict[str, str], model: bool) -> None:
        self.project = project
        self.workspace = workspace
        self.ports = ports
        self.model = model
        self.state = workspace / "fake-state"
        self.state.mkdir(exist_ok=True)
        os.chmod(self.state, 0o777)
        (self.state / "mode").write_text("normal\n", encoding="utf-8")
        self.env_file = workspace / ".env.installation"
        # The deployment's own generated files and project locks stay in this
        # run's directory: the checkout's .env.models, build/ai-up and its lock
        # are never opened.
        self.model_env = workspace / ".env.models"
        self.state_dir = workspace / "ai-up-state"
        self.env = {
            **os.environ,
            "COMPOSE_PROJECT_NAME": project,
            "POSTGRES_DB": "opendx_aiup",
            "POSTGRES_USER": "aiup_admin",
            "POSTGRES_PASSWORD": "aiup-" + project + "-disposable",
            "BIND_ADDRESS": "127.0.0.1",
            "OIDC_ISSUER_URL": f"http://localhost:{ports['KEYCLOAK_PORT']}/realms/dxos",
            "AI_UP_STATE_DIR": str(self.state_dir),
            "AI_UP_MODEL_ENV": str(self.model_env),
            "AI_UP_LOCK_DIR": str(workspace / "locks"),
            **ports,
        }
        if model:
            self.env.update({
                "COMPOSE_FILE": ":".join((
                    "compose.yaml", "compose.rag.yaml", "compose.models.yaml", "compose.litellm.yaml",
                    str(FAKE_OVERLAY.relative_to(ROOT)))),
                "AI_UP_FAKE_SCRIPT": str(FAKE_SCRIPT),
                "AI_UP_FAKE_STATE": str(self.state),
                "AI_UP_FAKE_DIMENSION": "8",
                "AI_UP_FAKE_USER": "%d:%d" % (os.getuid(), os.getgid()),
            })
        else:
            self.env["COMPOSE_FILE"] = ":".join(("compose.yaml", "compose.rag.yaml"))
        self.env.pop("COMPOSE_PROJECT_NAME_OVERRIDE", None)

    # -- process helpers -------------------------------------------------

    def run_script(self, script: Path, config: Path | None = None, timeout: int = 3600,
                   extra: tuple[str, ...] = (),
                   extra_env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
        arguments = ["bash", str(script)]
        if config is not None:
            arguments += ["--config", str(config)]
        arguments += ["--env-file", str(self.env_file), *extra]
        env = self.env if extra_env is None else {**self.env, **extra_env}
        return subprocess.run(arguments, cwd=ROOT, env=env, capture_output=True, text=True, timeout=timeout)

    def deploy(self, config: Path, timeout: int = 3600) -> subprocess.CompletedProcess:
        result = self.run_script(SCRIPT, config, timeout)
        self.assert_no_secrets(result.stdout + result.stderr)
        return result

    def assert_no_secrets(self, output: str) -> None:
        """The deployment output must never carry a generated secret value."""
        for path in (self.env_file, self.state_dir / "proxy.env", self.state_dir / "runtime.env"):
            if not path.exists():
                continue
            for line in path.read_text(encoding="utf-8").splitlines():
                name, _, value = line.partition("=")
                if not value or len(value) < 16:
                    continue
                if not any(marker in name for marker in ("PASSWORD", "SECRET", "TOKEN", "MASTER_KEY")):
                    continue
                if value in output:
                    raise AssertionError("the deployment output carried the value of " + name)

    def spawn(self, config: Path) -> subprocess.Popen:
        return subprocess.Popen(
            ["bash", str(SCRIPT), "--config", str(config), "--env-file", str(self.env_file)],
            cwd=ROOT, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1)

    def fault_injection(self, needle: str) -> dict[str, str]:
        """Environment that fails exactly the Compose call naming ``needle``.

        A real post-cutover service failure cannot be provoked on this stack
        without breaking the installation, so the deployment is pointed at a
        ``docker`` wrapper that refuses the named call and delegates everything
        else to the real client. The failure is therefore in the deployment's own
        command, which is what the failure path has to survive.
        """
        binary = self.workspace / "fault-bin"
        binary.mkdir(exist_ok=True)
        wrapper = binary / "docker"
        wrapper.write_text(
            "#!/bin/sh\n"
            "# Fault injection for the deployment failure paths.\n"
            "if [ -n \"${AI_UP_FAULT:-}\" ]; then\n"
            "  for argument in \"$@\"; do\n"
            "    if [ \"$argument\" = \"$AI_UP_FAULT\" ]; then\n"
            "      echo \"[fault-injection] refusing: docker $*\" >&2\n"
            "      exit 1\n"
            "    fi\n"
            "  done\n"
            "fi\n"
            "exec " + str(shutil.which("docker")) + " \"$@\"\n",
            encoding="utf-8")
        wrapper.chmod(0o755)
        return {"PATH": str(binary) + os.pathsep + self.env["PATH"], "AI_UP_FAULT": needle}

    def compose(self, *arguments: str, timeout: int = 600) -> subprocess.CompletedProcess:
        env_files = ["--env-file", str(self.env_file)] if self.env_file.exists() else []
        return subprocess.run(
            ["docker", "compose", *env_files, *arguments], cwd=ROOT, env=self.env,
            capture_output=True, text=True, timeout=timeout)

    def sql(self, statement: str) -> str:
        result = self.compose("exec", "-T", "postgres", "psql", "-v", "ON_ERROR_STOP=1",
                              "-U", self.env["POSTGRES_USER"], "-d", self.env["POSTGRES_DB"], "-tAc", statement)
        if result.returncode != 0:
            raise AssertionError("database query failed: " + result.stderr.strip())
        return result.stdout.strip()

    def destroy(self) -> None:
        """Remove this run's disposable project, its volumes and its local images.

        The project name carries the run token, so the volumes removed here belong
        to the installation this run created. The prefix guard refuses any project
        the suite did not create, and the development project is never named with
        it: no other data volume can be reached from here.
        """
        if not self.project.startswith(DISPOSABLE_PROJECT_PREFIX) or self.project == "opendx-os":
            raise AssertionError("refusing to remove the project " + self.project)
        self.compose("down", "--timeout", "20", "--volumes", "--rmi", "local", timeout=300)

    def write_declaration(self, name: str, content: str) -> Path:
        path = self.workspace / name
        path.write_text(content, encoding="utf-8")
        return path

    def prestart_fake(self) -> None:
        """Start the synthetic local endpoint on the private model network.

        A deployment's local endpoint is operator-managed: the deployment starts
        the proxy and the optional Ollama runtime, never an arbitrary service.
        The test therefore starts its own upstream first, on the same private
        network the proxy joins. Placeholder deployment paths satisfy the overlay
        for this one service; they are not exported to the deployment itself.
        """
        placeholders = {
            # Compose renders the whole project, so a one-service start still
            # needs every required installation value. They are synthetic and are
            # not exported to the deployment itself.
            "AI_DATABASE_PASSWORD": "ai-up-fake-api",
            "AI_WORKER_DATABASE_PASSWORD": "ai-up-fake-worker",
            "AI_DISPATCHER_DATABASE_PASSWORD": "ai-up-fake-dispatcher",
            "AI_GATEWAY_DATABASE_PASSWORD": "ai-up-fake-gateway",
            "AI_MODEL_CATALOG_DATABASE_PASSWORD": "ai-up-fake-catalog",
            "PARSER_SERVICE_TOKEN": "ai-up-fake-parser-token-0123456789abcdef",
            "RAG_WORKER_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000001",
            "RAG_FILE_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000002",
            "RAG_DISPATCHER_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000003",
        }
        for name, key in (
            ("catalog.yaml", "RAG_MODEL_CATALOG_FILE"),
            ("runtime.env", "RAG_MODEL_CREDENTIAL_ENV_FILE"),
            ("litellm.yaml", "RAG_MODEL_LITELLM_CONFIG_FILE"),
            ("proxy.env", "RAG_MODEL_LITELLM_SECRET_ENV_FILE"),
            ("litellm-runtime.env", "RAG_MODEL_LITELLM_CREDENTIAL_ENV_FILE"),
        ):
            path = self.workspace / name
            if not path.exists():
                path.write_text("", encoding="utf-8")
            placeholders[key] = str(path)
        start_env = {**self.env, **placeholders}
        result = subprocess.run(
            ["docker", "compose", "up", "-d", "model-route-fake"], cwd=ROOT,
            env=start_env, capture_output=True, text=True, timeout=600)
        if result.returncode != 0:
            raise AssertionError("the synthetic local endpoint did not start: " + result.stderr[-2000:])
        deadline = time.time() + 120
        while time.time() < deadline:
            listed = subprocess.run(
                ["docker", "ps", "-q", "--filter", "label=com.docker.compose.project=" + self.project,
                 "--filter", "label=com.docker.compose.service=model-route-fake"],
                capture_output=True, text=True).stdout.strip().splitlines()
            if listed:
                health = subprocess.run(
                    ["docker", "inspect", "-f", "{{.State.Health.Status}}", listed[0]],
                    capture_output=True, text=True).stdout.strip()
                if health == "healthy":
                    return
            time.sleep(2)
        raise AssertionError("the synthetic local endpoint did not become healthy")

    # -- observed state --------------------------------------------------

    def selected(self) -> dict[str, dict]:
        rows = self.sql("SELECT coalesce(json_agg(row_to_json(x)), '[]') FROM ("
                        " SELECT DISTINCT ON (purpose) purpose, profile_id, profile_revision, action"
                        " FROM cap_ai.model_deployment_events ORDER BY purpose, selection_revision DESC) x")
        return {row["purpose"]: row for row in json.loads(rows) if row["action"] == "SELECT"}

    def profile_rows(self) -> list[dict]:
        return json.loads(self.sql("SELECT coalesce(json_agg(row_to_json(x)), '[]') FROM ("
                                   " SELECT id, revision, purpose, dimension, enabled FROM cap_ai.model_profiles"
                                   " ORDER BY purpose, revision, created_at) x"))

    def counts(self) -> dict:
        queries = {
            "profiles": "SELECT count(*) FROM cap_ai.model_profiles",
            "generations": "SELECT count(*) FROM cap_ai.embedding_generations",
            "ready_generations": "SELECT count(*) FROM cap_ai.embedding_generations WHERE state='READY'",
            "vectors": "SELECT count(*) FROM cap_ai.chunk_embeddings",
            "embed_jobs": "SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='EMBED'",
            "parse_jobs": "SELECT count(*) FROM cap_ai.ingestion_jobs WHERE kind='PARSE'",
            "runs": "SELECT count(*) FROM cap_ai.processing_runs",
            "chunks": "SELECT count(*) FROM cap_ai.knowledge_chunks",
        }
        return {name: int(self.sql(query)) for name, query in queries.items()}

    def container(self, service: str) -> str:
        return self.compose("ps", "-q", service).stdout.strip()

    def wait_service_running(self, service: str, limit: int = 180) -> None:
        """Wait until a service container exists and is not restarting."""
        deadline = time.time() + limit
        while time.time() < deadline:
            container = self.container(service)
            if container:
                status = subprocess.run(
                    ["docker", "inspect", "-f", "{{.State.Status}}", container],
                    capture_output=True, text=True).stdout.strip()
                if status == "running":
                    return
            time.sleep(2)
        raise AssertionError("%s did not stay running" % service)

    def assert_serving(self, service: str = "cap-ai", port: int = 8084,
                       path: str = "/api/cap/ai/documents", limit: int = 180) -> None:
        """Assert the lexical runtime actually answers, and never restarted.

        A container that exists can be in a restart loop, so existence is not
        availability. This waits until the service answers on its HTTP port (the
        reply is ``401`` for an anonymous call, which is exactly the authorized
        search contract) and refuses any restarted container: a crash loop cannot
        satisfy it.
        """
        deadline = time.time() + limit
        last = "no container"
        while time.time() < deadline:
            container = self.container(service)
            if container:
                facts = subprocess.run(
                    ["docker", "inspect", "-f", "{{.State.Status}} {{.RestartCount}}", container],
                    capture_output=True, text=True).stdout.split()
                if len(facts) == 2 and facts[0] == "running":
                    if facts[1] != "0":
                        raise AssertionError("%s restarted %s times: lexical search is not available"
                                             % (service, facts[1]))
                    probe = self.compose(
                        "exec", "-T", service, "sh", "-c",
                        'wget -S -O /dev/null --timeout=5 "http://127.0.0.1:%d%s" 2>&1 || true'
                        % (port, path), timeout=120)
                    output = probe.stdout + probe.stderr
                    if "HTTP/" in output:
                        return
                    last = output.strip()[-300:] or "no HTTP response"
            time.sleep(3)
        raise AssertionError("%s did not answer on port %d (%s): %s" % (service, port, path, last))

    def running(self, *services: str) -> list[str]:
        output = self.compose("ps", "--status", "running", "-q", *services).stdout
        return [line for line in output.splitlines() if line.strip()]

    def model_settings(self) -> dict[str, str]:
        """Read the generated model settings file the Compose project consumes."""
        if not self.model_env.exists():
            raise AssertionError("the deployment did not write its model settings file")
        settings = {}
        for line in self.model_env.read_text(encoding="utf-8").splitlines():
            name, _, value = line.partition("=")
            if name:
                settings[name] = value
        return settings

    def assert_models_off(self, service: str = "cap-ai") -> None:
        """Assert the runtime is configured for models off, not degraded into it.

        Only the model overlay defines the installation switch, so the FTS-only and
        disabled renders leave it absent and the runtime's own default (off)
        applies. An absent and an explicit false therefore mean one thing here,
        and the assertion checks the state that matters: no model call can be
        admitted and the process did not log the degraded-boot line.
        """
        result = self.compose("exec", "-T", service, "sh", "-c", 'printf %s "${RAG_MODELS_ENABLED-}"')
        if result.returncode != 0:
            raise AssertionError("could not read the runtime environment: " + result.stderr[-800:])
        if result.stdout.strip() not in ("", "false"):
            raise AssertionError("the runtime was started with model calls on: "
                                 + result.stdout.strip()[:40])
        logs = self.compose("logs", "--no-log-prefix", "--tail", "200", service)
        if "model calls unavailable" in logs.stdout + logs.stderr:
            raise AssertionError("the runtime booted degraded instead of configured for lexical search")

    def fake_counts(self) -> dict:
        log = self.state / "requests.jsonl"
        counts = {"EMBED": 0, "GENERATE": 0}
        if log.exists():
            for line in log.read_text(encoding="utf-8").splitlines():
                if line.strip():
                    purpose = json.loads(line).get("purpose")
                    if purpose in counts:
                        counts[purpose] += 1
        return counts

    def set_mode(self, mode: str) -> None:
        (self.state / "mode").write_text(mode + "\n", encoding="utf-8")

    def reset_fake(self) -> None:
        (self.state / "requests.jsonl").write_text("", encoding="utf-8")
        self.set_mode("normal")


class WithdrawnPurposeRuleTest(unittest.TestCase):
    """The cutover drain rule, pinned without Docker.

    ``ai-up`` may only leave the API and worker running when the live selection is
    exactly the one this run keeps. A purpose the runtime holds but this run no
    longer qualifies is a change like any other, so the runtime is drained before
    the withdrawal.
    """

    LIVE_BOTH = {"purposes": [
        {"purpose": "EMBED", "profile_id": "embed", "revision": 1, "qualified": True},
        {"purpose": "GENERATE", "profile_id": "llm", "revision": 1, "qualified": True},
    ]}

    def declaration(self, *purposes: str) -> dict:
        profile_ids = {"EMBED": "embed", "GENERATE": "llm"}
        return {"purposes": [
            {"purpose": purpose, "declared": True, "profile_id": profile_ids[purpose], "revision": 1}
            for purpose in purposes
        ]}

    def test_an_unchanged_selection_needs_no_drain(self):
        self.assertTrue(ai_up_snapshot.selection_matches(
            self.LIVE_BOTH, self.declaration("EMBED", "GENERATE"),
            {"EMBED": True, "GENERATE": True}))

    def test_a_replaced_profile_needs_a_drain(self):
        self.assertFalse(ai_up_snapshot.selection_matches(
            self.LIVE_BOTH, self.declaration("EMBED", "GENERATE"),
            {"EMBED": False, "GENERATE": True}))

    def test_a_withdrawn_purpose_needs_a_drain(self):
        """The finding this rule fixes: a probe failure withdraws a live purpose."""
        self.assertFalse(
            ai_up_snapshot.selection_matches(
                self.LIVE_BOTH, self.declaration("EMBED"),
                {"EMBED": True, "GENERATE": False}),
            "a currently-selected purpose absent from the expected set was reconciled "
            "under a running API and worker")

    def test_a_mismatched_live_selection_needs_a_drain(self):
        """A selected route whose declaration moved is still held by the runtime."""
        live = {"purposes": [
            {"purpose": "EMBED", "selected": True, "selected_profile_id": "embed",
             "selected_revision": 1, "qualified": True},
            {"purpose": "GENERATE", "selected": True, "selected_profile_id": "old-llm",
             "selected_revision": 1, "qualified": False},
        ]}
        self.assertFalse(ai_up_snapshot.selection_matches(
            live, self.declaration("EMBED", "GENERATE"),
            {"EMBED": True, "GENERATE": True}),
            "a selected but mismatched purpose was not treated as held")

    def test_a_selected_purpose_the_declaration_dropped_needs_a_drain(self):
        live = {"purposes": [
            {"purpose": "EMBED", "selected": True, "selected_profile_id": "embed",
             "selected_revision": 1, "qualified": True},
            {"purpose": "GENERATE", "selected": True, "selected_profile_id": "llm",
             "selected_revision": 1, "qualified": False},
        ]}
        self.assertFalse(ai_up_snapshot.selection_matches(
            live, self.declaration("EMBED"), {"EMBED": True, "GENERATE": False}),
            "a selected purpose missing from the declaration was not drained")

    def test_an_unreconciled_runtime_is_a_change_and_the_runtime_is_started(self):
        # Nothing is selected yet, so this run moves the selection; the drain is a
        # no-op when nothing runs, and the services are started afterwards.
        self.assertFalse(ai_up_snapshot.selection_matches(
            {"purposes": []}, self.declaration("EMBED", "GENERATE"),
            {"EMBED": True, "GENERATE": True}))


class DeploymentLockTest(unittest.TestCase):
    """One Compose project has one deployment lock, wherever its state lives."""

    def test_state_dirs_do_not_split_the_project_lock(self):
        if not docker_available() or shutil.which("flock") is None:
            self.skipTest("NOT_RUN: docker compose and flock are required")
        project = "opendx-aiup-lock-" + secrets.token_hex(3)
        with tempfile.TemporaryDirectory(prefix="ai-up-lock-") as directory:
            workspace = Path(directory)
            lock_dir = workspace / "locks"
            lock_dir.mkdir(mode=0o700)
            lock = lock_dir / ("ai-up." + project + ".lock")
            env_file = workspace / ".env.installation"
            env_file.write_text("COMPOSE_PROJECT_NAME=" + project + "\n", encoding="utf-8")
            env = {
                **os.environ,
                "AI_UP_LOCK_DIR": str(lock_dir),
                # If the project lock is missed, fail before any Compose call:
                # a missing CLI parent keeps the script from building or starting
                # the disposable project this case never intended to create.
                "DXOS_MODEL_BIN": str(workspace / "missing" / "dxos-model"),
                "COMPOSE_FILE": str(workspace / "missing-compose.yaml"),
            }
            with open(lock, "w", encoding="utf-8") as handle:
                import fcntl
                fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
                for state in ("state-one", "state-two"):
                    result = subprocess.run(
                        ["bash", str(SCRIPT), "--config", str(ROOT / "models.example.yaml"),
                         "--env-file", str(env_file), "--state-dir", str(workspace / state)],
                        cwd=ROOT, env=env, capture_output=True, text=True, timeout=60)
                    self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertIn("another ai-up deployment holds", result.stderr,
                                  "state directory %s bypassed the project lock:\n%s%s" % (
                                      state, result.stdout, result.stderr))


class AiUpDeploymentTest(unittest.TestCase):
    """The coordinated model deployment on a disposable project."""

    @classmethod
    def setUpClass(cls) -> None:
        if not DISPOSABLE:
            raise unittest.SkipTest("NOT_RUN: TEST_DISPOSABLE_DATABASE=yes is required for a disposable project")
        if not docker_available():
            raise unittest.SkipTest("NOT_RUN: docker with the compose plugin is required")
        if MODEL_PROJECT == "opendx-os":
            raise AssertionError("refusing to run against the development project")
        for script in (SCRIPT, RAG_SCRIPT, FAKE_SCRIPT, FAKE_OVERLAY, SNAPSHOT_MODULE):
            if not script.exists():
                raise unittest.SkipTest(f"NOT_RUN: {script} is missing")
        # Register both cleanups before the first resource exists: a failure while
        # the baseline deploys would otherwise leak the project and its volumes,
        # because tearDownClass does not run when setUpClass raises.
        cls.checkout_before = checkout_state()
        directory = tempfile.TemporaryDirectory()
        cls.directory = directory
        cls.addClassCleanup(directory.cleanup)
        cls.workspace = Path(directory.name)
        harness = DeploymentHarness(MODEL_PROJECT, cls.workspace, port_map(0), model=True)
        cls.harness = harness
        cls.addClassCleanup(harness.destroy)
        cls.baseline = harness.write_declaration("models-baseline.yaml", declaration())
        harness.prestart_fake()
        result = harness.deploy(cls.baseline)
        if result.returncode != 0:
            raise AssertionError("the baseline deployment failed:\n" + result.stdout[-6000:] + result.stderr[-4000:])
        cls.baseline_state = harness.counts()

    def setUp(self) -> None:
        self.harness.reset_fake()
        # Each case is independent: a case that leaves a purpose withdrawn, a
        # different route selected or a model-calling service stopped is repaired
        # by deploying the reviewed baseline again, so one failure never cascades
        # into the next case's assertion.
        repaired = (set(self.harness.selected()) == {"EMBED", "GENERATE"}
                    and len(self.harness.running("cap-ai", "cap-ai-worker")) == 2)
        if not repaired:
            result = self.harness.deploy(self.baseline)
            self.assertEqual(result.returncode, 0, result.stdout[-4000:] + result.stderr[-2000:])

    # -- cases -----------------------------------------------------------

    def test_ai_up_same_config(self):
        """A second deployment of the same declaration changes nothing."""
        before = self.harness.counts()
        cap_ai = self.harness.container("cap-ai")
        worker = self.harness.container("cap-ai-worker")
        selected = self.harness.selected()
        self.harness.reset_fake()
        result = self.harness.deploy(self.baseline)
        self.assertEqual(result.returncode, 0, result.stdout[-4000:] + result.stderr[-2000:])
        after = self.harness.counts()
        self.assertEqual(before, after, "an unchanged declaration created or removed rows")
        self.assertEqual(self.harness.selected(), selected, "an unchanged declaration moved the selection")
        self.assertEqual(self.harness.container("cap-ai"), cap_ai, "an unchanged declaration restarted the API")
        self.assertEqual(self.harness.container("cap-ai-worker"), worker, "an unchanged declaration restarted the worker")
        self.assertEqual(self.harness.fake_counts(), {"EMBED": 1, "GENERATE": 1},
                         "an unchanged deployment made an extra model call")
        self.harness.assert_serving("cap-ai")

    def test_ai_up_llm_only_change(self):
        """An LLM-only change leaves the embedding identity and vectors untouched."""
        before = self.harness.counts()
        selection = self.harness.selected()
        embed = selection.get("EMBED")
        generate = selection.get("GENERATE")
        self.assertIsNotNone(embed)
        self.assertIsNotNone(generate)
        config = self.harness.write_declaration("models-llm-only.yaml", declaration(llm_revision="llm-r2"))
        result = self.harness.deploy(config)
        self.assertEqual(result.returncode, 0, result.stdout[-4000:] + result.stderr[-2000:])
        after = self.harness.counts()
        selected = self.harness.selected()
        self.assertEqual((selected["EMBED"]["profile_id"], selected["EMBED"]["profile_revision"]),
                         (embed["profile_id"], embed["profile_revision"]),
                         "an LLM-only change moved the embedding selection")
        self.assertNotEqual(selected["GENERATE"]["profile_id"], generate["profile_id"],
                            "the generation identity did not change")
        # Deployment-only invariants: this stack carries no parsed documents, so the
        # generation and vector counts cannot move for any reason and prove nothing
        # about vectors on their own. The precondition below fails loudly if the
        # stack ever gains documents, which is when these rows must be revisited;
        # vector survival and generation isolation are pinned by
        # TestWorkerDeploymentReembed against real PostgreSQL/pgvector.
        self.assertEqual(before["chunks"], 0,
                         "the deployment stack gained parsed documents; revisit the vector assertions")
        self.assertEqual(after["ready_generations"], before["ready_generations"],
                         "deployment-only: a generation change must not be created")
        self.assertEqual(after["vectors"], before["vectors"],
                         "deployment-only: no vector may be written")
        self.harness.assert_serving("cap-ai")
        # Restore the reviewed baseline for the following cases.
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)

    def test_ai_up_embedding_change(self):
        """An embedding change makes a new profile and keeps old generations isolated."""
        before = self.harness.counts()
        old = self.harness.selected()["EMBED"]
        old_generations = self.harness.sql(
            "SELECT count(*) FROM cap_ai.embedding_generations g JOIN cap_ai.model_profiles p"
            " ON p.id=g.model_profile_id AND p.revision=g.profile_revision"
            f" WHERE p.id='{old['profile_id']}' AND g.state='READY'")
        config = self.harness.write_declaration("models-embed-change.yaml",
                                                declaration(embed_revision="embed-r2"))
        result = self.harness.deploy(config)
        self.assertEqual(result.returncode, 0, result.stdout[-4000:] + result.stderr[-2000:])
        after = self.harness.counts()
        selected = self.harness.selected()["EMBED"]
        self.assertNotEqual(selected["profile_id"], old["profile_id"], "the embedding identity did not change")
        self.assertGreater(after["profiles"], before["profiles"], "no new immutable profile was recorded")
        # The old generation and its vectors are retained, never reused or deleted.
        self.assertEqual(self.harness.sql(
            "SELECT count(*) FROM cap_ai.embedding_generations g JOIN cap_ai.model_profiles p"
            " ON p.id=g.model_profile_id AND p.revision=g.profile_revision"
            f" WHERE p.id='{old['profile_id']}' AND g.state='READY'"), old_generations)
        # Deployment-only invariants, as in the LLM-only case: no parsed document
        # exists here, so these counts are pinned to zero and the assertion is that
        # the deployment invented no work. A deployment that fabricated a
        # generation or a vector for the new profile would break them; whether real
        # vectors survive the change is pinned by TestWorkerDeploymentReembed.
        self.assertEqual(before["chunks"], 0,
                         "the deployment stack gained parsed documents; revisit the vector assertions")
        self.assertEqual(after["vectors"], before["vectors"],
                         "deployment-only: a deployment re-embedded without new work")
        self.assertEqual(after["embed_jobs"], before["embed_jobs"],
                         "a deployment fabricated embedding jobs")
        # No reparse: the parsed run, chunks and parse jobs are the same.
        self.assertEqual(after["runs"], before["runs"])
        self.assertEqual(after["chunks"], before["chunks"])
        self.assertEqual(after["parse_jobs"], before["parse_jobs"])
        # The superseded profile is still recorded but is not the live selection.
        superseeded = self.harness.sql(
            "SELECT count(*) FROM cap_ai.model_profiles WHERE id=" + "'" + old["profile_id"] + "'")
        self.assertEqual(superseeded, "1")
        # Lexical retrieval stays available: the API answers and did not restart.
        self.harness.assert_serving("cap-ai")
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)

    def test_ai_up_interrupted(self):
        """An interrupted cutover leaves no model-calling service running."""
        previous = self.harness.selected()["EMBED"]
        config = self.harness.write_declaration("models-interrupted.yaml",
                                                declaration(embed_revision="embed-r3"))
        process = self.harness.spawn(config)
        seen = []
        interrupted = threading.Event()

        def watch() -> None:
            for line in process.stdout or []:
                seen.append(line)
                if "starting the knowledge stack on the verified snapshot" in line and not interrupted.is_set():
                    interrupted.set()
                    process.send_signal(15)

        reader = threading.Thread(target=watch, daemon=True)
        reader.start()
        try:
            process.wait(timeout=900)
        finally:
            reader.join(5)
            if process.stdout is not None:
                process.stdout.close()
        self.assertTrue(interrupted.is_set(), "the cutover stage was not reached:\n" + "".join(seen)[-4000:])
        self.assertNotEqual(process.returncode, 0, "an interrupted cutover reported success")
        self.assertEqual(self.harness.running("cap-ai", "cap-ai-worker"), [],
                         "an interrupted cutover left model-calling services running")
        # The selection was reconciled before the interrupt, so a rerun starts the
        # new snapshot and does not repeat the work.
        selected = self.harness.selected()["EMBED"]
        self.assertNotEqual(selected["profile_id"], previous["profile_id"],
                            "the interrupted run did not reconcile the reviewed selection")
        rerun = self.harness.deploy(config)
        self.assertEqual(rerun.returncode, 0, rerun.stdout[-4000:] + rerun.stderr[-2000:])
        self.assertNotEqual(self.harness.running("cap-ai", "cap-ai-worker"), [],
                            "the rerun did not start the model-calling services")
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)

    def test_ai_up_unavailable_model(self):
        """An unavailable route is reported, never replaced, and FTS stays up.

        This is also the withdrawal case: ``GENERATE`` is selected when the run
        starts and the failed probe withdraws it, so the API and worker must be
        drained before the selection changes (finding: a withdrawal was reconciled
        under a running worker).
        """
        self.assertIn("GENERATE", self.harness.selected(),
                      "the baseline must hold a generation selection for this case")
        cap_ai_before = self.harness.container("cap-ai")
        worker_before = self.harness.container("cap-ai-worker")
        self.harness.set_mode("llm-fail")
        try:
            config = self.harness.write_declaration("models-unavailable.yaml",
                                                    declaration(llm_revision="llm-unavailable"))
            result = self.harness.deploy(config)
        finally:
            self.harness.set_mode("normal")
        output = result.stdout[-6000:] + result.stderr[-4000:]
        self.assertNotEqual(result.returncode, 0, "an unavailable route reported success")
        self.assertIn("generation route unavailable", output)
        # The drain line is printed before the (long) start-up output, so the whole
        # deployment log is searched rather than its tail.
        self.assertIn("stopping the model-calling API and worker before the snapshot changes",
                      result.stdout + result.stderr,
                      "a currently-selected purpose was withdrawn under a running API and worker")
        self.assertNotEqual(self.harness.container("cap-ai"), cap_ai_before,
                            "the API was not restarted on the withdrawn snapshot")
        self.assertNotEqual(self.harness.container("cap-ai-worker"), worker_before,
                            "the worker was not restarted on the withdrawn snapshot")
        selected = self.harness.selected()
        self.assertIn("EMBED", selected, "a failed generation route disabled the embedding route")
        self.assertNotIn("GENERATE", selected, "an unavailable route stayed selected")
        # Lexical retrieval is available: the API answers and never restarted.
        self.harness.assert_serving("cap-ai")
        self.assertEqual(self.harness.fake_counts()["GENERATE"], 1,
                         "the failed route was retried or replaced by another model")
        # Correcting the upstream and rerunning selects the reviewed route again.
        self.assertEqual(self.harness.deploy(config).returncode, 0)
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)

    def test_ai_up_failure_after_cutover_degrades(self):
        """A failure between the cutover and the identity check is visible and safe."""
        fault = self.harness.fault_injection("keycloak")
        previous = self.harness.selected()["EMBED"]
        config = self.harness.write_declaration("models-post-cutover.yaml",
                                                declaration(embed_revision="embed-r4", llm_revision="llm-r4"))
        result = self.harness.run_script(SCRIPT, config, extra_env=fault)
        output = result.stdout[-6000:] + result.stderr[-4000:]
        self.assertNotEqual(result.returncode, 0,
                            "a failure after the cutover reported success:\n" + output)
        self.assertIn("FAIL:", output)
        self.assertIn("Keycloak could not be started after the cutover", output)
        # The selection had already moved, so the runtime must not be left
        # dispatching model calls on a snapshot nobody verified: model calls are
        # switched off (and the services are either restarted that way or stopped).
        settings = self.harness.model_settings()
        self.assertEqual(settings.get("RAG_MODELS_ENABLED"), "false",
                         "the failure left model calls enabled on an unverified snapshot")
        for service in ("cap-ai", "cap-ai-worker"):
            if self.harness.container(service):
                self.harness.assert_models_off(service)
        # Lexical search is still available from the degraded runtime.
        self.harness.assert_serving("cap-ai")
        # Correcting the cause and rerunning resumes the deployment.
        retry = self.harness.deploy(config)
        self.assertEqual(retry.returncode, 0, retry.stdout[-4000:] + retry.stderr[-2000:])
        self.assertNotEqual(self.harness.selected()["EMBED"]["profile_id"], previous["profile_id"],
                            "the recovered deployment did not select the reviewed profile")
        self.assertEqual(self.harness.model_settings().get("RAG_MODELS_ENABLED"), "true",
                         "the recovered deployment did not turn model calls back on")
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)

    def test_ai_up_disabled_models(self):
        """A disabled declaration drains the live runtime before withdrawing it."""
        self.assertIn("EMBED", self.harness.selected(),
                      "the baseline must hold a selection for this withdrawal case")
        cap_ai_before = self.harness.container("cap-ai")
        worker_before = self.harness.container("cap-ai-worker")
        config = self.harness.write_declaration("models-disabled.yaml", declaration(enabled=False))
        self.harness.reset_fake()
        result = self.harness.deploy(config)
        self.assertEqual(result.returncode, 0, result.stdout[-4000:] + result.stderr[-2000:])
        self.assertIn("stopping the model-calling API and worker before the snapshot changes",
                      result.stdout + result.stderr,
                      "a disabled declaration withdrew the live selection under a running API and worker")
        self.assertNotEqual(self.harness.container("cap-ai"), cap_ai_before,
                            "the API was not restarted on the disabled snapshot")
        self.assertNotEqual(self.harness.container("cap-ai-worker"), worker_before,
                            "the worker was not restarted on the disabled snapshot")
        self.assertEqual(self.harness.selected(), {}, "a disabled declaration selected a route")
        self.assertEqual(self.harness.fake_counts(), {"EMBED": 0, "GENERATE": 0},
                         "a disabled declaration made a model call")
        self.harness.wait_service_running("cap-ai")
        self.harness.assert_models_off("cap-ai")
        self.harness.assert_serving("cap-ai")
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)

    def test_suite_is_hermetic(self):
        """The suite never writes to the checkout's private deployment state."""
        self.assertEqual(self.checkout_before, checkout_state(),
                         "the deployment suite touched the checkout's private state")


class ModelsDisabledFreshProjectTest(unittest.TestCase):
    """A models-disabled ``ai-up`` declaration installs no model schema.

    The check runs on a *fresh* volume: the deployment must not provision pgvector
    or the ``cap_ai`` model schema just because the volume lacks them. Only a
    declaration that selects models may do that.
    """

    def setUp(self) -> None:
        if not DISPOSABLE:
            raise unittest.SkipTest("NOT_RUN: TEST_DISPOSABLE_DATABASE=yes is required for a disposable project")
        if not docker_available():
            raise unittest.SkipTest("NOT_RUN: docker with the compose plugin is required")
        if OFF_PROJECT == "opendx-os":
            raise AssertionError("refusing to run against the development project")
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.workspace = Path(self.directory.name)
        self.harness = DeploymentHarness(OFF_PROJECT, self.workspace, port_map(2), model=False)
        self.addCleanup(self.harness.destroy)

    def test_ai_up_disabled_models_installs_no_model_schema(self):
        """The declaration turns models off, so the FTS schema is the whole schema."""
        config = self.harness.write_declaration("models-off-fresh.yaml", declaration(enabled=False))
        result = self.harness.run_script(SCRIPT, config)
        self.assertEqual(result.returncode, 0, result.stdout[-6000:] + result.stderr[-4000:])
        self.assertEqual(self.harness.sql("SELECT count(*) FROM pg_extension WHERE extname='vector'"), "0",
                         "a models-disabled declaration installed pgvector")
        self.assertEqual(self.harness.sql("SELECT count(*) FROM information_schema.tables"
                                          " WHERE table_schema='cap_ai' AND table_name='model_profiles'"), "0",
                         "a models-disabled declaration installed the model schema")
        settings = self.harness.model_settings()
        self.assertEqual(settings.get("RAG_MODELS_ENABLED"), "false")
        for switch in ("RAG_MODELS_SCHEMA_ENABLED", "RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED",
                       "RAG_AGENT_MODELS_SCHEMA_ENABLED"):
            self.assertEqual(settings.get(switch), "false",
                             switch + " did not follow the models-disabled declaration")
        self.assertEqual(self.harness.running("litellm"), [], "a models-disabled declaration started the proxy")
        self.assertTrue(self.harness.model_env.exists())
        self.harness.wait_service_running("cap-ai")
        self.harness.assert_models_off("cap-ai")
        self.harness.assert_serving("cap-ai")
        self.assertEqual(self.harness.sql("SELECT status FROM core.apps WHERE app_id='personal_library'"), "Ready")


class FtsOnlyDeploymentTest(unittest.TestCase):
    """``make rag-up`` boots without the model/pgvector schema."""

    def setUp(self) -> None:
        if not DISPOSABLE:
            raise unittest.SkipTest("NOT_RUN: TEST_DISPOSABLE_DATABASE=yes is required for a disposable project")
        if not docker_available():
            raise unittest.SkipTest("NOT_RUN: docker with the compose plugin is required")
        if FTS_PROJECT == "opendx-os":
            raise AssertionError("refusing to run against the development project")
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.workspace = Path(self.directory.name)
        self.harness = DeploymentHarness(FTS_PROJECT, self.workspace, port_map(1), model=False)
        self.addCleanup(self.harness.destroy)

    def test_rag_up_without_vector_schema(self):
        """The FTS-only stack has no model schema, no proxy and no model call."""
        result = self.harness.run_script(RAG_SCRIPT)
        self.assertEqual(result.returncode, 0, result.stdout[-4000:] + result.stderr[-2000:])
        self.assertEqual(self.harness.sql("SELECT count(*) FROM pg_namespace WHERE nspname='cap_ai'"), "1")
        self.assertEqual(self.harness.sql("SELECT count(*) FROM pg_extension WHERE extname='vector'"), "0",
                         "the FTS-only stack installed pgvector")
        self.assertEqual(self.harness.sql("SELECT count(*) FROM information_schema.tables"
                                          " WHERE table_schema='cap_ai' AND table_name='model_profiles'"), "0",
                         "the FTS-only stack installed the model schema")
        self.assertEqual(self.harness.sql("SELECT count(*) FROM core.schema_migrations WHERE scope='cap-ai' AND version='0013'"), "0")
        self.assertEqual(self.harness.running("litellm"), [], "the FTS-only stack started the model proxy")
        self.harness.wait_service_running("cap-ai")
        self.harness.assert_models_off("cap-ai")
        self.harness.assert_serving("cap-ai")
        self.assertEqual(self.harness.sql("SELECT status FROM core.apps WHERE app_id='personal_library'"), "Ready")


if __name__ == "__main__":
    unittest.main()
