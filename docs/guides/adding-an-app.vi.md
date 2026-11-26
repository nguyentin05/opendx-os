---
title: Thêm ứng dụng
description: Khai báo gói ứng dụng Tier 3 và đăng ký dữ liệu cùng các vai trò tương ứng.
diataxis: how-to
audience: [app-author]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Thêm ứng dụng theo hướng khai báo

Một ứng dụng là một gói bao gồm các khai báo cấu hình, không chứa mã nguồn backend và không yêu cầu thay đổi đối với Tier 1 hoặc Tier 2.[manifest schema](../reference/manifest/schema.vi.md) là tài liệu quy định cấu trúc và các yêu cầu đối với gói ứng dụng. Trong đó, `apps/procurement` là ví dụ đầy đủ nhất, bao gồm bảng quy trình, bảng thông thường, bảng con, tệp đính kèm, workflow và dữ liệu tri thức. Trong khi đó
`apps/process-fixture` là ví dụ tối giản nhất có tích hợp workflow.

## 1. Thiết lập cấu trúc gói ứng dụng

```text
apps/<app>/
├── manifest.yaml          # bắt buộc
├── workflows/<flow>.yaml  # tùy chọn: các luồng Kestra được khai báo trong spec.workflows
└── knowledge/*.md|*.pdf   # tùy chọn: các nguồn dữ liệu được khai báo trong spec.knowledge (yêu cầu cap-ai)
```

## 2. Khai báo manifest

1. `metadata`: Trường `name` được sử dụng làm `app_id` (chỉ bao gồm chữ cái viết thường, chữ số và dấu `_`),
   cùng với các trường `displayName` và `version`.
2. `spec.capabilities`: Luôn phải có `cap-process`. Bổ sung `cap-hr` để sử dụng chức năng quản lý phòng ban và phạm vi truy cập `department`, `cap-work` để quản lý các công việc, và `cap-ai` để sử dụng dữ liệu tri thức.
3. `spec.roles`: Khai báo tất cả các vai trò mà ứng dụng sử dụng. Theo quy ước, tên vai trò có định dạng  `role_<app>_<role>`, Trường`description` cần được viết bằng tiếng Việt và sẽ được hiển thị trên giao diện Workspace UI.
4. `spec.tables`, Khai báo cho từng bảng, bao gồm:
   - Các cột dữ liệu nghiệp vụ và khóa ngoại;
   - Quy tắc `access` cho từng vai trò, bao gồm phạm vi truy cập (`own`, `department`, `organization`)
     và trường `recordFilter` tùy chọn;
   - Cấu hình `ui` (các cột hiển thị, trường biểu mẫu, nhãn).
   Đối với bảng quy trình, cần bổ sung `stateMachine` (và `attachments` nếu có các tệp cần được quét để bảo đảm an toàn). Những bản ghi thuộc một bản ghi khác, chẳng hạn các dòng chi tiết của một yêu cầu, cần được khai báo trong bảng con với trường `belongsTo`.
5. `spec.sharedAccess`: Khai báo các cột dữ liệu dùng chung mà một vai trò được phép đọc, chẳng hạn
   `core.users.display_name`.
6. `spec.workflows`, Khai báo nếu các lệnh cần thực hiện thao tác mở hoặc đóng các công việc: Tham khảo
   [Chạy các workflow](running-workflows.vi.md).

Các ngưỡng xử lý và logic nghiệp vụ cần được thể hiện thông qua các khai báo cấu hình và workflow. Việc bổ sung ứng dụng không được làm phát sinh nhánh điều kiện phụ thuộc vào tên ứng dụng trong các thành phần dùng chung, giao diện người dùng hoặc các câu lệnh DDL khởi tạo hệ thống (`make check-architecture` sẽ từ chối những thay đổi này).

## 3. Kiểm tra tính hợp lệ và rà soát kế hoạch

