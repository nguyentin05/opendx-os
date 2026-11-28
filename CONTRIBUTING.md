# Đóng góp cho OpenDX-OS

**Tiếng Việt** | [English](CONTRIBUTING.en.md)

Đóng góp cần tuân theo [kiến trúc và các quyết định đã thông qua](docs/architecture/index.md)
cùng các hợp đồng liên quan tới thay đổi:

- [Dữ liệu và manifest](docs/reference/data-contracts/index.md)
- [Tiêu chí bảo mật và nghiệm thu](docs/reference/security/index.md)
- [Giấy phép và vệ sinh mã nguồn](docs/contribute/source-hygiene.md)
- [Kiểm chứng và bằng chứng](docs/contribute/verification.md)
- [Thêm một ứng dụng](docs/guides/adding-an-app.md) và [phát hành](docs/contribute/releasing.md)

Khi thay đổi một giao diện dùng chung, hãy xác định các bên tạo và bên dùng bị
ảnh hưởng, cập nhật hợp đồng gốc cùng các ví dụ, và mô tả yêu cầu về tương thích
hoặc chuyển đổi. Khi thay đổi một thiết kế đã thông qua, hãy làm theo quy trình
thay đổi quyết định trong tài liệu kiến trúc.

Giữ thay đổi tập trung và không làm hỏng phần việc không liên quan. Dùng nhánh
hoặc worktree khi cần tách biệt; liên kết tới issue liên quan nếu có. Trong
bàn giao hoặc PR, hãy mô tả vấn đề, hành vi sau thay đổi, các bước kiểm tra đã
chạy và những phần chưa kiểm chứng. Commit message cần rõ ràng, nêu phần bị ảnh
hưởng và mục đích, ví dụ `feat(operator): reconcile excess Hasura column permissions`.

Tài liệu dự án viết bằng tiếng Anh và bản tiếng Anh là bản gốc. Các file cộng
đồng ở thư mục gốc (`README`, `CONTRIBUTING`, `CODE_OF_CONDUCT`, `SECURITY`,
`ROADMAP`) có hai bản: `X.en.md` là bản gốc tiếng Anh, `X.md` là bản dịch tiếng
Việt. Khi sửa một bản, hãy sửa bản còn lại trong cùng PR.

Đóng góp gốc được cấp phép theo [Apache-2.0](LICENSE). Xem tài liệu vệ sinh mã
nguồn để biết header theo từng định dạng và các ngoại lệ của bên thứ ba.
