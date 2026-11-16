# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Render a status banner from page front matter.

Material for MkDocs exposes page metadata to hooks. Keeping the banner in a
hook avoids overriding theme templates, so theme upgrades do not break it.
"""

STATUS = {
    "draft": ("note", "Draft"),
    "proposed": ("info", "Proposed"),
    "deprecated": ("warning", "Deprecated"),
    "historical": ("danger", "Historical"),
}


def on_page_markdown(markdown, page, config, files):
    meta = page.meta or {}
    status = meta.get("status")
    if status not in STATUS:
        return markdown

    kind, label = STATUS[status]
    note = (
        f'!!! {kind} "{label}"\n\n'
        f"    This page is marked **{status}**. Check the current contracts and\n"
        f"    implementation before relying on it.\n\n"
    )
    return note + markdown
