---
title: Hoàn thành yêu cầu đầu tiên
description: Tạo, gửi và phê duyệt một đề nghị mua sắm qua API runtime.
diataxis: tutorial
audience: [evaluator, app-author]
status: active
owner: platform
last_reviewed: 2026-11-23
---

# Hoàn thành yêu cầu đầu tiên

Hướng dẫn này dùng tổ chức demo và ứng dụng mẫu phê duyệt mua sắm. Qua đó,
trải nghiệm thực tế toàn bộ các bước: đăng nhập, tải file lên, quét virus, ghi dữ liệu
qua GraphQL và gửi các command có version thông qua gateway.

## Tạo dữ liệu cho tổ chức demo

Sau khi stack đã healthy và Operator đã cài các ứng dụng mẫu:

```bash
scripts/seed-demo.py seed
```

Lệnh này tạo ba phòng ban và sáu tài khoản Keycloak. Mật khẩu demo là
`DEMO_PASSWORD` (default `Demo@2026`), và có thể chạy lại lệnh này an toàn.

| Tài khoản | Phòng ban | Vai trò |
| --- | --- | --- |
| `an.nguyen` | Kinh doanh | người đề nghị mua sắm, nhân viên chấm công |
| `cuong.le` | Kinh doanh (trưởng phòng) | người đề nghị và trưởng phòng mua sắm, quản lý chấm công |
| `binh.tran` | Kỹ thuật | người đề nghị mua sắm, kỹ thuật viên bảo trì, nhân viên chấm công |
| `dung.pham` | Kỹ thuật (trưởng phòng) | trưởng phòng mua sắm, quản lý bảo trì, quản lý chấm công |
| `giang.vo` | Tài chính | tài chính mua sắm, nhân viên chấm công |
| `quan.tri` | Tài chính | operator nền tảng, kiểm toán viên, quản trị nhân sự |

Khi cap-ai được bật, năm tài khoản nghiệp vụ còn được cấp thêm vai trò thư viện
cá nhân.

## Chạy luồng phê duyệt

Cách nhanh nhất là chạy một đề nghị qua toàn bộ chuỗi phê duyệt và in ra từng bước:

```bash
scripts/seed-demo.py flow
```

Script này:

1. tạo một đề nghị mua sắm với tài khoản `an.nguyen`, kèm báo giá được tải lên
   cap-process và quét bằng ClamAV;
2. cho thấy trưởng phòng không thao tác được trên bản nháp (`404`);
3. gửi đề nghị bằng một command có version, và hiển thị việc cần duyệt mà
   workflow mở trong Kestra;
4. cho thấy các trường hợp bị từ chối: trưởng phòng của phòng ban khác (`404`) và
   người đề nghị tự duyệt đề nghị của chính mình (`403`);
5. phê duyệt với tư cách trưởng phòng (`BUDGET_REVIEW`), rồi với tư cách tài chính
   (`APPROVED`);
6. với một đề nghị thứ hai, từ chối thao tác từ chối không kèm lý do (`400`), từ chối
   có kèm lý do, và cho thấy người đề nghị đọc được lý do đó.

Các kịch bản demo khác:

```bash
scripts/seed-demo.py scopes   # mỗi người thấy được những dữ liệu nào khi truy cập qua gateway
scripts/seed-demo.py admin    # cấp một vai trò cho người dùng, sau đó thu hồi lại
scripts/seed-demo.py org      # Tạo thêm phòng ban và điều chuyển một nhân viên sang đó
scripts/seed-demo.py lines    # tạo danh sách nhà cung cấp và các mặt hàng trong đề nghị mua sắm
```

## Xem kết quả

Mở Workspace UI tại <http://localhost:5173> và đăng nhập bằng một người dùng demo, ví dụ
`an.nguyen`. Trong **Mua sắm vật tư**, đề nghị đã được duyệt hiển thị trạng thái,
version và **Lịch sử**: đã tạo, đã gửi, phòng ban đã duyệt và tài chính đã duyệt.
`cuong.le` thấy các đề nghị đang chờ mình trong **Chờ tôi xử lý** và các việc cần
duyệt trong **Việc của tôi**.

Bản thân các dòng dữ liệu nằm trong PostgreSQL:

```bash
docker compose exec postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  -c "SELECT status, version FROM domain.procurement_requests"'
```

## Những gì bạn vừa sử dụng

| Bước | Contract |
| --- | --- |
| Đăng nhập và chọn vai trò | [Danh tính, vai trò và phạm vi](../reference/data-contracts/identity-and-roles.vi.md) |
| Ghi bản ghi và version | [Ghi, version và audit](../reference/data-contracts/writes-and-versions.vi.md) |
| Command và chuyển trạng thái | [Schema của manifest](../reference/manifest/schema.vi.md) |
| Sự kiện workflow | [Triển khai workflow và sự kiện](../reference/api/events.vi.md) |
| Cách ly file | [File, OT và BI](../reference/data-contracts/files-ot-bi.vi.md) |

Đọc tiếp phần [hướng dẫn](../guides/index.vi.md) để thêm một ứng dụng, hoặc phần
[kiến trúc](../architecture/index.vi.md) để hiểu các tầng.