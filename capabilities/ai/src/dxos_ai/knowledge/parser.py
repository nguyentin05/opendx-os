# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

import logging
from dataclasses import dataclass
from functools import lru_cache
from io import BytesIO
from pathlib import Path

from dxos_ai.config import settings
from dxos_ai.knowledge.sources import MAX_BYTES, VerifiedSource

MAX_CHUNKS = 512
CHUNK_SIZE = 1800


@dataclass(frozen=True)
class Chunk:
    text: str
    section_path: tuple[str, ...]
    element_ids: tuple[str, ...]
    pages: tuple[int, ...]


@dataclass(frozen=True)
class ParsedDocument:
    chunks: list[Chunk]
    picture: bytes | None


@lru_cache(maxsize=2)
def converter(pictures: bool):
    from docling.datamodel.backend_options import MarkdownBackendOptions
    from docling.datamodel.base_models import InputFormat
    from docling.datamodel.pipeline_options import PdfPipelineOptions, TesseractCliOcrOptions
    from docling.document_converter import DocumentConverter, MarkdownFormatOption, PdfFormatOption

    logging.getLogger("docling").setLevel(logging.CRITICAL + 1)
    return DocumentConverter(
        allowed_formats=[InputFormat.PDF, InputFormat.MD],
        format_options={
            InputFormat.PDF: PdfFormatOption(pipeline_options=PdfPipelineOptions(
                artifacts_path=Path(settings().ai_parser_artifacts),
                enable_remote_services=False,
                allow_external_plugins=False,
                do_table_structure=False,
                do_ocr=True,
                ocr_options=TesseractCliOcrOptions(lang=["eng"]),
                document_timeout=140,
                generate_picture_images=pictures,
            )),
            InputFormat.MD: MarkdownFormatOption(backend_options=MarkdownBackendOptions(
                fetch_images=False,
            )),
        },
    )


def parse_document(source: VerifiedSource, *, pictures: bool = False) -> ParsedDocument:
    from docling.datamodel.base_models import ConversionStatus
    from docling.datamodel.document import DocumentStream
    from docling_core.types.doc import PictureItem, SectionHeaderItem, TableItem, TextItem

    if source.media_type == "application/pdf":
        artifacts = Path(settings().ai_parser_artifacts) / "docling-project--docling-layout-heron"
        if not (artifacts / "model.safetensors").is_file():
            raise ValueError("PARSER_ASSETS_UNAVAILABLE")
    extension = "pdf" if source.media_type == "application/pdf" else "md"
    stream = DocumentStream(name=f"source.{extension}", stream=BytesIO(source.data))
    result = converter(pictures).convert(stream, max_num_pages=100, max_file_size=MAX_BYTES)
    if result.status != ConversionStatus.SUCCESS:
        raise ValueError("PARSER_INCOMPLETE")

    document = result.document
    sections: list[str] = []
    chunks: list[Chunk] = []
    picture = None
    for item, _ in document.iterate_items():
        if isinstance(item, SectionHeaderItem):
            sections = sections[:max(0, item.level - 1)] + [item.text[:200]]
        if pictures and picture is None and isinstance(item, PictureItem):
            bitmap = item.get_image(document)
            if bitmap is not None:
                bitmap.thumbnail((512, 512))
                output = BytesIO()
                bitmap.convert("RGB").save(output, format="PNG")
                picture = output.getvalue()
        if isinstance(item, TextItem):
            body = item.text.strip()
        elif isinstance(item, TableItem):
            body = item.export_to_markdown(doc=document).strip()
        else:
            continue
        for offset in range(0, len(body), CHUNK_SIZE):
            chunks.append(Chunk(
                body[offset:offset + CHUNK_SIZE],
                tuple(sections), (item.self_ref,), tuple(sorted({p.page_no for p in item.prov})),
            ))
            if len(chunks) > MAX_CHUNKS:
                raise ValueError("PARSER_CHUNK_LIMIT")
    if not chunks:
        raise ValueError("PARSER_NO_TEXT")
    return ParsedDocument(chunks, picture)
