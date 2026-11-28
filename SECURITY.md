# Chính sách bảo mật

**Tiếng Việt** | [English](SECURITY.en.md)

## Phiên bản được hỗ trợ

| Phiên bản | Được hỗ trợ |
| :--- | :--- |
| `main` (chưa phát hành) | Có |

## Các bất biến của kiến trúc bảo mật

Các yêu cầu bảo mật dự kiến và kịch bản nghiệm thu được định nghĩa trong
[tiêu chí bảo mật và nghiệm thu](docs/reference/security/index.md),
còn cơ chế nằm trong [hợp đồng dữ liệu](docs/reference/data-contracts/index.md).
Phần triển khai vẫn đang phát triển; các yêu cầu đó không có nghĩa là mọi thuộc
tính bảo mật đã được kiểm chứng trên hệ thống đang chạy.

## Báo cáo lỗ hổng

Vui lòng **không** báo cáo lỗ hổng bảo mật qua GitHub issue công khai.
Hãy dùng tính năng GitHub Private Vulnerability Reporting (Security Advisories)
của repository và gửi kèm:

- Thành phần bị ảnh hưởng và phiên bản
- Các bước tái hiện hoặc proof-of-concept
- Mức ảnh hưởng có thể có tới việc cách ly workspace, phân quyền, cách ly tệp hoặc tính toàn vẹn của nhật ký kiểm toán

Nhóm duy trì sẽ xác nhận đã nhận báo cáo trong vòng 48 giờ và phối hợp lịch sửa lỗi cùng thời điểm công bố.
