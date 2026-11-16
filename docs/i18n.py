# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Bilingual site: Vietnamese at the site root, English under /en/.

English pages (``page.md``) are the source of truth. A Vietnamese translation
sits next to its source as ``page.vi.md``. ``mkdocs.yml`` builds the Vietnamese
site: a translation replaces its English page, untranslated pages fall back to
English, and navigation titles come from ``extra.nav_vi``. ``mkdocs.en.yml``
inherits that configuration, excludes translations, and is built into ``en/``
right after the main build, so ``mkdocs build`` and ``mkdocs serve`` produce both.
"""

from __future__ import annotations

import os
from pathlib import Path
from urllib.parse import urlsplit

import yaml
from mkdocs.commands.build import build
from mkdocs.config import load_config
from mkdocs.exceptions import PluginError
from mkdocs.plugins import event_priority

SUFFIX = ".vi.md"

NOTES = {
    "untranslated": (
        '!!! info "Chưa có bản tiếng Việt"\n\n'
        "    Trang này hiện chỉ có bằng tiếng Anh.\n\n"
    ),
    "stale": (
        '!!! warning "Bản dịch có thể đã cũ"\n\n'
        "    Bản tiếng Anh đã được cập nhật sau lần dịch gần nhất. Khi hai bản khác\n"
        '    nhau, <a href="{link}">bản tiếng Anh</a> là bản chuẩn.\n\n'
    ),
}


def _vietnamese(config) -> bool:
    return config.theme["language"] == "vi"


def _roots(config) -> tuple[str, str]:
    """Absolute paths of the Vietnamese and English site roots, e.g. /opendx-os/."""
    base = urlsplit(config.site_url or "/").path
    vi = base if _vietnamese(config) else base[: -len("en/")]
    return vi, vi + "en/"


def _front_matter(path: Path) -> dict:
    text = path.read_text(encoding="utf-8")
    if not text.startswith("---\n"):
        return {}
    end = text.find("\n---", 4)
    if end == -1:
        return {}
    return yaml.safe_load(text[4:end]) or {}


@event_priority(100)  # before plugins such as llmstxt read the file list
def on_files(files, config):
    if not _vietnamese(config):
        # Excluded files still sit in the list; drop translations entirely.
        for file in list(files):
            if file.src_uri.endswith(SUFFIX):
                files.remove(file)
        return files
    for file in list(files):
        if not file.src_uri.endswith(SUFFIX) or file.inclusion.is_excluded():
            continue
        source_uri = file.src_uri[: -len(SUFFIX)] + ".md"
        source = files.get_file_from_path(source_uri)
        if source is None:
            raise PluginError(f"{file.src_uri} has no English source {source_uri}")
        files.remove(source)
        files.remove(file)  # Files indexes by src_uri; re-add after renaming
        file.abs_src_path = file.abs_src_path  # keep reading the .vi.md file
        file.src_uri = source_uri  # take over the source page's URL and nav entry
        file.dest_uri, file.url = source.dest_uri, source.url  # drop cached .vi paths
        file.translation = True
        files.append(file)
    return files


def on_nav(nav, config, files):
    if not _vietnamese(config):
        return nav
    titles = config.extra.get("nav_vi") or {}

    def walk(items):
        for item in items:
            if item.title in titles:
                item.title = titles[item.title]
            walk(getattr(item, "children", None) or [])

    walk(nav.items)
    return nav


def on_page_markdown(markdown, page, config, files):
    if not _vietnamese(config):
        return markdown
    if not getattr(page.file, "translation", False):
        return NOTES["untranslated"] + markdown
    source = _front_matter(Path(page.file.abs_src_path[: -len(SUFFIX)] + ".md"))
    if str(source.get("last_reviewed", "")) > str(page.meta.get("source_reviewed", "")):
        return NOTES["stale"].format(link=_roots(config)[1] + page.url) + markdown
    return markdown


def on_page_context(context, page, config, nav):
    if getattr(page.file, "translation", False) and page.edit_url:
        page.edit_url = page.edit_url[: -len(".md")] + SUFFIX
    # Absolute paths: the theme's `url` filter keeps them as they are.
    vi, en = _roots(config)
    config.extra["alternate"] = [
        {"name": "Tiếng Việt", "link": vi + page.url, "lang": "vi"},
        {"name": "English", "link": en + page.url, "lang": "en"},
    ]
    return context


def on_post_build(config):
    if not _vietnamese(config) or os.environ.get("DOCS_SKIP_EN") == "1":
        return
    english = load_config(
        config_file=str(Path(config.config_file_path).with_name("mkdocs.en.yml")),
        site_dir=os.path.join(config.site_dir, "en"),
    )
    english.plugins.on_startup(command="build", dirty=False)
    try:
        build(english)
    finally:
        english.plugins.on_shutdown()
    # The landing page is Vietnamese only; send /en/ to it.
    Path(english.site_dir, "index.html").write_text(
        '<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0; url=../">'
        '<link rel="canonical" href="../"><a href="../">OpenDX-OS</a>\n',
        encoding="utf-8",
    )
