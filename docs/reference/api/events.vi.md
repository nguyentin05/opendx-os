---
title: Triển khai workflow và sự kiện
description: Cách triển khai các flow, định tuyến sự kiện và xử lý các callback tác động của workflow.
diataxis: reference
audience: [platform-developer, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
---

### Triển khai workflow

Các flow của ứng dụng là các Kestra flow nằm trong package của ứng dụng (`workflows/*.yaml`). Operator triển khai các flow này; không cần tải lên thủ công và cũng không cần khởi động lại bất kỳ dịch vụ nào.

**Những nội dung một flow được phép chứa.** Package của ứng dụng không chứa mã nguồn, vì vậy Operator từ chối những flow mà nó không thể kiểm soát và xác minh:

- `id` phải trùng với workflow id trong manifest và `namespace` phải là `dxos.apps.<app_id>`, nhằm ngăn ứng dụng thay thế flow của ứng dụng khác;
- chỉ có chính xác một trigger, đó là webhook được định danh bằng `{{ secret('DXOS_WEBHOOK_KEY') }}`;
- chỉ được sử dụng các task thuộc những loại `io.kestra.plugin.core.flow.If`, `io.kestra.plugin.core.http.Request` và `io.kestra.plugin.core.log.Log`;
- một request phải là phương thức `POST` gửi đến `{{ secret('DXOS_PROCESS_URL') }}/api/cap/process/effects/<effect>`.

Các script, container, lịch chạy (schedule) và request đến bất kỳ host nào khác đều bị từ chối. Package sẽ không được đưa vào cho đến khi flow đáp ứng các yêu cầu.

**Triển khai và khôi phục.** Khi Operator được cung cấp thông tin kết nối đến workflow engine (`KESTRA_URL`, `KESTRA_USER`, `KESTRA_PASSWORD`), mỗi lần reconcile, Operator sẽ đảm bảo engine lưu giữ các flow của những ứng dụng đã cài đặt và ghi nhận từng flow vào `core.app_workflows` (`app_id`, `workflow_id`, `namespace`, `flow_id`, `definition` đã triển khai và `revision` của engine). Khi tệp workflow thay đổi, flow sẽ được triển khai lại mà không cần cài đặt lại ứng dụng. Nếu một flow bị xóa, vô hiệu hóa hoặc chỉnh sửa trực tiếp trong engine, Operator sẽ khôi phục flow từ định nghĩa đã được ghi nhận. Khi gỡ cài đặt ứng dụng hoặc xóa một workflow khỏi manifest, flow tương ứng và bản ghi của nó sẽ bị xóa. Nếu không có engine, Operator vẫn cài đặt các ứng dụng nhưng không can thiệp vào workflow.

**Định tuyến sự kiện.** `core.app_workflows` đóng vai trò là bảng định tuyến. cap-process gửi một sự kiện đã được lưu trữ bền vững đến webhook của flow được ghi nhận cho workflow duy nhất thuộc ứng dụng phát sinh sự kiện và đăng ký nhận loại sự kiện đó:

`<KESTRA_URL>/api/v1/executions/webhook/<namespace>/<flow_id>/<KESTRA_WEBHOOK_KEY>`.

Đích nhận không bao giờ được lấy từ sự kiện hoặc từ bên gọi. Khi flow chưa được ghi nhận, ứng dụng chưa được cài đặt hoặc engine từ chối yêu cầu, sự kiện vẫn nằm trong outbox và được thử gửi lại theo cơ chế backoff; lý do được lưu trong `last_error` nhưng không bao gồm thông tin về đích nhận. Một sự kiện thuộc ứng dụng không khai báo workflow đăng ký nhận sự kiện đó sẽ được đóng lại kèm ghi chú "no workflow subscribes to this event". Nếu có nhiều subscriber cùng đăng ký một loại sự kiện, hệ thống sẽ xem đây là lỗi, không tự lựa chọn một subscriber.

**Gọi callback.** Một flow yêu cầu cap-process thực thi một effect bằng cách gửi `{"event_id", "step_name"}`. Workflow engine là một dịch vụ, không phải người dùng: engine không đăng nhập bằng Keycloak mà xác thực bằng thông tin xác thực của workflow service (`WORKFLOW_SERVICE_TOKEN`, tương ứng với secret `DXOS_WORKFLOW_TOKEN` trong Kestra) dưới dạng bearer token. Với thông tin xác thực này, bên gọi hoạt động dưới danh nghĩa tài khoản dịch vụ workflow của nền tảng. Đây là một bản ghi trong `core.users` được khởi tạo sẵn với vai trò `role_workflow`, đồng thời là actor của các work item và bản ghi kiểm toán do tài khoản này tạo ra. Thông tin xác thực này chỉ cho phép thực hiện những gì các ứng dụng đã cài đặt khai báo: effect được xác định dựa trên sự kiện đã lưu trữ và manifest, đồng thời mỗi effect chỉ được thực thi tối đa một lần cho mỗi sự kiện và mỗi step. Gateway không định tuyến đến các endpoint effect.

| Cấu hình | Thành phần sử dụng | Ý nghĩa |
| --- | --- | --- |
| `KESTRA_URL`, `KESTRA_USER`, `KESTRA_PASSWORD` | Operator, cap-process | URL cơ sở và thông tin xác thực basic của engine |
| `KESTRA_WEBHOOK_KEY` | cap-process, Kestra (`DXOS_WEBHOOK_KEY`) | Khóa của webhook trigger trong các flow của ứng dụng |
| `WORKFLOW_SERVICE_TOKEN` | cap-process, Kestra (`DXOS_WORKFLOW_TOKEN`) | Thông tin xác thực của workflow service, có độ dài tối thiểu 32 ký tự |
| `DXOS_PROCESS_URL` (Kestra secret) | Kestra | Địa chỉ để các flow truy cập cap-process bên trong mạng nội bộ |
