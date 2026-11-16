---
title: Yêu cầu
description: Docker, Compose và các công cụ cần có trước khi bắt đầu hướng dẫn.
diataxis: tutorial
audience: [evaluator, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
source_reviewed: 2026-11-16
---

# Yêu cầu

Máy của bạn cần có:

- Docker Engine 24 trở lên và Docker Compose v2;
- ít nhất 8 GB RAM dành cho Docker (khuyến nghị 16 GB);
- các cổng `5173`, `8080`, `8085`, `9080` và `5432` đang trống, hoặc đã đổi giá
  trị trong `.env`;
- `bash`, `curl` và `git`.

Công cụ không bắt buộc:

- Go 1.24+ và Node.js 22+, chỉ khi bạn build Operator hoặc UI bên ngoài Compose;
- Python 3.12+ kèm PyYAML để chạy các kiểm tra của repo và tạo dữ liệu demo;
- `postgresql-client` để xem trực tiếp cơ sở dữ liệu.

Mặc định stack chỉ mở cổng trên `127.0.0.1`. Không đưa stack ra mạng không tin
cậy. Tài khoản demo và dữ liệu mẫu chỉ dùng để đánh giá trên máy cá nhân.

Tiếp theo: [chạy stack trên máy](local-stack.md).
