---
title: Bảo mật và tiêu chí nghiệm thu
description: Các bất biến bảo mật, phản hồi giao thức và các kịch bản nghiệm thu S01–S08.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Bảo mật và tiêu chí nghiệm thu

Các trang này xác định những thuộc tính bảo mật bắt buộc và các kịch bản nghiệm thu đối với phân quyền, lệnh, tệp, workflow, OT, BI và nhật ký kiểm toán. Các chi tiết về cơ chế được trình bày trong [hợp đồng dữ liệu](../data-contracts/index.vi.md). Nội dung mô tả hệ thống mục tiêu; việc chỉ vượt qua các bước kiểm tra tĩnh không đủ để chứng minh các cơ chế bảo mật được thực thi trong thời gian chạy.

[ADR 0005](../../adr/0005-department-and-application-data-model.md) xác định mỗi bản cài đặt chỉ có một tổ chức: cơ chế phân tách theo workspace được thay thế bằng cơ chế cô lập ứng dụng, phòng ban và chủ sở hữu; mã nguồn, fixtures và các bài kiểm thử đều tuân theo mô hình này. Một yêu cầu chỉ được xem là đáp ứng khi có kết quả kiểm tra trong thời gian chạy được ghi nhận để chứng minh điều đó.

| Trang | Nội dung |
| --- | --- |
| [Các bất biến](invariants.vi.md) | Các thuộc tính bảo mật bắt buộc và hợp đồng quy định phản hồi giao thức |
| [Các kịch bản nghiệm thu](acceptance.vi.md) | Các kịch bản S01–S08, những trường hợp cần xem xét bổ sung và các điều kiện kiểm định trực tiếp |