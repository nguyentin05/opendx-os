---
title: Dữ liệu tri thức trong gói ứng dụng
description: Xuất bản các nguồn dữ liệu tri thức như một phần của gói ứng dụng.
diataxis: how-to
audience: [app-author]
status: active
owner: platform
last_reviewed: 2026-11-16
---

<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright 2026 DX-OS Open-Core Contributors -->
# Dữ liệu tri thức khai báo trong gói ứng dụng

Một ứng dụng có thể khai báo các nguồn dữ liệu cục bộ ở định dạng Markdown hoặc PDF văn bản được hỗ trợ thông qua Knowledge v1. Quyền truy cập được quyết định ngay lập tức bởi trạng thái xuất bản hiện tại trong Core; bản sao chỉ mục hoặc bản sao thông báo không bao giờ có quyền cấp quyền truy cập. Trạng thái `Ready` của ứng dụng và trạng thái lập chỉ mục dữ liệu tri thức là hai trạng thái độc lập. Xem [hợp đồng manifest](../../reference/rag/knowledge-contract.vi.md), [thư viện cá nhân](personal-library.vi.md) và [quy trình xác minh](../../contribute/verification.vi.md).

## Khai báo và vai trò

Trong một hệ thống đã bật cap-ai và đáp ứng các điều kiện xác nhận đối với những thành phần tiền nhiệm, hãy thêm cap-ai vào danh sách capability của ứng dụng và khai báo các nguồn dữ liệu. Ví dụ, tệp `knowledge/approval-policy.md` được đóng gói cùng ứng dụng procurement có thể được kích hoạt bằng cấu hình sau:

```yaml
knowledge:
  version: 1
  sources:
    - id: approval-policy
      path: knowledge/approval-policy.md
      mediaType: text/markdown
      indexingProfile: standard-text-v1
      access:
        mode: ROLE
        allowedRoles: [role_procurement_requester, role_procurement_manager, role_procurement_finance]
```

Manifest procurement thông thường không tự động kích hoạt cap-ai. Điều này giúp các yêu cầu cài đặt hiện có của ứng dụng không phụ thuộc vào tính năng dữ liệu tri thức tùy chọn.
Đối với chế độ `RESTRICTED`, hãy khai báo `access.grantsManagedBy` để tham chiếu đến một mục trong `knowledge.grantPolicies` có chứa `allowedRoles` do ứng dụng quản lý. Không bao giờ khai báo quyền cấp cho người dùng cụ thể trong YAML.

Vai trò hiện tại của người quản lý cho phép họ quản lý việc cấp quyền mà không cần có quyền `READ` đối với tài liệu. Để có quyền `READ`, người dùng còn phải đang nắm giữ một vai trò nguồn được cho phép và có một quyền cấp đích danh, chưa bị thu hồi trong thế hệ phân quyền hiện tại. Quản trị viên ứng dụng không có quyền vượt qua các giới hạn này và các tệp cá nhân không thể được cấp quyền truy cập.

## Thành phần và cấu hình

Áp dụng Core migration 0003 và cap_ai migration 0003 bằng các migration runner đã xác minh checksum trước khi triển khai những thành phần sử dụng gói ứng dụng. Quá trình khởi tạo mới và cơ chế chiếu ConfigMap trong Kubernetes đều bao gồm cùng các tệp này. Dữ liệu cũ, các giới hạn đối với nguồn dữ liệu và nhật ký kiểm toán vẫn được giữ lại ngay cả khi các tính năng bị vô hiệu hóa.

Operator yêu cầu `RAG_PACKAGE_ARTIFACT_ROOT` và `CLAMAV_ADDRESS` đối với gói ứng dụng có chứa nguồn dữ liệu tri thức. Thư mục artifact root do môi trường triển khai quản lý và phải được giữ riêng tư; thao tác đọc nguồn dữ liệu chỉ được giới hạn trong thư mục gốc của gói ứng dụng, có giới hạn dung lượng và được quét kiểm tra. Các bản sao đã được phê duyệt vẫn có thể sử dụng sau khi thư mục gói ứng dụng có thể thay đổi hoặc tiến trình Operator không còn tồn tại. Hãy mount chính artifact root đó ở chế độ chỉ đọc vào cap-ai. Bộ phân tích cú pháp chỉ nhận dữ liệu dạng byte và vẫn phải tuân thủ các giới hạn sandbox đã được xác nhận.

cap-ai yêu cầu `RAG_PACKAGE_ENABLED=true`, cùng với các cờ xác nhận nguồn dữ liệu, metadata và bộ phân tích cú pháp, các biến `RAG_PACKAGE_ARTIFACT_ROOT`, `AI_DISPATCHER_DATABASE_URL` và `RAG_DISPATCHER_SERVICE_SUBJECT`. Biến cuối cùng là một định danh dịch vụ OIDC thứ ba, riêng biệt do môi trường triển khai quản lý, không dùng chung với định danh của worker hoặc File Service. Các listener nội bộ không được đưa ra gateway công khai. Vai trò cơ sở dữ liệu của dispatcher chỉ có thể nhận và xác nhận các thông báo dữ liệu tri thức, đồng thời gọi chức năng đối soát gói ứng dụng trong phạm vi được cấp quyền; vai trò này không thể đọc các chunk tùy ý hoặc thay đổi quyền cấp truy cập. Vai trò cơ sở dữ liệu của user API không thể thực hiện đối soát gói ứng dụng.

