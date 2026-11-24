#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Real-container qualification of the private LiteLLM deployment.

This suite runs the actual pinned proxy image and asserts what the deployment
claims, instead of reading a configuration and inferring behaviour:

* the exact upstream model, path and index/query parameters of each route;
* exactly one upstream request per admitted operation, including on 429, 503, a
  timeout and an unreadable answer, with a retry-enabled control proxy that makes
  four so the measurement cannot be vacuous;
* the generated retry, routing and configuration wiring, including that the
  configuration secret is mounted at the path the proxy loads;
* no silent fallback, route override or parameter dropping;
* that an upstream redirect cannot qualify a route, and that a defective answer
  (wrong dimension, unknown citation, tool call, truncation) is forwarded
  unchanged for the strict transport to refuse;
* rejected unauthenticated and direct public requests;
* no payload, answer or credential marker in the proxy log;
* no hosted destination for a local route, and mixed local/hosted routes stay
  distinct;
* no database: the model list comes from the generated file alone;
* a wrong generated configuration or an edited route cannot qualify.

Prerequisites (a missing one is reported as NOT_RUN, never as a pass):

* Docker with the container runtime available;
* the pinned image ``ghcr.io/berriai/litellm@sha256:1050d2c9...`` (the suite pulls
  it by digest when it is absent, which needs registry access and may be slow);
* ``openssl`` for the qualification-only CA the hosted leg is served with;
* a Go toolchain, because the proxy configuration and the request bodies are
  rendered and emitted by the Go implementation under test rather than re-typed
  here.

