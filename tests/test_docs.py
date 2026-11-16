# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Documentation checks for the MkDocs site.

The tests enforce the published-documentation contract:

* every public page is reachable from navigation;
* every Vietnamese translation has an English source and records the source
  revision it was translated from, and the English site excludes translations;
* internal pages are excluded and never linked from public pages;
* every public page carries valid front matter;
* relative links resolve;
* the ADR index lists every decision record;
* public pages do not reintroduce stale data-model names;
* every redirect target exists.
"""

from __future__ import annotations

import fnmatch
import re
import unittest
from datetime import date
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs"
MKDOCS = ROOT / "mkdocs.yml"
MKDOCS_EN = ROOT / "mkdocs.en.yml"
TRANSLATION = ".vi.md"

DIATAXIS = {"tutorial", "how-to", "reference", "explanation"}
STATUSES = {"draft", "proposed", "active", "deprecated", "historical"}
REQUIRED_META = {
    "title",
    "description",
    "diataxis",
    "audience",
    "status",
    "owner",
    "last_reviewed",
}

# ADRs are historical records; their original vocabulary is preserved.
STALE_TERM_ALLOWLIST = ("adr/",)

STALE_TERMS = (
    "workspace_id",
    "core.workspaces",
    "core.file_objects",
    "core.app_registry",
    "core.app_definitions",
    "core.capability_registry",
    "core.employees",
    "core.work_items",
    "core.command_receipts",
    "core.processed_workflow_steps",
)


class MkDocsLoader(yaml.SafeLoader):
    """SafeLoader that tolerates MkDocs' python/name YAML tag."""


def _construct_python_name(loader, tag_suffix, node):
    if isinstance(node, yaml.ScalarNode):
        return loader.construct_scalar(node)
    return None


MkDocsLoader.add_multi_constructor(
    "tag:yaml.org,2002:python/name:", _construct_python_name
)


MkDocsLoader.add_constructor("!ENV", lambda loader, node: None)


def load_config(path: Path = MKDOCS) -> dict:
    with path.open(encoding="utf-8") as handle:
        return yaml.load(handle, Loader=MkDocsLoader) or {}


def nav_targets(node) -> list[str]:
    targets: list[str] = []
    if isinstance(node, str):
        targets.append(node)
    elif isinstance(node, list):
        for item in node:
            targets.extend(nav_targets(item))
    elif isinstance(node, dict):
        for value in node.values():
            targets.extend(nav_targets(value))
    return targets


def excluded_patterns(config: dict) -> list[str]:
    raw = config.get("exclude_docs") or ""
    lines = (line.strip() for line in raw.splitlines())
    return [line for line in lines if line and not line.startswith("#")]


def is_excluded(rel: str, patterns: list[str]) -> bool:
    for pattern in patterns:
        if pattern.startswith("/"):
            pattern = pattern[1:]
        if pattern.endswith("/"):
            if rel == pattern[:-1] or rel.startswith(pattern):
                return True
        elif rel == pattern or fnmatch.fnmatch(rel, pattern):
            return True
    return False


def public_pages(patterns: list[str]) -> dict[str, Path]:
    pages: dict[str, Path] = {}
    for path in sorted(DOCS.rglob("*.md")):
        rel = path.relative_to(DOCS).as_posix()
        if not is_excluded(rel, patterns):
            pages[rel] = path
    return pages


def front_matter(path: Path) -> dict | None:
    text = path.read_text(encoding="utf-8")
    if not text.startswith("---\n"):
        return None
    end = text.find("\n---", 4)
    if end == -1:
        return None
    data = yaml.safe_load(text[4:end])
    return data if isinstance(data, dict) else None


