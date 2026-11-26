---
title: Thư viện tài liệu cá nhân
description: Cho phép tiếp nhận và truy xuất tài liệu chỉ dành cho chủ sở hữu.
diataxis: how-to
audience: [app-author, operator]
status: active
owner: platform
last_reviewed: 2026-11-16
---

# Thư viện tài liệu cá nhân cục bộ

Thư viện cá nhân tiếp nhận các tệp PDF chứa văn bản và tệp Markdown có trạng thái `CLEAN`, chỉ cho phép chủ sở hữu truy cập, thông qua File Service hiện có. Sau khi đăng ký, tài liệu sẽ được đưa vào hàng đợi để trích xuất cục bộ, phân đoạn nội dung có cấu trúc và lập chỉ mục tìm kiếm từ vựng trên PostgreSQL. Tính năng này không yêu cầu khóa mô hình, pgvector hoặc dịch vụ lưu trữ bên ngoài. Endpoint `/answers` dành cho dữ liệu cá nhân trả về `SEARCH_ONLY`; các yêu cầu bắt buộc phải tạo câu trả lời sẽ bị từ chối. Quyền thành viên trong thư viện ứng dụng được kiểm tra dựa trên quyền hạn hiện tại của Core ở mỗi thao tác được bảo vệ. Quản trị viên ứng dụng không được kế thừa quyền truy cập vào nội dung riêng tư của người dùng khác.

## Thành phần runtime và các điều kiện kích hoạt

Áp dụng các migration của Core và cap_ai đã được xác minh checksum bằng lệnh `python3 scripts/migrate_rag.py --help`. Quá trình khởi tạo mới sử dụng cùng các câu lệnh SQL. Cả hai điểm thực thi migration đều nâng cấp bảng ghi nhận migration cũ của Core sang định dạng `(scope, version)` theo cơ chế giao dịch, đồng thời giữ nguyên các phiên bản và checksum hiện có trong phạm vi `core`. Core migration 0002 chỉ cho phép `text/markdown` khi sử dụng chế độ `OWNER_ONLY`; chính sách MIME hiện tại đối với các tệp đính kèm thông thường không thay đổi. cap_ai 0002 bổ sung DocIR theo từng lần chạy, các chunk, liên kết nguồn gốc, FTS đơn giản và cơ chế kích hoạt có fencing. Để hoàn tác việc kích hoạt tính năng, hãy vô hiệu hóa tính năng này; đồng thời giữ lại các giới hạn đối với nguồn dữ liệu và bằng chứng liên quan, thay vì xóa các bảng này hoặc mở rộng quyền truy cập tệp.

Khi nâng cấp, hãy áp dụng lại hoặc đối soát manifest của thư viện bằng Operator hiện tại. Các điều kiện truy vấn tệp riêng tư yêu cầu kết nối Hasura phải có quyền đọc ở cấp cột đối với các bảng phân quyền của Core; các vai trò ứng dụng không được truy cập trực tiếp metadata của những bảng phân quyền này và cũng không được cấp thêm quyền ghi.

API yêu cầu các biến `RAG_METADATA_ENABLED=true`, `RAG_SOURCE_QUALIFIED=true`, sau đó là `RAG_PERSONAL_ENABLED=true`, `RAG_METADATA_QUALIFIED=true` và `RAG_PARSER_QUALIFIED=true`. Tất cả các cờ xác nhận đều mặc định ở trạng thái tắt. Những cờ này thể hiện xác nhận của người vận hành rằng các điều kiện kiểm tra trực tiếp tương ứng đã được đáp ứng, chứ không phải kết quả kiểm thử. Cấu hình `RAG_FILE_API_URL` để kết nối với File Service được bảo vệ độc lập. Không bao giờ đưa các listener nội bộ của parser/worker ra gateway công khai.

