---
title: Định danh, vai trò và phạm vi truy cập
description: Cách thức hoạt động của định danh, việc cấp quyền vai trò, quyền sở hữu ứng dụng và các phạm vi truy cập.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


# Định danh, vai trò và phạm vi truy cập

- Mỗi bản cài đặt chỉ phục vụ một tổ chức. Keycloak chịu trách nhiệm xác thực; token chỉ mang thông tin định danh (`sub`, email, name). Vai trò mà người gọi sử dụng được chỉ định trong `X-Hasura-Role` và chỉ được chấp nhận khi `core.user_roles` hiện đang cấp quyền cho vai trò đó ([ADR 0007](../../adr/0007-keycloak-authenticates-platform-authorizes.md)). Tên vai trò xuất hiện trong phần thân yêu cầu (body), header hoặc token không tự tạo ra quyền.
- Mỗi vai trò thuộc quyền sở hữu của một ứng dụng, một chức năng (capability) hoặc nền tảng (`core.roles`). Với mỗi vai trò và thao tác, ứng dụng khai báo một **phạm vi truy cập (scope)**: `own` (các bản ghi do người gọi tạo), `department` (các bản ghi thuộc phòng ban của người gọi trong `cap_hr.staff`) hoặc `organization` (toàn bộ tổ chức), cùng với một `recordFilter` tùy chọn. Quyền của Hasura và các lệnh của cap-process đều áp dụng cùng một phạm vi truy cập.
- Dữ liệu mang hai nhãn độc lập: `app_id` (xác định dữ liệu thuộc ứng dụng nào) và, đối với các ứng dụng sử dụng cap-hr, `org_unit_id` (xác định phòng ban nào). Cả hai giá trị đều do máy chủ gán và không bao giờ thay đổi.
- `created_by` và `org_unit_id` được thiết lập bởi các trigger dựa trên session (`hasura.user` của Hasura hoặc `dxos.actor_id` của cap-process), không lấy từ dữ liệu đầu vào của client.
- Các thao tác chèn dữ liệu lồng nhau (nested inserts), chèn hoặc cập nhật khi đã tồn tại (upserts) và các mối quan hệ (relationships) không được tạo ra thêm bất kỳ con đường nào để vượt qua cơ chế phân quyền. Nếu quyền cho một thao tác chưa được thiết kế, không cấp quyền thực hiện thao tác đó.
