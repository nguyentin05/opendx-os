---
title: Đặc tả hợp đồng dữ liệu
description: Định danh, vai trò, các bảng dùng chung và bảng miền nghiệp vụ, thao tác ghi dữ liệu, phiên bản, tệp, OT và BI.
diataxis: reference
audience: [app-author, platform-developer, security-reviewer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


# Đặc tả hợp đồng dữ liệu

Các trang này tập hợp đặc tả hợp đồng dữ liệu chính thức được sử dụng chung bởi Operator, các chức năng (capabilities), giao diện người dùng (UI) và các gói ứng dụng. Mỗi trang nêu rõ những yêu cầu đã được chấp nhận nhưng chưa được triển khai (OT và BI, cơ chế neo mốc kiểm tra kiểm toán, các khái niệm ngữ nghĩa dùng chung); các kiểu dữ liệu Go hiện có và các tệp YAML mẫu không phải là bằng chứng cho thấy một yêu cầu đã được thực thi. Xem thêm [các thay đổi về kiến trúc và hợp đồng](../../architecture/index.md#decisions-and-contract-changes) và [các tiêu chí nghiệm thu bảo mật](../security/index.md).

| Trang | Nội dung |
| --- | --- |
| [Định danh, vai trò và phạm vi truy cập](identity-and-roles.md) | Mỗi bản cài đặt chỉ phục vụ một tổ chức, quyền sở hữu vai trò và các phạm vi `own`/`department`/`organization` |
| [Các bảng dùng chung](shared-tables.md) | Các bảng `core.*` và vùng lưu trữ do các chức năng quản lý |
| [Các bảng miền nghiệp vụ](domain-tables.md) | Các bảng quy trình, bảng thông thường và bảng con, cùng các bảng mẫu của ứng dụng |
| [Thao tác ghi dữ liệu, phiên bản và kiểm toán](writes-and-versions.md) | Quyền sở hữu trường dữ liệu, cơ chế kiểm soát đồng thời lạc quan, biên nhận lệnh và chuỗi kiểm toán |
| [Tệp, OT và BI](files-ot-bi.md) | Cách ly, loại bỏ dữ liệu OT trùng lặp, đối soát dữ liệu xuất và truy xuất nguồn gốc |
| [Các khái niệm ngữ nghĩa](semantic-concepts.md) | Các khái niệm dùng chung, chủ thể chịu trách nhiệm ghi dữ liệu và các bản sao |

Đặc tả hợp đồng Manifest nằm trong mục [Manifest](../manifest/index.md).
