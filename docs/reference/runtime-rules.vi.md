---
title: Các quy tắc tại thời điểm thực thi
description: Phần nội dung trong manifest và hợp đồng định danh mà các dịch vụ phải kiểm tra lại trước khi thực hiện hành động.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Các quy tắc tại thời điểm thực thi

Operator xác thực toàn bộ manifest trước khi một ứng dụng chuyển sang trạng thái `Ready`. cap-process chỉ tải các ứng dụng ở trạng thái `Ready` có tất cả các chức năng được bật, đồng thời kiểm tra lại những dữ liệu được đưa vào SQL hoặc được sử dụng để quyết định quyền truy cập; nếu phát hiện một bản ghi bị hỏng, hệ thống sẽ từ chối xử lý theo nguyên tắc an toàn (fail closed).

## Các quy tắc được dịch vụ thực thi

- Mỗi bảng có tên theo định dạng `domain.<identifier>` và chỉ được khai báo một lần; các cột phải có tên định danh, sử dụng kiểu dữ liệu được hỗ trợ và không được định nghĩa lại các cột hệ thống (`id`, `created_by`, `org_unit_id` khi có cap-hr, `status` trên bảng quy trình, `version`, `created_at`, `updated_at`).
- Một tên lệnh chỉ được khai báo bởi đúng một bảng và lệnh đó phải được thực thi trên chính bảng này.
- Bảng con phải chỉ định một bảng cha thuộc cùng ứng dụng và bản thân bảng cha đó không được là bảng con; bảng con không được có máy trạng thái hoặc tệp đính kèm. cap-process chỉ đọc các bản ghi của bảng con.
- Mọi quy tắc truy cập và lệnh đều phải chỉ định một vai trò đã được khai báo; phạm vi truy cập phải là `own` (bản ghi của chính người dùng), `department` (yêu cầu có cap-hr) hoặc `organization` (toàn tổ chức).
- `recordFilter` chỉ được sử dụng `_and`, `_or` và các cột đã khai báo, được so sánh bằng `_eq`/`_neq` với một giá trị literal tương thích kiểu dữ liệu, `null` hoặc `X-Hasura-User-Id`. `_eq` với giá trị `null` tương ứng với IS NULL; `_neq` với giá trị `null` tương ứng với IS NOT NULL. Các phép so sánh SQL thông thường với NULL không trả về kết quả khớp.
- Tệp đính kèm phải sử dụng cột kiểu `uuid` có khóa ngoại tham chiếu đến `core.files`; các tác động của workflow phải là những bước `create_work_item` hoặc `complete_work_item` thuộc các lệnh đã khai báo. Chỉ `create_work_item` mới được khai báo các trường.
- Tên vai trò phải bắt đầu bằng `role_`, nhằm ngăn việc sử dụng tên vai trò mà Hasura xem là có đặc quyền (chẳng hạn `admin`); `dxos-authz` không bao giờ cấp các vai trò có tên như vậy.

Tệp đính kèm chỉ được phép tham chiếu đến tệp thuộc cùng ứng dụng và do chính người dùng đang thực hiện thao tác tải lên (`core.enforce_attachments`, một trigger trên mọi bảng domain được tạo tự động). Khi tệp đính kèm bị từ chối, hệ thống phát sinh SQLSTATE `23514` (`check_violation`) cùng thông báo `attachment <column>: only a file you uploaded to this application can be attached`. Một vai trò chỉ được đọc metadata của tệp nếu người dùng thuộc vai trò đó đã tải tệp lên hoặc tệp được đính kèm vào một bản ghi mà vai trò đó có quyền đọc.

Một bản ghi con sẽ bị từ chối nếu bản ghi cha không còn ở trạng thái cho phép chỉnh sửa hoặc nếu thao tác khiến bản ghi con chuyển sang một bản ghi cha khác. Cả hai trường hợp từ chối đều sử dụng SQLSTATE `23514` với tiền tố thông báo ổn định: `the parent record can no longer be changed (status <status>)` và `a row cannot move to another parent record`. Các hệ thống đã cài đặt áp dụng hàm được định nghĩa lại bằng lệnh `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope core --through 0005` (cùng runner này cũng áp dụng `0004_attachment_refusal.sql`).


