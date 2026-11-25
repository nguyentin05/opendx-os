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
* an unavailable route is reported, is not replaced by an alternate model, and
  lexical search stays available;
* a disabled declaration starts no proxy and makes no model call;
* ``rag-up`` boots an FTS-only installation without the model/pgvector schema.

Prerequisites (a missing one is NOT_RUN, never a pass): Docker with the Compose
plugin, the built images for the stack, and ``TEST_DISPOSABLE_DATABASE=yes`` with
a project name other than ``opendx-os``. The suite never prints a rendered secret.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import threading
import time
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "ai-up.sh"
RAG_SCRIPT = ROOT / "scripts" / "rag-up.sh"
FAKE_SCRIPT = ROOT / "scripts" / "integration" / "fixtures" / "ai_up_fake.py"
FAKE_OVERLAY = ROOT / "scripts" / "integration" / "fixtures" / "ai-up-fake.yaml"

MODEL_PROJECT = os.environ.get("AI_UP_TEST_PROJECT", "opendx-aiup-test")
FTS_PROJECT = os.environ.get("AI_UP_TEST_FTS_PROJECT", "opendx-aiup-fts-test")
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
        self.env = {
            **os.environ,
            "COMPOSE_PROJECT_NAME": project,
            "POSTGRES_DB": "opendx_aiup",
            "POSTGRES_USER": "aiup_admin",
            "POSTGRES_PASSWORD": "aiup-" + project + "-disposable",
            "BIND_ADDRESS": "127.0.0.1",
            "OIDC_ISSUER_URL": f"http://localhost:{ports['KEYCLOAK_PORT']}/realms/dxos",
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
                   extra: tuple[str, ...] = ()) -> subprocess.CompletedProcess:
        arguments = ["bash", str(script)]
        if config is not None:
            arguments += ["--config", str(config)]
        arguments += ["--env-file", str(self.env_file), *extra]
        return subprocess.run(arguments, cwd=ROOT, env=self.env, capture_output=True, text=True, timeout=timeout)

    def deploy(self, config: Path, timeout: int = 3600) -> subprocess.CompletedProcess:
        result = self.run_script(SCRIPT, config, timeout)
        self.assert_no_secrets(result.stdout + result.stderr)
        return result

    def assert_no_secrets(self, output: str) -> None:
        """The deployment output must never carry a generated secret value."""
        for path in (self.env_file, ROOT / "build" / "ai-up" / "proxy.env",
                     ROOT / "build" / "ai-up" / "runtime.env"):
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

    def running(self, *services: str) -> list[str]:
        output = self.compose("ps", "--status", "running", "-q", *services).stdout
        return [line for line in output.splitlines() if line.strip()]

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
        for script in (SCRIPT, RAG_SCRIPT, FAKE_SCRIPT, FAKE_OVERLAY):
            if not script.exists():
                raise unittest.SkipTest(f"NOT_RUN: {script} is missing")
        cls.directory = tempfile.TemporaryDirectory()
        cls.workspace = Path(cls.directory.name)
        cls.harness = DeploymentHarness(MODEL_PROJECT, cls.workspace, port_map(0), model=True)
        cls.baseline = cls.harness.write_declaration("models-baseline.yaml", declaration())
        cls.harness.prestart_fake()
        result = cls.harness.deploy(cls.baseline)
        if result.returncode != 0:
            raise AssertionError("the baseline deployment failed:\n" + result.stdout[-6000:] + result.stderr[-4000:])
        cls.baseline_state = cls.harness.counts()

    @classmethod
    def tearDownClass(cls) -> None:
        if getattr(cls, "harness", None) is not None:
            cls.harness.compose("down", "--timeout", "20")
        if getattr(cls, "directory", None) is not None:
            cls.directory.cleanup()

    def setUp(self) -> None:
        self.harness.reset_fake()
        # Each case is independent: a case that leaves a purpose withdrawn or a
        # different route selected is repaired by deploying the reviewed baseline.
        if set(self.harness.selected()) != {"EMBED", "GENERATE"}:
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
        self.assertEqual(after["ready_generations"], before["ready_generations"])
        self.assertEqual(after["vectors"], before["vectors"], "an LLM-only change re-embedded vectors")
        self.assertNotEqual(selected["GENERATE"]["profile_id"], generate["profile_id"],
                            "the generation identity did not change")
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
        self.assertEqual(after["vectors"], before["vectors"], "a deployment re-embedded without new work")
        # No reparse: the parsed run, chunks and parse jobs are the same.
        self.assertEqual(after["runs"], before["runs"])
        self.assertEqual(after["chunks"], before["chunks"])
        self.assertEqual(after["parse_jobs"], before["parse_jobs"])
        # The superseded profile is still recorded but is not the live selection.
        superseeded = self.harness.sql(
            "SELECT count(*) FROM cap_ai.model_profiles WHERE id=" + "'" + old["profile_id"] + "'")
        self.assertEqual(superseeded, "1")
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
        """An unavailable route is reported, never replaced, and FTS stays up."""
        self.harness.set_mode("llm-fail")
        try:
            config = self.harness.write_declaration("models-unavailable.yaml",
                                                    declaration(llm_revision="llm-unavailable"))
            result = self.harness.deploy(config)
        finally:
            self.harness.set_mode("normal")
        self.assertNotEqual(result.returncode, 0, "an unavailable route reported success")
        self.assertIn("generation route unavailable", result.stdout[-4000:] + result.stderr[-2000:])
        selected = self.harness.selected()
        self.assertIn("EMBED", selected, "a failed generation route disabled the embedding route")
        self.assertNotIn("GENERATE", selected, "an unavailable route stayed selected")
        self.assertNotEqual(self.harness.running("cap-ai"), [], "FTS runtime is not running")
        self.assertEqual(self.harness.fake_counts()["GENERATE"], 1,
                         "the failed route was retried or replaced by another model")
        # Correcting the upstream and rerunning selects the reviewed route again.
        self.assertEqual(self.harness.deploy(config).returncode, 0)
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)

    def test_ai_up_disabled_models(self):
        """A disabled declaration reconciles no route and makes no model call."""
        config = self.harness.write_declaration("models-disabled.yaml", declaration(enabled=False))
        self.harness.reset_fake()
        result = self.harness.deploy(config)
        self.assertEqual(result.returncode, 0, result.stdout[-4000:] + result.stderr[-2000:])
        self.assertEqual(self.harness.selected(), {}, "a disabled declaration selected a route")
        self.assertEqual(self.harness.fake_counts(), {"EMBED": 0, "GENERATE": 0},
                         "a disabled declaration made a model call")
        self.harness.wait_service_running("cap-ai")
        environment = self.harness.compose("exec", "-T", "cap-ai", "sh", "-c", "printf %s \"$RAG_MODELS_ENABLED\"")
        self.assertEqual(environment.stdout.strip(), "false", environment.stderr[-800:])
        self.assertEqual(self.harness.deploy(self.baseline).returncode, 0)


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
        self.workspace = Path(self.directory.name)
        self.harness = DeploymentHarness(FTS_PROJECT, self.workspace, port_map(1), model=False)

    def tearDown(self) -> None:
        if getattr(self, "harness", None) is not None:
            self.harness.compose("down", "--timeout", "20")
        if getattr(self, "directory", None) is not None:
            self.directory.cleanup()

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
        environment = self.harness.compose("exec", "-T", "cap-ai", "sh", "-c", "printf %s \"$RAG_MODELS_ENABLED\"")
        self.assertEqual(environment.stdout.strip(), "false", environment.stderr[-800:])
        self.assertEqual(self.harness.sql("SELECT status FROM core.apps WHERE app_id='personal_library'"), "Ready")


if __name__ == "__main__":
    unittest.main()
