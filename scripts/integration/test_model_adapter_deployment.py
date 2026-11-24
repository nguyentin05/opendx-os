# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Static deployment checks for the opt-in hosted model adapter.

No provider, container or live deployment is involved. Every case runs
``docker compose ... config`` and parses the resolved configuration in memory, so
it asserts presence and absence only: the fixtures are disposable canaries and no
case prints a resolved configuration or a credential value.

The checks cover the four deployment risks of the optional bundled adapter: it is
opt-in, it is the only service that receives a vendor key, it publishes no port
and trusts one documented CA, and an existing external-endpoint installation is
unchanged.
"""

from __future__ import annotations

import json
import os
import runpy
import stat
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
ADAPTER_OVERLAY = "compose.model-adapter.yaml"
BASE_FILES = ("compose.yaml", "compose.rag.yaml", "compose.models.yaml")
OPT_IN_FILES = BASE_FILES + (ADAPTER_OVERLAY,)
PREFLIGHT = ROOT / "scripts" / "check_model_adapter_env.py"

# Disposable fixture values. They are canaries: a test fails if one reaches a
# transcript, and nothing here is a working credential.
INTERNAL_TOKEN = "internal-adapter-token-canary"
VENDOR_KEY = "vendor-provider-key-canary"
OVERRIDE_TOKEN = "vendor-path-token-canary"
DB_PASSWORD = "database-password-canary"
PARSER_TOKEN = "parser-token-canary"
CANARIES = (INTERNAL_TOKEN, VENDOR_KEY, OVERRIDE_TOKEN, DB_PASSWORD, PARSER_TOKEN)

# Deployment settings every overlay needs, independent of the adapter.
REQUIRED_SETTINGS = {
    "AI_DATABASE_PASSWORD": DB_PASSWORD + "-api",
    "AI_WORKER_DATABASE_PASSWORD": DB_PASSWORD + "-worker",
    "AI_DISPATCHER_DATABASE_PASSWORD": DB_PASSWORD + "-dispatcher",
    "AI_GATEWAY_DATABASE_PASSWORD": DB_PASSWORD + "-gateway",
    "PARSER_SERVICE_TOKEN": PARSER_TOKEN,
    "RAG_WORKER_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000001",
    "RAG_FILE_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000002",
    "RAG_DISPATCHER_SERVICE_SUBJECT": "33333333-3333-4333-8333-000000000003",
}

ADAPTER_CONFIG = {"schema_version": 1, "routes": []}


class ModelAdapterDeploymentTest(unittest.TestCase):
    """The adapter overlay and the unchanged external-endpoint path."""

    @classmethod
    def setUpClass(cls):
        cls.directory = tempfile.TemporaryDirectory()
        cls.workspace = Path(cls.directory.name)
        # Probe the prerequisite before creating fixtures, so a machine without the
        # compose plugin reports NOT_RUN instead of a misleading failure.
        docker = shutil.which("docker")
        if docker is None:
            raise unittest.SkipTest("NOT_RUN: docker with the compose plugin is required")
        probe = subprocess.run([docker, "compose", "version"], capture_output=True, text=True)
        if probe.returncode != 0:
            raise unittest.SkipTest("NOT_RUN: the docker compose plugin is required")

        cls.internal_env = cls.write(
            "internal.env", "MODEL_ADAPTER_TOKEN=" + INTERNAL_TOKEN + "\n")
        cls.provider_env = cls.write("provider.env", "OPENAI_API_KEY=" + VENDOR_KEY + "\n")
        cls.colliding_provider_env = cls.write(
            "provider-collision.env",
            "OPENAI_API_KEY=" + VENDOR_KEY + "\nMODEL_ADAPTER_TOKEN=" + OVERRIDE_TOKEN + "\n")
        cls.adapter_config = cls.write("adapter.json", json.dumps(ADAPTER_CONFIG))
        cls.tls_cert = cls.write("tls.crt", "certificate fixture, not a real certificate\n")
        cls.tls_key = cls.write("tls.key", "private key fixture, not a real key\n")
        cls.ca_file = cls.write("adapter-ca.crt", "CA fixture, not a real certificate\n")
        # Host secret files are owner-only: the preflight enforces this.
        for path in (cls.internal_env, cls.provider_env, cls.colliding_provider_env,
                     cls.tls_key, cls.ca_file):
            os.chmod(path, 0o600)

    @classmethod
    def tearDownClass(cls):
        cls.directory.cleanup()

    @classmethod
    def write(cls, name, content):
        path = cls.workspace / name
        descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write(content)
        return path

    # -- helpers ---------------------------------------------------------

    def settings(self, overrides=None, omit=()):
        """Every deployment setting, with opt-in adapter paths by default."""
        values = dict(REQUIRED_SETTINGS)
        values.update({
            "RAG_MODEL_CATALOG_FILE": str(self.adapter_config),
            "RAG_MODEL_CREDENTIAL_ENV_FILE": str(self.internal_env),
            "RAG_MODEL_ADAPTER_CREDENTIAL_ENV_FILE": str(self.provider_env),
            "RAG_MODEL_ADAPTER_CONFIG_FILE": str(self.adapter_config),
            "RAG_MODEL_ADAPTER_TLS_CERT_FILE": str(self.tls_cert),
            "RAG_MODEL_ADAPTER_TLS_KEY_FILE": str(self.tls_key),
            "RAG_MODEL_ADAPTER_CA_FILE": str(self.ca_file),
        })
        for name in omit:
            values.pop(name, None)
        values.update(overrides or {})
        return values

    def compose(self, files, settings=None, quiet=False):
        """Run compose config with fixture settings in its process environment."""
        arguments = ["docker", "compose", "--env-file", os.devnull]
        for name in files:
            arguments += ["-f", name]
        arguments += ["config", "--quiet"] if quiet else ["config", "--format", "json"]
        return subprocess.run(arguments, cwd=ROOT, env={**os.environ, **settings},
                              capture_output=True, text=True)

    def config(self, files, settings=None):
        """The resolved configuration, asserted to be clean of leaked values."""
        result = self.compose(files, settings or self.settings())
        for canary in CANARIES:
            self.assertNotIn(canary, result.stderr, "compose reported a fixture value")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        return json.loads(result.stdout)

    def preflight(self, provider, internal=None):
        return subprocess.run(
            [sys.executable, str(PREFLIGHT),
             "--provider-env", str(provider),
             "--internal-env", str(internal or self.internal_env)],
            cwd=ROOT, capture_output=True, text=True)

    # -- cases -----------------------------------------------------------

    def test_bundled_adapter_opt_in(self):
        """The adapter exists only with its own overlay, built by the shared image."""
        legacy = self.config(BASE_FILES)
        self.assertNotIn("cap-ai-model-adapter", legacy["services"])

        opt_in = self.config(OPT_IN_FILES)
        adapter = opt_in["services"]["cap-ai-model-adapter"]
        build = adapter["build"]
        self.assertEqual(Path(build["context"]).resolve(), (ROOT / "capabilities").resolve())
        self.assertEqual(build["dockerfile"], "Dockerfile")
        self.assertEqual(build["args"]["SERVICE"], "cap-ai-model-adapter")
        self.assertEqual(Path(build["additional_contexts"]["legal"]).resolve(), ROOT.resolve())
        self.assertEqual(adapter["restart"], "unless-stopped")
        self.assertFalse(adapter.get("command"))
        self.assertFalse(adapter.get("privileged"))
        environment = adapter["environment"]
        self.assertEqual(environment["MODEL_ADAPTER_CONFIG_FILE"], "/run/secrets/model-adapter-config")
        self.assertEqual(environment["MODEL_ADAPTER_TLS_CERT_FILE"], "/run/secrets/model-adapter-tls-cert")
        self.assertEqual(environment["MODEL_ADAPTER_TLS_KEY_FILE"], "/run/secrets/model-adapter-tls-key")
        self.assertEqual(environment["MODEL_ADAPTER_LISTEN"], ":8443")

        # The internal token file is loaded last, so a provider file that wrongly
        # repeats MODEL_ADAPTER_TOKEN cannot override the deployment's own token.
        colliding = self.config(OPT_IN_FILES, self.settings({
            "RAG_MODEL_ADAPTER_CREDENTIAL_ENV_FILE": str(self.colliding_provider_env)}))
        self.assertEqual(colliding["services"]["cap-ai-model-adapter"]["environment"]["MODEL_ADAPTER_TOKEN"],
                         INTERNAL_TOKEN)

    def test_adapter_secret_isolation(self):
        """Only the adapter receives the vendor key; nothing else gains a credential."""
        config = self.config(OPT_IN_FILES)
        adapter = config["services"]["cap-ai-model-adapter"]["environment"]
        self.assertEqual(adapter["OPENAI_API_KEY"], VENDOR_KEY)
        self.assertEqual(adapter["MODEL_ADAPTER_TOKEN"], INTERNAL_TOKEN)
        for name in ("DATABASE_URL", "AI_MODEL_CATALOG_DATABASE_URL", "AI_MODEL_ADMIN_DATABASE_URL",
                     "AI_MODEL_APPROVAL_DATABASE_URL", "OIDC_ISSUER_URL", "OIDC_JWKS_URL",
                     "OIDC_AUDIENCE", "PARSER_SERVICE_TOKEN", "SSL_CERT_FILE"):
            self.assertNotIn(name, adapter, "the adapter holds %s" % name)
        for name, value in adapter.items():
            self.assertNotIn("postgres://", value, "the adapter holds a DSN in %s" % name)
            self.assertNotIn("postgresql://", value, "the adapter holds a DSN in %s" % name)

        # The Gateway and the worker receive the internal token and no vendor key,
        # and they never load the provider credential file.
        for name in ("cap-ai", "cap-ai-worker"):
            environment = config["services"][name]["environment"]
            self.assertEqual(environment["MODEL_ADAPTER_TOKEN"], INTERNAL_TOKEN)
            rendered = json.dumps(environment)
            self.assertNotIn("OPENAI_API_KEY", environment)
            self.assertNotIn(VENDOR_KEY, rendered)
            for marker in ("dxos_ai_model_catalog", "dxos_ai_model_admin", "dxos_authz", "dxos_ai_owner"):
                self.assertNotIn(marker, rendered, "%s gained %s" % (name, marker))

        # No other service receives the vendor key either.
        for name, service in config["services"].items():
            if name == "cap-ai-model-adapter":
                continue
            self.assertNotIn(VENDOR_KEY, json.dumps(service.get("environment") or {}),
                             "%s received the vendor key" % name)

        # The resolved configuration stayed in memory: the helper asserts an empty
        # stderr for every render, and a quiet render writes nothing at all.
        quiet = self.compose(OPT_IN_FILES, self.settings(), quiet=True)
        self.assertEqual(quiet.returncode, 0, quiet.stderr)
        self.assertEqual(quiet.stdout, "")
        for canary in CANARIES:
            self.assertNotIn(canary, quiet.stderr)

    def test_adapter_tls_and_no_public_port(self):
        """No published port, read-only secret material, one documented CA."""
        config = self.config(OPT_IN_FILES)
        adapter = config["services"]["cap-ai-model-adapter"]
        self.assertFalse(adapter.get("ports"), "the adapter publishes a host port")
        self.assertFalse(adapter.get("expose"), "the adapter exposes a port")
        self.assertFalse(adapter.get("network_mode"), "the adapter overrides its network")
        self.assertTrue(adapter["read_only"])
        self.assertEqual(adapter["cap_drop"], ["ALL"])
        self.assertEqual(adapter["security_opt"], ["no-new-privileges:true"])

        # Configuration, certificate and key are file secrets at the paths the
        # service reads. Docker mounts a secret read-only, and the adapter declares
        # no volume mount at all, so it has no writable path of its own.
        secret_targets = {item["target"]: item["source"] for item in adapter["secrets"]}
        self.assertEqual(secret_targets, {
            "/run/secrets/model-adapter-config": "model-adapter-config",
            "/run/secrets/model-adapter-tls-cert": "model-adapter-tls-cert",
            "/run/secrets/model-adapter-tls-key": "model-adapter-tls-key",
        })
        self.assertFalse(adapter.get("volumes"), "the adapter declares a volume mount")
        secrets = config["secrets"]
        self.assertEqual(Path(secrets["model-adapter-config"]["file"]).resolve(), self.adapter_config.resolve())
        self.assertEqual(Path(secrets["model-adapter-tls-cert"]["file"]).resolve(), self.tls_cert.resolve())
        self.assertEqual(Path(secrets["model-adapter-tls-key"]["file"]).resolve(), self.tls_key.resolve())

        # The Gateway and worker trust the adapter through one read-only CA bundle,
        # and never through an insecure switch.
        for name in ("cap-ai", "cap-ai-worker"):
            service = config["services"][name]
            mounts = [mount for mount in service.get("volumes") or []
                      if mount["target"] == "/run/secrets/model-adapter-ca"]
            self.assertEqual(len(mounts), 1, "%s lacks the adapter CA mount" % name)
            self.assertEqual(mounts[0]["type"], "bind")
            self.assertTrue(mounts[0]["read_only"], "%s mounts the CA writable" % name)
            self.assertEqual(Path(mounts[0]["source"]).resolve(), self.ca_file.resolve())
            self.assertEqual(service["environment"]["SSL_CERT_FILE"], "/run/secrets/model-adapter-ca")
        rendered = json.dumps(config)
        for weakened in ("insecure_skip_verify", "MODEL_ADAPTER_INSECURE", "SSL_CERT_FILE=/dev/null"):
            self.assertNotIn(weakened, rendered)

        # The certificate hostname and bundle construction are documented, not
        # guessed by the operator.
        guide = (ROOT / "docs" / "guides" / "knowledge" / "models.md").read_text(encoding="utf-8")
        self.assertIn("DNS:cap-ai-model-adapter", guide)
        self.assertIn("subjectAltName", guide)
        self.assertIn("cat /etc/ssl/certs/ca-certificates.crt", guide)
        self.assertIn("SSL_CERT_FILE", guide)
        # The adapter and the two trusting services run as UID 10001, so every
        # host file a container reads must be owned and readable by that UID. The
        # CA bundle is easy to miss because it is not secret: an operator-only 0600
        # bundle would leave cap-ai and cap-ai-worker unable to verify the adapter.
        chown = next(line for line in guide.splitlines() if line.startswith("chown 10001:10001"))
        chmod = next(line for line in guide.splitlines() if line.startswith("chmod 0400"))
        for path in ("adapter-tls.key", "adapter-tls.crt", "model-adapter.json",
                     "adapter-ca-bundle.crt"):
            self.assertIn(path, chown, "the guide does not chown %s" % path)
            self.assertIn(path, chmod, "the guide does not chmod %s" % path)

    def test_legacy_models_overlay_unchanged(self):
        """An existing external-endpoint installation keeps its behavior."""
        config = self.config(BASE_FILES)
        self.assertNotIn("cap-ai-model-adapter", config["services"])
        for name in ("cap-ai", "cap-ai-worker"):
            service = config["services"][name]
            self.assertEqual(service["environment"]["RAG_MODEL_DEPLOYMENT_FILE"], "/run/secrets/model-catalog")
            self.assertNotIn("SSL_CERT_FILE", service["environment"])
            targets = {mount["target"] for mount in service.get("volumes") or []}
            self.assertNotIn("/run/secrets/model-adapter-ca", targets)
        for name, service in config["services"].items():
            environment = service.get("environment") or {}
            for setting in ("MODEL_ADAPTER_CONFIG_FILE", "MODEL_ADAPTER_TLS_CERT_FILE",
                            "MODEL_ADAPTER_TLS_KEY_FILE", "MODEL_ADAPTER_LISTEN"):
                self.assertNotIn(setting, environment, "%s has the opt-in setting %s" % (name, setting))
        self.assertIn("model-catalog", config["secrets"])

        # compose.models.yaml still describes the external endpoint path, and the
        # opt-in overlay is a separate file.
        models = (ROOT / "compose.models.yaml").read_text(encoding="utf-8")
        self.assertIn("RAG_MODEL_DEPLOYMENT_FILE", models)
        self.assertIn("RAG_MODEL_CREDENTIAL_ENV_FILE", models)
        self.assertNotIn("cap-ai-model-adapter", models)
        self.assertNotIn("SSL_CERT_FILE", models)

    def test_missing_opt_in_settings_fail_safely(self):
        """A missing path stops Compose with a static message and no value."""
        for setting in ("RAG_MODEL_ADAPTER_CONFIG_FILE", "RAG_MODEL_ADAPTER_TLS_CERT_FILE",
                        "RAG_MODEL_ADAPTER_TLS_KEY_FILE", "RAG_MODEL_ADAPTER_CA_FILE",
                        "RAG_MODEL_ADAPTER_CREDENTIAL_ENV_FILE"):
            with self.subTest(setting=setting):
                result = self.compose(OPT_IN_FILES, self.settings(omit=(setting,)), quiet=True)
                self.assertNotEqual(result.returncode, 0, "%s was accepted" % setting)
                self.assertEqual(result.stdout, "")
                self.assertIn(setting, result.stderr)
                for canary in CANARIES:
                    self.assertNotIn(canary, result.stderr)
                self.assertNotIn(str(self.tls_key), result.stderr)

    def test_reserved_provider_names_fail_safely(self):
        """The preflight rejects a provider file that could shadow the token."""
        valid = self.preflight(self.provider_env)
        self.assertEqual(valid.returncode, 0, valid.stderr)
        self.assertEqual(valid.stderr, "")
        self.assertIn("provider references=1", valid.stdout)
        for canary in CANARIES:
            self.assertNotIn(canary, valid.stdout)

        cases = {
            "internal token": "MODEL_ADAPTER_TOKEN=" + OVERRIDE_TOKEN + "\n",
            "adapter setting": "MODEL_ADAPTER_CONFIG_FILE=/run/secrets/other\n",
            "runtime DSN": "DATABASE_URL=postgres://user@db/app\n",
            "platform namespace": "AI_MODEL_ADMIN_DATABASE_URL=postgres://admin@db/app\n",
            "repeated key": "OPENAI_API_KEY=" + VENDOR_KEY + "\nOPENAI_API_KEY=" + VENDOR_KEY + "-2\n",
            "not a reference": "lowercase-key=" + VENDOR_KEY + "\n",
        }
        for name, content in cases.items():
            with self.subTest(case=name):
                provider = self.write("rejected-%s.env" % name.replace(" ", "-"), content)
                result = self.preflight(provider)
                self.assertEqual(result.returncode, 2, "case %s: %s" % (name, result.stderr))
                self.assertEqual(result.stdout, "")
                for canary in CANARIES:
                    self.assertNotIn(canary, result.stderr)
                self.assertIn("check-model-adapter-env", result.stderr)

        # The internal file carries exactly one name and stays owner-only.
        for name, content in {
            "missing": "OTHER_TOKEN=" + INTERNAL_TOKEN + "\n",
            "extra": "MODEL_ADAPTER_TOKEN=%s\nOPENAI_API_KEY=%s\n" % (INTERNAL_TOKEN, VENDOR_KEY),
            "empty": "MODEL_ADAPTER_TOKEN=\n",
        }.items():
            with self.subTest(internal=name):
                internal = self.write("internal-%s.env" % name, content)
                result = self.preflight(self.provider_env, internal)
                self.assertEqual(result.returncode, 2, "internal %s: %s" % (name, result.stderr))
                self.assertEqual(result.stdout, "")
                for canary in CANARIES:
                    self.assertNotIn(canary, result.stderr)

        # Simulate an unsafe mode without creating a readable credential file.
        preflight = runpy.run_path(str(PREFLIGHT))
        info = mock.Mock(st_mode=stat.S_IFREG | stat.S_IRUSR | stat.S_IRGRP,
                         st_size=self.provider_env.stat().st_size)
        with mock.patch.object(os, "stat", return_value=info):
            with self.assertRaisesRegex(preflight["Rejected"], "must not grant group or other access"):
                preflight["read_pairs"](self.provider_env, "provider")

    def test_env_example_documents_opt_in_settings(self):
        """The sample environment names each opt-in setting with no value."""
        example = (ROOT / ".env.example").read_text(encoding="utf-8")
        for setting in ("RAG_MODEL_ADAPTER_CONFIG_FILE", "RAG_MODEL_ADAPTER_CREDENTIAL_ENV_FILE",
                        "RAG_MODEL_ADAPTER_TLS_CERT_FILE", "RAG_MODEL_ADAPTER_TLS_KEY_FILE",
                        "RAG_MODEL_ADAPTER_CA_FILE"):
            with self.subTest(setting=setting):
                self.assertRegex(example, r"(?m)^%s=\s*$" % setting)
        self.assertNotIn("BEGIN PRIVATE KEY", example)


if __name__ == "__main__":
    unittest.main()
