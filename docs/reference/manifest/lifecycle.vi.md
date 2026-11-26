---
title: Vòng đời Manifest
description: Quá trình cài đặt, nâng cấp, các bản ghi trong registry và những thành phần sử dụng tại thời điểm thực thi.
diataxis: reference
audience: [app-author, platform-developer, operator]
status: active
owner: platform
last_reviewed: 2026-11-23
---


### Cài đặt và registry

Việc cài đặt một ứng dụng sẽ ghi các dữ liệu sau trong một giao dịch duy nhất, sau khi DDL và metadata của Hasura được thực thi thành công: bản ghi `core.apps` (trạng thái `Ready`), các vai trò của ứng dụng trong `core.roles` và các chức năng của ứng dụng trong `core.app_capabilities`. Mỗi lần triển khai đều được ghi nhận trong `core.app_deployments`; nếu nâng cấp thất bại, phiên bản trước đó vẫn giữ trạng thái `Ready` và lỗi được ghi vào `last_error`; nếu cài đặt lần đầu thất bại, bản ghi sẽ chuyển sang trạng thái `Failed`. Hệ thống sẽ từ chối ứng dụng trước khi thực hiện bất kỳ thay đổi nào nếu vai trò thuộc quyền sở hữu của ứng dụng khác, chức năng thuộc về nền tảng hoặc chức năng chưa được bật. Tại một thời điểm, tối đa chỉ có một ứng dụng ở trạng thái `Ready` được phép bật thư viện tài liệu cá nhân. Khi gỡ cài đặt, bản ghi được đánh dấu là `Removed`, các quyền của Hasura bị xóa, dữ liệu tri thức được đóng gói cùng ứng dụng được ngừng sử dụng và các chức năng được giải phóng; tuy nhiên, các bảng miền nghiệp vụ, dữ liệu, vai trò và việc gán vai trò vẫn được giữ lại. Những vai trò bị loại khỏi manifest sẽ bị xóa cùng với các phân công tương ứng.

Một bản nâng cấp có thể bổ sung bảng, cột cho phép giá trị NULL hoặc trạng thái mới, cũng như các cột hệ thống cần thiết cho khai báo mới nếu những cột đó có giá trị mặc định (ví dụ: `status` khi một bảng được bổ sung máy trạng thái). Một bảng không còn được khai báo sẽ mất các quyền Hasura nhưng vẫn giữ nguyên các bản ghi. Hệ thống sẽ từ chối nâng cấp nếu có thay đổi về kiểu dữ liệu hoặc khả năng nhận giá trị NULL, bổ sung cột bắt buộc mới, loại bỏ một cột khỏi khai báo hoặc xóa một trạng thái đang được các bản ghi sử dụng; phiên bản trước đó vẫn tiếp tục hoạt động. Một bản ghi `core.apps` được ghi theo cấu trúc một bảng duy nhất trước khi ADR 0008 được áp dụng sẽ bị các dịch vụ chức năng từ chối cho đến khi ứng dụng được cài đặt lại.

### Các thành phần sử dụng

cap-process đọc các bản ghi ở trạng thái `Ready` trong `core.apps` mà tất cả chức năng của ứng dụng đều đã được bật ([các quy tắc tại thời điểm thực thi](../runtime-rules.vi.md#các-quy-tắc-được-dịch-vụ-thực-thi)). Một lệnh được thực thi trên bảng khai báo lệnh đó và chỉ được chấp nhận đối với bản ghi mà vai trò đang thực hiện có quyền đọc theo phạm vi `select` và bộ lọc bản ghi của bảng đó; đồng thời, vai trò thực hiện được ghi nhận trong bản ghi kiểm toán và sự kiện ([các hợp đồng](../runtime-rules.vi.md#định-danh-vai-trò-và-lệnh)).