def markdown_links(path: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    text = re.sub(r"`[^`]*`", "", text)
    return re.findall(r"\[[^\]]*\]\(([^)\s]+)(?:\s+\"[^\"]*\")?\)", text)


def resolve_link(source: Path, target: str) -> Path | None:
    if not target or target.startswith(("#", "http://", "https://", "mailto:")):
        return None
    if "{{" in target or "{%" in target:
        return None
    target = target.split("#", 1)[0].split("?", 1)[0]
    if not target:
        return None
    if target.startswith("/"):
        return DOCS / target.lstrip("/")
    resolved = (source.parent / target).resolve()
    if target.endswith("/"):
        return resolved / "index.md"
    if not resolved.suffix:
        return resolved / "index.md"
    return resolved


def redirect_maps(config: dict) -> dict[str, str]:
    for plugin in config.get("plugins", []):
        if isinstance(plugin, dict) and "redirects" in plugin:
            value = plugin["redirects"] or {}
            return value.get("redirect_maps") or {}
    return {}


class DocsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.config = load_config()
        cls.patterns = excluded_patterns(cls.config)
        cls.pages = public_pages(cls.patterns)
        # Translations are reached through their source page.
        cls.sources = {
            rel: path for rel, path in cls.pages.items() if not rel.endswith(TRANSLATION)
        }
        cls.translations = {
            rel: path for rel, path in cls.pages.items() if rel.endswith(TRANSLATION)
        }
        cls.nav = nav_targets(cls.config.get("nav", []))

    def test_nav_targets_exist(self):
        for target in self.nav:
            if target.startswith(("http://", "https://")):
                continue
            with self.subTest(target=target):
                self.assertTrue(
                    (DOCS / target).is_file(),
                    f"navigation target does not exist: {target}",
                )

    def test_nav_reaches_every_public_page(self):
        targets = set(self.nav)
        for rel in self.sources:
            with self.subTest(page=rel):
                self.assertIn(rel, targets, f"{rel} is not reachable from nav")

    def test_internal_docs_are_excluded(self):
        self.assertTrue(
            any("_internal" in pattern for pattern in self.patterns),
            "mkdocs.yml must exclude docs/_internal/",
        )
        self.assertTrue((DOCS / "_internal" / "README.md").is_file())

    def test_public_pages_have_front_matter(self):
        for rel, path in {**self.sources, **self.translations}.items():
            with self.subTest(page=rel):
                meta = front_matter(path)
                self.assertIsNotNone(meta, f"{rel} is missing front matter")
                missing = REQUIRED_META - set(meta)
                self.assertFalse(missing, f"{rel} is missing {sorted(missing)}")
                self.assertIn(meta["diataxis"], DIATAXIS)
                self.assertIn(meta["status"], STATUSES)
                self.assertIsInstance(meta["audience"], list)
                self.assertTrue(meta["audience"])
                self.assertIsInstance(meta["owner"], str)
                self.assertTrue(meta["owner"].strip())
                reviewed = str(meta["last_reviewed"])
                self.assertRegex(reviewed, r"^\d{4}-\d{2}-\d{2}$")
                date.fromisoformat(reviewed)

    def test_translations_have_source_and_revision(self):
        for rel, path in self.translations.items():
            with self.subTest(page=rel):
                source = rel[: -len(TRANSLATION)] + ".md"
                self.assertIn(source, self.sources, f"{rel} has no English source")
                reviewed = str((front_matter(path) or {}).get("source_reviewed", ""))
                self.assertRegex(reviewed, r"^\d{4}-\d{2}-\d{2}$")
                date.fromisoformat(reviewed)

    def test_english_site_excludes_translations(self):
        english = excluded_patterns(load_config(MKDOCS_EN))
        # English skips translations and the Vietnamese-only landing page.
        self.assertEqual(set(english) - set(self.patterns), {"*.vi.md", "/index.html"})
        self.assertFalse(set(self.patterns) - set(english))

    def test_public_pages_do_not_link_internal(self):
        for rel, path in self.pages.items():
            for target in markdown_links(path):
                with self.subTest(page=rel, target=target):
                    self.assertNotIn("_internal", target)

    def test_public_pages_links_resolve(self):
        for rel, path in self.pages.items():
            for target in markdown_links(path):
                resolved = resolve_link(path, target)
                if resolved is None:
                    continue
                with self.subTest(page=rel, target=target):
                    self.assertTrue(
                        resolved.exists(),
                        f"{rel} links to missing target: {target} ({resolved})",
                    )

    def test_adr_index_lists_all_adrs(self):
        index = (DOCS / "adr" / "index.md").read_text(encoding="utf-8")
        adrs = sorted(
            path.name
            for path in (DOCS / "adr").glob("0*.md")
            if not path.name.endswith(".vi.md")
        )
        self.assertTrue(adrs)
        for name in adrs:
            self.assertIn(name, index, f"ADR index does not list {name}")

    def test_redirect_targets_exist(self):
        redirects = redirect_maps(self.config)
        self.assertTrue(redirects, "redirect_maps must record moved pages")
        for source, target in redirects.items():
            with self.subTest(source=source, target=target):
                self.assertTrue(
                    (DOCS / target).is_file(),
                    f"redirect target does not exist: {target}",
                )

    def test_public_pages_have_no_stale_terms(self):
        for rel, path in self.pages.items():
            if rel.startswith(STALE_TERM_ALLOWLIST):
                continue
            text = path.read_text(encoding="utf-8")
            for term in STALE_TERMS:
                with self.subTest(page=rel, term=term):
                    self.assertNotIn(term, text)


if __name__ == "__main__":
    unittest.main()
