# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Render a status banner from page front matter.

Material for MkDocs exposes page metadata to hooks. Keeping the banner in a
hook avoids overriding theme templates, so theme upgrades do not break it.
"""

KINDS = {"draft": "note", "proposed": "info", "deprecated": "warning", "historical": "danger"}

TEXT = {
    "en": {
        "labels": {"draft": "Draft", "proposed": "Proposed", "deprecated": "Deprecated", "historical": "Historical"},
        "body": "This page is marked **{label}**. Check the current contracts and\n"
        "    implementation before relying on it.",
    },
    "vi": {
        "labels": {"draft": "Bản nháp", "proposed": "Đề xuất", "deprecated": "Đã ngừng dùng", "historical": "Lưu trữ"},
        "body": "Trang này đang ở trạng thái **{label}**. Hãy đối chiếu với contract và\n"
        "    implementation hiện tại trước khi dựa vào nội dung.",
    },
}


def on_page_markdown(markdown, page, config, files):
    meta = page.meta or {}
    # Material shows its own nav icon for any `status`; the banner replaces it.
    status = meta.pop("status", None)
    if status not in KINDS:
        return markdown

    text = TEXT.get(config.theme["language"], TEXT["en"])
    label = text["labels"][status]
    body = text["body"].format(label=label.lower())
    return f'!!! {KINDS[status]} "{label}"\n\n    {body}\n\n' + markdown
