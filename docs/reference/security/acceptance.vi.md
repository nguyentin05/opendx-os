---
title: Các kịch bản nghiệm thu
description: Tám nhóm kịch bản nghiệm thu bắt buộc và các trường hợp cần xem xét bổ sung.
diataxis: reference
audience: [security-reviewer, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-23
---


## Tám nhóm kịch bản nghiệm thu bắt buộc

| ID | Kịch bản | Bằng chứng cần có khi triển khai |
| --- | --- | --- |
| S01 | Cô lập theo ứng dụng, phòng ban, chủ sở hữu và vai trò hiện tại | Các luồng CRUD, relationships, nested, upsert và xử lý qua service trực tiếp phải tuân thủ phạm vi vai trò `own`/`department`/`organization` và ACL của tài nguyên. Thử nghiệm các liên kết xuyên ứng dụng, thuộc phòng ban khác và chủ sở hữu khác; thu hồi tư cách thành viên trong DB trong khi JWT vẫn còn hiệu lực; kiểm tra trường hợp vai trò mới được cấp trong DB nhưng token chưa được làm mới vai trò, actor bị vô hiệu hóa trong DB và authority không khả dụng. Các vai trò thông thường không được tự gán phạm vi truy cập đặc quyền, trạng thái hoặc phiên bản. Các luồng xử lý tệp gốc và Hasura phải được xác nhận độc lập |
| S02 | Khử trùng lặp dữ liệu OT | Gửi cùng một bộ giá trị `(app_id, device_id, event_dedup_key)` nhiều lần chỉ tạo ra một sự kiện và một tác động công việc cho mỗi workflow đã đăng ký |
| S03 | Tính idempotency của workflow/lệnh | Khi thử lại cùng một khóa và payload sau khi commit, hệ thống trả về kết quả trước đó; khóa trùng lặp đi kèm payload khác sẽ bị từ chối; sự cố khiến một bước phải chạy lại không được làm lặp lại các tác động nghiệp vụ |
| S04 | Phiên bản cũ và xử lý đồng thời | Chỉnh sửa nội dung thông qua CRUD làm tăng phiên bản chính xác một lần; thao tác phê duyệt bằng phiên bản cũ trả về `409`; khi hai lệnh được thực thi đồng thời với cùng một phiên bản, chỉ một lệnh thành công; lần thực thi thất bại không tạo thêm bản ghi kiểm toán hoặc sự kiện mới |
| S05 | Trạng thái độc lập giữa các ứng dụng | Việc chuyển đổi trạng thái trên một bản ghi không ghi đè trạng thái bản ghi của ứng dụng khác, ngay cả khi chúng dùng chung một work item/nhân viên |
| S06 | Cách ly tệp | Các tệp ở trạng thái `QUARANTINED`/`INFECTED` bị chặn khỏi các thao tác tải xuống, OCR và gửi dữ liệu; không cho phép tự ghi trạng thái `CLEAN`; khi thay đổi nội dung, phải tạo tệp mới và quét lại |
| S07 | Đối soát dữ liệu xuất | Chạy lại một batch không làm tăng tổng số liệu; batch thất bại và chưa được công bố không được tham gia vào BI; cùng một UUID nhưng khác loại thực thể hoặc ứng dụng phải được phân biệt |
| S08 | Truy vết nguồn gốc dữ liệu | Mỗi chỉ số phải truy xuất được đầy đủ và chính xác đến nguồn `(app_id, entity_type, entity_id, entity_version)`; kiểm thử cả trường hợp tổng hợp từ nhiều bản ghi và nhiều phiên bản |

Chọn các bước kiểm tra phù hợp với phạm vi và ghi lại kết quả theo tài liệu [xác minh](../../contribute/verification.vi.md). Mỗi bản phát hành phải ghi nhận bằng chứng cho tất cả các nhóm trên, tương ứng với revision và artifact được phát hành. S02, S07 và S08 cần dịch vụ cap-data, dự kiến được triển khai trong phiên bản v1.1.0 (#111); cho đến thời điểm đó, bản phát hành phải ghi rõ các nhóm này chưa được triển khai thay vì đánh dấu là đã vượt qua kiểm thử; riêng S06 hiện chưa có luồng OCR để kiểm tra.

## Các trường hợp cần xem xét bổ sung

Kiểm tra để bảo đảm việc lựa chọn vai trò luôn nằm trong phạm vi quyền đã được xác thực; các yêu cầu và cache UI cũ phải bị loại bỏ khi ngữ cảnh thay đổi; các capability đã bị vô hiệu hóa vẫn không khả dụng đối với những client cũ. OCR phải kiểm tra quyền truy cập tệp đính kèm của người gọi; RAG phải kiểm tra quyền truy cập nguồn dữ liệu và các trích dẫn. Đối với kiểm toán, hãy kiểm thử các thao tác ghi nối tiếp đồng thời và khả năng phát hiện những chỉnh sửa đối với lịch sử đã được neo checkpoint. Mỗi phát hiện cần mô tả vị trí, kịch bản có thể tái hiện, mức độ ảnh hưởng và hướng khắc phục.

Các nguồn tham khảo về hành vi của Hasura: [quyền trên từng hàng (row permissions)](https://hasura.io/docs/2.0/auth/authorization/permissions/row-level-permissions/), [lỗi HTTP của GraphQL](https://hasura.io/docs/2.0/deployment/graphql-engine-flags/reference/#preserve-401-errors). Khi phiên bản được triển khai thay đổi, hãy kiểm tra tài liệu tương ứng với phiên bản đó.

### Xác nhận điều kiện đối với nguồn riêng tư tích hợp sẵn

Các kiểm tra unit/SQL ở tầng nền tảng nguồn không đủ để xác nhận mức độ sẵn sàng phát hành của thư viện riêng tư. Chạy `scripts/test-rag.sh source` trên môi trường PostgreSQL 17, Hasura, File Service, bộ lưu trữ S3 riêng tư và dịch vụ quét tệp dùng riêng cho kiểm thử. Các biến môi trường bắt buộc:

`TEST_DATABASE_URL`, `RAG_TEST_HASURA_URL`, `RAG_TEST_HASURA_ADMIN_SECRET`, `RAG_TEST_FILE_API_URL`, `RAG_TEST_S3_ENDPOINT`, `RAG_TEST_S3_BUCKET`, `RAG_TEST_OIDC_TOKEN_A`, `RAG_TEST_OIDC_TOKEN_B`, `RAG_TEST_OIDC_TOKEN_ADMIN`.

Bản cài đặt phải có một thư viện cá nhân knowledge-v1 ở trạng thái Ready, trong đó cờ tải lên riêng tư chỉ được bật trong quá trình xác nhận điều kiện. Sử dụng các định danh dùng riêng cho kiểm thử; runner sẽ khởi tạo tư cách thành viên và thực hiện thu hồi/khôi phục tư cách thành viên của A. Token và DSN phải được giữ trong biến môi trường và tuyệt đối không được in ra. Cần có Python websocket-client để kiểm tra subscription trên session cũ. Nếu thiếu điều kiện tiên quyết, kết quả phải là `NOT_RUN` và mã thoát khác 0, tuyệt đối không được đánh dấu `PASS`. Cổng kiểm tra này không bật các route knowledge hoặc hosted model.

R01/R02/R04/R13 yêu cầu kiểm thử việc A/B tải lên cùng một nội dung byte nhưng nhận các file ID khác nhau; từ chối quyền truy cập tệp gốc và metadata đối với chủ sở hữu khác/quản trị viên; chính sách riêng tư không thể thay đổi; kiểm tra trạng thái CLEAN/hash; từ chối nguồn bị nhiễm; từ chối truy cập đối tượng trong bucket khi chưa xác thực; và thu hồi quyền trong khi vẫn sử dụng cùng JWT cùng một subscription metadata đã mở trước đó. Việc thu hồi quyền trước bước kiểm tra tệp cuối cùng sẽ chặn việc truy cập nội dung tệp, nhưng dữ liệu byte đã được gửi đi thì không thể thu hồi lại. Giữ tính năng tải lên riêng tư ở trạng thái tắt ngoài quá trình xác nhận điều kiện cho đến khi các kiểm thử âm trực tiếp này đều đạt yêu cầu. Chế độ cá nhân cũng yêu cầu xác nhận điều kiện đối với parser/worker và trình duyệt; xem [hướng dẫn thư viện cá nhân](../../guides/knowledge/personal-library.md). Các chế độ package/model vẫn được dành riêng và phải trả về `NOT_RUN` cho đến khi được triển khai.

### Xác nhận điều kiện đối với metadata và dịch vụ nội bộ

Các route metadata mặc định bị đóng. `RAG_METADATA_ENABLED=true` và `RAG_SOURCE_QUALIFIED=true` là các xác nhận từ môi trường triển khai dành cho một bản cài đặt dùng riêng cho kiểm thử đã được xác nhận đủ điều kiện; không thiết lập các biến này trước khi cổng kiểm tra trực tiếp đối với nguồn dữ liệu vượt qua. cap-ai công khai sử dụng `DATABASE_URL` cho dxos_ai_api và xác minh danh tính OIDC một cách nghiêm ngặt. Các mật khẩu DB tùy chọn gồm `AI_DATABASE_PASSWORD`, `AI_WORKER_DATABASE_PASSWORD` và `AI_DISPATCHER_DATABASE_PASSWORD`; việc không thiết lập mật khẩu không đồng nghĩa với việc được cấp quyền đăng nhập.

Các dịch vụ nội bộ yêu cầu một địa chỉ `RAG_INTERNAL_LISTEN` riêng biệt, được khai báo tường minh; các subject UUID được đưa vào allowlist phải khác nhau: `RAG_WORKER_SERVICE_SUBJECT` và `RAG_FILE_SERVICE_SUBJECT`; đồng thời phải có cấu hình issuer/audience/JWKS hiện hữu và `RAG_SOURCE_QUALIFIED=true`. cap-ai cũng yêu cầu `AI_WORKER_DATABASE_URL` cho dxos_ai_worker; File Service nội bộ yêu cầu `RAG_INTERNAL_AI_URL` và `RAG_INTERNAL_BEARER_TOKEN` do môi trường triển khai quản lý để xác thực subject của File Service. Các claim về vai trò của người dùng không thể thay thế các service subject này. Subject đã được xác minh của worker quyết định danh tính của worker; body không được phép chỉ định một worker khác. Giữ các listener nội bộ ngoài các route của gateway công khai. Các HTTP client của validator phải từ chối redirect và các đích đến tùy ý được chỉ định theo từng request. Token dịch vụ hết hạn, authority không khả dụng hoặc lease bị thu hồi đều phải dẫn đến việc từ chối truy cập nguồn dữ liệu.

`​scripts/test-rag.sh metadata` yêu cầu môi trường của chế độ source cùng với `RAG_TEST_AI_API_URL`; script sẽ chạy cổng kiểm tra nguồn thực tế trước, sau đó kiểm tra việc đăng ký đồng thời qua REST/DB, xung đột receipt, khả năng cô lập metadata giữa A/B/quản trị viên và thao tác xóa bền vững. Các bộ kiểm thử unit/SQL riêng biệt kiểm tra việc tiêm lỗi rollback, lease, khởi động lại worker, ủy quyền truy cập nguồn và thu hồi quyền trong khi đang fetch dữ liệu. Nếu thiếu điều kiện tiên quyết để kiểm thử trực tiếp, kết quả phải là `NOT_RUN` và mã thoát khác 0. Các bài kiểm thử chỉ sử dụng DB hoặc các bảng package chưa hoạt động không đủ để xác nhận việc định tuyến danh tính dịch vụ thực tế, quyền cấp cho package, chất lượng parser hoặc tính riêng tư của model.