Build và chạy `operator/cmd/dxos-knowledge-dispatcher` với các biến `DATABASE_URL` dành cho vai trò dispatcher có phạm vi quyền hạn hẹp, `RAG_INTERNAL_AI_URL`, `RAG_DISPATCHER_BEARER_TOKEN` và các cờ xác nhận thành phần tiền nhiệm. Dispatcher thuê các thông báo theo cơ chế lease một cách bền vững và thử xử lý lại với khoảng thời gian chờ có giới hạn. Tùy chọn `--once` chỉ xử lý tối đa một thông báo để phục vụ xác minh. Workflow publisher không xử lý các thông báo dữ liệu tri thức mà để lại cho consumer này. Cả hai dịch vụ đều không ghi thông tin xác thực hoặc nội dung phản hồi vào log.

Cấp cho worker biến `RAG_INTERNAL_AI_URL` để sử dụng endpoint ủy quyền xử lý nguồn dữ liệu trong gói ứng dụng, bên cạnh endpoint File Service riêng tư dành cho tải tệp lên đã có. Cung cấp subject OIDC thực tế của worker đã được xác minh dưới dạng một Core identity có trạng thái `ACTIVE` để phục vụ kiểm toán quá trình kích hoạt gói ứng dụng. Gói ứng dụng không có người tải lên; do đó, không tạo tệp giả hoặc chủ sở hữu giả để bổ sung thông tin này. Token dịch vụ có thời hạn sử dụng và phải được gia hạn thông qua cơ chế xử lý thông tin xác thực do môi trường triển khai quản lý.

## Xuất bản, quyền truy cập và giao diện người dùng

Một lần xuất bản có thay đổi thành công sẽ đồng thời ghi nhận generation, source incarnation, authorization generation và một thông báo bền vững. Quá trình đối soát có tính idempotent. Việc thay đổi chế độ, giới hạn vai trò hoặc chính sách quản lý của người quản lý sẽ thu hồi các quyền đã cấp, ngay cả khi dispatcher bỏ lỡ những lần xuất bản trung gian theo chuỗi A → B → A. Việc xóa nguồn dữ liệu sẽ lập tức chặn quyền truy cập thông qua nguồn gốc, metadata, trích dẫn, tìm kiếm và worker được ủy quyền. Việc thêm lại cùng một khai báo không thể khôi phục các quyền đã bị thu hồi.

Khi nội dung thay đổi, phiên bản đang hoạt động cũ vẫn đủ điều kiện sử dụng cho đến khi phiên bản thay thế hoàn tất. Lỗi bộ phân tích cú pháp không xóa phiên bản đang hoạt động còn đủ điều kiện sử dụng. Khi nội dung quay trở lại phiên bản từng tồn tại trong lịch sử, hệ thống sẽ đưa một công việc mới có cơ chế fencing vào hàng đợi nếu một phiên bản khác đang hoạt động. Quá trình này vẫn giữ nguyên danh tính phiên bản chuẩn, lịch sử công việc ở trạng thái kết thúc và các lần chạy `COMPLETE` bất biến.

Shell phát hiện dữ liệu tri thức của ứng dụng thông qua registry hiện tại. Giao diện hiển thị riêng trạng thái lập chỉ mục hoặc lỗi, không gộp chúng với trạng thái `Ready` của ứng dụng; đồng thời tìm kiếm trong phạm vi ứng dụng và vai trò hiện tại, cũng như tải các trích dẫn thông qua API được bảo vệ. Khi chuyển đổi vai trò, ứng dụng hoặc người dùng, các yêu cầu cũ và trạng thái cấp quyền tương ứng sẽ bị loại bỏ.

Giao diện quản lý quyền cấp truy cập hiển thị giới hạn vai trò, các bản sửa đổi và trạng thái quyền cấp cho người dùng cụ thể mà không hiển thị tiêu đề, đoạn trích hoặc tham chiếu đến artifact. Khi Core đã cập nhật trạng thái mới hơn trạng thái đối soát, giao diện quản lý sẽ thông báo rằng đang chờ xuất bản và từ chối mọi thao tác ghi mới cho đến khi người quản lý làm mới dữ liệu sau khi thông báo được xử lý. ETag cũ sẽ trả về lỗi `409` và yêu cầu đưa ra quyết định mới dựa trên dữ liệu được cập nhật; hệ thống không tự động thử lại thao tác đó như một lần cấp quyền mới.

Các endpoint công khai được bổ sung gồm:

- `GET /api/cap/ai/app-knowledge?app_id=...`
- `GET /documents/{id}/grant-management`
- `POST /documents/{id}/grants`
- `DELETE /documents/{id}/grants/{user_id}`

