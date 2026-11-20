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

.PHONY: help check check-hygiene check-architecture test test-unit test-integration clean

help:
	@echo "OpenDX-OS (DX-OS Open-Core) Build & Verification Targets:"
	@echo "  make check              Run the static repository checks"
	@echo "  make check-hygiene      Verify open-source metadata files, SPDX headers, and text encoding"
	@echo "  make check-architecture Verify Tier 1 and Tier 2 runtime code stays domain-agnostic"
	@echo "  make test               Run available module unit and contract test suites"
	@echo "  make test-unit          Run fast unit and contract tests without external service dependencies"
	@echo "  make test-integration   Run real-database and service integration suites (requires TEST_DATABASE_URL)"
	@echo "  make clean              Remove temporary build and dist artifacts"

check: check-hygiene check-architecture

check-hygiene:
	@python3 scripts/check_hygiene.py

check-architecture:
	@python3 scripts/check_architecture.py

test-unit: check
	@set -e; \
	echo "[test-unit] Running operator unit tests..."; \
	(cd operator && go test ./... -count=1); \
	echo "[test-unit] Running capability unit tests..."; \
	(cd capabilities && go test ./... -count=1); \
	echo "[test-unit] Running contract and hygiene tests..."; \
	python3 -m unittest discover -s tests/ -p "test_*.py" -v; \
	if [ -d workspace-ui ] && [ -d workspace-ui/node_modules ]; then \
		echo "[test-unit] Running workspace-ui tests..."; \
		(cd workspace-ui && npm test && npm run build); \
	elif [ "$$CI" = "true" ]; then \
		echo "[test-unit] FAIL: workspace-ui/node_modules is missing and CI=true; run 'npm --prefix workspace-ui ci' first"; \
		exit 1; \
	else \
		echo "[test-unit] workspace-ui/: SKIPPED (run 'npm --prefix workspace-ui install' to enable UI suite)"; \
	fi

test-integration:
	@set -e; \
	if [ -z "$$TEST_DATABASE_URL" ]; then \
		echo "FAIL: TEST_DATABASE_URL is required for make test-integration"; \
		exit 1; \
	fi; \
	(cd operator && go test -tags=integration ./... -count=1 -v); \
	(cd capabilities && go test -tags=integration ./... -count=1 -v)

test: test-unit

clean:
	rm -rf dist/ build/ coverage/
