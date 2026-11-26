---
title: Các route của capability
description: Tập hợp các endpoint HTTP dự kiến của cap-hr, cap-process, cap-data và cap-ai.
diataxis: reference
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


# Các route của capability

Mọi route đều được truy cập thông qua gateway bằng Keycloak bearer token và, đối với các lệnh có phạm vi theo vai trò, phải gửi kèm vai trò thực thi trong `X-Hasura-Role`.

`contracts/runtime-v1.openapi.yaml` mô tả các route của cap-process.

| Capability | Các route | Trạng thái |
| --- | --- | --- |
| `dxos-authz` | `GET /api/auth/me`, `GET /api/auth/apps`; đối với `role_operator`: `GET /api/auth/users`, `GET /api/auth/roles`, `PUT` và `DELETE /api/auth/users/{user_id}/roles/{role}` ([Tích hợp giao diện người dùng](../../guides/integrating-the-ui.vi.md#2-xác-định-danh-tính-và-quyền-truy-cập-của-người-dùng)) | Đã triển khai |
| `cap-hr` | `GET /api/cap/hr/me`, `GET /api/cap/hr/org-units`, `GET /api/cap/hr/staff`; đối với `role_hr_admin`: `POST /api/cap/hr/org-units`, `PATCH /api/cap/hr/org-units/{id}`, `PUT` và `DELETE /api/cap/hr/staff/{user_id}` ([Tích hợp giao diện người dùng](../../guides/integrating-the-ui.vi.md#8-quản-trị)) | Đã triển khai |
| `cap-process` | `POST /api/cap/process/commands/{app_id}/{command}`, `POST /api/cap/process/files/upload`, `GET /api/cap/process/files/{file_id}`, `GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history` (xem bên dưới); `POST /api/cap/process/files/private/upload` dành cho các nguồn của thư viện cá nhân ([Đặc tả hợp đồng tri thức](../rag/knowledge-contract.vi.md)) | Đã triển khai |
| Tác động của `cap-process` | `POST /api/cap/process/effects/work-item`, chỉ dành cho workflow service; gateway không định tuyến đến route này ([Sự kiện](events.md#workflow-deployment)) | Đã triển khai |
| `cap-data` | `POST /api/cap/data/ot/events`, `POST /api/cap/data/lakehouse/export` | Chưa được đưa vào sử dụng: các handler đang truy cập những bảng core đã ngừng sử dụng và không được triển khai bởi Compose hoặc định tuyến qua gateway; dự kiến trong v1.1.0 (#111) |
| `cap-ai` | [API tri thức và nguồn dữ liệu](../rag/api.md), [các câu trả lời từ mô hình tùy chọn](../rag/answers.md) và các cuộc hội thoại của chủ sở hữu bên dưới, nằm dưới `/api/cap/ai/` | Đã triển khai trong overlay `compose.rag.yaml`. Các route `/rag` cũ và các route trích xuất dữ liệu không khả dụng |

## Lịch sử bản ghi

`GET /api/cap/process/records/{app_id}/{table}/{entity_id}/history` trả về lịch sử kiểm toán của một bản ghi. `{table}` là tên bảng trong registry theo định dạng mà portal tiếp nhận, `schema.table`. Gửi bearer token và vai trò thực thi trong `X-Hasura-Role`, tương tự như khi gửi một lệnh.

Trước khi đọc các dòng dữ liệu kiểm toán, bản ghi được kiểm tra bằng cùng các quy tắc viewer và select như khi thực thi một lệnh. Bảng không xác định, bảng con, bản ghi không tồn tại và bản ghi nằm ngoài phạm vi truy cập của người gọi đều trả về 404, do đó route này không tiết lộ sự tồn tại của bản ghi; application, table hoặc record ID không đúng định dạng sẽ trả về 400. Phản hồi có cấu trúc `{"entries":[{"at","action","actor":{"id","display_name"},"acting_role","acting_role_description","version","status"}]}`: tối đa 200 mục mới nhất, được sắp xếp từ cũ đến mới. `acting_role_description` là phần mô tả vai trò trong registry và sẽ bị lược bỏ nếu giá trị rỗng. `status` chỉ xuất hiện khi bảng có state machine và quy tắc select của vai trò cho phép hiển thị `status`. Khi quy tắc select của vai trò không cho phép hiển thị `created_by`, các trường `actor`, `acting_role` và `acting_role_description` sẽ bị lược bỏ; `at`, `action` và `version` vẫn được giữ lại. Payload kiểm toán, các cột chain/sequence và hash không bao giờ được trả về.

## Các cuộc hội thoại của chủ sở hữu

Gửi access token dạng bearer. Máy chủ xác định người dùng Core đang hoạt động và các vai trò hiện tại của người dùng trong ứng dụng nguồn. Đường dẫn cơ sở là `/api/cap/ai/conversations`.

| Phương thức và đường dẫn tương đối | Yêu cầu và phản hồi |
| --- | --- |
| `POST` (đường dẫn cơ sở) | Gửi `{"title":"…"}` để tạo một cuộc hội thoại thuộc quyền sở hữu của người gọi. Trả về 201 cùng ID cuộc hội thoại và timestamp `created_at`. |
| `GET` (đường dẫn cơ sở) | Liệt kê các cuộc hội thoại thuộc quyền sở hữu của người gọi. Có thể truyền thêm `limit` và `cursor`. Trả về danh sách `conversations` cùng timestamp tạo và `next_cursor` có thể nhận giá trị null. |
| `GET /{id}` | Đọc lịch sử hội thoại của chủ sở hữu theo thứ tự. Có thể truyền thêm `limit` và `after_seq`. Trả về `conversation_id`, `messages` và `next_after_seq` có thể nhận giá trị null. |
| `DELETE /{id}` | Chấm dứt quyền truy cập vào cuộc hội thoại. Trả về 204. |
| `POST /{id}/messages` | Gửi `{"query":"…"}` và một `Idempotency-Key` cho mỗi ý định gửi yêu cầu. Trả về một lượt xử lý không sử dụng streaming. Khi thử lại do lỗi truyền tải, hãy sử dụng lại khóa và câu hỏi đó. |
| `GET /{id}/citations/{chunk}` | Truy xuất URL trích dẫn đã trả về cùng với chính xác các giá trị `document_id`, `version_id` và `message_seq` của tin nhắn assistant. Quyền READ hiện tại được kiểm tra trong mỗi lần truy xuất. |

Phản hồi của một lượt xử lý chứa `status` (`ANSWERED`, `SEARCH_ONLY` hoặc `NO_EVIDENCE`), `answer` có thể nhận giá trị null và `citations`. Phản hồi cũng có thể bao gồm `search_status`, `warnings`, `reason` và `usage`. Phản hồi kết thúc và phản hồi phát lại (replay) đều mang `message_seq` bền vững của tin nhắn assistant; sequence của tin nhắn USER liền trước nhỏ hơn một đơn vị. Replay đang chờ xử lý trả về `SEARCH_ONLY` cùng `TURN_PENDING` và không bao gồm sequence.

Khi tìm kiếm thất bại và không có phạm vi nào hoàn tất thành công, phản hồi kết thúc là `SEARCH_ONLY` cùng `SEARCH_UNAVAILABLE`. Khi một số phạm vi hoàn tất nhưng những phạm vi khác thất bại, phản hồi chứa cảnh báo chung `SEARCH_INCOMPLETE` và vẫn có thể sử dụng bằng chứng thuộc phạm vi được cấp quyền. Các trường hợp từ chối quyền truy cập theo phạm vi sẽ được che giấu.

Việc sử dụng lại một khóa với nội dung văn bản khác hoặc gửi một ý định khác trong khi ý định trước đó đang được xử lý sẽ trả về 409. Người gọi không được xác thực nhận 401; các cuộc hội thoại thuộc người khác và những trích dẫn không thể truy cập trả về 404; tính năng chat bị vô hiệu hóa trên cap-ai trả về 503. Việc thu hồi quyền truy cập nguồn dữ liệu vẫn bảo toàn bản ghi hội thoại đã được gửi đến chủ sở hữu, đồng thời hạn chế quyền truy cập vào các trích dẫn trong tương lai và ngữ cảnh được cung cấp cho mô hình. Xem [chính sách xử lý](../rag/authorization.md).
