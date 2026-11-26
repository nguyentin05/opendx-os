---
title: Tài liệu tham chiếu
description: Các đặc tả hợp đồng chuẩn cho manifest, mô hình dữ liệu, bảo mật.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---


---
title: Tài liệu tham chiếu
description: Các đặc tả hợp đồng chuẩn cho manifest, mô hình dữ liệu, bảo mật, API, CLI và RAG.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Tài liệu tham chiếu

Các trang tài liệu tham chiếu mô tả hành vi bắt buộc. Khi nội dung tài liệu và phần triển khai không thống nhất, hãy xem nội dung tài liệu là yêu cầu chuẩn và báo cáo điểm chưa khớp, thay vì sao chép cách triển khai vào đặc tả hợp đồng.

| Lĩnh vực | Nội dung |
| --- | --- |
| [Đặc tả hợp đồng dữ liệu](data-contracts/index.vi.md) | Định danh, vai trò, bảng dữ liệu, thao tác ghi, phiên bản, tệp, OT và BI |
| [Manifest](manifest/index.vi.md) | Các trường `DXApplication`, vòng đời cài đặt và nâng cấp |
| [Bảo mật và tiêu chí nghiệm thu](security/index.vi.md) | Các bất biến, phản hồi giao thức, S01–S08 |
| [API](api/index.vi.md) | Các route của capability, ranh giới thực thi lệnh, sự kiện |
| [CLI](cli/dxos-operator.vi.md) | Các lệnh `dxos-operator` |
| [RAG](rag/index.vi.md) | API tri thức, phân quyền truy cập nguồn dữ liệu, gateway mô hình |
| [Các quy tắc tại thời điểm thực thi](runtime-rules.vi.md) | Tập hợp các quy tắc mà dịch vụ phải kiểm tra lại trước khi thực hiện hành động |

Các tệp có thể đọc bằng máy vẫn được lưu tại thư mục gốc của repository, trong thư mục `contracts/`.
