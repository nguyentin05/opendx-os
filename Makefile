# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

.PHONY: help check-hygiene test clean

help:
	@echo "OpenDX-OS (DX-OS Open-Core) Build & Verification Targets:"
	@echo "  make check-hygiene  Verify required FOSS metadata files, UTF-8/LF line endings, and SPDX headers"
	@echo "  make test           Run available module test suites and report status per module"
	@echo "  make clean          Remove temporary build and dist artifacts"

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

test: check-hygiene
	@set -e; \
	if [ -d operator ] && [ -f operator/go.mod ]; then \
		echo "[test] Running operator tests..."; \
		(cd operator && go test ./... -v); \
	else \
		echo "[test] operator/: NOT_APPLICABLE (module not yet created)"; \
	fi; \
	if [ -d capabilities ] && [ -f capabilities/go.mod ]; then \
		echo "[test] Running capability tests..."; \
		(cd capabilities && go test ./... -v); \
	else \
		echo "[test] capabilities/: NOT_APPLICABLE (module not yet created)"; \
	fi; \
	if [ -d tests ]; then \
		echo "[test] Running integration and contract tests..."; \
		if command -v pytest >/dev/null 2>&1; then \
			pytest tests/ -v; \
		else \
			python3 -m unittest discover -s tests/ -v; \
		fi; \
	else \
		echo "[test] tests/: NOT_APPLICABLE (no tests dir)"; \
	fi; \
	if [ -d workspace-ui ] && [ -d workspace-ui/node_modules ]; then \
		echo "[test] Running workspace-ui tests..."; \
		(cd workspace-ui && npm test && npm run build); \
	else \
		echo "[test] workspace-ui/: SKIPPED (run 'npm --prefix workspace-ui install' to enable UI suite)"; \
	fi

clean:
	rm -rf dist/ build/ coverage/
