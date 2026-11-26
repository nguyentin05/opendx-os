---
title: Ghi dữ liệu, phiên bản và kiểm toán
description: Quyền sở hữu trường dữ liệu, cơ chế kiểm soát đồng thời lạc quan, biên nhận lệnh và hoạt động kiểm toán.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


# Ghi dữ liệu, phiên bản và kiểm toán

## Ma trận ghi dữ liệu và quyền sở hữu trường

| Thao tác | Vai trò người dùng | Trách nhiệm của máy chủ |
| --- | --- | --- |
| Tạo bản ghi | Chỉ được ghi các cột nghiệp vụ nằm trong danh sách cho phép `insert` | Gán thông tin người tạo và phòng ban, trạng thái ban đầu, phiên bản `1`, dấu thời gian; kiểm tra các khóa ngoại (FK) và điều kiện khởi tạo |
| Chỉnh sửa nội dung | Các cột nằm trong danh sách cho phép `update`, chỉ khi bản ghi đang ở trạng thái thuộc `editableStates` | Lọc theo phạm vi truy cập và quyền hạn, tăng phiên bản, ghi nhận thời gian và thông tin kiểm toán trong cùng một transaction |
| Chuyển đổi trạng thái | Gửi lệnh kèm `expected_version` và payload được khai báo cho lệnh đó | Kiểm tra vai trò, trạng thái hiện tại, quyền trên bản ghi, các tệp liên quan và payload; ghi trạng thái cùng các cột dữ liệu trong payload một cách nguyên tử |
| Upsert/thao tác thay đổi dữ liệu lồng nhau | Mặc định bị từ chối; nếu được cấp phép, phải đáp ứng cả quy trình `insert` và `update` | Không được đặt lại trạng thái/phiên bản, thay đổi người tạo hoặc phòng ban, hay bỏ qua các khóa ngoại |
| Xóa | Chỉ áp dụng với các hàng thuộc bảng con, do vai trò có quyền chỉnh sửa bản ghi cha thực hiện | Từ chối thao tác khi bản ghi cha không còn ở trạng thái cho phép chỉnh sửa; tăng phiên bản của bản ghi cha; ghi thông tin kiểm toán về hàng đã xóa. Bản ghi quy trình và bản ghi thông thường không bao giờ bị xóa: sử dụng lệnh `cancel` |

Loại bỏ `status`, `version`, `created_by`, `org_unit_id` và các dấu thời gian khỏi danh sách những cột mà client được phép chèn/cập nhật. Sử dụng các trigger và preset phía máy chủ, không chỉ dựa vào mệnh đề SQL `DEFAULT` được khai báo. Đối với `core.files`, client không được ghi `scan_status`, mã băm xác minh hoặc kết quả quét; quyền này thuộc về dịch vụ quét tệp/dịch vụ tệp.

Quyền đọc được khai báo theo từng bảng, vai trò và cột. Ví dụ, `role_maintenance_technician` được đọc các cột của bản ghi bảo trì được liệt kê trong quy tắc `select` và, thông qua `sharedAccess`, chỉ được đọc những cột cần thiết của `core.users`. Một relationship chỉ được tạo khi bảng đích cũng có quyền đọc. Một vai trò chỉ thuộc về một ứng dụng, vì vậy vai trò quản lý không cấp bất kỳ quyền nào bên ngoài ứng dụng đó; chỉ riêng tên vai trò (`role_procurement_manager`, `role_timesheet_manager`) không hàm ý quyền trên toàn hệ thống.

Hasura thực thi quyền theo từng bảng, thao tác, vai trò, hàng và cột; PostgreSQL `GRANT` không thể thay thế metadata của Hasura. Các bảng miền nghiệp vụ duy trì trạng thái và phiên bản độc lập, ngay cả khi chúng cùng tham chiếu đến một nhân viên hoặc một work item.

## Phiên bản, lệnh, kiểm toán và thử lại

**Cơ chế được lựa chọn:** một trigger `BEFORE UPDATE` trên bảng miền nghiệp vụ thiết lập `NEW.version = OLD.version + 1` và `updated_at` cho mỗi thao tác UPDATE thành công. Client và Command API không tự tăng phiên bản; Operator tạo trigger cùng với bảng. Một lần thử lại đã được nhận diện sẽ không thực hiện thêm thao tác UPDATE nào.

