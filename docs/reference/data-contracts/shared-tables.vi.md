---
title: Các bảng dùng chung
description: Các bảng core và vùng lưu trữ do các chức năng (capabilities) quản lý mà mọi bản cài đặt đều sử dụng chung.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


# Các bảng dùng chung

Tier 1 (`core`) không bao giờ tham chiếu đến các bảng thuộc Tier 2 hoặc Tier 3. ID của các thực thể sử dụng UUID; ID kiểm toán sử dụng BIGSERIAL.

| Bảng | Các trường và ràng buộc |
| --- | --- |
| `core.users` | `id` (trùng với `sub` của Keycloak), `email` (duy nhất, không phân biệt chữ hoa chữ thường), `display_name`, `status` (`ACTIVE`/`DISABLED`). Đây là bản sao một chiều của thông tin định danh từ Keycloak: `dxos-authz` tạo bản ghi khi người dùng gửi yêu cầu lần đầu tiên và cập nhật `email` cùng `display_name`; thành phần này không bao giờ thay đổi `status` và từ chối tài khoản có trạng thái `DISABLED`. Không vai trò nào tại thời điểm thực thi được phép xóa bản ghi. Việc đánh dấu các tài khoản bị vô hiệu hóa hoặc xóa trong Keycloak thành `DISABLED` cần có chức năng đồng bộ tài khoản (#23), hiện chưa được triển khai. |
| `core.roles` | `name`, chủ sở hữu là `app_id` hoặc `cap_id` (tối đa một trong hai; cả hai đều không có đối với vai trò của nền tảng), `description`. |
| `core.user_roles` | `(user_id, role_name)`, `granted_by`, `granted_at`: nơi lưu trữ duy nhất thông tin người dùng nào đang nắm giữ vai trò nào. Các bản ghi này được thay đổi bởi quản trị viên nền tảng thông qua `dxos-authz`, thành phần gọi các hàm `core.grant_role` và `core.revoke_role`; mỗi thay đổi đều được ghi vào chuỗi kiểm toán của `core`. |
| `core.apps` | `app_id`, `display_name`, `version`, `spec` đã được xác thực, `status` (`Pending`/`Applying`/`Ready`/`Failed`/`Removed`), `last_error`. Các bản ghi không bao giờ bị xóa. |
| `core.capabilities` | `cap_id`, `display_name`, `category_hpdi` (`H`, `P`, `D` hoặc `I`), `base_route`, `enabled`. |
| `core.app_capabilities` | `(app_id, cap_id)`; không thể vô hiệu hóa một chức năng khi vẫn còn ứng dụng đã cài đặt đang sử dụng chức năng đó. |
| `core.app_deployments` | Mỗi lần thử cài đặt hoặc nâng cấp tương ứng với một bản ghi, bao gồm trạng thái và `last_error`. |
| `core.app_workflows` | Các luồng xử lý mà Operator đã triển khai cho từng workflow của ứng dụng (`namespace`, `flow_id`, `definition`, `revision`); cap-process sử dụng các thông tin này để định tuyến sự kiện. |
| `core.schema_migrations` | Sổ ghi nhận các migration trong `platform/postgres/migrations` đã được áp dụng cho cơ sở dữ liệu này, kèm theo checksum để phát hiện migration đã bị chỉnh sửa. |
| `core.files` | `id`, `app_id`, `bucket`, `object_key`, `file_name`, `scan_status`, `sha256_hash`, `uploaded_by`, `mime_type`, `size_bytes` và thông tin lập lịch quét tệp; các cột định danh và nội dung là bất biến. |
| `core.audit_logs` | Nhật ký chỉ cho phép ghi nối tiếp (append-only), được liên kết bằng chuỗi hash riêng cho từng ứng dụng (`chain` = `app_id` hoặc `core`): `actor_id`, `acting_role`, `action`, `entity_type`, `entity_id`, `entity_version`, `payload`. |
| `core.outbox_events` | Các sự kiện được lưu trữ bền vững, bao gồm `app_id`, thông tin định danh và phiên bản thực thể, nội dung sự kiện (`payload`) và trạng thái gửi. |

Các bảng registry, audit và outbox được ghi bởi Operator hoặc dịch vụ được phân công; các vai trò người dùng chỉ được đọc những dữ liệu mà ứng dụng của họ cấp quyền thông qua `sharedAccess`.

Theo [ADR 0002](../../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md), các thuộc tính thuộc miền nghiệp vụ như `skill_level` và `work_shift` phải được đặt trong các bảng của ứng dụng; việc dùng chung thông tin người dùng không đồng nghĩa với việc được phép truy cập xuyên ứng dụng.

### Lưu trữ của các chức năng (Capability storage)

[ADR 0003](../../adr/0003-modular-capability-schema-separation-for-iot-data.md) tách kernel khỏi các câu lệnh SQL của từng chức năng trong `platform/postgres/capabilities/`. Các câu lệnh này được tải dựa trên danh sách chức năng trong `ENABLED_CAPABILITIES`. Mỗi chức năng sở hữu schema riêng và tự đăng ký trong `core.capabilities`.

| Chức năng | Các bảng |
| --- | --- |
| `cap-hr` | `cap_hr.org_units` (các phòng ban, có thể có phòng ban cấp trên), `cap_hr.staff` (mỗi người dùng thuộc một phòng ban, có trường `position`). Các bảng này được quản lý bởi người dùng nắm giữ vai trò chức năng `role_hr_admin` thông qua dịch vụ cap-hr. Dịch vụ chỉ thực hiện thay đổi thông qua các hàm đã được kiểm toán (`cap_hr.create_org_unit`, `update_org_unit`, `assign_staff`, `remove_staff`); mỗi thay đổi đều được ghi vào chuỗi kiểm toán của `core`. |
| `cap-work` | `cap_work.work_items`: `app_id`, `title`, `description`, `priority`, `board_column`, `assignee_id`, bản ghi nguồn (`source_type`, `source_id`), `created_by`. |
| `cap-process` | `cap_process.command_receipts` (lưu biên nhận để bảo đảm tính idempotency của lệnh), `cap_process.processed_workflow_steps` (loại bỏ việc thực thi trùng lặp các tác động). |
| `cap-data` | `cap_data.devices`, `cap_data.ot_events`, `cap_data.export_batches`, `cap_data.data_lineage`. Các trình xử lý của dịch vụ cap-data vẫn đang hướng đến những bảng core đã ngừng sử dụng và dự kiến sẽ được cập nhật trong phiên bản v1.1.0 (#111). |
| `cap-ai` | `cap_ai.*`: các collection tri thức, tài liệu, phiên bản, chunk và embedding; các tác vụ nhập dữ liệu; registry mô hình, các lượt đặt trước và thông tin sử dụng; các cuộc hội thoại của Assistant. Các bảng được tạo bởi những migration trong `platform/postgres/migrations/cap_ai/` ([tài liệu tham chiếu RAG](../rag/index.md)). |

Các ứng dụng không khai báo một chức năng sẽ không được cấp quyền hoặc thiết lập mối quan hệ trên các bảng thuộc chức năng đó. Việc thu hồi quyền truy cập hoặc vô hiệu hóa một chức năng không đồng nghĩa với việc được phép xóa dữ liệu đã lưu trữ của chức năng đó.
