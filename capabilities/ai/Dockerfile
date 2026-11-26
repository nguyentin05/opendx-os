# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
FROM ghcr.io/astral-sh/uv:0.13.3 AS uv

FROM python:3.12-slim-bookworm
COPY --from=uv /uv /usr/local/bin/uv
RUN apt-get update && apt-get install -y --no-install-recommends \
      libgl1 libglib2.0-0 tesseract-ocr tesseract-ocr-eng \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home dxos
WORKDIR /app
ENV UV_PYTHON_DOWNLOADS=never UV_COMPILE_BYTECODE=1 PATH="/app/.venv/bin:$PATH"
COPY pyproject.toml uv.lock ./
RUN uv sync --locked --no-dev --no-install-project
RUN hf download docling-project/docling-layout-heron \
      --revision 8f39ad3c0b4c58e9c2d2c84a38465abf757272d8 \
      --include '*.json' '*.safetensors' LICENSE \
      --local-dir /opt/docling-models/docling-project--docling-layout-heron
ENV HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 HAYSTACK_TELEMETRY_ENABLED=false LANGSMITH_TRACING=false
COPY alembic.ini ./
COPY migrations ./migrations
COPY src ./src
RUN uv sync --locked --no-dev --no-editable
USER dxos
EXPOSE 8084
CMD ["uvicorn", "dxos_ai.main:create_app", "--factory", "--host", "0.0.0.0", "--port", "8084"]
