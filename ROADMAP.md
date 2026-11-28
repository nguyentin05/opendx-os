# Lộ trình phát triển OpenDX-OS

**Tiếng Việt** | [English](ROADMAP.en.md)

Lộ trình này theo dõi **OpenDX-OS (DX-OS Open-Core)** theo từng bản phát hành.
Thay đổi của mỗi phiên bản nằm trong [CHANGELOG.md](CHANGELOG.md); việc đang mở
được theo dõi trong [GitHub issues](https://github.com/nguyentin05/opendx-os/issues).

---

## v0.1.0: Nền tảng lõi, operator và capability quy trình (phát hành 2026-11-09)

- [x] Ranh giới ba tầng (`platform/`, `operator/`, `capabilities/`, `workspace-ui/`, `apps/`), hợp đồng dữ liệu và các kịch bản nghiệm thu `S01`–`S08`.
- [x] Metadata mã nguồn mở, header SPDX và kiểm tra ranh giới giữa các tầng.
- [x] Schema lõi PostgreSQL, Hasura, Keycloak, APISIX, SeaweedFS và ClamAV.
- [x] `dxos-operator` với `validate`, `plan`, `apply` và `reconcile`.
- [x] `cap-process`: lệnh có kiểm tra phiên bản, biên nhận idempotency và vòng đời cách ly tệp.
- [x] Bộ cài đặt bên ngoài cây mã nguồn, script sao lưu và khôi phục, và workflow CI đầu tiên.

## v0.2.0: Mô hình tổ chức, operator cho Kubernetes và tri thức (phát hành 2026-11-16)

- [x] Mỗi bản cài đặt là một tổ chức, dữ liệu phân theo ứng dụng và phòng ban ([ADR 0005](docs/adr/0005-department-and-application-data-model.md)).
- [x] Keycloak xác thực, `dxos-authz` phân quyền ([ADR 0007](docs/adr/0007-keycloak-authenticates-platform-authorizes.md)).
- [x] Manifest nhiều bảng, payload của lệnh và triển khai workflow Kestra ([ADR 0006](docs/adr/0006-where-application-business-logic-lives.md), [ADR 0008](docs/adr/0008-an-application-declares-several-tables.md)).
- [x] CRD `DXApplication` và controller tự sửa drift của Hasura ([ADR 0004](docs/adr/0004-kubernetes-crd-operator.md)).
- [x] `cap-hr`, và `cap-ai` nạp tri thức gốc với truy xuất theo phạm vi ([ADR 0009](docs/adr/0009-native-rag-identity-and-source-boundaries.md)).
- [x] Ứng dụng mua sắm liên phòng ban và dữ liệu mẫu cho tổ chức demo.
- [x] Trang tài liệu song ngữ.

## v0.3.0: Workspace UI và quy trình phân phối (phát hành 2026-11-23)

- [x] Trợ lý Workspace trên tri thức của ứng dụng và tri thức cá nhân (#103).
- [x] Tách controller thành binary và image `dxos-controller` riêng (#106).
- [x] Kiểm tra CI bắt buộc, quét bảo mật, build image và workflow phát hành có attestation (#84, #87, #101).
- [x] Workspace UI MVP: tìm ứng dụng, danh sách và biểu mẫu dựng từ schema, thao tác trên bản ghi, tệp đính kèm và Việc của tôi (#104).
- [x] Bổ sung cho Workspace UI: lịch sử kiểm toán (#41).
- [x] Công việc theo phòng ban trong Việc của tôi (#27).
- [x] Làm mới access token trong bộ nhớ trước khi hết hạn, không tải lại các màn hình đang mở (#38).
- [x] Hộp việc cần làm suy ra từ trạng thái bản ghi, công việc duyệt được đóng bằng `complete_work_item` (#42).
- [x] Nội dung Workspace UI và nhãn ứng dụng mẫu bằng tiếng Việt (#45).
- [x] Chạy `cap-ai` từ stack Compose mặc định mà không phải cấu hình token thủ công.
- [x] Tài liệu khớp với phần triển khai (#46).
- [x] Danh mục và toàn văn giấy phép bên thứ ba, Hasura bản cộng đồng, và tệp giấy phép trong mọi image (#48).
- [x] Chỉ bắt đầu phát hành từ một tag đã push (#137).

## v1.0.0: Bản phát hành dự thi

- [ ] Hướng dẫn chạy nhanh trong README và ảnh chụp màn hình (#47).
- [x] `dxos-operator version` báo đúng phiên bản build (#138).
- [x] Làm mới toolchain build của tài liệu và giao diện (#139).
- [x] Thao tác mô hình bền vững cho việc nạp tri thức (#136).

## v1.1.0: Capability dữ liệu

- [ ] Dịch vụ `cap-data` trong stack Compose: thu nhận dữ liệu OT (`S02`), xuất Parquet (`S07`) và truy vết nguồn gốc BI (`S08`). Dịch vụ chưa được triển khai, và các handler vẫn ghi vào các bảng `core` đã ngừng dùng (`core.ot_events`, `core.devices`, `core.work_items`, `core.export_batches`, `core.data_lineage`) thay vì schema `cap_data`, nên phải chuyển chúng trước (#111).

## Sau này

- [ ] Dịch vụ `cap-work` và bảng Kanban (#27, #44).
- [ ] Quyền sở hữu khái niệm ngữ nghĩa theo [ADR 0002](docs/adr/0002-semantic-schema-governance-and-cross-app-data-sync.md).
- [x] Đồng bộ tài khoản Keycloak (#23).
- [ ] Ví dụ GitOps với Argo CD (#28).
- [ ] Kiểm chứng chuỗi băm kiểm toán, neo bên ngoài cơ sở dữ liệu.