## Định danh, vai trò và lệnh

`contracts/fixtures/identity-claims.json` minh họa các claims đã giải mã từ access token của Keycloak. Đây không phải là token đã được ký. Token chỉ mang thông tin định danh (`sub`, email, name, audience `dxos-runtime`). Bên gọi chỉ định vai trò mà mình sử dụng để thực hiện thao tác thông qua `X-Hasura-Role`; vai trò này chỉ được chấp nhận khi `core.user_roles` hiện đang cấp vai trò đó. Bên gọi chỉ có đúng một vai trò có thể bỏ qua trường này. Các header `X-Hasura-User-Id` thô không bao giờ tạo ra quyền hạn. Các lệnh gọi tác động của workflow được gửi từ workflow engine. Thành phần này sử dụng thông tin xác thực của dịch vụ workflow thay cho token người dùng và hoạt động với tư cách tài khoản dịch vụ workflow của nền tảng ([triển khai workflow](api/events.vi.md)).

`dxos-authz` cung cấp hai API chỉ đọc dành cho giao diện người dùng: `GET /api/auth/me` (thông tin định danh và các vai trò của bên gọi) và `GET /api/auth/apps` (các ứng dụng đã cài đặt mà bên gọi có vai trò, kèm theo những vai trò đó và các bảng của từng ứng dụng theo đúng nội dung manifest khai báo). Cả hai API đều không cấp bất kỳ quyền nào. Đối với các operator của nền tảng (`role_operator`), dịch vụ còn cung cấp chức năng liệt kê tài khoản và vai trò, cũng như cấp hoặc thu hồi vai trò; mỗi thay đổi đều được ghi vào chuỗi nhật ký kiểm toán `core`. Xem thêm [tích hợp giao diện người dùng](../guides/integrating-the-ui.vi.md).

Một lệnh được chấp nhận khi đáp ứng các điều kiện sau:

1. Vai trò đang thực hiện thao tác được phép sử dụng lệnh đó;
2. Bản ghi có thể được vai trò đó đọc theo phạm vi truy cập của vai trò (các bản ghi của chính người dùng, phòng ban của bên gọi lấy từ `cap_hr.staff` hoặc toàn bộ tổ chức) và theo bộ lọc bản ghi tương ứng;
3. Bản ghi đáp ứng bộ lọc bản ghi của lệnh (ví dụ: không ai được phép phê duyệt yêu cầu do chính mình tạo);
4. Payload chỉ chứa các cột được lệnh khai báo, có đầy đủ mọi cột bắt buộc và mỗi giá trị đều phù hợp với kiểu dữ liệu của cột tương ứng (nếu không, trả về mã lỗi 400);
5. Phiên bản và trạng thái phải khớp;
6. Mọi tệp đính kèm mà lệnh yêu cầu đều phải có trạng thái `CLEAN`.

Một bản ghi nằm ngoài phạm vi truy cập của vai trò sẽ trả về mã 404, nhằm tránh tiết lộ sự tồn tại của bản ghi đó.

