# OpenDX-OS

**Tiếng Việt** | [English](README.en.md)

**OpenDX-OS (DX-OS Open-Core)** là nền tảng lắp ghép cho các ứng dụng nghiệp vụ,
phát triển cho kỳ thi OLP PMNM 2026. Mỗi ứng dụng khai báo dữ liệu, quyền,
quy trình và cấu hình giao diện của mình, còn hạ tầng và các capability thì dùng chung.

## Cấu trúc dự án

| Thư mục | Mục đích |
| --- | --- |
| `platform/`, `operator/` | Hạ tầng dùng chung và control plane viết bằng Go, lập kế hoạch cấu hình cho từng ứng dụng |
| `capabilities/`, `workspace-ui/` | Các dịch vụ H-P-D-I dùng lại được và App Shell viết bằng React/TypeScript |
| `apps/` | Các gói ứng dụng mẫu: bảo trì, mua sắm và chấm công |

Xem [kiến trúc](docs/architecture/index.md) để biết ranh giới giữa các tầng, các
thành phần đã lên kế hoạch và các quyết định thiết kế đã thông qua.

## Tình trạng phát triển

Repository gồm SQL khởi tạo cơ sở dữ liệu, cấu hình Compose để chạy trên máy,
Operator, các dịch vụ capability, Workspace UI và các ứng dụng mẫu.
Workspace UI đăng nhập qua Keycloak, tìm các ứng dụng người dùng được phép dùng
từ registry lúc chạy, rồi dựng danh sách bản ghi, biểu mẫu, bản ghi con và các
thao tác quy trình từ metadata của ứng dụng. Trang Việc của tôi gom công việc từ
mọi ứng dụng được phép, và Trợ lý mở ngay bên cạnh trang đang xem.

Dự án đang được phát triển. Vòng đời triển khai và bằng chứng nghiệm thu đầu
cuối được theo dõi trong tài liệu dự án.

[Lộ trình](ROADMAP.md) theo dõi các mốc đã lên kế hoạch.
[Hợp đồng dữ liệu và manifest](docs/reference/data-contracts/index.md) mô tả hành
vi dự kiến và những chỗ tuần tự hoá còn thiếu; code hiện tại chưa thực thi mọi
yêu cầu trong đó.

## Phát triển và kiểm tra trên máy

Các bước kiểm tra của repository cần Python 3; bộ test Python cần thêm PyYAML.
Các module Go khai báo toolchain và dependency trong
[operator/go.mod](operator/go.mod) và [capabilities/go.mod](capabilities/go.mod).
Giao diện dùng Node/npm và [package manifest của nó](workspace-ui/package.json).

Từ thư mục gốc của repository:

```bash
make check
python3 -m unittest discover -s tests -v
```

`make test` chạy thêm các bộ test Go hiện có và, khi đã cài dependency của giao
diện, cả test giao diện lẫn bước build. Xem [kiểm chứng](docs/contribute/verification.md)
để biết lệnh cho từng module, điều kiện cần và giới hạn của độ bao phủ.

## Chạy trên máy

Docker Compose khởi động nền tảng: PostgreSQL, Hasura, operator, Keycloak,
SeaweedFS, ClamAV, Kestra, cap-process, APISIX và Workspace UI.

```bash
scripts/init-env.sh     # tạo .env với secret ngẫu nhiên (không bắt buộc khi chạy trên máy)
docker compose up -d --build
scripts/test-compose.sh # đợi từng dịch vụ sẵn sàng và kiểm tra nó phản hồi
scripts/seed-demo.py seed    # tài khoản, phòng ban và vai trò demo
scripts/seed-demo.py scopes  # mỗi người demo nhìn thấy những gì
scripts/seed-demo.py flow    # đưa một đề nghị mua sắm qua toàn bộ chuỗi duyệt
```

Khi không có `.env`, Compose dùng giá trị mặc định cho môi trường phát triển và
chỉ mở cổng trên 127.0.0.1. `scripts/init-env.sh` tạo `.env` với secret ngẫu
nhiên; hãy chạy nó trước khi đặt `BIND_ADDRESS` để mở stack ra ngoài. Role và mật
khẩu cơ sở dữ liệu được đặt khi volume dữ liệu khởi tạo lần đầu, nên sau khi đổi
chúng cần chạy `docker compose down -v`. Dịch vụ `operator` cài mọi gói ứng dụng
trong `apps/`; chép một gói vào hoặc xoá nó đi thì lượt chạy kế tiếp (mỗi 30 giây)
sẽ cài hoặc gỡ gói đó (xem [hướng dẫn cài đặt](docs/operations/installation.md)).

## Tài liệu

Trang tài liệu được build từ `docs/`; sơ đồ repository nằm ở
[docs/README.md](docs/README.md).

- [Kiến trúc](docs/architecture/index.md) và [ADR](docs/adr/index.md)
- [Hợp đồng dữ liệu và manifest](docs/reference/data-contracts/index.md)
- [Bảo mật và nghiệm thu](docs/reference/security/index.md)
- [Thêm một ứng dụng](docs/guides/adding-an-app.md)
- [Yêu cầu phát hành](docs/contribute/releasing.md)
- [Đóng góp](CONTRIBUTING.md) và [báo cáo lỗ hổng bảo mật](SECURITY.md)

## Giấy phép

Code gốc của dự án được cấp phép theo [Apache-2.0](LICENSE). Xem [NOTICE](NOTICE)
và [COPYRIGHT.txt](COPYRIGHT.txt) để biết thông tin ghi công và dependency.
