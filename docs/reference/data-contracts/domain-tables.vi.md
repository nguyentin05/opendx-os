---
title: Các bảng miền nghiệp vụ
description: Các bảng quy trình, bảng thông thường và bảng con được tạo từ manifest của ứng dụng.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


# Các bảng miền nghiệp vụ và trạng thái ban đầu

Một ứng dụng khai báo một hoặc nhiều bảng miền nghiệp vụ theo dạng `domain.<name>` ([ADR 0008](../../adr/0008-an-application-declares-several-tables.md)); Operator tạo từng bảng từ manifest. Tên của mỗi bảng chỉ thuộc về một ứng dụng.

| Loại bảng | Khai báo | Các cột hệ thống |
| --- | --- | --- |
| Bảng quy trình (Process table) | Một `stateMachine` | `id`, `created_by`, `org_unit_id` (khi sử dụng cap-hr), `status` (được kiểm tra dựa trên state machine), `version`, `created_at`, `updated_at` |
| Bảng thông thường (Plain table) | Không khai báo `stateMachine` cũng như `belongsTo` | Các cột tương tự nhưng không có `status` |
| Bảng con (Child table) | `belongsTo: {table, column}` | `id`, `created_by`, `version`, `created_at`, `updated_at` và cột tham chiếu đến bảng cha (NOT NULL, khóa ngoại trỏ đến bảng cha) |

Một bản ghi quy trình bắt đầu ở trạng thái `stateMachine.initial`, chỉ được chỉnh sửa thông qua Hasura khi đang ở trạng thái cho phép chỉnh sửa và chỉ được thay đổi `status` thông qua các lệnh của cap-process. Một bản ghi thông thường (ví dụ: một mục trong danh sách do ứng dụng quản lý) được đọc và ghi thông qua Hasura theo các quy tắc truy cập của bảng và không có trạng thái. Trên mọi loại bảng, `version` đều bắt đầu từ `1`.

Một bản ghi thuộc bảng con là một phần của bản ghi trong bảng cha (ví dụ: một dòng trong phiếu yêu cầu), với cấu trúc lồng nhau chỉ sâu một cấp:

- Một vai trò được phép đọc bản ghi con khi một trong các quy tắc `select` áp dụng cho bảng cha cho phép đọc bản ghi cha tương ứng;
- Một vai trò được phép thêm, cập nhật hoặc xóa bản ghi con khi quy tắc `update` áp dụng cho bảng cha cho phép thao tác trên bản ghi cha và, nếu bảng cha là bảng quy trình, bản ghi cha phải đang ở trạng thái cho phép chỉnh sửa;
- Mỗi thay đổi đều làm tăng `version` của bản ghi cha trong cùng một transaction, do đó, một lệnh được gửi với phiên bản đã đọc trước khi thay đổi sẽ nhận phản hồi `409`;
- Cột tham chiếu đến bảng cha không bao giờ thay đổi và bản ghi thuộc bảng con không có phòng ban riêng.

Cả quyền của Hasura và các trigger `core.enforce_parent`, `core.touch_parent` đều thực thi những quy tắc này; trigger sẽ khóa bản ghi cha để việc thay đổi bản ghi con và thực thi lệnh trên bản ghi cha không thể diễn ra xen kẽ. Một thay đổi trên bảng con bị từ chối sẽ phát sinh SQLSTATE `23514` (`check_violation`) với tiền tố thông báo cố định là `the parent record can no longer be changed (status <status>)` hoặc `a row cannot move to another parent record`. Các bản cài đặt hiện có áp dụng migration `0005_parent_refusal.sql` bằng lệnh `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope core --through 0005` (xem [các quy tắc tại thời điểm thực thi](../runtime-rules.vi.md)).

Các bảng quy trình của những ứng dụng mẫu:

| Ứng dụng mẫu | Các cột nghiệp vụ | Trạng thái ban đầu |
| --- | --- | --- |
| Procurement (Mua sắm) | `vendor_name`, `item_description`, `quoted_amount NUMERIC(14,2) > 0`, `quote_file_id` → `core.files`, `vendor_id` → `domain.procurement_vendors`, `rejection_reason` | `DRAFT` |
| Maintenance (Bảo trì) | `assignee_id` → `core.users`, `device_id` → `cap_data.devices`, `issue_summary`, `resolution_note` | `OPEN` |
| Timesheet (Bảng chấm công) | `work_date`, `logged_hours NUMERIC(4,2)` nằm trong khoảng `(0, 24]` | `SUBMITTED` |

Procurement còn khai báo một bảng thông thường là `domain.procurement_vendors` (danh sách nhà cung cấp do bộ phận tài chính quản lý, được tham chiếu thông qua `vendor_id` của phiếu yêu cầu) và một bảng con là `domain.procurement_items` (các dòng chi tiết của phiếu yêu cầu).

Đây là dữ liệu khai báo dành cho các ứng dụng mẫu, không phải danh sách bảng cố định được định nghĩa bên trong nền tảng. Các bảng thuộc cùng một ứng dụng có thể tham chiếu lẫn nhau bằng khóa ngoại. Nếu một bản ghi được tạo ở trạng thái đã gửi, như trường hợp của timesheet, luồng tạo bản ghi phải thực thi các điều kiện nghiệp vụ tương ứng bên trong transaction; chỉ được mở quyền CRUD khi tất cả các điều kiện đó đã được thực thi đầy đủ.