Chạy `cap-ai-worker` với các biến cấu hình riêng gồm `AI_WORKER_DATABASE_URL`, `RAG_WORKER_SERVICE_SUBJECT`, `RAG_WORKER_BEARER_TOKEN`, `RAG_INTERNAL_FILE_URL`, `PARSER_SOCKET` và `PARSER_SERVICE_TOKEN` (tham khảo phần kiểm tra cấu hình khi khởi động). Worker cũng yêu cầu cả ba cờ xác nhận nguồn dữ liệu, metadata và parser. Cung cấp cho OIDC service subject của worker chính xác danh tính worker đã được cấu hình trong môi trường triển khai. Worker hỗ trợ hai lease đồng thời, heartbeat mỗi 15 giây, cơ chế thử lại có giới hạn và một Embedding Gateway tùy chọn được xác nhận riêng. Vai trò cơ sở dữ liệu có phạm vi quyền hạn hẹp của worker không thể đọc Core hoặc ghi tùy ý các bản ghi trong cap_ai.

Build parser riêng biệt:

```sh
docker build --build-context legal=. -f capabilities/Dockerfile.parser -t dxos-rag-parser:test capabilities
python3 scripts/integration/test_parser_sandbox.py
```

Chạy image này với các tùy chọn `--network none --memory 512m --cpus 1 --pids-limit 64 --read-only --tmpfs /tmp:rw,noexec,nosuid,size=220m` và một volume Unix socket riêng tư được cấu hình rõ ràng, chỉ chia sẻ với worker. Thiết lập `PARSER_SOCKET` và `PARSER_SERVICE_TOKEN`; giá trị của biến sau phải khớp với `PARSER_SERVICE_TOKEN` trên worker. Không cung cấp cho parser thông tin xác thực về lưu trữ, cơ sở dữ liệu, OIDC hoặc nhà cung cấp dịch vụ. Dịch vụ chỉ nhận metadata multipart và dữ liệu nguồn dưới dạng byte đã được xác thực; quá trình trích xuất chạy trong một nhóm tiến trình con với thời hạn thực thi nghiêm ngặt. Các giới hạn về bộ nhớ, CPU và mạng của container là yêu cầu đối với môi trường triển khai, không phải đặc tính tự động có được chỉ bằng cách chạy tệp thực thi.

## Phạm vi trích xuất đã được xác nhận

