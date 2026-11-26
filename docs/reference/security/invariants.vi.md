---
title: Các bất biến bảo mật
description: Các thuộc tính bảo mật bắt buộc và đặc tả hợp đồng phản hồi giao thức.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

## Các bất biến bảo mật

- Backend chịu trách nhiệm xác thực danh tính và các quyền hiệu lực; giao diện người dùng (UI) hoặc vai trò được chỉ định trong yêu cầu không tự cấp quyền. Áp dụng nguyên tắc đặc quyền tối thiểu cho người dùng và các định danh dịch vụ.
- Token Keycloak chỉ mang thông tin định danh. Các vai trò mà người gọi được phép sử dụng được truy vấn từ `core.user_roles` trong mỗi yêu cầu ([ADR 0007](../../adr/0007-keycloak-authenticates-platform-authorizes.md)); một vai trò được chỉ định trong header hoặc claim của token chỉ là yêu cầu, không phải quyền được cấp.
- Các kết nối cơ sở dữ liệu tại thời điểm thực thi sử dụng các vai trò có đặc quyền tối thiểu, không sử dụng `superuser` hoặc `BYPASSRLS`. Operator là ngoại lệ duy nhất thuộc control plane: thành phần này tạo schema, bảng, trigger và cấp quyền, vì vậy trong môi trường Compose, Operator kết nối bằng tài khoản `POSTGRES_USER`. Không có dịch vụ xử lý yêu cầu nào hoặc Hasura được phép sử dụng tài khoản này (#116).
- Các vai trò cơ sở dữ liệu phục vụ xử lý yêu cầu có thời gian chờ thực thi câu lệnh là 10 giây. Hasura, cap-process, authz và cap-hr cũng có thời gian chờ 30 giây đối với transaction ở trạng thái idle; riêng API của cap-ai chỉ áp dụng giới hạn thời gian thực thi câu lệnh. Các thành phần nhập dữ liệu tri thức chạy nền, knowledge dispatcher, model gateway, Kestra và các vai trò migration/test không áp dụng giới hạn vì chúng thực hiện các tác vụ kéo dài. Các bản cài đặt hiện có áp dụng `0006_runtime_statement_limits.sql` bằng lệnh `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope core --through 0006`, sau đó khởi động lại các dịch vụ để những kết nối mới nhận được các thiết lập này.
- Thực thi phạm vi truy cập theo ứng dụng, chủ sở hữu bản ghi và phòng ban trên cả các bản ghi lẫn mối liên kết, thông qua cả các thao tác CRUD và các luồng xử lý của dịch vụ. Tư cách thành viên hiện tại trong cơ sở dữ liệu là nguồn xác thực có thẩm quyền; Keycloak xác minh danh tính và các claim về vai trò trong JWT không thể ghi đè thao tác thu hồi quyền trong cơ sở dữ liệu.
- Việc chuyển đổi trạng thái, kiểm tra phiên bản và ghi nhật ký kiểm toán phải được thực hiện nguyên tử. Mọi thao tác chỉnh sửa bản ghi đều phải tuân theo cơ chế quản lý phiên bản; thao tác chèn bản ghi không được phép bỏ qua trạng thái ban đầu.
- Chỉ được sử dụng các tệp đã được xác nhận sạch và có quyền truy cập phù hợp. Các vai trò thông thường không được tự xác nhận trạng thái `CLEAN`; lỗi trong quá trình quét không được làm phát sinh quyền truy cập.
- Việc phân phối sự kiện có thể lặp lại; mọi tác động nghiệp vụ phải có khả năng xử lý an toàn khi thao tác được thử lại. Các thao tác đối soát và xuất dữ liệu cũng phải an toàn khi được thực hiện lại.
- RAG bị giới hạn theo ứng dụng, nguồn dữ liệu, collection, chủ sở hữu và quyền của người gọi; kết quả phải kèm theo các nguồn được trích dẫn. OCR không được phép vượt quá phạm vi quyền thông qua `file_id` do client cung cấp.
- Các thao tác ghi nối tiếp vào nhật ký kiểm toán phải được tuần tự hóa trong phạm vi từng chuỗi kiểm toán đã được xác định rõ ràng và phải có các checkpoint được lưu bên ngoài cơ sở dữ liệu (cơ chế neo checkpoint hiện chưa được triển khai); bằng chứng của chuỗi kiểm toán cũ phải được bảo toàn thông qua một quy trình migration đã được phê duyệt. Khi thay đổi cơ chế kiểm toán, cần kiểm tra các thao tác ghi đồng thời và phát hiện những chỉnh sửa được thực hiện trước checkpoint. Payload kiểm toán không bao giờ được gửi ra khỏi máy chủ dưới dạng dữ liệu thô: lịch sử bản ghi là một tập dữ liệu chiếu cố định gồm thời gian, tác nhân thực hiện, hành động, vai trò thực thi, phiên bản và, đối với các bảng quy trình, trạng thái.

## Đặc tả hợp đồng phản hồi giao thức

REST Command: yêu cầu hoặc payload không đúng định dạng trả về `400`; thiếu thông tin xác thực hoặc thông tin xác thực không hợp lệ trả về `401`; vai trò hoặc `recordFilter` từ chối lệnh trả về `403`; bản ghi không tồn tại hoặc bản ghi mà vai trò thực thi không có quyền đọc trả về `404`; phiên bản đã cũ, trạng thái nằm ngoài phạm vi `from` của lệnh hoặc khóa idempotency được sử dụng lại với payload khác trả về `409`; tệp đính kèm bắt buộc bị thiếu hoặc không ở trạng thái `CLEAN` trả về `422`; dependency không khả dụng trả về `503`. Việc kiểm tra quyền đọc được thực hiện trước tiên, do đó bản ghi nằm ngoài phạm vi truy cập của người gọi sẽ không để lộ trạng thái, phiên bản hay các tệp của bản ghi đó.

Hasura GraphQL: cần kiểm tra `data`, `errors`, số lượng bản ghi bị ảnh hưởng và trạng thái trong cơ sở dữ liệu. Bộ lọc bản ghi có thể trả về kết quả rỗng; một trường không được phép truy cập có thể gây ra lỗi GraphQL ngay cả khi HTTP trả về `200`. Yêu cầu được xem là đáp ứng khi không có dữ liệu hoặc mối quan hệ nằm ngoài phạm vi quyền bị tiết lộ và không có thao tác ghi trái phép nào được thực hiện; không bắt buộc mọi trường hợp từ chối đều phải trả về HTTP `403`.
