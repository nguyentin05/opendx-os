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

.PHONY: help check-hygiene test test-unit test-integration clean

help:
	@echo "OpenDX-OS (DX-OS Open-Core) Build & Verification Targets:"
	@echo "  make check-hygiene     Verify required FOSS metadata files, UTF-8/LF line endings, and SPDX headers"
	@echo "  make test              Run available module unit and contract test suites"
	@echo "  make test-unit         Run fast unit and contract tests without external service dependencies"
	@echo "  make test-integration  Run real-database and service integration suites (requires TEST_DATABASE_URL)"
	@echo "  make clean             Remove temporary build and dist artifacts"

check-hygiene:
	@set -e; \
	echo "[check-hygiene] Checking required repository metadata files..."; \
	for f in LICENSE LICENSES/LICENSE NOTICE COPYRIGHT.txt README.md ROADMAP.md CHANGELOG.md CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md .editorconfig .gitattributes .gitignore Makefile; do \
		if [ ! -s "$$f" ]; then \
			echo "FAIL: missing or empty required file: $$f"; \
			exit 1; \
		fi; \
	done; \
	echo "[check-hygiene] Checking for CRLF line endings in tracked/text files..."; \
	if grep -Il $$'\r' LICENSE NOTICE COPYRIGHT.txt README.md CHANGELOG.md CONTRIBUTING.md CODE_OF_CONDUCT.md SECURITY.md .editorconfig .gitattributes .gitignore Makefile AGENTS.md 2>/dev/null; then \
		echo "FAIL: CRLF line endings detected"; \
		exit 1; \
	fi; \
	if [ -f scripts/check_hygiene.py ]; then \
		python3 scripts/check_hygiene.py; \
	elif [ -f scripts/check_pof.py ]; then \
		python3 scripts/check_pof.py; \
	else \
		echo "[check-hygiene] scripts/check_hygiene.py: NOT_APPLICABLE (not yet added)"; \
	fi; \
	echo "[check-hygiene] PASS"

test-unit: check-hygiene
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