| Bộ chuyển đổi | Phiên bản phụ thuộc được cố định | Giấy phép | Phạm vi hỗ trợ |
| --- | --- | --- | --- |
| Markdown AST | [goldmark 1.7.8](https://github.com/yuin/goldmark/tree/v1.7.8) | MIT | Tiêu đề, đoạn văn/danh sách, khối mã được bao bằng dấu phân cách và bảng dạng pipe có nhãn, sử dụng UTF-8; không tải các liên kết từ xa hoặc liên kết tệp |
| Bố cục PDF văn bản | [Poppler utilities 25.12.0-r0, Alpine 3.23](https://pkgs.alpinelinux.org/packages?branch=v3.23&name=poppler-utils) | GPL-2.0-or-later | Các dòng văn bản và bảng đơn giản có căn chỉnh, thông tin nguồn gốc theo trang/hộp giới hạn; không hỗ trợ hình ảnh nhúng hoặc bố cục không rõ ràng |
| Bộ tách token cục bộ | `unicode-rune-v1` trong repository này | Apache-2.0 | Đếm số ký tự Unicode scalar, độc lập với bộ tách token của bất kỳ mô hình nào |

Worker từ chối các phiên bản bộ chuyển đổi khác. Container cố định phiên bản APK; cần xác nhận lại trước khi thay đổi phiên bản này. Các fixture chuẩn (gold fixture) trong `capabilities/internal/knowledge/docir/testdata/` là những tài liệu gốc được tạo để kiểm thử, bao gồm văn bản Markdown/tiếng Việt, một bảng phê duyệt được căn chỉnh tại trang 4 của tệp PDF, một tệp PDF chỉ chứa hình ảnh và một fixture giới hạn 201 trang. Quan hệ trong bảng `>= 10,000,000 VND → Director` được giữ nguyên trong các dòng có nhãn và trích dẫn. Đây là một tập dữ liệu hồi quy quy mô nhỏ, không phải cam kết hỗ trợ mọi bố cục PDF hoặc khả năng truy xuất ngữ nghĩa tiếng Việt tùy ý. FTS đơn giản sử dụng chuẩn hóa NFC và các thuật ngữ từ vựng chính xác, không thực hiện stemming hoặc dịch thuật.

Các giới hạn bao gồm: kích thước nguồn 20 MiB, tối đa 200 trang, thời gian xử lý 120 giây, bộ nhớ container 512 MiB, dung lượng đầu ra sau giải mã 200 MiB, hình ảnh 25 MP, 50.000 phần tử và 20.000 chunk. Các quy trình xử lý hiện tại từ chối toàn bộ hình ảnh trong PDF trước khi giải mã. Tài liệu được quét sẽ trả về `OCR_UNAVAILABLE`; bố cục không rõ ràng hoặc dữ liệu đầu vào sai định dạng sẽ khiến quá trình xử lý thất bại và không được kích hoạt. Văn bản alt của hình ảnh trong Markdown là nội dung do tác giả cung cấp, không phải bằng chứng OCR; các đích hình ảnh không bao giờ được tải xuống. Không kích hoạt OCR/VLM, trích xuất hình ảnh hoặc bộ ghi tài nguyên dẫn xuất. Các route tài nguyên chỉ phục vụ những artifact có trạng thái `AVAILABLE` chính xác từ một lần chạy `COMPLETE` đang hoạt động; các quy trình xử lý chỉ có văn bản này không tạo ra artifact nào. Các quy trình này không tạo tệp bị bỏ dở và cũng không lên lịch quét dọn dữ liệu lưu trữ theo cách xóa dữ liệu.

`chunk-v1` sử dụng mục tiêu 512 rune cho mỗi đoạn văn và giới hạn tối đa cứng là 768 rune. Các dòng logic của bảng giữ lại nhãn cột/đơn vị; những dòng quá dài sẽ giữ lại các ô nhận diện ngắn và phát cảnh báo. Khối mã được phân tách tại ranh giới dòng; các dòng quá dài sẽ khiến quá trình xử lý thất bại. Mỗi chunk chỉ nằm trong một phần tử/mục nội dung, do đó không có phần chồng lấn (overlap bằng 0, nằm trong giới hạn 64 rune). Quy trình thận trọng này ưu tiên khả năng truy xuất nguồn gốc hơn việc gom các khối nhỏ không liên quan vào cùng một chunk. Các lần chạy `COMPLETE` là bất biến; một lần thử mới có fencing có thể thay thế ứng viên thất bại mà không xóa phiên bản cũ vẫn đủ điều kiện hoạt động.

Parser image bổ sung Poppler (`poppler-utils`, GPL-2.0-or-later) dưới dạng một tệp thực thi riêng biệt; xem mục 3 trong `COPYRIGHT.txt`. Các image chứa Poppler khi được phân phối phải kèm theo thông báo giấy phép GPL phù hợp và mã nguồn tương ứng; quy trình phát triển này không phát hành parser image.

## Tích hợp trình duyệt

Shell đọc `/api/auth/me` và `/api/auth/apps` bằng bearer token hợp lệ. Endpoint thứ hai hiện bao gồm khai báo `knowledge` hiện tại, kể cả thư viện chỉ có capability hợp lệ nhưng mảng `tables` rỗng. Chỉ một thư viện đang được bật được chọn. Vai trò được phép mà người dùng đang nắm giữ được sử dụng độc lập với vai trò ứng dụng hiện tại. Các danh sách miền nghiệp vụ thông thường tiếp tục sử dụng những cột được khai báo trong registry và Hasura.

Cung cấp `window.DXOS_CONFIG = {issuer, clientID}` trước UI bundle để cấu hình một OIDC client công khai có các redirect URI được cho phép và bật PKCE S256. Môi trường production yêu cầu HTTPS và cấu hình tường minh; chỉ localhost mới sử dụng cấu hình Keycloak mặc định dành cho phát triển. Access token được giữ trong bộ nhớ, hết hạn theo cơ chế kiểm soát cục bộ và bị xóa khi đăng xuất; thao tác đăng xuất cũng kết thúc phiên Keycloak. Chỉ verifier/state tạm thời của PKCE được lưu trong `sessionStorage`. Không có trường nhập token thủ công, cấu hình nhà cung cấp/khóa hoặc thông tin xác thực được lưu trong bộ nhớ trình duyệt.

Thư viện tải tệp lên ở chế độ riêng tư, kiểm tra định kỳ trạng thái quét thực tế, chỉ đăng ký các nguồn có trạng thái `CLEAN`, hiển thị trạng thái đang chờ xử lý/sẵn sàng/lỗi và hỗ trợ tìm kiếm, trích dẫn được bảo vệ, tải tệp gốc và xóa logic. Việc tải lại tệp sẽ tạo một nguồn dữ liệu mới; API thay thế phiên bản vẫn được kiểm tra revision. Khi chuyển đổi người dùng/ứng dụng/vai trò, hệ thống hủy các yêu cầu đang thực hiện và ngăn các callback cũ cập nhật giao diện, ngay cả khi cơ chế truyền tải không thực hiện việc hủy yêu cầu. Thao tác làm mới quyền sẽ tải lại phiên registry hiện tại.

## Xác minh và giới hạn

Chạy các bài kiểm thử module và kiểm tra giao diện người dùng được liệt kê trong [quy trình xác minh](../../contribute/verification.vi.md). Sử dụng `-tags=integration` và một cơ sở dữ liệu PostgreSQL 17 dùng riêng cho kiểm thử được cấu hình qua `TEST_DATABASE_URL` để kiểm tra cơ sở dữ liệu knowledge/files, bao gồm các trường hợp tiến trình thực tế bị dừng đột ngột và thử lại, kích hoạt bằng fencing cũ và thu hồi quyền nguồn/vai trò. Các bài kiểm thử sử dụng File Service/authority giả lập cục bộ không phải là bằng chứng xác nhận khả năng chấp nhận thực tế của Hasura/scanner/OIDC.

Lệnh `scripts/test-rag.sh personal` chạy các điều kiện kiểm tra nguồn dữ liệu và metadata trước, sau đó kiểm thử parser/worker cục bộ với hai người dùng A/B nạp cùng một nội dung byte, các route được bảo vệ khi cố tình đoán đường dẫn, chế độ `SEARCH_ONLY` và quy trình trình duyệt thực tế bằng Playwright Chromium. Quy trình yêu cầu tất cả các biến nguồn dữ liệu/metadata được nêu trong [xác nhận nguồn dữ liệu](../../reference/security/acceptance.vi.md#native-private-source-qualification), cùng với `RAG_TEST_AI_API_URL`, `RAG_TEST_UI_URL` và các đường dẫn `RAG_TEST_UI_STORAGE_A`/`_B` đến các tệp Playwright storage-state. Những tệp này phải chứa các phiên IdP hiện có của cùng hai người dùng fixture A/B riêng biệt tương ứng với các API token. Cài đặt Playwright Chromium trong môi trường kiểm thử. Các tệp này và thông tin xác thực là dữ liệu đầu vào riêng tư phục vụ kiểm thử, không phải tệp mã nguồn. Sử dụng một môi trường cài đặt dùng riêng cho kiểm thử; các runner sẽ tạo nguồn dữ liệu kiểm thử và thay đổi tư cách thành viên của fixture trong quá trình kiểm tra thu hồi quyền. Nếu thiếu bất kỳ điều kiện tiên quyết nào, kết quả phải là `NOT_RUN`, không bao giờ được xem là `PASS`. Không được báo cáo bất kỳ điều kiện kiểm tra trực tiếp nào là đã đáp ứng chỉ dựa trên nội dung trang này hoặc bằng chứng từ container/bài kiểm thử đơn vị.