Một lệnh so sánh `id`, phạm vi truy cập và quyền hạn của vai trò thực hiện, trạng thái và `expected_version` trong cùng một transaction bằng cách sử dụng khóa hàng (row lock) hoặc một câu lệnh UPDATE có điều kiện tương đương. Nếu bản ghi hợp lệ trong phạm vi truy cập của người gọi nhưng phiên bản đã thay đổi, trả về `409`; không ghi trạng thái, thông tin kiểm toán hoặc sự kiện nghiệp vụ cho lần thử không thành công.

Ví dụ: người phê duyệt đọc `version=1`; người yêu cầu chỉnh sửa `quoted_amount` thông qua CRUD, khiến bản ghi chuyển thành `version=2`; một lệnh phê duyệt mang `expected_version=1` phải bị từ chối. Khi hai lệnh có cùng phiên bản được thực thi đồng thời, tối đa chỉ một lần chuyển đổi trạng thái được phép thành công.

Một lệnh khai báo `payload` sẽ ghi các cột đó trong cùng câu lệnh thay đổi trạng thái, nhờ vậy trạng thái mới và dữ liệu của nó cùng sử dụng một phiên bản, một hàng kiểm toán và một sự kiện. Payload của yêu cầu chỉ được chứa các cột đã khai báo; cột bắt buộc phải có mặt và không được để trống, đồng thời mỗi giá trị phải phù hợp với kiểu dữ liệu của cột tương ứng (payload bị từ chối sẽ trả về `400` và không làm thay đổi dữ liệu). Cột không được đề cập trong payload sẽ giữ nguyên giá trị. Lệnh không khai báo payload sẽ sử dụng payload rỗng.

Mỗi lệnh phải có một khóa idempotency. Phạm vi khóa bao gồm actor, ứng dụng, thực thể, lệnh và khóa; máy chủ lưu dấu vân tay payload cùng kết quả trong cùng một transaction. Khi thử lại với cùng khóa và payload, máy chủ trả về kết quả đã ghi nhận sau khi kiểm tra quyền hiện tại, trước khi đánh giá lại phiên bản cũ; nếu cùng một khóa được sử dụng với payload khác, máy chủ trả về `409`. Lược đồ biên nhận và các thành phần sử dụng nó phải triển khai đầy đủ phạm vi này; chỉ lưu kết quả trong bộ nhớ là không đủ.

Ghi thay đổi, thông tin kiểm toán và sự kiện bền vững trong cùng một transaction. cap-process ghi sự kiện vào `core.outbox_events` trong transaction của lệnh; sau đó, publisher chuyển sự kiện đến workflow mà operator đã triển khai cho ứng dụng (`core.app_workflows`). Một lời gọi HTTP đến Kestra sau khi commit không bao giờ được là cơ chế phân phối duy nhất. Workflow loại bỏ việc thực thi trùng lặp của từng tác động dựa trên định danh sự kiện và bước thực hiện (`cap_process.processed_workflow_steps`); việc thử lại sau sự cố không tạo ra các tác động nghiệp vụ trùng lặp.

Các tác động của workflow trên cùng một bản ghi phải tuân theo thứ tự sự kiện, không phải thứ tự sự kiện đến; xem [các quy tắc tại thời điểm thực thi](../runtime-rules.vi.md#định-danh-vai-trò-và-lệnh).

Hoạt động kiểm toán được tuần tự hóa theo từng chuỗi (mỗi ứng dụng có một chuỗi riêng, ngoài ra còn có chuỗi `core`) trước khi đọc giá trị đầu chuỗi và tính toán mã băm, bằng cách sử dụng `pg_advisory_xact_lock(hashtext('dxos.audit:' || chain))` bên trong transaction. Chuỗi này chỉ phát hiện được hàng đã bị thay đổi hoặc xóa khi đối chiếu với một giá trị đầu chuỗi đã biết: việc neo các checkpoint đã ký bên ngoài PostgreSQL là một yêu cầu hiện chưa được triển khai, do đó chuỗi này không thể chống lại hành vi viết lại lịch sử bởi người dùng có quyền superuser trên cơ sở dữ liệu.