```bash
go -C operator run ./cmd/dxos-operator validate ../apps/<app>/manifest.yaml
go -C operator run ./cmd/dxos-operator plan ../apps/<app>/manifest.yaml
go -C operator run ./cmd/dxos-operator plan -format hasura ../apps/<app>/manifest.yaml
```

Các lệnh `validate` và `plan` không làm thay đổi dữ liệu hoặc cấu hình hệ thống. Trước khi cài đặt, cần rà soát DDL được tạo ra và các quyền truy cập Hasura của từng vai trò.

## 4. Cài đặt ứng dụng

- **Sử dụng Docker Compose:** Sao chép gói ứng dụng vào thư mục `apps/` (hoặc `OPERATOR_APPS_DIR`). Dịch vụ
  `operator` sẽ tự động cài đặt ứng dụng trong chu kỳ xử lý tiếp theo, chậm nhất sau khoảng thời gian được cấu hình trong
  `OPERATOR_RECONCILE_INTERVAL` (mặc định là `30s`). Theo dõi quá trình này bằng lệnh:
  `docker compose logs -f operator`. EKhi chỉnh sửa gói ứng dụng, hệ thống sẽ tự động nâng cấp ứng dụng. Khi xóa gói ứng dụng, hệ thống sẽ gỡ cài đặt ứng dụng tương ứng.
- **Không sử dụng Docker Compose:** Thực hiện lệnh `dxos-operator apply apps/<app>/manifest.yaml` cùng với
  [Cấu hình môi trường dành cho Operator](../reference/cli/dxos-operator.md#environment).
- **Sử dụng Kubernetes:** Thực hiện `dxos-operator export-crd apps/<app> | kubectl apply -f -`
  ([Hướng dẫn cài đặt](../operations/installation.md#kubernetes-operator-development-cluster)).

Ứng dụng được xác định là cài đặt thành công khi bản ghi tương ứng trong bảng `core.apps` có trạng thái `Ready`.Nếu quá trình cài đặt bị từ chối, thông tin lỗi sẽ được ghi nhận trong trường `last_error`,  Nếu quá trình nâng cấp không thể thực hiện, phiên bản trước đó vẫn tiếp tục hoạt động để phục vụ các yêu cầu ([lifecycle](../reference/manifest/lifecycle.md)).

## 5. Cấp quyền vai trò và kiểm tra hoạt động

1. Người quản trị nền tảng (Platform Operator) cấp các vai trò của ứng dụng cho người dùng
   ([Quản trị hệ thống](integrating-the-ui.md#8-administration)), Đồng thời, quản trị viên nhân sự (HR Administrator) phân công người dùng vào các phòng ban nếu ứng dụng sử dụng cap-hr.
2. Giao diện Workspace UI tự động hiển thị ứng dụng từ registry mà không cần xây dựng lại (rebuild)
3. Kiểm tra các nội dung sau: phạm vi truy cập của từng vai trò, trạng thái khởi tạo ban đầu, các lệnh và trường hợp hệ thống từ chối thực hiện, Phiên bản dữ liệu không còn mới nhất (`409`), quá trình kiểm tra tệp đính kèm, tính an toàn khi thử lại các thao tác trong workflow, bảo đảm không phát sinh tác động nghiệp vụ trùng lặp, Việc vô hiệu hóa một capability mà ứng dụng đang yêu cầu phải khiến ứng dụng ngừng hoạt động. Quá trình đồng bộ và điều chỉnh trạng thái ứng dụng không được làm thay đổi các quyền đã cấp cho những ứng dụng khác.

Để kiểm tra khả năng mở rộng và tích hợp linh hoạt (pluggability), hãy xây dựng một ứng dụng nằm ngoài phạm vi các ví dụ hiện có. Kết hợp các chức năng đọc dữ liệu người dùng, tệp đính kèm, phạm vi truy cập theo phòng ban và workflow. Đồng thời, kiểm tra để bảo đảm một sự kiện được gửi lặp lại không làm phát sinh việc xử lý nghiệp vụ nhiều lần. 

[Hướng dẫn xác minh](../contribute/verification.vi.md).
