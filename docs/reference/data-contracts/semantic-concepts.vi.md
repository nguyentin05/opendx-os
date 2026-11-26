---
title: Quyền sở hữu các khái niệm ngữ nghĩa
description: Cách quản lý các khái niệm miền nghiệp vụ dùng chung, chủ thể chịu trách nhiệm ghi dữ liệu và các bản sao dữ liệu.
diataxis: reference
audience: [platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


# Quyền sở hữu các khái niệm ngữ nghĩa

[ADR 0002](../../adr/0002-semantic-schema-governance-and-cross-app-data-sync.md#3-three-scenario-governance-for-domain-attributes-tier-3) xác định ba trường hợp: các thuộc tính miền nghiệp vụ độc lập, một khái niệm dùng chung được xác định rõ ràng bằng URI `semanticType`, và các khai báo xung đột bị từ chối trong giai đoạn lập kế hoạch/đối soát. Một khái niệm dùng chung có một chủ thể chịu trách nhiệm ghi dữ liệu (Write Owner) duy nhất và các bản sao cho phép nhiều bên đọc (Multi-Reader); các cập nhật từ Write Owner tạo ra các sự kiện outbox trong cùng transaction để workflow phân phối đến nhiều đích. Chỉ riêng việc các cột có cùng tên không bao giờ đủ để xác lập ý nghĩa dùng chung.

Các kiểu dữ liệu không tương thích hoặc việc có nhiều Write Owner cho cùng một khái niệm phải khiến quá trình xử lý thất bại trước khi cấu hình được công bố. Cơ chế tuần tự hóa manifest, registry quyền sở hữu, hợp đồng phân phối bản sao và bộ kiểm tra xác thực thực thi các quy tắc vẫn cần được đặc tả và triển khai; parser hiện tại chưa định nghĩa các trường `semanticType`. Phần này ghi nhận yêu cầu đã được chấp nhận, nhưng không khẳng định rằng cơ chế đồng bộ đã hoạt động.
