---
title: Triển khai workflow
description: Khai báo, triển khai và gỡ lỗi các luồng xử lý của ứng dụng được thực thi bởi Kestra.
diataxis: how-to
audience: [app-author, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Vận hành workflow

Một gói ứng dụng không chứa mã nguồn. Các workflow của gói ứng dụng là những Kestra flow được lưu trong thư mục `workflows/*.yaml`, do Operator triển khai và nhận các sự kiện được lưu trữ bền vững từ cap-process.

## Khai báo workflow

Thêm một tệp flow vào gói ứng dụng và tham chiếu đến tệp đó trong `spec.workflows`. Flow phải đáp ứng các yêu cầu sau:

- Sử dụng workflow ID được khai báo trong manifest và namespace `dxos.apps.<app_id>`;
- Khai báo chính xác một webhook trigger với khóa `DXOS_WEBHOOK_KEY`;
- Chỉ sử dụng các task `If`, `http.Request` và `Log`;
- Gọi các effect bằng phương thức `POST` đến địa chỉ:
  `{{ secret('DXOS_PROCESS_URL') }}/api/cap/process/effects/<effect>`.

Các script, container, lịch chạy định kỳ và yêu cầu gửi đến những máy chủ khác đều bị từ chối. Operator sẽ không đưa gói ứng dụng vào hệ thống cho đến khi flow đáp ứng các yêu cầu.

## Triển khai và khôi phục

Operator triển khai các flow trong quá trình reconcile và ghi nhận từng flow trong `core.app_workflows`. Khi tệp workflow thay đổi, flow sẽ được triển khai lại mà không cần cài đặt lại ứng dụng.

Nếu một flow bị xóa, vô hiệu hóa hoặc chỉnh sửa trực tiếp trong Kestra, flow đó sẽ được khôi phục từ định nghĩa đã ghi nhận trong lần xử lý tiếp theo. Khi xóa một workflow khỏi manifest hoặc gỡ cài đặt ứng dụng, flow tương ứng cùng bản ghi của nó sẽ bị xóa.

Nếu chưa cấu hình workflow engine, Operator vẫn cài đặt các ứng dụng nhưng không xử lý các workflow.

## Xử lý sự kiện

`core.app_workflows` xác định tuyến xử lý sự kiện. cap-process gửi một sự kiện đã được lưu trữ đến flow duy nhất thuộc ứng dụng phát sinh sự kiện và đăng ký nhận loại sự kiện đó.

Đích nhận không bao giờ được xác định từ nội dung sự kiện hoặc từ bên gửi yêu cầu. Nếu không có flow nào được ghi nhận, sự kiện sẽ tiếp tục nằm trong outbox và được thử gửi lại theo cơ chế backoff.

Flow gửi yêu cầu gọi ngược (callback) với dữ liệu `{"event_id", "step_name"}`, sử dụng thông tin xác thực của workflow service thay vì token người dùng. Effect được xác định dựa trên sự kiện đã lưu trữ và manifest, đồng thời chỉ được thực thi tối đa một lần cho mỗi cặp sự kiện và bước xử lý.

Các effect trên cùng một bản ghi được thực hiện theo thứ tự sự kiện, không phụ thuộc vào thứ tự worker gửi yêu cầu callback. Xem thêm [các quy tắc runtime](../reference/runtime-rules.vi.md#identity-roles-and-commands).

## Gỡ lỗi

```bash
docker compose logs -f operator
docker compose logs -f cap-process
```

Kiểm tra các nội dung sau:

1. Gói ứng dụng đã được chấp nhận và `core.app_workflows` có bản ghi tương ứng;
2. Flow tồn tại trong Kestra với namespace và ID đúng như dự kiến;
3. Bản ghi trong outbox không có `last_error`, hoặc thông báo lỗi chỉ rõ đích nhận bị từ chối;
4. Event ID và tên bước xử lý khớp với dữ liệu callback mà flow gửi đi.

Toàn bộ đặc tả chi tiết được trình bày trong [triển khai workflow và sự kiện](../reference/api/events.vi.md).