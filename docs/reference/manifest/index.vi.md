---
title: Tài liệu tham chiếu Manifest
description: Các trường DXApplication, khai báo bảng, quy trình cài đặt và vòng đời nâng cấp.
diataxis: reference
audience: [app-author, platform-developer, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Tài liệu tham chiếu Manifest

Manifest `DXApplication` khai báo các chức năng, vai trò, quyền truy cập dùng chung, bảng dữ liệu nghiệp vụ, workflow và các nguồn dữ liệu tri thức tùy chọn của một ứng dụng. Bộ kiểm tra xác thực của operator (`operator/internal/manifest`) là phần triển khai tham chiếu cho quy trình tuần tự hóa này; các tệp `apps/*/manifest.yaml` là những mẫu đã được xác thực.

| Trang | Nội dung |
| --- | --- |
| [Lược đồ](schema.vi.md) | Các trường cấp cao nhất, `spec.tables`, quy tắc truy cập và máy trạng thái |
| [Vòng đời](lifecycle.vi.md) | Quy trình cài đặt, quy tắc nâng cấp, các bản ghi registry và những thành phần sử dụng tại thời điểm thực thi |

Lược đồ có thể đọc bằng máy được lưu tại `contracts/manifest-v1.schema.json` trong repository. Chỉ dựa vào cấu trúc schema không đủ để xác định tính an toàn về mặt ngữ nghĩa của một manifest.
