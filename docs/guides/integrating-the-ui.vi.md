---
title: Tích hợp giao diện người dùng
description: Kết nối một màn hình với API của hệ thống và tuân thủ các quy tắc về vai trò và phiên bản.
diataxis: how-to
audience: [app-author]
status: active
owner: platform
last_reviewed: 2026-11-23
---

---
# Tích hợp giao diện người dùng

Tài liệu này mô tả các API mà `workspace-ui` gọi theo đúng thứ tự cần thiết trong một phiên làm việc. Tất cả các yêu cầu đều được gửi thông qua gateway (`http://localhost:9080` khi sử dụng Compose) kèm theo access token Keycloak của người dùng. Backend chịu trách nhiệm thực thi mọi quyền truy cập; giao diện người dùng chỉ quyết định nội dung nào được hiển thị.

Các quy ước kỹ thuật được trình bày trong: [Quy tắc runtime](../reference/runtime-rules.vi.md), [Hợp đồng dữ liệu](../reference/data-contracts/index.vi.md), [ADR 0007](../adr/0007-keycloak-authenticates-platform-authorizes.md) và [ADR 0008](../adr/0008-an-application-declares-several-tables.md).

Nội dung hiển thị trên Workspace UI sử dụng tiếng Việt; các định danh, tên trường API và URL được giữ nguyên như đã công bố. Trên cổng thông tin, mục **Không gian của tôi** (My Workspace) hiển thị các ứng dụng mà người dùng được cấp quyền truy cập, bản xem trước công việc và các trang đã mở gần đây trong phiên hiện tại. Sử dụng chức năng **Làm mới quyền** (Refresh permissions) để cập nhật các quyền ứng dụng mới được cấp hoặc đã bị thu hồi.

Điều khiển **Vai trò đang sử dụng** (Acting role) xuất hiện khi người dùng có thể lựa chọn giữa nhiều vai trò trong một ứng dụng. Các liên kết đến bản ghi được bảo vệ sẽ mang theo vai trò đang sử dụng; máy chủ kiểm tra những bản ghi mà vai trò đó được phép đọc.

## 1. Đăng nhập

| Thiết lập | Giá trị khi sử dụng Compose |
| --- | --- |
| Issuer | `http://localhost:8085/realms/dxos` |
| Client | `dxos-portal` (public client) |
| Flow | Authorization code with PKCE (`S256`); không cho phép đăng nhập bằng mật khẩu |
| Redirect URIs | `http://localhost:5173/*`, `http://localhost:15173/*`, `http://localhost:9080/*` |

Access token chỉ chứa thông tin định danh. Gửi token này trong header `Authorization: Bearer <token>` ở mọi yêu cầu API bên dưới.

Portal chỉ lưu access token, refresh token và ID token trong bộ nhớ. Trước khi một trong hai token hết hạn (lấy theo thời hạn ngắn hơn), hệ thống sử dụng refresh token để lấy access token mới mà vẫn duy trì cùng một danh tính đăng nhập. Nhờ đó, các màn hình đang mở và biểu mẫu đã nhập dữ liệu không bị khởi tạo lại.

Khi tải lại trang, nếu trình duyệt vẫn còn marker không chứa thông tin bí mật `dxos.signedIn` và phiên Keycloak còn hoạt động, hệ thống sẽ thực hiện đăng nhập ngầm với `prompt=none`, sau đó quay lại đúng trang trước đó. Nếu Keycloak yêu cầu người dùng tương tác, màn hình chào mừng sẽ xuất hiện mà không hiển thị thông báo lỗi.

Phiên đăng nhập kết thúc khi phiên SSO của Keycloak đạt giới hạn thời gian không hoạt động hoặc thời lượng tối đa, khi yêu cầu làm mới token bị từ chối hoặc khi người dùng đăng xuất.

Khi đăng xuất, hệ thống xóa các token và chuyển hướng đến `end_session_endpoint` của Keycloak cùng các tham số `id_token_hint`, `client_id` và `post_logout_redirect_uri` (chính là địa chỉ gốc của portal). Vì vậy, lần đăng nhập tiếp theo trên cùng trình duyệt sẽ yêu cầu nhập lại mật khẩu.

