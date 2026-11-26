---
title: Tệp, OT và BI
description: Cách ly tệp, loại bỏ dữ liệu OT trùng lặp, đối soát dữ liệu xuất và truy xuất nguồn gốc.
diataxis: reference
audience: [platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Tệp, OT và BI

## Tệp

Các tệp mới được tạo sẽ có trạng thái `QUARANTINED`; chỉ dịch vụ quét tệp mới được phép chuyển trạng thái của chúng thành `CLEAN` hoặc `INFECTED`. Các thao tác tải xuống, OCR và các lệnh sử dụng tệp phải kiểm tra quyền của người gọi (vai trò và phạm vi truy cập), đồng thời xác nhận tệp đang ở trạng thái `CLEAN`. Nội dung đã được quét không được phép ghi đè trên cùng một định danh tệp; việc thay thế tệp phải tạo ra một định danh mới và yêu cầu quét lại. Nếu dịch vụ quét gặp lỗi, tệp vẫn giữ trạng thái chưa được xác nhận là sạch.

## OT và BI

Các yêu cầu OT và BI dưới đây đã được chấp nhận nhưng chưa được triển khai: các trình xử lý của cap-data vẫn đang sử dụng những bảng core đã ngừng hoạt động và sẽ được chuyển sang schema `cap_data` trong phiên bản v1.1.0 (#111). Các bảng `cap_data.ot_events`, `export_batches` và `data_lineage` đã tồn tại, nhưng hiện chưa có dịch vụ nào ghi dữ liệu vào các bảng này.

OT xác thực thiết bị, lưu trữ một sự kiện duy nhất cho mỗi cặp `(device_id, event_dedup_key)` và phát ra sự kiện chuẩn `ot.event.accepted`. Workflow được khai báo bởi ứng dụng sẽ đánh giá các ngưỡng và tạo công việc phù hợp. Capability này không tự chọn trước ứng dụng bảo trì; việc phân phối sự kiện trùng lặp không làm phát sinh thêm tác động nghiệp vụ.

Khóa định danh nguồn BI đầy đủ là `(app_id, entity_type, entity_id, entity_version)`. Chỉ xuất các bản ghi đang ở trạng thái thuộc `terminalStates` được ứng dụng khai báo và chỉ đọc các batch đã được phát hành thành công. Việc xuất lại cùng một phiên bản nguồn không được tạo thêm dữ liệu đóng góp; chọn phiên bản mới nhất theo `(app_id, entity_type, entity_id)`. Hai loại thực thể có cùng UUID vẫn được xem là hai nguồn riêng biệt. Hai lần xuất có cùng khóa phiên bản nhưng nội dung khác nhau phải được phát hiện và xử lý như một lỗi đối soát, thay vì tùy ý chọn một trong hai.

Thông tin truy vết nguồn gốc dữ liệu (lineage) phải bảo toàn đầy đủ tập hợp các bản ghi nguồn đứng sau mỗi chỉ số tổng hợp, không chỉ lưu một ID đại diện. Cần kiểm tra DDL của registry, batch, receipt và lineage để đối chiếu với các yêu cầu này trước khi kết nối các thành phần sử dụng dữ liệu; việc một bảng tồn tại không đồng nghĩa với việc bảng đó đã đáp ứng các yêu cầu.