Các endpoint này nằm dưới `/api/cap/ai`. Mọi thao tác thay đổi dữ liệu đều yêu cầu `If-Match` và `Idempotency-Key`. Tìm kiếm chấp nhận `scope: {kind: APP, app_id}`; phạm vi dữ liệu cá nhân vẫn được giữ riêng biệt. Trong phạm vi triển khai này, không kích hoạt các chức năng tạo nội dung, tạo embedding hoặc reranking thông qua dịch vụ bên ngoài.

## Xác minh trực tiếp trên môi trường thực tế

Chạy `scripts/test-rag.sh package` trên một môi trường PostgreSQL 17 dùng riêng cho kiểm thử. Quy trình này chạy kiểm tra điều kiện nguồn dữ liệu trước, sau đó thực hiện các bước kiểm thử thực tế sau:

- Xuất bản thông qua Operator;
- Tiếp nhận và kiểm tra nguồn dữ liệu bằng scanner/artifact;
- Gửi thông báo thông qua dispatcher đã xác thực;
- Kích hoạt worker;
- Kiểm tra các trường hợp ROLE/RESTRICTED A/B và các thao tác kiểm tra của manager/admin;
- Kiểm tra trường hợp bỏ lỡ chuỗi A/B/A;
- Kiểm tra việc xóa rồi thêm lại sau một khoảng thời gian trì hoãn;
- Thay thế nội dung;
- Kiểm tra các trường hợp nội dung A → B → A với trạng thái hoàn tất hoặc bị hủy;
- Thu hồi quyền của vai trò cơ sở dữ liệu trong khi vẫn sử dụng cùng một JWT.

Quy trình cũng xác minh rằng các tài liệu cá nhân hiện có không bị thay đổi. Mỗi lần xuất bản sử dụng một tiến trình Operator mới; việc gửi thông báo được chủ động trì hoãn giữa các lần xuất bản.

Cung cấp đầy đủ các biến môi trường liên quan đến nguồn dữ liệu và metadata, đường dẫn `RAG_TEST_OPERATOR` và `RAG_TEST_DISPATCHER` đã được build, `RAG_PACKAGE_ARTIFACT_ROOT` ở phía host, `CLAMAV_ADDRESS`, `AI_DISPATCHER_DATABASE_URL`, `RAG_INTERNAL_AI_URL`, `RAG_TEST_INTERNAL_AI_URL`, `RAG_DISPATCHER_BEARER_TOKEN`, các cờ xác nhận thành phần tiền nhiệm và biến tùy chọn `RAG_TEST_FIXTURE_ROOT` dành cho các tệp fixture tạm thời, riêng tư.

`RAG_TEST_COMPOSE_FILE` phải chỉ đến một cấu hình Compose dạng JSON dành cho một stack dùng riêng để kiểm thử, có tên bắt đầu bằng `dxos-rag-*`, với một dịch vụ `worker` chạy lệnh `/live/bin/cap-ai-worker`.

Để xác minh trạng thái sẵn sàng trong khi quá trình lập chỉ mục đang tạm dừng, runner chỉ chủ động dừng hoặc khởi động lại dịch vụ worker thuộc stack này. Worker sẽ được khôi phục khi runner thoát.

Thông tin xác thực phải được cung cấp thông qua các biến môi trường hoặc cấu hình riêng tư và không bao giờ được in ra.

Runner của gói ứng dụng cũng yêu cầu `RAG_TEST_UI_URL` và các trạng thái lưu trữ thực tế của IdP gồm `RAG_TEST_UI_STORAGE_A`, `RAG_TEST_UI_STORAGE_B` và `RAG_TEST_UI_STORAGE_ADMIN` để kiểm thử tìm kiếm/trích dẫn trong ứng dụng bằng Chromium, các điều khiển cấp quyền của manager khi không có quyền `READ`, cũng như việc chuyển đổi ứng dụng/vai trò. Hãy chuẩn bị các trạng thái này sau khi build giao diện người dùng, sử dụng trình duyệt thực tế được cài đặt theo hướng dẫn trong tài liệu dành cho người dùng. Các điều kiện tiên quyết còn thiếu sẽ được đánh dấu là `NOT_RUN`, không bao giờ được xem là `PASS`.

Phạm vi hỗ trợ runtime bị giới hạn bởi giới hạn của bộ phân tích cú pháp đã được xác nhận và bằng chứng thu thập được từ quá trình chạy thực tế. Bản tài liệu này không phải là kết quả kiểm thử và cũng không phải là sự cho phép kích hoạt một môi trường cài đặt khác.

Các chức năng truy xuất ngữ nghĩa tùy chọn và tạo câu trả lời có căn cứ sẽ được xây dựng dựa trên chính các cơ chế kiểm tra nguồn dữ liệu và quyền cấp truy cập này. Xem [truy xuất và tạo câu trả lời bằng mô hình](models.vi.md).

Trạng thái sẵn sàng của mô hình không làm thay đổi trạng thái `Ready` của ứng dụng hoặc trạng thái sẵn sàng của tìm kiếm từ vựng.