Bộ lọc bản ghi chỉ so sánh các cột của chính bản ghi, do đó không thể yêu cầu hai bước xử lý phải được thực hiện bởi hai người khác nhau. Trong `apps/procurement`, một người có đồng thời cả hai vai trò `role_procurement_manager` và `role_procurement_finance` có thể phê duyệt cả hai bước trong yêu cầu của người khác. Việc phân tách nhiệm vụ giữa các bước thuộc trách nhiệm của công tác quản trị vai trò: không cấp cả hai vai trò cho cùng một người (#114).

Các hàng trong bảng con (chẳng hạn các dòng chi tiết của một yêu cầu) được ghi thông qua GraphQL. Mỗi thay đổi đều làm tăng phiên bản của bản ghi cha, vì vậy một lệnh sử dụng phiên bản đã đọc trước khi thay đổi sẽ nhận được mã lỗi 409; khi bản ghi cha rời khỏi các trạng thái cho phép chỉnh sửa, cơ sở dữ liệu sẽ từ chối mọi thay đổi tiếp theo.

Việc thay đổi trạng thái cùng các cột trong payload, bản ghi nhật ký kiểm toán (gồm actor, vai trò đang sử dụng và lệnh), sự kiện outbox và biên nhận idempotency được commit trong cùng một transaction. Một biên nhận được định danh bằng actor, ứng dụng, thực thể, lệnh và `Idempotency-Key`. Dấu vân tay của biên nhận là giá trị SHA-256 của JSON chuẩn hóa `{entity_id, expected_version, payload}`. Khi thử lại với cùng khóa và cùng yêu cầu, hệ thống trả về phản hồi đã lưu sau khi kiểm tra lại quyền truy cập hiện tại; nếu gửi một yêu cầu khác với cùng khóa, hệ thống trả về mã lỗi 409. `command-request.json` và `command-committed.json` là các ví dụ tương ứng về yêu cầu và phản hồi.

`event-retry.json` giống hệt `event-command-committed.json`: các lần thử gửi lại qua tầng truyền tải phải giữ nguyên event id và payload. Trường `outbox_events.id` trong cơ sở dữ liệu chính là `event_id` được truyền qua giao thức. Việc gửi sự kiện không cấp quyền; một tác động của workflow phải xác định nguồn từ sự kiện đã được lưu và các bước đã khai báo của ứng dụng, tuyệt đối không lấy thông tin từ body của bên gọi. Mỗi bước chỉ được thực thi tối đa một lần cho mỗi sự kiện (`cap_process.processed_workflow_steps`). Một sự kiện được chuyển đến luồng mà operator đã triển khai cho ứng dụng đó (`core.app_workflows`), không bao giờ được chuyển đến một đích do chính sự kiện chỉ định.

`complete_work_item` đặt `board_column = 'DONE'` cho các work item của ứng dụng có tham chiếu đến bản ghi của sự kiện, đồng thời commit bản cập nhật này cùng với biên nhận. Lệnh trả về số lượng work item đã hoàn thành, hỗ trợ phát lại thông qua cùng một biên nhận và không bao giờ tác động đến work item của ứng dụng khác.

Các work item được tạo bởi một tác động sẽ ghi nhận `entity_version` của sự kiện. Các tác động trên cùng một bản ghi được tuần tự hóa: thao tác hoàn thành chỉ đóng những work item được tạo bởi các sự kiện trước đó; thao tác tạo sẽ bị bỏ qua nếu một sự kiện hoàn thành xảy ra sau đó đã được ghi nhận. Nhờ vậy, tập hợp các work item đang mở tuân theo thứ tự sự kiện thay vì thứ tự sự kiện đến. Các hệ thống đã cài đặt áp dụng `0007_work_item_source_version.sql` bằng lệnh `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope core --through 0007`.

## Fixtures và kiểm tra

`contracts/fixtures/negative-cases.json` liệt kê các trường hợp bắt buộc phải bị từ chối. Những trường hợp được đánh dấu `TESTED` đã được bao phủ bởi các bài kiểm thử tích hợp của cap-process; những trường hợp còn lại ghi nhận các cơ sở đầu vào cho thiết kế. Các bài kiểm thử hợp đồng kiểm tra tính nhất quán của những ví dụ này:

```bash
python3 -m unittest discover -s tests -p test_machine_contracts.py -v
```

Chỉ kiểm tra cấu trúc schema không đủ để xác định tính an toàn về mặt ngữ nghĩa của một manifest.

Cấu hình của cap-process: `DATABASE_URL`, `OIDC_ISSUER_URL`, `OIDC_JWKS_URL`, `OIDC_AUDIENCE`, `ROLE_CACHE_TTL`, `KESTRA_URL`, `KESTRA_USER`, `KESTRA_PASSWORD`, `KESTRA_WEBHOOK_KEY`, `WORKFLOW_SERVICE_TOKEN`, `WORKFLOW_SERVICE_USER_ID`, `WORKFLOW_SERVICE_ROLE`, `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`, `CLAMAV_ADDRESS`. Các bài kiểm thử cơ sở dữ liệu yêu cầu `TEST_DATABASE_URL` và build tag `integration`. Giao diện người dùng chỉ nhận cấu hình công khai về issuer, client, callback và gateway; thông tin xác thực của dịch vụ không bao giờ được để lộ ra bên ngoài.