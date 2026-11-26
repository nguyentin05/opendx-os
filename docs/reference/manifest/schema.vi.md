---
title: Lược đồ Manifest
description: Các trường trong manifest DXApplication và phần khai báo bảng của ứng dụng.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Lược đồ Manifest

Bộ kiểm tra xác thực của operator (`operator/internal/manifest`) là phần triển khai tham chiếu cho quy trình tuần tự hóa này. `contracts/manifest-v1.schema.json` chỉ kiểm tra cấu trúc; các quy tắc bên dưới có đề cập đến những trường khác sẽ được thực thi bởi bộ kiểm tra xác thực. `apps/*/manifest.yaml` là các mẫu đã được xác thực và `contracts/fixtures/app-ready.json` là bản ghi `core.apps` tương ứng. Mỗi bản cài đặt chỉ phục vụ một tổ chức, do đó manifest không khai báo workspace.

Kiểm tra một gói bằng lệnh `dxos-operator validate <manifest>` và xem các thao tác DDL cùng Hasura được tạo ra bằng lệnh `dxos-operator plan <manifest>` ([CLI](../cli/dxos-operator.md)).

## Cấp cao nhất

| Trường | Ý nghĩa và dữ liệu bắt buộc |
| --- | --- |
| `apiVersion`, `kind` | `dxos.io/v1alpha1` và `DXApplication` |
| `metadata` | `name` (chính là `app_id`, theo biểu thức `^[a-z][a-z0-9_]*$`), `displayName` và `version` của gói ứng dụng. Cả ba trường đều bắt buộc |
| `spec.capabilities` | Danh sách không rỗng, không trùng lặp các chức năng Tier 2: `cap-process`, `cap-hr`, `cap-work`, `cap-data`, `cap-ai`. Bắt buộc có `cap-process` vì chức năng này thực thi các lệnh của ứng dụng. `cap-hr` bổ sung các phòng ban (`org_unit_id`, phạm vi `department`); `cap-work` là yêu cầu bắt buộc đối với các tác động tạo work item; `cap-ai` là yêu cầu bắt buộc đối với dữ liệu tri thức. Mỗi chức năng phải được bật (`ENABLED_CAPABILITIES`) khi ứng dụng được cài đặt |
| `spec.roles` | Ít nhất một vai trò thuộc quyền quản lý của ứng dụng: `name` (bắt đầu bằng `role_`, theo sau là chữ cái thường, chữ số hoặc `_`) và `description` tùy chọn. Workspace UI sẽ hiển thị nội dung mô tả này thay cho tên vai trò. Mọi vai trò được sử dụng ở bất kỳ vị trí nào trong manifest đều phải được khai báo tại đây. Mỗi vai trò chỉ thuộc về đúng một ứng dụng; `role_operator`, `role_auditor` và `role_workflow` là các vai trò dành riêng |
| `spec.sharedAccess` | Tùy chọn. Quyền đọc đối với các bảng dùng chung (xem [bên dưới](#shared-access)). Không có quyền ghi |
| `spec.tables` | Các bảng dữ liệu nghiệp vụ (xem [bên dưới](#tables)). Bắt buộc có ít nhất một bảng, ngoại trừ ứng dụng chỉ phục vụ dữ liệu tri thức, có khai báo `spec.knowledge` và không có workflow |
| `spec.workflows` | Tùy chọn. Các luồng mà operator triển khai lên workflow engine (xem [bên dưới](#workflows)) |
| `spec.knowledge` | Tùy chọn; yêu cầu có `cap-ai`. Bao gồm các nguồn dữ liệu tri thức được đóng gói cùng ứng dụng (`version: 1`, `sources`, `grantPolicies`) và cấu hình bật/tắt thư viện cá nhân (`personalLibrary: {enabled, allowedRoles}`). Xem [đóng gói dữ liệu tri thức](../../guides/knowledge/package-knowledge.vi.md) và [thư viện cá nhân](../../guides/knowledge/personal-library.vi.md) |

## Quyền truy cập dùng chung

Mỗi mục trong `spec.sharedAccess` chỉ định một `table`, các `roles` được phép đọc bảng đó và các `columns` mà những vai trò này được phép đọc. Bảng phải thuộc danh mục cố định; nếu bảng thuộc về một capability thì capability tương ứng phải có trong `spec.capabilities`.

| Bảng | Các cột được phép đọc | Yêu cầu |
| --- | --- | --- |
| `core.users` | `id`, `email`, `display_name`, `status` | |
| `core.files` | `id`, `file_name`, `mime_type`, `size_bytes`, `scan_status`, `sha256_hash`, `uploaded_by`, `scanned_at`, `created_at` | |
| `cap_hr.org_units` | `id`, `parent_id`, `code`, `name` | `cap-hr` |
| `cap_hr.staff` | `user_id`, `org_unit_id`, `position`, `joined_at` | `cap-hr` |
| `cap_work.work_items` | `id`, `title`, `description`, `priority`, `board_column`, `assignee_id`, `source_type`, `source_id`, `created_by`, `created_at`, `updated_at` | `cap-work` |
| `cap_data.devices` | `id`, `device_code`, `name`, `location`, `status`, `last_reading` | `cap-data` |

Các bản ghi trong `core.files` và `cap_work.work_items` chỉ được giới hạn trong phạm vi `app_id` của chính ứng dụng. Một vai trò chỉ nhìn thấy các tệp do người dùng thuộc vai trò đó tải lên hoặc được đính kèm vào những bản ghi mà vai trò đó có quyền đọc. Tương tự, vai trò chỉ nhìn thấy các work item có bản ghi nguồn (một hàng thuộc bảng cấp cao nhất) mà vai trò đó có quyền đọc; các work item không có nguồn sẽ được hiển thị trong phạm vi toàn ứng dụng.

## Các bảng

Mỗi mục trong `spec.tables` bao gồm:

| Trường | Ý nghĩa và dữ liệu bắt buộc |
| --- | --- |
| `name` | Có định dạng `domain.<name>`, duy nhất trong ứng dụng. Mỗi bảng chỉ thuộc về một ứng dụng |
| `columns` | Phải có ít nhất một cột nghiệp vụ, bao gồm: `name`, `type` (`uuid`, `text`, `date`, `numeric(14,2)` hoặc `numeric(4,2)`), `nullable` và `check` tùy chọn thuộc danh sách cho phép: `length(trim(<column>)) > 0` đối với kiểu `text`; `<column> > 0` hoặc `<column> > 0 AND <column> <= 24` đối với kiểu `numeric`. Các cột kiểu số không bao giờ chấp nhận NaN. Operator quản lý các cột hệ thống: `id`, `created_by`, `version`, `created_at`, `updated_at`; ngoài ra còn có `org_unit_id` đối với các ứng dụng sử dụng cap-hr trên bảng cấp cao nhất, `status` trên bảng quy trình và cột tham chiếu đến bảng cha trên bảng con. `status` và `org_unit_id` là các tên dành riêng trên tất cả các bảng |
| `foreignKeys` | Tùy chọn. `column` (một cột nghiệp vụ kiểu `uuid`, chỉ được tham chiếu một lần) → `references`: `core.users`, `core.files`, `cap_hr.org_units`, `cap_work.work_items`, `cap_data.devices` (kèm theo capability quản lý bảng đó) hoặc một bảng khác thuộc cùng ứng dụng. Không được tham chiếu đến bảng của ứng dụng khác. Khóa ngoại tham chiếu đến `core.files` phải được khai báo trong `attachments` |
| `access` | Danh sách cho phép vai trò và cột đối với các thao tác `select` và `insert` (cả hai đều bắt buộc), cùng với `update` tùy chọn; chỉ bảng con mới được phép có `delete`. Xem [quy tắc truy cập](#access-rules) |
| `belongsTo` | Tùy chọn; biến bảng thành bảng con (xem [bảng con](#child-tables)) |
| `stateMachine` | Tùy chọn; biến bảng thành bảng quy trình (xem [máy trạng thái](#state-machine)) |
| `attachments` | Tùy chọn, chỉ áp dụng cho bảng quy trình. Mỗi `column` là một khóa ngoại tham chiếu đến `core.files`; `requireCleanOnCommands` liệt kê các lệnh của bảng yêu cầu tệp phải có kết quả quét ở trạng thái `CLEAN` |
| `ui` | Các gợi ý hiển thị giao diện; không tạo ra bất kỳ quyền nào (xem [UI](#ui)) |

### Quy tắc truy cập

Mỗi quy tắc chỉ định một `role`, các `columns` mà vai trò đó được phép đọc hoặc ghi và `recordFilter` tùy chọn.

- Các quy tắc `select` và `update` phải khai báo `scope`:
  - `own`: `created_by` là người dùng đang thực hiện yêu cầu;
  - `department`: `org_unit_id` của bản ghi là phòng ban của người dùng đang thực hiện yêu cầu trong `cap_hr.staff`; yêu cầu có `cap-hr`;
  - `organization`: tất cả các bản ghi.
- `recordFilter` tùy chọn sẽ thu hẹp phạm vi truy cập.
- Các quy tắc `insert` không có `scope`: các trigger của cơ sở dữ liệu sẽ tự động ghi nhận người tạo và phòng ban.
- Một quy tắc `insert` có thể khai báo `presets`, dùng để gán giá trị `X-Hasura-User-Id` cho một cột nghiệp vụ kiểu `uuid`. Cột được gán giá trị mặc định không được đồng thời xuất hiện trong `columns` của quy tắc.
- Mỗi vai trò chỉ được có tối đa một quy tắc cho mỗi thao tác; các cột hệ thống không bao giờ được phép ghi.
- `columns: "*"` đại diện cho tất cả các cột mà thao tác được phép sử dụng: đối với `select`, bao gồm mọi cột của bảng, kể cả cột hệ thống; đối với `insert` và `update`, bao gồm mọi cột nghiệp vụ, trừ các cột được gán giá trị mặc định trong quy tắc `insert`. Trên bảng con, cột tham chiếu đến bảng cha vẫn có thể đọc và chỉ được bổ sung vào thao tác `insert`. Operator mở rộng `"*"` thành danh sách cột cụ thể trước khi tạo các quyền Hasura và trước khi lưu spec vào `core.apps`, do đó các dịch vụ và giao diện người dùng sẽ đọc được các danh sách thông thường. Một tài nguyên `DXApplication` vẫn giữ nguyên giá trị này dưới dạng `["*"]`. Khi một phiên bản mới bổ sung cột, các quy tắc sử dụng `"*"` sẽ cho phép đọc hoặc ghi cột đó sau khi phiên bản mới được cài đặt; kế hoạch thực thi cũng thể hiện các quyền đã được mở rộng. Không được kết hợp `"*"` với tên các cột cụ thể; trường `columns` bị bỏ qua hoặc để trống vẫn sẽ bị từ chối.

Một bộ lọc (`recordFilter` trong quy tắc hoặc lệnh) sử dụng `_and`, `_or` và các cột đã khai báo, được so sánh bằng `_eq` hoặc `_neq`, với cấu trúc lồng nhau tối đa 32 cấp. Giá trị phải là literal có kiểu dữ liệu tương ứng với cột hoặc, đối với cột `uuid`, là giá trị phiên `X-Hasura-User-Id`; không chấp nhận bất kỳ giá trị phiên nào khác.

### Bảng con

`belongsTo` chỉ định một `table` thuộc cùng ứng dụng và không phải là bảng con, cùng với một `column` là cột tham chiếu đến bảng cha do operator bổ sung.

- Bảng con không có `stateMachine` hoặc `attachments`; các hàng của bảng con phụ thuộc vào bản ghi cha.
- Các quy tắc `select`, `insert` và `update` của bảng con chỉ khai báo vai trò và các cột, không có `scope`, `recordFilter` hoặc `presets`. Bảng con có thể bổ sung các quy tắc `delete` chỉ định vai trò.
- Một vai trò xuất hiện trong quy tắc `select` phải có quy tắc `select` trên bảng cha. Một vai trò xuất hiện trong quy tắc `insert`, `update` hoặc `delete` phải có quy tắc `update` trên bảng cha.
- Cột tham chiếu đến bảng cha được phép thêm dữ liệu (`insert`) nhưng không bao giờ được phép cập nhật (`update`).

### Máy trạng thái

Bao gồm các trường `initial`, `editableStates`, `terminalStates` và `commands`. Mọi trạng thái trong `editableStates` và `terminalStates` đều phải có thể đạt tới thông qua máy trạng thái; đồng thời, không trạng thái nào được vừa là trạng thái có thể chỉnh sửa vừa là trạng thái kết thúc. Ràng buộc CHECK của cột `status` trong bảng phải liệt kê chính xác các trạng thái này.

Mỗi lệnh khai báo:

- `name`: tên lệnh, duy nhất trong ứng dụng;
- `from` và `to`: trạng thái nguồn và trạng thái đích; trạng thái kết thúc không có lệnh chuyển tiếp;
- `allowedRoles`: các vai trò được phép thực hiện lệnh;
- `recordFilter` tùy chọn. Ví dụ, `created_by _neq X-Hasura-User-Id` ngăn người dùng tự phê duyệt bản ghi của chính mình;
- `payload` tùy chọn: các cột nghiệp vụ mà bên gọi có thể thiết lập khi thực hiện lệnh (`column`, `required`). Các cột này không được là cột hệ thống, cột tệp đính kèm hoặc khóa ngoại ([ADR 0006](../../adr/0006-where-application-business-logic-lives.md)).

### UI

| Trường | Ý nghĩa |
| --- | --- |
| `listColumns` | Bắt buộc. Các cột đã khai báo được hiển thị trong danh sách |
| `formFields` | Bắt buộc. Các trường nhập liệu gồm: `name` (một cột nghiệp vụ), `label`, `type` (`text`, `number`, `date`, `uuid` hoặc `file`; `file` chỉ được sử dụng cho cột tệp đính kèm) và `required` |
| `actions` | Các lệnh của bảng; bảng quy trình liệt kê những lệnh này, các bảng khác không liệt kê lệnh nào |
| `title` | Tên tùy chọn của bảng được hiển thị trong cổng thông tin |
| `inboxActions` | Tập con tùy chọn của `actions`, xác định những hành động khiến bản ghi xuất hiện trong hộp thư đến của người dùng; nếu không khai báo, mọi hành động đều được tính |
| `stateLabels`, `actionLabels` | Các ánh xạ tùy chọn dùng để đặt tên hiển thị cho trạng thái và các hành động được liệt kê |

## Workflow

Mỗi mục trong `spec.workflows` bao gồm:

- `id`: một định danh, duy nhất trong ứng dụng.
- `file`: `workflows/<name>.yaml`, là một Kestra flow nằm trong gói ứng dụng và được operator triển khai ([triển khai workflow](../api/events.vi.md#workflow-deployment)).
- `subscribedEvents`: chính xác là `[command.committed]`.
- `effects`: phải có ít nhất một tác động, mỗi tác động bao gồm:
  - `stepName`: tên bước, duy nhất trong tất cả các flow của ứng dụng;
  - `kind`: loại tác động;
  - `whenCommands`: các lệnh thuộc ứng dụng.

Cả hai loại tác động đều yêu cầu có `cap-work`:

- `create_work_item` yêu cầu trường `fields`, bao gồm: `title`, `description`, `priority` (`LOW`, `MEDIUM`, `HIGH` hoặc `CRITICAL`) và `assigneeId` tùy chọn (kiểu UUID). Tác động này tạo một work item ở trạng thái mở, có bản ghi nguồn là bản ghi của lệnh.
- `complete_work_item` không khai báo `fields` và đánh dấu các work item đang mở của bản ghi thành `DONE`. Cả hai loại tác động đều tuân theo thứ tự sự kiện trên cùng một bản ghi; xem [các quy tắc tại thời điểm thực thi](../runtime-rules.vi.md#định-danh-vai-trò-và-lệnh).

Việc gửi sự kiện yêu cầu phải có chính xác một subscriber. Bộ kiểm tra xác thực chấp nhận nhiều flow, nhưng cap-process sẽ không gửi sự kiện của một ứng dụng nếu có nhiều hơn một flow đăng ký nhận sự kiện `command.committed`.