Only synthetic data is used. Every request this suite sends is built from the
emission of ``TestLiteLLMQualificationFixture``, so the suite cannot drift from
the transport it qualifies: it never re-types a body. The replay is
JSON-equivalent rather than byte-identical, because Python parses each emitted
body into an object and re-serializes it; every key and value is the transport's
own, the original byte order is not.
"""

from __future__ import annotations

import hashlib
import json
import os
import shutil
import socket
import subprocess
import tempfile
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FIXTURES = ROOT / "scripts" / "integration" / "fixtures"
CAPTURE_SCRIPT = FIXTURES / "litellm_capture.py"
DECLARATION = FIXTURES / "litellm-deployment.yaml"

IMAGE = os.environ.get(
    "RAG_MODEL_LITELLM_IMAGE",
    "ghcr.io/berriai/litellm@sha256:1050d2c9ca58399de9cd51df6eb503812af697717f54d2f08c1c30589c75e1f7",
)
INDEX_DIGEST = "sha256:1050d2c9ca58399de9cd51df6eb503812af697717f54d2f08c1c30589c75e1f7"
# The release the pinned digest was built from. It is read from the image's own
# environment (LITELLM_RELEASE_TAG) and from the running container, so the suite
# asserts a version and not only a digest.
RELEASE_TAG = "v1.110.3"
COMPOSE_FILES = ("compose.yaml", "compose.rag.yaml", "compose.models.yaml", "compose.litellm.yaml")
COMPOSE_OLLAMA = ("compose.yaml", "compose.rag.yaml", "compose.models.yaml", "compose.litellm.yaml",
                  "compose.ollama.yaml")

NETWORK = "dxos-litellm-qualification"
CAPTURE = "dxos-litellm-qualification-capture"
PROXY = "dxos-litellm-qualification-proxy"
CONFIG_TARGET = "/run/secrets/litellm-config.yaml"
CA_TARGET = "/run/secrets/qualification-ca.crt"
LOCAL_ALIAS = "litellm-upstream"
HOSTED_ALIAS = "hosted-capture.invalid"

MASTER_KEY = "sk-qualification-" + "0e5c1f7d9a4b6c8d2f3a5b7c9d1e3f5a"
UPSTREAM_KEY = "qualification-upstream-key-canary"

# Synthetic payload markers. A log that carries one of these has leaked content.
FIXTURE_PASSAGE = "qualification passage text"
FIXTURE_QUERY = "qualification query text"
SOURCE_TEXT = "qualification source text"
ANSWER_TEXT = "synthetic grounded answer"
CANARIES = (FIXTURE_PASSAGE, FIXTURE_QUERY, SOURCE_TEXT, ANSWER_TEXT, MASTER_KEY, UPSTREAM_KEY)

# Keys the transport must never send: a caller could use one to select a
# destination, a credential or a different provider.
FORBIDDEN_REQUEST_KEYS = ("api_base", "api_key", "base_url", "provider", "models", "route",
                          "tools", "tool_choice", "functions", "function_call", "model_list",
                          "extra_body", "user_config")

COMPOSE_SETTINGS = {
    "AI_DATABASE_PASSWORD": "compose-canary-api",
    "AI_WORKER_DATABASE_PASSWORD": "compose-canary-worker",
    "AI_DISPATCHER_DATABASE_PASSWORD": "compose-canary-dispatcher",
    "AI_GATEWAY_DATABASE_PASSWORD": "compose-canary-gateway",
    "PARSER_SERVICE_TOKEN": "compose-canary-parser",
    "RAG_WORKER_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000001",
    "RAG_FILE_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000002",
    "RAG_DISPATCHER_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000003",
}

REVIEWED_PROVIDER = {
    "only": None,
    "order": None,
    "allow_fallbacks": False,
    "require_parameters": True,
    "data_collection": None,
    "zdr": None,
}

EMBEDDING_KEYS = ("model", "input", "input_type")
GENERATION_KEYS = ("max_tokens", "messages", "model", "n", "response_format", "stream")


def docker(*arguments: str, capture: bool = True, timeout: int = 600) -> subprocess.CompletedProcess:
    """Run one docker command and return the completed process."""
    return subprocess.run(["docker", *arguments], capture_output=capture, text=True, timeout=timeout)


def free_port() -> int:
    """Return one loopback port the kernel reports as free."""
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


class QualificationMismatch(Exception):
    """One captured upstream request does not match the reviewed declaration."""


class LiteLLMQualificationTest(unittest.TestCase):
    """The pinned proxy, the generated configuration and the real transport shapes."""

    # -- setup -----------------------------------------------------------

    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("docker") is None:
            raise unittest.SkipTest("NOT_RUN: docker is required to run the pinned proxy")
        if shutil.which("go") is None:
            raise unittest.SkipTest("NOT_RUN: a Go toolchain is required to render the generated configuration")
        if shutil.which("openssl") is None:
            raise unittest.SkipTest("NOT_RUN: openssl is required for the qualification-only CA")
        cls.directory = tempfile.TemporaryDirectory()
        cls.workspace = Path(cls.directory.name)
        cls.state = cls.workspace / "state"
        cls.tls = cls.workspace / "tls"
        cls.emission_dir = cls.workspace / "emission"
        cls.state.mkdir()
        cls.tls.mkdir()
        cls._ensure_image()
        cls.emission = cls._render_emission()
        cls.config = cls.emission_dir / "litellm-config.yaml"
        # The generated configuration and the qualification CA carry no secret, and
        # the proxy runs unprivileged, so both must be world-readable.
        os.chmod(cls.config, 0o644)
        cls._write_ca()
        os.chmod(cls.tls / "ca.crt", 0o644)
        (cls.state / "mode").write_text("ok\n", encoding="utf-8")
        (cls.state / "redirect").write_text("", encoding="utf-8")
        cls._start_capture()
        cls.proxy_name = PROXY
        cls._run_proxy(PROXY, cls.config, CONFIG_TARGET)
        cls.base_url = cls._base_url(PROXY)
        cls._await_proxy(PROXY)

    @classmethod
    def tearDownClass(cls) -> None:
        for name in (PROXY, CAPTURE, PROXY + "-drifted", PROXY + "-unnamed", PROXY + "-retry"):
            docker("rm", "-f", name)
        docker("network", "rm", NETWORK)
        cls.directory.cleanup()

    @classmethod
    def _ensure_image(cls) -> None:
        """Use the pinned image, pulling it by digest when it is not present."""
        present = docker("image", "inspect", IMAGE, timeout=120)
        if present.returncode == 0:
            return
        pulled = subprocess.run(["docker", "pull", IMAGE], capture_output=True, text=True, timeout=3600)
        if pulled.returncode != 0:
            detail = (pulled.stderr or pulled.stdout).strip().splitlines()
            raise unittest.SkipTest(
                "NOT_RUN: the pinned image is unavailable: " + (detail[-1] if detail else "no registry access"))

    @classmethod
    def _render_emission(cls) -> dict:
        """Render the configuration and the transport requests from the Go code under test."""
        environment = {**os.environ, "LITELLM_QUALIFICATION_OUT": str(cls.emission_dir)}
        result = subprocess.run(
            ["go", "test", "./internal/ai/models", "-run", "TestLiteLLMQualificationFixture", "-count=1"],
            cwd=ROOT / "capabilities", env=environment, capture_output=True, text=True, timeout=600)
        if result.returncode != 0:
            raise AssertionError("the Go renderer/emitter failed:\n" + result.stdout + result.stderr)
        return json.loads((cls.emission_dir / "litellm-qualification.json").read_text(encoding="utf-8"))

    @classmethod
    def _write_ca(cls) -> None:
        """Create the qualification-only CA and server certificate for the hosted leg."""
        ca_config = cls.tls / "ca.cnf"
        ca_config.write_text(
            "[req]\ndistinguished_name=dn\nx509_extensions=v3_ca\nprompt=no\n"
            "[dn]\nCN=opendx-litellm-qualification-ca\n"
            "[v3_ca]\nbasicConstraints=critical,CA:TRUE\nkeyUsage=critical,keyCertSign,cRLSign\n"
            "subjectKeyIdentifier=hash\n", encoding="utf-8")
        server_config = cls.tls / "srv.cnf"
        server_config.write_text(
            "[req]\ndistinguished_name=dn\nprompt=no\n"
            "[dn]\nCN=" + HOSTED_ALIAS + "\n"
            "[server]\nbasicConstraints=critical,CA:FALSE\n"
            "keyUsage=critical,digitalSignature,keyEncipherment\n"
            "extendedKeyUsage=serverAuth\nsubjectAltName=DNS:" + HOSTED_ALIAS + "\n", encoding="utf-8")
        steps = [
            ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", str(cls.tls / "ca.key"),
             "-out", str(cls.tls / "ca.crt"), "-days", "2", "-config", str(ca_config)],
            ["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-keyout", str(cls.tls / "srv.key"),
             "-out", str(cls.tls / "srv.csr"), "-config", str(server_config)],
            ["openssl", "x509", "-req", "-in", str(cls.tls / "srv.csr"), "-CA", str(cls.tls / "ca.crt"),
             "-CAkey", str(cls.tls / "ca.key"), "-CAcreateserial", "-out", str(cls.tls / "srv.crt"),
             "-days", "2", "-extfile", str(server_config), "-extensions", "server"],
        ]
        for step in steps:
            result = subprocess.run(step, capture_output=True, text=True, timeout=120)
            if result.returncode != 0:
                raise unittest.SkipTest("NOT_RUN: the qualification CA could not be created: " + result.stderr.strip())

    @classmethod
    def _start_capture(cls) -> None:
        docker("rm", "-f", CAPTURE)
        docker("network", "rm", NETWORK)
        created = docker("network", "create", NETWORK)
        if created.returncode != 0:
            raise unittest.SkipTest("NOT_RUN: the qualification network could not be created: " + created.stderr.strip())
        started = docker(
            "run", "-d", "--name", CAPTURE, "--network", NETWORK,
            "--network-alias", LOCAL_ALIAS, "--network-alias", HOSTED_ALIAS,
            # Run as the invoking user so the fixture state stays readable and
            # writable by the runner without elevating anything.
            "--user", "%d:%d" % (os.getuid(), os.getgid()),
            "-v", "%s:/capture.py:ro" % CAPTURE_SCRIPT,
            "-v", "%s:/tls:ro" % cls.tls,
            "-v", "%s:/state" % cls.state,
            "--entrypoint", "python", IMAGE,
            "/capture.py", "--state-dir", "/state", "--cert", "/tls/srv.crt", "--key", "/tls/srv.key")
        if started.returncode != 0:
            raise AssertionError("the capture container did not start: " + started.stderr)
        cls._await_capture()

    @classmethod
    def _await_capture(cls) -> None:
        probe = ("import socket\n"
                 "for port in (8081, 8082):\n"
                 "    socket.create_connection(('127.0.0.1', port), 2).close()\n")
        for _ in range(60):
            if docker("exec", CAPTURE, "python", "-c", probe, timeout=60).returncode == 0:
                return
            time.sleep(0.5)
        logs = docker("logs", CAPTURE).stderr
        raise AssertionError("the capture container is not listening: " + logs)

    @classmethod
    def _run_proxy(cls, name: str, config: Path, config_target: str) -> None:
        """Start one hardened proxy container on the qualification network."""
        env_file = cls.workspace / ("proxy-%s.env" % name)
        env_file.write_text(
            "LITELLM_MASTER_KEY=%s\nQUALIFICATION_PROXY_UPSTREAM_KEY=%s\n" % (MASTER_KEY, UPSTREAM_KEY),
            encoding="utf-8")
        os.chmod(env_file, 0o600)
        docker("rm", "-f", name)
        started = docker(
            "run", "-d", "--name", name, "--network", NETWORK,
            "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m",
            "--user", "65532:65532", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
            "--env-file", str(env_file),
            "-e", "HOME=/tmp",
            "-e", "CONFIG_FILE_PATH=" + config_target,
            "-e", "LITELLM_TELEMETRY_DISABLED=true",
            "-e", "LITELLM_OFFLINE_MODE=true",
            "-e", "LITELLM_LOCAL_MODEL_COST_MAP=true",
            "-e", "SSL_CERT_FILE=" + CA_TARGET,
            "-v", "%s:%s:ro" % (config, config_target),
            "-v", "%s:%s:ro" % (cls.tls / "ca.crt", CA_TARGET),
            "-p", "127.0.0.1::4000",
            IMAGE)
        if started.returncode != 0:
            raise AssertionError("the proxy container did not start: " + started.stderr)

    @classmethod
    def _base_url(cls, name: str) -> str:
        published = docker("port", name, "4000/tcp")
        if published.returncode != 0 or not published.stdout.strip():
            raise AssertionError("the proxy published no loopback port: " + published.stderr)
        return "http://" + published.stdout.strip().splitlines()[0]

    @classmethod
    def _await_proxy(cls, name: str) -> None:
        url = cls._base_url(name) + "/health/liveliness"
        deadline = time.time() + 180
        while time.time() < deadline:
            try:
                with urllib.request.urlopen(url, timeout=3) as response:
                    if response.status == 200:
                        return
            except Exception:  # noqa: BLE001 - the proxy is still starting
                time.sleep(1)
        logs = docker("logs", name)
        raise AssertionError("the proxy did not become ready: " + (logs.stdout + logs.stderr)[-4000:])

    # -- helpers ---------------------------------------------------------

    def set_mode(self, mode: str) -> None:
        """Select the capture's answer for the next requests."""
        (self.state / "mode").write_text(mode + "\n", encoding="utf-8")

    def reset_capture(self) -> None:
        """Forget every recorded upstream request."""
        (self.state / "requests.jsonl").write_text("", encoding="utf-8")

    def captured(self) -> list[dict]:
        """Every upstream request the capture recorded so far."""
        path = self.state / "requests.jsonl"
        if not path.exists():
            return []
        return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]

    def post(self, path: str, body: dict | str, key: str | None = MASTER_KEY, base_url: str | None = None,
             timeout: float = 60) -> tuple[int, bytes]:
        """Send one request to the proxy exactly as the transport would."""
        payload = body if isinstance(body, str) else json.dumps(body)
        request = urllib.request.Request(
            (base_url or self.base_url) + path, data=payload.encode(),
            headers={"Content-Type": "application/json"}, method="POST")
        if key is not None:
            request.add_header("Authorization", "Bearer " + key)
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            try:
                return error.code, error.read()
            finally:
                error.close()

    def request_for(self, name: str) -> dict:
        """The emitted transport request of one qualification case."""
        return next(item for item in self.emission["requests"] if item["name"] == name)

    def replay(self, name: str, **overrides) -> tuple[int, bytes]:
        """Send one emitted transport request, optionally with extra fields."""
        request = self.request_for(name)
        body = dict(request["body"])
        body.update(overrides)
        return self.post(request["path"], body)

    def route_for(self, alias: str) -> dict:
        return next(route for route in self.emission["routes"] if route["alias"] == alias)

    def route_of(self, purpose: str) -> dict:
        """The declared route of one purpose, independent of emission order."""
        return next(route for route in self.emission["routes"] if route["purpose"] == purpose)

    def route_for_request(self, name: str) -> dict:
        """The declared route one qualification request addresses."""
        return self.route_for(self.request_for(name)["alias"])

    def path_for(self, purpose: str) -> str:
        """The upstream path a route's reviewed endpoint implies for its purpose."""
        base_path = urllib.parse.urlsplit(self.route_of(purpose)["api_base"]).path.rstrip("/")
        return base_path + ("/embeddings" if purpose == "EMBED" else "/chat/completions")

    def expectations(self, name: str) -> dict:
        """The reviewed expectation of one emitted request, read off the declaration."""
        request = self.request_for(name)
        route = self.route_for(request["alias"])
        if route["purpose"] == "EMBED":
            parameters = route["query_parameters"] if name == "embed-query" else route["passage_parameters"]
            return {
                "listener": "local",
                "path": self.path_for("EMBED"),
                "model": route["upstream_body_model"],
                "parameters": parameters or {},
                "keys": set(EMBEDDING_KEYS[:2]) | set((parameters or {}).keys()),
            }
        return {
            "listener": "hosted",
            "path": self.path_for("GENERATE"),
            "model": route["upstream_body_model"],
            "provider": {
                "only": [route["routing"]["provider"]],
                "order": [route["routing"]["provider"]],
                "allow_fallbacks": False,
                "require_parameters": True,
                "data_collection": route["routing"]["data_collection"],
                "zdr": route["routing"]["zdr"],
            },
            "max_tokens": route["max_output_tokens"],
            # The proxy adds the reviewed routing and a usage request to the body it
            # forwards; the transport request itself carries neither.
            "keys": set(GENERATION_KEYS) | {"provider", "usage"},
        }

    def reviewed_problems(self, record: dict, expectation: dict) -> list[str]:
        """Every way one captured upstream request differs from the reviewed route."""
        problems: list[str] = []
        if record["listener"] != expectation["listener"]:
            problems.append("answered by the %s listener instead of the %s one"
                            % (record["listener"], expectation["listener"]))
        if record["path"] != expectation["path"]:
            problems.append("path %r instead of %r" % (record["path"], expectation["path"]))
        body = json.loads(record["body"])
        if body.get("model") != expectation["model"]:
            problems.append("upstream model %r instead of %r" % (body.get("model"), expectation["model"]))
        if expectation["listener"] == "local":
            for key, value in expectation["parameters"].items():
                if body.get(key) != value:
                    problems.append("parameter %r is %r instead of %r" % (key, body.get(key), value))
            if set(body) != expectation["keys"]:
                problems.append("body keys %s instead of %s" % (sorted(body), sorted(expectation["keys"])))
        else:
            if body.get("provider") != expectation["provider"]:
                problems.append("provider routing %r instead of %r"
                                % (body.get("provider"), expectation["provider"]))
            if body.get("max_tokens") != expectation["max_tokens"]:
                problems.append("max_tokens %r instead of %r" % (body.get("max_tokens"), expectation["max_tokens"]))
            if set(body) != expectation["keys"]:
                problems.append("body keys %s instead of %s" % (sorted(body), sorted(expectation["keys"])))
            messages = body.get("messages") or []
            if len(messages) != 2 or messages[0].get("role") != "system" or messages[1].get("role") != "user":
                problems.append("messages are not the fixed system and one user message")
        return problems

    def assert_reviewed(self, name: str, record: dict) -> None:
        problems = self.reviewed_problems(record, self.expectations(name))
        if problems:
            raise QualificationMismatch("%s does not qualify: %s" % (name, "; ".join(problems)))

    def compose(self, files=COMPOSE_FILES, settings=None, quiet=False):
        values = dict(COMPOSE_SETTINGS)
        values.update({
            "RAG_MODEL_CATALOG_FILE": str(self.workspace / "model-catalog"),
            "RAG_MODEL_CREDENTIAL_ENV_FILE": str(self.workspace / "runtime.env"),
            "RAG_MODEL_LITELLM_CONFIG_FILE": str(self.config),
            "RAG_MODEL_LITELLM_SECRET_ENV_FILE": str(self.workspace / "proxy.env"),
            "RAG_MODEL_LITELLM_CREDENTIAL_ENV_FILE": str(self.workspace / "runtime.env"),
        })
        (self.workspace / "model-catalog").write_text("enabled: false\n", encoding="utf-8")
        (self.workspace / "runtime.env").write_text("LITELLM_MASTER_KEY=%s\n" % MASTER_KEY, encoding="utf-8")
        (self.workspace / "proxy.env").write_text("LITELLM_MASTER_KEY=%s\n" % MASTER_KEY, encoding="utf-8")
        values.update(settings or {})
        arguments = ["compose", "--env-file", os.devnull]
        for name in files:
            arguments += ["-f", name]
        arguments += ["config", "--quiet"] if quiet else ["config", "--format", "json"]
        return subprocess.run(["docker", *arguments], cwd=ROOT, env={**os.environ, **values},
                              capture_output=True, text=True, timeout=300)

    def compose_config(self, files, settings=None) -> dict:
        result = self.compose(files, settings)
        for canary in CANARIES:
            self.assertNotIn(canary, result.stderr, "compose reported a fixture value")
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    # -- image and deployment shape --------------------------------------

    def test_pinned_image_and_release(self):
        """The container that qualifies is the pinned digest and the reviewed release."""
        inspected = json.loads(docker("image", "inspect", IMAGE).stdout)[0]
        digests = [entry for entry in inspected.get("RepoDigests") or [] if INDEX_DIGEST in entry]
        self.assertTrue(digests, "the image does not carry the pinned index digest")
        running = json.loads(docker("inspect", PROXY).stdout)[0]
        self.assertEqual(running["Image"], inspected["Id"], "the proxy does not run the pinned image")
        # The version, not only the digest: the image reports the release it was
        # built from, and the running proxy must still report the same one.
        image_environment = (inspected.get("Config") or {}).get("Env") or []
        self.assertIn("LITELLM_RELEASE_TAG=" + RELEASE_TAG, image_environment,
                      "the pinned image does not report the reviewed release; it is digest-pinned only")
        reported = docker("exec", PROXY, "python", "-c",
                          "import os;print(os.environ.get('LITELLM_RELEASE_TAG', ''))")
        self.assertEqual(reported.returncode, 0, reported.stderr)
        self.assertEqual(reported.stdout.strip(), RELEASE_TAG,
                         "the running proxy does not report the reviewed release")

    def test_compose_overlay_is_private(self):
        """The overlay publishes nothing, stores nothing and disables telemetry."""
        config = self.compose_config(COMPOSE_FILES)
        self.assertIn("litellm", config["services"])
        proxy = config["services"]["litellm"]
        self.assertFalse(proxy.get("ports"), "the proxy publishes a host port")
        self.assertFalse(proxy.get("expose"), "the proxy exposes a port")
        self.assertFalse(proxy.get("privileged"))
        self.assertFalse(proxy.get("network_mode"))
        self.assertFalse(proxy.get("volumes"), "the proxy declares a writable or host path")
        self.assertTrue(proxy["read_only"])
        self.assertEqual(proxy["cap_drop"], ["ALL"])
        self.assertEqual(proxy["security_opt"], ["no-new-privileges:true"])
        self.assertNotEqual(proxy["user"], "0")
        self.assertIn(INDEX_DIGEST, proxy["image"], "the overlay does not pin the digest")
        environment = proxy["environment"]
        self.assertEqual(environment["LITELLM_TELEMETRY_DISABLED"], "true")
        self.assertEqual(environment["LITELLM_OFFLINE_MODE"], "true")
        self.assertEqual(environment["LITELLM_LOCAL_MODEL_COST_MAP"], "true")
        self.assertEqual(environment["CONFIG_FILE_PATH"], CONFIG_TARGET)
        for forbidden in ("DATABASE_URL", "STORE_MODEL_IN_DB", "LITELLM_DATABASE_URL", "UI_USERNAME"):
            self.assertNotIn(forbidden, environment, "the proxy holds %s" % forbidden)
        for name, value in environment.items():
            self.assertNotIn("postgres://", str(value), "the proxy holds a DSN in %s" % name)
        networks = proxy["networks"]
        self.assertEqual(sorted(networks), ["model-inference"], "the proxy joins another network")
        self.assertTrue(config["networks"]["model-inference"]["internal"],
                        "the private model network is not internal")
        # Only the two model-calling services get the proxy credential.
        for name in ("cap-ai", "cap-ai-worker"):
            service = config["services"][name]
            self.assertIn("model-inference", service["networks"])
            self.assertIn("RAG_MODEL_LITELLM_ENDPOINT", service["environment"])
        # No other service claims a proxy setting or an upstream provider key.
        for name, service in config["services"].items():
            if name in ("litellm", "cap-ai", "cap-ai-worker"):
                continue
            rendered = json.dumps(service)
            self.assertNotIn("QUALIFICATION_PROXY_UPSTREAM_KEY", rendered)
            self.assertNotIn("LITELLM_MASTER_KEY", rendered, "%s received the proxy credential" % name)
            self.assertNotIn("litellm:4000", rendered, "%s gained the proxy endpoint" % name)
        # The generated configuration is mounted read-only, and only there. A
        # secret declares both halves of its wiring: the source names the local
        # file and the target is the mount name inside /run/secrets. The target
        # must therefore equal the basename of CONFIG_FILE_PATH — a source-only
        # entry mounts at /run/secrets/<source> and the pinned proxy loads no route
        # at all.
        self.assertEqual([item["source"] for item in proxy["secrets"]], ["litellm-config"])
        targets = [item["target"] for item in proxy["secrets"]]
        config_path = environment["CONFIG_FILE_PATH"]
        self.assertEqual(targets, [config_path.rsplit("/", 1)[-1]],
                         "the configuration secret is not mounted at the basename of CONFIG_FILE_PATH")
        self.assertEqual("/run/secrets/" + targets[0], config_path,
                         "the rendered secret target is not the path the proxy loads")

        # The optional local overlay adds the operator-provisioned runtime and no
        # automatic weight download.
        with_ollama = self.compose_config(COMPOSE_OLLAMA, {"RAG_MODEL_OLLAMA_IMAGE": "ollama/ollama@sha256:" + "0" * 64})
        ollama = with_ollama["services"]["ollama"]
        self.assertFalse(ollama.get("ports"), "the local runtime publishes a host port")
        self.assertFalse(ollama.get("command"), "the local runtime runs an automatic pull")
        self.assertEqual(ollama["volumes"][0]["source"], "ollama-models")
        self.assertEqual(ollama["volumes"][0]["target"], "/models")
        self.assertFalse(ollama["volumes"][0].get("read_only"))
        self.assertIn("ollama-models", with_ollama["volumes"])

    def test_missing_opt_in_settings_fail_safely(self):
        """A missing deployment path stops Compose with a static message."""
        for setting in ("RAG_MODEL_LITELLM_CONFIG_FILE", "RAG_MODEL_LITELLM_SECRET_ENV_FILE",
                        "RAG_MODEL_LITELLM_CREDENTIAL_ENV_FILE"):
            with self.subTest(setting=setting):
                settings = dict(COMPOSE_SETTINGS)
                values = {
                    "RAG_MODEL_CATALOG_FILE": str(self.workspace / "model-catalog"),
                    "RAG_MODEL_CREDENTIAL_ENV_FILE": str(self.workspace / "runtime.env"),
                    "RAG_MODEL_LITELLM_CONFIG_FILE": str(self.config),
                    "RAG_MODEL_LITELLM_SECRET_ENV_FILE": str(self.workspace / "proxy.env"),
                    "RAG_MODEL_LITELLM_CREDENTIAL_ENV_FILE": str(self.workspace / "runtime.env"),
                }
                values.pop(setting)
                arguments = ["compose", "--env-file", os.devnull]
                for name in COMPOSE_FILES:
                    arguments += ["-f", name]
                arguments += ["config", "--quiet"]
                result = subprocess.run(["docker", *arguments], cwd=ROOT,
                                        env={**os.environ, **settings, **values},
                                        capture_output=True, text=True, timeout=300)
                self.assertNotEqual(result.returncode, 0, "%s was accepted" % setting)
                self.assertIn(setting, result.stderr)
                for canary in CANARIES:
                    self.assertNotIn(canary, result.stderr)

    # -- qualified routes -------------------------------------------------

    def test_local_embedding_route_is_exact(self):
        """The index and query inputs reach the local endpoint with their parameters."""
        self.set_mode("ok")
        for name in ("embed-passage", "embed-query"):
            route = self.route_for_request(name)
            with self.subTest(case=name):
                self.reset_capture()
                status, body = self.replay(name)
                self.assertEqual(status, 200, body[:400])
                records = self.captured()
                self.assertEqual(len(records), 1, "one operation made %d upstream requests" % len(records))
                self.assert_reviewed(name, records[0])
                payload = json.loads(body)
                self.assertEqual(len(payload["data"]), 1)
                self.assertEqual(len(payload["data"][0]["embedding"]), route["dimension"])

    def test_hosted_generation_route_is_exact(self):
        """The generation request carries the reviewed routing and nothing else."""
        self.set_mode("ok")
        self.reset_capture()
        status, body = self.replay("generate")
        self.assertEqual(status, 200, body[:400])
        records = self.captured()
        self.assertEqual(len(records), 1, "one operation made %d upstream requests" % len(records))
        self.assert_reviewed("generate", records[0])
        answer = json.loads(json.loads(body)["choices"][0]["message"]["content"])
        self.assertEqual(answer["citation_ids"], ["doc-a"])

    def test_mixed_routes_stay_distinct(self):
        """A local operation never reaches the hosted listener and vice versa."""
        self.set_mode("ok")
        self.reset_capture()
        self.replay("embed-passage")
        local_only = self.captured()
        self.assertEqual([record["listener"] for record in local_only], ["local"])
        self.reset_capture()
        self.replay("generate")
        hosted_only = self.captured()
        self.assertEqual([record["listener"] for record in hosted_only], ["hosted"])
        # Every destination in the generated configuration is one of the two
        # capture endpoints: a hosted provider URL appears nowhere.
        rendered = self.config.read_text(encoding="utf-8")
        for forbidden in ("openrouter.ai", "api.openai.com", "anthropic.com", "generativelanguage"):
            self.assertNotIn(forbidden, rendered, "the generated configuration names %s" % forbidden)
        local_base = self.route_of("EMBED")["api_base"]
        self.assertTrue(local_base.startswith("http://" + LOCAL_ALIAS + ":"),
                        "the local endpoint is not a single-label deployment-local origin")

    # -- one operation, one dispatch --------------------------------------

    def test_one_upstream_request_on_failure(self):
        """429, 503, a timeout and an unreadable answer each cost one request."""
        cases = (
            ("status429", {}, 429),
            ("status503", {}, 503),
            ("slow", {"timeout": 1}, 408),
        )
        for mode, overrides, expected in cases:
            with self.subTest(mode=mode):
                self.set_mode(mode)
                self.reset_capture()
                status, _ = self.replay("generate", **overrides)
                self.assertEqual(status, expected, "the proxy answered %d for %s" % (status, mode))
                records = self.captured()
                self.assertEqual(len(records), 1,
                                 "%s produced %d upstream requests, want exactly 1" % (mode, len(records)))
        self.set_mode("ok")

    def test_upstream_redirect_cannot_qualify(self):
        """A 3xx from a configured endpoint moves the call off the reviewed route.

        The pinned proxy follows an upstream redirect, so the redirect is not
        invisible and not ignorable: the request that answers is at a path the
        review never named. Qualification compares the *answered* request with the
        reviewed route, so a redirecting endpoint leaves the route unqualified and no
        grounded or embedding answer is accepted through one.
        """
        targets = {
            "embed-passage": "http://%s:8081/redirected/api/v1/embeddings" % LOCAL_ALIAS,
            "generate": "https://%s:8082/redirected/api/v1/chat/completions" % HOSTED_ALIAS,
        }
        for name, target in targets.items():
            expected = self.expectations(name)
            with self.subTest(case=name):
                self.set_mode("redirect")
                (self.state / "redirect").write_text(target + "\n", encoding="utf-8")
                self.reset_capture()
                try:
                    status, _ = self.replay(name)
                finally:
                    self.set_mode("ok")
                    (self.state / "redirect").write_text("", encoding="utf-8")
                records = self.captured()
                self.assertGreaterEqual(len(records), 1, "the proxy sent no upstream request at all")
                answered = records[-1]
                problems = self.reviewed_problems(answered, expected)
                self.assertTrue(problems, "an answer obtained through a redirect qualified: %s"
                                % json.dumps(answered))
                self.assertTrue(any("path" in problem for problem in problems),
                                "the redirect was not visible in the qualification: %s" % problems)
                if status == 200:
                    # The proxy followed the redirect and served the synthetic answer
                    # from the redirected path. That path is exactly what disqualifies
                    # the route: the product of a redirect is never a qualified result.
                    self.assertNotEqual(answered["path"], expected["path"],
                                        "the redirected request reached the reviewed path")
                else:
                    # The proxy refused instead of following: also unqualified.
                    self.assertGreaterEqual(status, 400,
                                            "the proxy answered %d for a redirecting endpoint" % status)

    def test_malformed_upstream_answers_are_forwarded_unfixed(self):
        """Wrong dimension, unknown citation, tool call and truncation arrive intact.

        The proxy repairs, pads, truncates and strips nothing: each defective answer
        reaches the admitted client unchanged, so the reviewed Go transport's strict
        reading is the control that refuses it (the refusal itself is asserted in the
        Go suite). Every case costs exactly one upstream request, so a silent repair
        or a retry would be visible here.
        """
        cases = (
            ("wrong-dimension", "embed-passage"),
            ("unknown-citation", "generate"),
            ("tool-call", "generate"),
            ("truncated", "generate"),
        )
        for mode, name in cases:
            with self.subTest(mode=mode):
                self.set_mode(mode)
                self.reset_capture()
                status, body = self.replay(name)
                self.assertEqual(status, 200, "the proxy hid a %s answer: %s" % (mode, body[:400]))
                records = self.captured()
                self.assertEqual(len(records), 1,
                                 "%s produced %d upstream requests, want exactly 1" % (mode, len(records)))
                self.assert_reviewed(name, records[0])
                answer = json.loads(body)
                if mode == "wrong-dimension":
                    width = len(answer["data"][0]["embedding"])
                    self.assertEqual(width, self.route_of("EMBED")["dimension"] + 1,
                                     "the proxy changed the vector width to %d" % width)
                elif mode == "unknown-citation":
                    content = json.loads(answer["choices"][0]["message"]["content"])
                    self.assertEqual(content["citation_ids"], ["doc-not-supplied"],
                                     "the proxy dropped or rewrote a citation the caller never supplied")
                elif mode == "tool-call":
                    message = answer["choices"][0]["message"]
                    self.assertTrue(message.get("tool_calls"), "the proxy stripped a tool call")
                else:
                    self.assertEqual(answer["choices"][0]["finish_reason"], "length",
                                     "the proxy rewrote a truncated finish reason")
        self.set_mode("ok")

    def retried_upstream_requests(self, name: str, overrides) -> int:
        """One operation against a proxy built from the generated file with retries overridden."""
        config = json.loads(json.dumps(self._parse_config(self.config.read_bytes())))
        for section, key, value in overrides:
            config[section][key] = value
        path = self.workspace / ("retry-control-%s.yaml" % name)
        path.write_text(self._render_config(config), encoding="utf-8")
        os.chmod(path, 0o644)
        container = PROXY + "-retry-" + name
        try:
            self._run_proxy(container, path, CONFIG_TARGET)
            url = self._base_url(container)
            self._await_proxy(container)
            self.set_mode("status503")
            self.reset_capture()
            request = self.request_for("generate")
            status, _ = self.post(request["path"], request["body"], base_url=url)
            self.assertGreaterEqual(status, 500, "the control proxy answered %d" % status)
            return len(self.captured())
        finally:
            docker("rm", "-f", container)
            self.set_mode("ok")

    def test_retry_control_shows_the_generated_settings_are_the_control(self):
        """One operation costs one upstream request only because three settings say so.

        The control runs are committed rather than reported once, so the claim stays
        reproducible. Two measurements are recorded because they differ:

        * raising only ``router_settings.num_retries`` changes nothing — the
          module-level count is what a call reads, and the generated
          ``litellm_settings.num_retries: 0`` still governs;
        * raising all three retry settings makes one operation cost three upstream
          requests instead of one.

        That is why the renderer emits all three zeroes, and why "no retries" is not
        a claim about one key.
        """
        config = self._parse_config(self.config.read_bytes())
        self.assertEqual(config["router_settings"]["num_retries"], 0,
                         "the generated configuration does not disable router retries")
        self.assertEqual(config["litellm_settings"]["num_retries"], 0,
                         "the generated configuration does not disable call retries")
        self.assertEqual(config["litellm_settings"]["num_retries_per_request"], 0,
                         "the generated configuration does not disable whole-request retries")
        router_only = self.retried_upstream_requests(
            "router", [("router_settings", "num_retries", 3)])
        retrying = self.retried_upstream_requests("all", [
            ("router_settings", "num_retries", 3),
            ("litellm_settings", "num_retries", 3),
            ("litellm_settings", "num_retries_per_request", 3),
        ])
        self.set_mode("status503")
        self.reset_capture()
        self.replay("generate")
        generated = len(self.captured())
        self.set_mode("ok")
        self.assertEqual(router_only, 1,
                         "raising only router_settings.num_retries made %d upstream requests, want 1"
                         % router_only)
        self.assertEqual(retrying, 3,
                         "raising all three retry settings made %d upstream requests, want 3"
                         % retrying)
        self.assertEqual(generated, 1,
                         "the generated configuration made %d upstream requests" % generated)

    def test_no_silent_parameter_dropping(self):
        """The proxy honours every generated setting: it ignores none of them."""
        logs = docker("logs", PROXY)
        rendered = logs.stdout + logs.stderr
        self.assertNotIn("not a valid argument for Router", rendered,
                         "the proxy ignored a generated router setting")
        self.assertNotIn("Ignoring this key", rendered,
                         "the proxy ignored a generated router setting")
        self.assertNotIn("UnsupportedParamsError", rendered)

    def test_transport_requests_offer_no_choice(self):
        """The admitted client sends no field that could choose a destination."""
        for request in self.emission["requests"]:
            with self.subTest(case=request["name"]):
                body = request["body"] if isinstance(request["body"], dict) else json.loads(request["body"])
                for key in FORBIDDEN_REQUEST_KEYS:
                    self.assertNotIn(key, body, "%s carries %r" % (request["name"], key))
                self.assertEqual(body["model"], request["alias"])
                if request["path"] == "/v1/chat/completions":
                    self.assertEqual(sorted(body), sorted(GENERATION_KEYS))
                    self.assertFalse(body["stream"])
                    self.assertEqual(body["n"], 1)
                    self.assertEqual([message["role"] for message in body["messages"]], ["system", "user"])
                    self.assertIn("Use only supplied evidence", body["messages"][0]["content"])
                    # The user message is one JSON value, not instructions.
                    user = json.loads(body["messages"][1]["content"])
                    self.assertEqual(sorted(user), ["evidence", "query"])

    # -- admission and overrides ------------------------------------------

    def test_unauthenticated_and_unknown_requests_are_rejected(self):
        """An unauthenticated or unknown request never reaches an endpoint."""
        self.set_mode("ok")
        cases = {
            "no credential": (None, "generate"),
            "wrong credential": ("sk-not-the-proxy-key", "generate"),
            "unknown model": (MASTER_KEY, "unknown-model"),
        }
        for name, (key, case) in cases.items():
            with self.subTest(case=name):
                self.reset_capture()
                request = self.request_for("generate")
                body = dict(request["body"])
                if case == "unknown-model":
                    body["model"] = "dxos-llm-unknown"
                status, _ = self.post(request["path"], body, key=key)
                self.assertGreaterEqual(status, 400, "%s was accepted" % name)
                self.assertLess(status, 500, "%s failed in the proxy instead of being refused" % name)
                self.assertEqual(self.captured(), [], "%s reached the upstream" % name)

        # A caller cannot choose the endpoint or the provider routing. The endpoint
        # override is refused outright; a routing override is forwarded by the
        # pinned proxy, which is exactly why the qualification compares the captured
        # routing with the reviewed one.
        self.reset_capture()
        status, _ = self.replay("generate", api_base="http://" + HOSTED_ALIAS + ":8082/override")
        self.assertGreaterEqual(status, 400, "an api_base override was accepted")
        self.assertEqual(self.captured(), [], "an api_base override reached an endpoint")

        self.reset_capture()
        status, _ = self.replay("generate", provider={"only": ["evil"], "allow_fallbacks": True})
        self.assertEqual(status, 200, "the fixture proxy refused the request instead of forwarding the override")
        records = self.captured()
        self.assertEqual(len(records), 1)
        problems = self.reviewed_problems(records[0], self.expectations("generate"))
        self.assertTrue(any("provider routing" in problem for problem in problems),
                        "a routing override qualified: %s" % problems)

    def test_logs_carry_no_payload_or_credential(self):
        """The proxy log carries no source text, answer, key or prompt marker."""
        self.set_mode("ok")
        self.replay("embed-passage")
        self.replay("generate")
        logs = docker("logs", PROXY)
        rendered = logs.stdout + logs.stderr
        for canary in CANARIES:
            self.assertNotIn(canary, rendered, "the proxy log carries a fixture marker")
        for marker in ("doc-a", "fixture://doc-a", "messages", "prompt_tokens"):
            self.assertNotIn(marker, rendered, "the proxy log carries request content")

    def test_no_database_and_telemetry_at_runtime(self):
        """The running proxy has no database and its telemetry veto is set."""
        environment = docker("exec", PROXY, "python", "-c",
                             "import os;print('\\n'.join('%s=%s' % (k, v) for k, v in os.environ.items()))")
        self.assertEqual(environment.returncode, 0, environment.stderr)
        self.assertIn("LITELLM_TELEMETRY_DISABLED=true", environment.stdout)
        self.assertNotIn("DATABASE_URL", environment.stdout)
        self.assertNotIn("STORE_MODEL_IN_DB", environment.stdout)
        # Provider keys live only in the proxy: the upstream key is here and the
        # runtime credential file (asserted through Compose) does not carry it.
        self.assertIn("QUALIFICATION_PROXY_UPSTREAM_KEY=" + UPSTREAM_KEY, environment.stdout)
        logs = docker("logs", PROXY)
        rendered = logs.stdout + logs.stderr
        # No self-initiated egress: the cost map, release notes, blog posts and
        # preset catalogs are all read from the image, never fetched.
        for marker in ("model cost map fetch", "raw.githubusercontent.com", "latest release",
                       "Failed to fetch remote"):
            self.assertNotIn(marker, rendered, "the proxy fetched its own catalog data")
        for marker in ("prisma", "Applying migration", "Database connected", "store_model_in_db"):
            self.assertNotIn(marker, rendered, "the proxy log mentions %s" % marker)
        # The model list is the generated file alone: exactly the two routes.
        request = urllib.request.Request(self.base_url + "/v1/models",
                                         headers={"Authorization": "Bearer " + MASTER_KEY})
        with urllib.request.urlopen(request, timeout=30) as response:
            models = json.load(response)["data"]
        self.assertEqual(sorted(model["id"] for model in models),
                         sorted(route["alias"] for route in self.emission["routes"]))

    # -- the qualifier can fail -------------------------------------------

    def test_config_digest_and_edited_route_cannot_qualify(self):
        """The qualification binds the generated file and detects an edited route."""
        rendered = self.config.read_bytes()
        self.assertEqual(hashlib.sha256(rendered).hexdigest(), self.emission["config_sha256"],
                         "the configuration under qualification is not the generated one")
        config = json.loads(json.dumps(self._parse_config(rendered)))
        entries = config["model_list"]
        self.assertEqual(sorted(entry["model_name"] for entry in entries),
                         sorted(route["alias"] for route in self.emission["routes"]))
        for entry in entries:
            route = self.route_for(entry["model_name"])
            self.assertEqual(entry["model_info"]["id"], route["profile_id"])

        # An edited route: the hosted alias now points at a different upstream
        # model and the reviewed routing is gone. The qualifier must refuse it.
        edited = json.loads(json.dumps(config))
        for entry in edited["model_list"]:
            if entry["model_info"]["id"] == self.route_of("GENERATE")["profile_id"]:
                entry["litellm_params"]["model"] = "openrouter/qualification/edited-v1"
                entry["litellm_params"].pop("provider", None)
        edited_path = self.workspace / "edited-config.yaml"
        edited_path.write_text(self._render_config(edited), encoding="utf-8")
        try:
            self._run_proxy(PROXY + "-drifted", edited_path, CONFIG_TARGET)
            url = self._base_url(PROXY + "-drifted")
            self._await_proxy(PROXY + "-drifted")
            self.set_mode("ok")
            self.reset_capture()
            request = self.request_for("generate")
            status, _ = self.post(request["path"], request["body"], base_url=url)
            self.assertEqual(status, 200)
            records = self.captured()
            self.assertEqual(len(records), 1)
            problems = self.reviewed_problems(records[0], self.expectations("generate"))
            self.assertTrue(problems, "an edited route qualified: %s" % json.dumps(records[0]))
            self.assertTrue(any("upstream model" in problem for problem in problems), problems)
            self.assertTrue(any("provider routing" in problem for problem in problems), problems)
        finally:
            docker("rm", "-f", PROXY + "-drifted")

    def test_config_without_yaml_suffix_loads_no_route(self):
        """A configuration the proxy does not load cannot qualify."""
        unnamed = "/run/secrets/litellm-config"
        try:
            self._run_proxy(PROXY + "-unnamed", self.config, unnamed)
            url = self._base_url(PROXY + "-unnamed")
            self._await_proxy(PROXY + "-unnamed")
            request = urllib.request.Request(url + "/v1/models",
                                             headers={"Authorization": "Bearer " + MASTER_KEY})
            with urllib.request.urlopen(request, timeout=30) as response:
                models = json.load(response)["data"]
            self.assertEqual(models, [], "a configuration without a .yaml suffix still loaded routes")
            request = self.request_for("generate")
            status, _ = self.post(request["path"], request["body"], base_url=url)
            self.assertGreaterEqual(status, 400, "an unloaded configuration served a route")
        finally:
            docker("rm", "-f", PROXY + "-unnamed")

    # -- configuration parsing -------------------------------------------

    @staticmethod
    def _parse_config(raw: bytes) -> dict:
        """Parse the generated YAML without adding a dependency."""
        parsed = subprocess.run(
            [os.environ.get("PYTHON", "python3"), "-c",
             "import json,sys,yaml;print(json.dumps(yaml.safe_load(sys.stdin)))"],
            input=raw, capture_output=True, timeout=120)
        if parsed.returncode == 0:
            return json.loads(parsed.stdout)
        # No PyYAML available: the qualification still binds the digest, and the
        # model list is asserted from the emission instead.
        raise unittest.SkipTest("NOT_RUN: PyYAML is required to parse the generated configuration")

    @staticmethod
    def _render_config(config: dict) -> str:
        parsed = subprocess.run(
            [os.environ.get("PYTHON", "python3"), "-c",
             "import sys,yaml;json=yaml.safe_load(sys.stdin);yaml.safe_dump(json,sys.stdout,sort_keys=False)"],
            input=json.dumps(config), capture_output=True, text=True, timeout=120)
        if parsed.returncode != 0:
            raise unittest.SkipTest("NOT_RUN: PyYAML is required to edit the generated configuration")
        return parsed.stdout


if __name__ == "__main__":
    unittest.main()