## 2. Xác định danh tính và quyền truy cập của người dùng

| API | Dữ liệu trả về |
| --- | --- |
| `GET /api/auth/me` | `id`, `email`, `display_name` và `roles`: tất cả vai trò mà người dùng đang nắm giữ. Mỗi vai trò bao gồm `name`, `description` và thông tin chủ sở hữu (`app_id` hoặc `cap_id`; cả hai đều không có giá trị đối với vai trò nền tảng). |
| `GET /api/auth/apps` | `apps`: danh sách các ứng dụng đã cài đặt mà người dùng có ít nhất một vai trò. |

Mỗi phần tử trong `apps` bao gồm:

| Trường | Ý nghĩa |
| --- | --- |
| `app_id`, `display_name`, `version` | Thông tin định danh của ứng dụng. |
| `capabilities` | Các chức năng mà ứng dụng sử dụng. |
| `roles` | Các vai trò mà người dùng đang nắm giữ trong ứng dụng này (`name`, `description`). |
| `work_item_roles` | Tên các vai trò của người dùng trong ứng dụng được liệt kê trong `sharedAccess` đã lưu của ứng dụng đối với `cap_work.work_items`. Mảng rỗng có nghĩa là không có vai trò nào đủ điều kiện. Nếu trường này không xuất hiện trong phản hồi từ máy chủ phiên bản cũ, hãy xem như một mảng rỗng. |
| `shared_access` | Các mục trong `sharedAccess` của ứng dụng có khai báo vai trò mà người dùng đang nắm giữ. Bao gồm `table`, `roles` (chỉ chứa các vai trò của người dùng) và `columns`. Mảng rỗng có nghĩa là không có mục nào. Nếu trường này không xuất hiện trong phản hồi từ máy chủ phiên bản cũ, hãy xem như một mảng rỗng. |
| `tables` | Các bảng của ứng dụng, được cung cấp chính xác theo khai báo trong manifest, bao gồm `name`, `columns`, `foreignKeys`, `access`, `ui` và các trường `stateMachine`, `attachments`, `belongsTo` nếu có ([Hợp đồng manifest](../reference/manifest/schema.vi.md)). |

Gọi `/api/auth/me` ngay sau khi đăng nhập. API này đồng thời đăng ký người dùng mới vào nền tảng.

Các thay đổi về vai trò có hiệu lực sau vài giây. Vì vậy, hãy gọi lại cả hai API khi người dùng mở lại bộ chọn vai trò, thay vì lưu kết quả vào bộ nhớ đệm trong suốt phiên làm việc.

Một người dùng có thể nắm giữ nhiều vai trò. Giao diện cho phép người dùng chọn vai trò mà họ muốn sử dụng và gửi vai trò đó qua header `X-Hasura-Role` trong các yêu cầu GraphQL và cap-process.

Nếu người dùng chỉ có đúng một vai trò, có thể bỏ qua header này. Yêu cầu sử dụng một vai trò mà người dùng không nắm giữ sẽ bị từ chối; header này không có tác dụng cấp thêm quyền.

## 3. Xây dựng màn hình từ `tables`

| Loại bảng | Cách nhận biết | Màn hình |
| --- | --- | --- |
| Bảng quy trình | Có `stateMachine` | Màn hình danh sách, biểu mẫu, mỗi lệnh trong `ui.actions` tương ứng với một nút và một mục **Lịch sử** trên trang chi tiết. |
| Bảng thông thường | Không có `stateMachine` và không có `belongsTo` | Màn hình danh sách, biểu mẫu và mục **Lịch sử** trên trang chi tiết. |
| Bảng con | Có `belongsTo` | Hiển thị dưới dạng lưới dữ liệu bên trong trang chi tiết của bản ghi cha; không xuất hiện dưới dạng một mục riêng trong menu. |

