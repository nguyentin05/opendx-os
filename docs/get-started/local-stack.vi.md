---
title: Chạy stack trên máy
description: Khởi động OpenDX-OS trong môi trường cục bộ bằng Docker Compose.
diataxis: tutorial
audience: [evaluator, operator]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Khởi chạy hệ thống cục bộ

Tại thư mục gốc của repository, thực hiện lần lượt các lệnh sau:

```bash
scripts/init-env.sh          # Tạo tệp .env với các thông tin bảo mật ngẫu nhiên
docker compose up -d --build
scripts/test-compose.sh      # Chờ các dịch vụ khởi động và kiểm tra từng điểm truy cập
```

Trong lần khởi chạy đầu tiên, hệ thống sẽ tự động tạo cơ sở dữ liệu, kích hoạt các chức năng được khai báo trong biến
`ENABLED_CAPABILITIES`, và cài đặt các gói ứng dụng trong thư mục `apps/`.
Mật khẩu cơ sở dữ liệu và cơ sở dữ liệu Kestra chỉ được khởi tạo một lần trên mỗi data volume; Sau khi thay đổi các thông tin này, cần tạo lại volume bằng lệnh: `docker compose down -v`.

## Các dịch vụ đang hoạt động

| Dịch vụ | Địa chỉ truy cập cục bộ |
| --- | --- |
| Giao diện Workspace (App Shell) | <http://localhost:5173> |
| Cổng API (APISIX) | <http://localhost:9080> |
| Hasura GraphQL API (đã tắt giao diện Console) | <http://localhost:8080> |
| Trang quản trị Keycloak | <http://localhost:8085/admin> |
| Kestra | <http://localhost:8082> |
| cap-process (truy cập trực tiếp; các ứng dụng client sử dụng thông qua API gateway) | <http://localhost:8091> |
| PostgreSQL | `localhost:5432` |

Các dịch vụ tri thức (knowledge services), bao gồm cap-ai, thư viện cá nhân (Personal Library) và Assistant, không nằm trong hệ thống này. Để khởi chạy các dịch vụ trên, sử dụng lệnh: `make rag-up`
([Hướng dẫn cài đặt](../operations/installation.vi.md)).

Dịch vụ `operator` chịu trách nhiện đồng bộ trạng thái của thư mục `apps/` theo chu kỳ 30 giây. Quá trình này bao gồm: cài đặt các gói ứng dụng mới hoặc có thay đổi, triển khai các workflow của ứng dụng lên Kestra, khôi phục và đồng bộ metadata của Hasura khi có thay đổi thủ công. Để theo dõi hoạt động của Operator, sử dụng lệnh:

```bash
docker compose logs -f operator
```

Khi dịch vụ Operator dừng hoạt động, các ứng dụng đã được cài đặt vẫn tiếp tục phục vụ yêu cầu. Các dịch vụ trong quá trình vận hành không gọi Operator ở mỗi lần xử lý yêu cầu.

## Kiểm tra quá trình cài đặt

```bash
scripts/test-compose.sh
docker compose ps
```

Nếu một dịch vụ ở trạng thái không khỏe mạnh (unhealthy), cần kiểm tra nhật ký hoạt động trước khi tiến hành thay đổi cấu hình:

```bash
docker compose logs --tail=100 <service>
```

Bước tiếp theo: [Thực hiện yêu cầu đầu tiên](first-request.vi.md).