Trang chi tiết bản ghi hiển thị lịch sử kiểm toán của bản ghi, bao gồm thời gian, người thực hiện, hành động, trạng thái sau khi thực hiện và vai trò đang sử dụng.

Lấy dữ liệu lịch sử thông qua API:

`GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history`

Yêu cầu phải kèm theo vai trò đang sử dụng ([Lịch sử bản ghi](../reference/api/capability-routes.md#record-history)).

Lịch sử được tải lại khi phiên bản bản ghi thay đổi. Nếu vai trò không có quyền đọc bản ghi, API trả về `404` và không hiển thị lịch sử.

Đặt tên cho mỗi bảng bằng `ui.title` nếu manifest có khai báo; nếu không, sử dụng tên bảng. Đặt tên cho trạng thái và hành động bằng `ui.stateLabels` và `ui.actionLabels`. Nếu không có nhãn, hãy sử dụng từ ngữ chung cho các tên workflow phổ biến (chẳng hạn `DRAFT` hoặc `approve`) hoặc tên đã được chuyển đổi sang dạng dễ đọc. Phần giao diện khung chung (shell) không chứa các nhãn dành riêng cho từng ứng dụng.

- `ui.listColumns` xác định các cột hiển thị trong danh sách; `ui.formFields` xác định các trường nhập liệu của biểu mẫu, bao gồm `name`, `label`, `type` (các kiểu `text`, `number`, `date`, `uuid` hoặc `file`) và `required`.

- Chỉ hiển thị một bảng khi có quy tắc `access.select` chỉ định vai trò đang sử dụng. Hiển thị chức năng **Tạo mới** khi có quy tắc `access.insert`, **Chỉnh sửa** khi có quy tắc `access.update` và **Xóa** trên một dòng của bảng con khi có quy tắc `access.delete`. Trường `columns` trong mỗi quy tắc xác định những trường dữ liệu mà vai trò đó được phép đọc hoặc ghi.

- Một bản ghi quy trình chỉ được phép chỉnh sửa khi `status` của bản ghi nằm trong danh sách `stateMachine.editableStates`. Một dòng dữ liệu thuộc bảng con chỉ được phép chỉnh sửa khi bản ghi cha của nó cũng đang ở trạng thái cho phép chỉnh sửa.

- Chỉ hiển thị một lệnh khi vai trò đang sử dụng nằm trong danh sách `allowedRoles` và `status` của bản ghi nằm trong danh sách `from`. Tuy nhiên, `recordFilter` của lệnh vẫn có thể từ chối thao tác (ví dụ: không ai được phép phê duyệt yêu cầu do chính mình tạo). Cần xử lý trường hợp API trả về `403`.

- Khóa ngoại tham chiếu đến một bảng khác trong cùng ứng dụng (`foreignKeys`) được hiển thị dưới dạng bộ chọn tham chiếu, sử dụng danh sách của bảng đó. Bộ chọn và trang chi tiết bản ghi xác định bản ghi được tham chiếu bằng cột có kiểu `text` đầu tiên mà vai trò có thể đọc trong `ui.listColumns` của bảng đích. Nếu vai trò không thể đọc cột nào trong số đó, hãy hiển thị ID của bản ghi.

- Các trường `created_by` và khóa ngoại tham chiếu đến những bảng dùng chung `core.users`, `core.files` hoặc `cap_data.devices` sẽ hiển thị lần lượt `display_name`, `file_name` hoặc `name` khi `shared_access` khai báo vai trò đang sử dụng cho bảng tương ứng, đồng thời cho phép đọc cả trường `id` và trường nhãn. Nếu không đáp ứng điều kiện này, hệ thống tiếp tục hiển thị ID.

- Định dạng giá trị theo kiểu dữ liệu của cột:
  - `timestamp` (bao gồm `created_at` và `updated_at`): hiển thị ngày và giờ theo định dạng `vi-VN`, sử dụng múi giờ của người xem.
  - `date`: hiển thị ngày theo định dạng `vi-VN`.
  - `integer` và `numeric(...)`: hiển thị số với cách phân tách chữ số theo định dạng `vi-VN`.

  Manifest không đánh dấu cột nào là tiền tệ, do đó không tự động thêm đơn vị tiền tệ.

Portal tự động mở bảng cấp cao nhất đầu tiên mà vai trò có quyền đọc trong danh sách bảng được khai báo bởi ứng dụng.

Màn hình danh sách hỗ trợ phân trang, sắp xếp và lọc văn bản theo giá trị khớp chính xác khi có cột kiểu `text` mà người dùng được phép đọc. Sử dụng chức năng **Mở** (Open) để xem một bản ghi. Các chức năng tạo mới, chỉnh sửa và điều khiển workflow tuân theo quyền truy cập và trạng thái được công bố cho bảng.

Các ứng dụng chỉ chứa dữ liệu tri thức sẽ mở phần dữ liệu tri thức của ứng dụng hoặc thư viện cá nhân đã được công bố.

## 4. Đọc và ghi bản ghi (GraphQL)

Sử dụng endpoint `POST /v1/graphql`.

Một bảng có tên `domain.<name>` sẽ được cung cấp dưới dạng `domain_<name>` trong GraphQL.

| Thao tác | Trường GraphQL |
| --- | --- |
| Liệt kê, lọc, sắp xếp | `domain_<name>(where:, order_by:, limit:, offset:)` |
| Lấy một bản ghi | `domain_<name>_by_pk(id:)` |
| Tạo mới | `insert_domain_<name>_one(object:)` |
| Chỉnh sửa | `update_domain_<name>_by_pk(pk_columns: {id:}, _set:)` |
| Xóa một dòng thuộc bảng con | `delete_domain_<name>_by_pk(id:)` |

Các mối quan hệ được xác định theo khóa ngoại:

- Từ một bản ghi đến bản ghi mà nó tham chiếu: sử dụng tên cột nhưng bỏ hậu tố `_id` (`vendor_id` trở thành `vendor`; `request_id` của bảng con trở thành `request`).

- Từ bản ghi được tham chiếu đến các bản ghi tham chiếu ngược lại: sử dụng tên theo định dạng `<table>_by_<column>` (ví dụ: `procurement_items_by_request_id` trên một bản ghi yêu cầu).

Các quy tắc cần tuân thủ khi xây dựng chức năng:

- Không bao giờ gửi các trường `id`, `created_by`, `org_unit_id`, `status`, `version`, `created_at` hoặc `updated_at`; máy chủ sẽ tự thiết lập các trường này. Khi tạo một dòng thuộc bảng con, cần gửi cột tham chiếu đến bản ghi cha (ví dụ: `request_id`); giá trị của cột này không được thay đổi sau đó.

- Vai trò không có quyền đối với một trường sẽ không được cung cấp trường đó. Vì vậy, GraphQL có thể trả về lỗi xác thực như `"field not found"` hoặc `"no mutations exist"`. Một dòng dữ liệu nằm ngoài phạm vi truy cập của vai trò sẽ không xuất hiện trong kết quả. Thao tác cập nhật hoặc xóa không khớp với bất kỳ dòng dữ liệu nào mà vai trò được phép thao tác sẽ trả về `null`.

- Mã trạng thái HTTP vẫn là `200` ngay cả khi yêu cầu bị từ chối. Cần kiểm tra trường `errors` và dữ liệu trả về.

- Mọi thay đổi đối với một bản ghi, cũng như mọi thay đổi đối với các dòng thuộc bảng con của bản ghi đó, đều làm tăng giá trị `version` của bản ghi. Hãy đọc lại bản ghi trước khi thực hiện một lệnh trên bản ghi đó.

## 5. Thay đổi trạng thái bản ghi (Commands)

```http
POST /api/cap/process/commands/{app_id}/{command}
X-Hasura-Role: <acting role>
Idempotency-Key: <a fresh UUID per user action>

{"entity_id": "<record id>", "expected_version": <version shown>, "payload": {}}
```

Trường `payload` chứa dữ liệu được khai báo trong `stateMachine.commands[].payload`: với mỗi phần tử, giá trị tương ứng là giá trị của cột đó (chuỗi đối với các kiểu `text`, `uuid` và `date`; số đối với các cột kiểu số).

Trước khi thực thi lệnh, hãy yêu cầu người dùng nhập các trường tương ứng và đánh dấu những trường có `required: true` là bắt buộc.

Ví dụ, lệnh `reject` của ứng dụng Procurement yêu cầu dữ liệu:

```json
{"rejection_reason": "..."}
```

Nếu lệnh không khai báo `payload`, hãy gửi `{}`.

Tên các lệnh là duy nhất trong phạm vi một ứng dụng, do đó không cần đưa tên bảng vào đường dẫn API. Phản hồi trả về `status` và `version` mới của bản ghi.

| Mã trạng thái | Ý nghĩa đối với giao diện |
| --- | --- |
| `200` | Thao tác thành công. Nếu gửi lại yêu cầu với cùng key và body, hệ thống sẽ trả về cùng một kết quả. |
| `400` | Dữ liệu `payload` không khớp với khai báo của lệnh: thiếu trường bắt buộc hoặc trường có giá trị rỗng, giá trị không đúng kiểu dữ liệu hoặc trường chưa được khai báo. |
| `401` | Token bị thiếu hoặc đã hết hạn: yêu cầu đăng nhập lại. |
| `403` | Vai trò không được phép thực hiện lệnh này trên bản ghi. |
| `404` | Vai trò không thể xem bản ghi này hoặc bản ghi không tồn tại. |
| `409` | Bản ghi đã thay đổi kể từ lần hiển thị trước hoặc không còn ở trạng thái mà lệnh cho phép bắt đầu: cần tải lại bản ghi. |
| `422` | Tệp đính kèm bắt buộc đang bị thiếu hoặc chưa có trạng thái `CLEAN`. |

Màn hình xác nhận phải hiển thị trạng thái và phiên bản của bản ghi đang được xem xét.

Nếu lệnh trả về `409`, portal sẽ tải lại bản ghi và yêu cầu người dùng xác nhận lại. Khi chưa xác định được kết quả thực hiện, nếu cần gửi lại yêu cầu thì phải sử dụng chính xác body và idempotency key ban đầu.

## 6. Tệp

1. Gửi yêu cầu `POST /api/cap/process/files/upload` với kiểu dữ liệu `multipart/form-data`, bao gồm `app_id` và `file`. Các định dạng được hỗ trợ là PDF, PNG hoặc JPEG, với dung lượng tối đa 20 MiB. Phản hồi trả về `file_id` cùng trạng thái quét `scan_status: QUARANTINED`.

2. Lưu `file_id` vào cột tệp đính kèm của bản ghi thông qua GraphQL. Chỉ tệp do chính người dùng đó tải lên cùng một ứng dụng mới được phép đính kèm.

Nếu yêu cầu bị từ chối, hệ thống trả về lỗi `permission-error` với thông báo bắt đầu bằng `Check constraint violation. attachment `. Hãy hiển thị một thông báo giải thích cố định thay vì hiển thị trực tiếp thông báo từ máy chủ.

3. Thăm dò trạng thái quét bằng truy vấn:

   ```graphql
   core_files(where: {id: {_eq: $id}}) { scan_status }
   ```

   Tiếp tục cho đến khi `scan_status` có giá trị `CLEAN` hoặc `INFECTED`. Bổ sung trường `file_name` nếu `shared_access` cho phép vai trò đang sử dụng đọc trường này.

   Các lệnh yêu cầu tệp đính kèm sẽ trả về `422` cho đến khi tệp có trạng thái `CLEAN`.

4. Sử dụng `GET /api/cap/process/files/{file_id}` để tải xuống một tệp có trạng thái `CLEAN` mà vai trò được phép đọc. Quyền đọc được áp dụng cho người tải tệp lên hoặc vai trò có quyền đọc bản ghi mà tệp đó được đính kèm.

Phản hồi chứa header `Content-Disposition` với tên tệp đã lưu (tên cơ sở, đã loại bỏ các ký tự điều khiển). Portal lưu tệp tải xuống bằng chính tên này.

## 7. Công việc (Work items)

Khi workflow của một ứng dụng mở một công việc (ví dụ: ứng dụng Procurement mở công việc **Review procurement request** khi một yêu cầu được gửi đi), hãy sử dụng `work_item_roles` từ `GET /api/auth/apps` để xác định các vai trò đủ điều kiện truy cập hộp thư công việc.
Gửi một vai trò đủ điều kiện trong header `X-Hasura-Role` của yêu cầu GraphQL. Hasura chịu trách nhiệm thực thi quyền truy cập đối với các công việc.

```graphql
query { cap_work_work_items(order_by: {created_at: desc}) {
  id title description priority board_column assignee_id source_type source_id created_at } }
```

`source_type` là tên bảng và `source_id` là ID của bản ghi liên quan đến công việc. Vì vậy, mỗi mục trong hộp thư công việc sẽ liên kết đến trang chi tiết của bản ghi tương ứng.
Một vai trò chỉ được đọc những công việc có bản ghi nguồn mà vai trò đó được phép đọc. Các công việc không có bản ghi nguồn vẫn hiển thị đối với toàn bộ ứng dụng.
Một công việc sẽ xuất hiện sau vài giây kể từ khi lệnh được thực hiện, sau khi workflow hoàn tất xử lý.

Mục **Cần bạn chú ý** (Needs your attention) hiển thị bản xem trước tối đa năm công việc gần đây thuộc các vai trò ứng dụng đủ điều kiện.

Mục **Việc của tôi** (My Work) tải thêm công việc, ẩn những công việc có `board_column` là `DONE`, hiển thị mô tả vai trò từ manifest của ứng dụng và lọc các kết quả đã tải theo ứng dụng và mức độ ưu tiên.

Nếu truy vấn theo vai trò thất bại, hệ thống hiển thị kết quả một phần hoặc thông báo không có dữ liệu. Khi người dùng mở một công việc hoặc liên kết gần đây, portal sẽ làm mới quyền truy cập trước khi đọc bản ghi nguồn.

### Chờ tôi xử lý (hộp thư bản ghi)

Hộp thư này được xác định dựa trên trạng thái bản ghi, không dựa trên work items.
Đối với mỗi lệnh mà vai trò đang sử dụng được phép thực hiện (lệnh nằm trong `ui.inboxActions`, hoặc nằm trong `ui.actions` nếu bảng không khai báo `ui.inboxActions`, đồng thời vai trò phải thuộc `allowedRoles`), hãy truy vấn bảng bằng chính vai trò đó với điều kiện:

```text
where: {_and: [{status: {_in: <the command's from states>}}, <recordFilter>]}
```
Sắp xếp theo `updated_at` giảm dần và giới hạn tối đa 20 bản ghi.
Quyền truy cập theo dòng của Hasura sẽ áp dụng phạm vi truy cập và bộ lọc `select` của vai trò. Do đó, một bản ghi sẽ tự động rời khỏi hộp thư ngay khi trạng thái của nó thay đổi.
Các lệnh có cùng `recordFilter` sẽ sử dụng chung một truy vấn; không được mở rộng phạm vi của một bộ lọc khác.
Trong bộ lọc, `X-Hasura-User-Id` được thay thế bằng ID của người dùng đã đăng nhập. Nếu xuất hiện bất kỳ placeholder `X-Hasura-*` nào khác, hãy bỏ qua nhóm lệnh tương ứng và đánh dấu kết quả là không đầy đủ.
Gộp các nhóm kết quả, loại bỏ các bản ghi trùng lặp theo ứng dụng, bảng và ID bản ghi; đồng thời giữ lại các nhãn lệnh có danh sách `from` chứa trạng thái của bản ghi.
Mỗi dòng dữ liệu liên kết đến URL trang chi tiết của bản ghi và mang theo vai trò đang sử dụng.
Nếu nhận được phản hồi `401`, hãy xóa phiên đăng nhập. Nếu nhận được `403`, hãy loại bỏ nhóm kết quả tương ứng. Các lỗi khác cần hiển thị kết quả một phần cùng chức năng thử lại.

## 8. Quản trị

Các API trong phần này chỉ dành cho người dùng nắm giữ vai trò nền tảng `role_operator` (vai trò này xuất hiện trong `/api/auth/me` và không có chủ sở hữu). Những người dùng khác sẽ nhận được phản hồi `403`.

| API | Chức năng |
| --- | --- |
| `GET /api/auth/users` | Trả về `users`: tất cả tài khoản cùng các trường `id`, `email`, `display_name`, `status` và danh sách `roles` mà tài khoản đang nắm giữ. |
| `GET /api/auth/roles` | Trả về `roles`: tất cả vai trò hiện có thể được cấp, bao gồm `name`, `description` và thông tin chủ sở hữu (`app_id` hoặc `cap_id`). |
| `PUT /api/auth/users/{user_id}/roles/{role}` | Cấp vai trò cho người dùng. Trả về `204`, kể cả khi người dùng đã có vai trò đó. |
| `DELETE /api/auth/users/{user_id}/roles/{role}` | Thu hồi vai trò của người dùng. Trả về `204`, kể cả khi người dùng chưa có vai trò đó. |

| Mã trạng thái | Ý nghĩa |
| --- | --- |
| `404` | Không tìm thấy người dùng hoặc vai trò không tồn tại, không còn hiệu lực (ứng dụng sở hữu vai trò chưa được cài đặt). |
| `409` | Thao tác phân quyền không thể thực hiện tại đây: `role_workflow` thuộc quyền quản lý của dịch vụ workflow và người vận hành không thể tự thu hồi vai trò `role_operator` của chính mình. |

Thay đổi được cập nhật ngay trong `/api/auth/me` và GraphQL của người dùng; đối với cap-process, thay đổi có hiệu lực sau vài giây mà không cần đăng nhập lại.

Mọi thay đổi đều được ghi nhận cùng thông tin người vận hành đã thực hiện. Một tài khoản sẽ xuất hiện trong danh sách sau khi tài khoản đó đăng nhập lần đầu.

### Phòng ban và nhân sự (cap-hr)

Tất cả người dùng đã đăng nhập đều có quyền đọc dữ liệu; các thao tác thay đổi yêu cầu vai trò chức năng `role_hr_admin`. Nếu không có vai trò này, hệ thống trả về `403`.

| API | Chức năng |
| --- | --- |
| `GET /api/cap/hr/me` | Trả về `org_unit` của người gọi (`id`, `code`, `name`, `parent_id`) và `position`. Cả hai có giá trị `null` nếu người dùng chưa thuộc phòng ban nào. Trường `administrator` cho biết người dùng có nắm giữ vai trò `role_hr_admin` hay không. |
| `GET /api/cap/hr/org-units` | Trả về `org_units`: tất cả phòng ban cùng các trường `id`, `parent_id`, `code`, `name`, `staff_count`. Xây dựng cây phân cấp phòng ban từ `parent_id`. |
| `GET /api/cap/hr/staff` | Trả về danh sách `staff` thuộc phòng ban của người gọi, bao gồm `user_id`, `display_name`, `email`, `org_unit_id`, `position`, `joined_at`. Quản trị viên có thể xem tất cả phòng ban hoặc một phòng ban được chỉ định bằng `?org_unit_id=`. Người dùng khác nếu chỉ định một phòng ban không thuộc quyền quản lý của mình sẽ nhận được `403`. |
| `POST /api/cap/hr/org-units` | Tạo phòng ban từ dữ liệu `{"code", "name", "parent_id"}` (`parent_id` là tùy chọn). Trả về `201` cùng `id` của phòng ban mới. |
| `PATCH /api/cap/hr/org-units/{id}` | Thay đổi các trường được gửi lên: `code`, `name`, `parent_id` (`null` có nghĩa là chuyển thành phòng ban cấp cao nhất). Trả về `204`. |
| `PUT /api/cap/hr/staff/{user_id}` | Phân công người dùng vào phòng ban bằng dữ liệu `{"org_unit_id", "position"}` (`position` là tùy chọn). Mỗi người dùng chỉ thuộc một phòng ban, vì vậy thao tác này sẽ chuyển người dùng sang phòng ban mới nếu cần. Trả về `204`. |
| `DELETE /api/cap/hr/staff/{user_id}` | Xóa người dùng khỏi phòng ban hiện tại. Trả về `204`. |

| Mã trạng thái | Ý nghĩa |
| --- | --- |
| `400` | Một trường dữ liệu bị thiếu, để trống, không đúng kiểu dữ liệu hoặc không được nhận diện. |
| `404` | Không tìm thấy phòng ban hoặc người dùng (bao gồm trường hợp xóa một người dùng chưa thuộc phòng ban nào). |
| `409` | Kèm theo thông báo để hiển thị: mã phòng ban đã được sử dụng, phòng ban cha không tồn tại hoặc việc thay đổi sẽ khiến một phòng ban trở thành cấp dưới của chính nó hay một trong các phòng ban con của nó. |

Việc chuyển người dùng sang phòng ban khác sẽ làm thay đổi dữ liệu mà người dùng có thể xem trong phạm vi `department`, bắt đầu từ yêu cầu tiếp theo.

Các bản ghi vẫn giữ nguyên phòng ban tại thời điểm chúng được tạo. Hệ thống hiện không cung cấp API để xóa phòng ban.

## 9. Dùng thử

Lệnh `scripts/seed-demo.py seed` tạo sáu tài khoản với mật khẩu `Demo@2026`, được phân bổ vào ba phòng ban.

Các lệnh `scripts/seed-demo.py scopes`, `flow`, `lines`, `admin` và `org` thực hiện những API đã trình bày ở trên từ dòng lệnh, đồng thời in ra dữ liệu và thao tác mà từng người dùng được phép xem và thực hiện.

Tập lệnh này là tài liệu tham khảo thực tế về cấu trúc của các yêu cầu API.

## 10. Trợ lý

Nút **Trợ lý** (Assistant) mở giao diện hội thoại, bản ghi nội dung trao đổi và các trích dẫn thông qua dịch vụ dùng chung `/api/cap/ai/conversations`.

Trên màn hình có chiều rộng nhỏ, ngăn giao diện trợ lý sẽ mở dưới dạng hộp thoại (modal). Nhấn phím Escape hoặc chọn **Đóng trợ lý** (Close assistant) để quay lại nút mở trợ lý.

Nội dung hội thoại và bản nháp vẫn được duy trì trong phiên hiện tại khi người dùng đóng rồi mở lại trợ lý.

Trợ lý sử dụng câu hỏi được nhập cùng nguồn dữ liệu tri thức mà trợ lý có thể truy cập. Các màn hình bản ghi và công việc vẫn hoạt động bình thường nếu dịch vụ Assistant bị gián đoạn.

## Những chức năng hiện chưa khả dụng

- **Thay đổi work items** (giao việc, di chuyển giữa các cột): `cap_work.work_items` hiện chưa có API route tương ứng. Tham khảo mục [Công việc](#7-work-items) để biết các chức năng đọc dữ liệu.

- **Tài khoản chưa từng đăng nhập** không xuất hiện trong danh sách cấp vai trò (đồng bộ tài khoản, #23).

- **Portal chưa có màn hình quản trị vai trò hoặc phòng ban**. Hãy sử dụng các API trong mục [Quản trị](#8-administration), hoặc chạy `scripts/seed-demo.py admin` và `org` trên môi trường demo.

- **Chưa hỗ trợ** dashboard tùy chỉnh, chế độ xem bảng công việc (board), chế độ xem lịch và bộ chọn mô hình cho Assistant.
