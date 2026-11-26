---
title: Câu trả lời từ mô hình
description: Cấu hình gateway mô hình tùy chọn để tạo câu trả lời cho dữ liệu tri thức.
diataxis: how-to
audience: [operator, platform-developer]
status: active
owner: platform
last_reviewed: 2026-11-24
---

# Truy xuất và trả lời bằng mô hình tùy chọn

Ranh giới tích hợp mô hình là tùy chọn. Thư viện cá nhân và chức năng tìm kiếm từ vựng trong gói ứng dụng vẫn hoạt động mà không cần pgvector, hồ sơ, thông tin xác thực hoặc mô hình. Nội dung cá nhân và câu hỏi chỉ được phép sử dụng các chức năng EMBED/GENERATE được lưu trữ trên máy chủ theo [chính sách xử lý đã sửa đổi](../../reference/rag/authorization.md#model-processing-policy): việc phê duyệt có kiểm toán của Platform Admin được thực hiện riêng biệt với việc kích hoạt model slot và phải có mục đích sử dụng, phiên bản hồ sơ/nhà cung cấp, trạng thái bật, phân loại nguồn/câu truy vấn độc lập và đánh giá về thời gian lưu giữ/cách sử dụng dữ liệu cho mục đích huấn luyện. Không có cơ chế đăng ký sử dụng dịch vụ lưu trữ trên máy chủ riêng cho từng người dùng. Hoạt động đưa dữ liệu PERSONAL ra ngoài khi chưa được phê duyệt sẽ bị từ chối. Các chức năng RERANK, OCR/VLM được lưu trữ trên máy chủ và việc cung cấp mô hình cục bộ nằm ngoài phạm vi của đợt triển khai này. Việc chọn ứng dụng không đồng nghĩa với việc cấp quyền. `/rag` phiên bản cũ vẫn không khả dụng.

Các migration 0008–0010 bổ sung chức năng xử lý PERSONAL có kiểm toán, truy xuất ngữ nghĩa PERSONAL và ranh giới mô hình AGENT hỗ trợ nhiều nguồn. Migration 0011 bổ sung các hàm quản trị slot có phạm vi hẹp, được kiểm toán và tài khoản đăng nhập quản trị triển khai; migration này tuân theo cùng cơ chế cho phép sử dụng agent schema và không thay đổi bất kỳ thiết lập mặc định nào. Hãy xem [các thay đổi về migration/consumer trong ADR 0009](../../adr/0009-native-rag-identity-and-source-boundaries.md#contract-amendment-and-implementation-delta-2026-11-18) trước khi bật hành vi mới; chỉ cấp phát một slot đang hoạt động không thể tự triển khai hoặc cấp quyền cho các chức năng này.

## Cài đặt bằng Compose

Tệp `compose.yaml` cơ sở chạy ngăn xếp hệ thống thông thường. Bổ sung `compose.rag.yaml` để chạy Knowledge API, cơ chế ủy quyền riêng tư của File Service, package dispatcher, parser và worker. Chỉ bổ sung `compose.models.yaml` khi cần hỗ trợ schema/runtime cho mô hình. Sử dụng cùng thứ tự tệp và tên project trong toàn bộ quá trình cài đặt.

1. Từ một bản checkout sạch, chạy:

   ```bash
   make rag-up
   ```

   Lệnh này sử dụng cùng Compose project với `docker compose up`; hãy đặt `COMPOSE_PROJECT_NAME` và `POSTGRES_PORT` nếu muốn chạy một ngăn xếp riêng biệt. Target này gọi `scripts/rag-up.sh`, tập lệnh sẽ tạo `.env.rag` (được Git bỏ qua, quyền truy cập tệp ở chế độ 0600) nếu tệp chưa tồn tại. Tập lệnh tự tạo mật khẩu DB ngẫu nhiên, token cho parser và ba client secret của Keycloak, đồng thời thiết lập các service subject, client ID và cờ FTS cố định. Tập lệnh không bao giờ in hoặc ghi đè secret; những lần chạy sau sẽ giữ nguyên tệp này.

2. Tập lệnh khởi chạy các tệp base và RAG bằng lệnh:

   `docker compose --env-file .env --env-file .env.rag -f compose.yaml -f compose.rag.yaml up -d --build`

   (gộp các tệp đối với những phiên bản Compose cũ hơn). Tập lệnh chờ cho đến khi `personal_library` chuyển sang trạng thái `Ready`, thiết lập `AI_AGENT_PROCESSING_APP_ID=personal_library` và `RAG_AGENT_ENABLED=true`, sau đó chạy lại `up -d`. Giao diện người dùng có tại `http://localhost:5173`; Assistant và thư viện cá nhân đã được bật. `scripts/test-compose.sh` và dữ liệu seed demo là các bước tiếp theo không bắt buộc.

3. Tệp overlay bật `cap-ai` trong `ENABLED_CAPABILITIES`. Danh sách này chỉ được áp dụng khi volume PostgreSQL được khởi tạo lần đầu. Nếu một volume hiện có được tạo mà không có capability này, `rag-up` sẽ dừng và hiển thị thông báo yêu cầu tạo lại volume đó; tập lệnh không xóa dữ liệu. Overlay models cũng yêu cầu `AI_GATEWAY_DATABASE_PASSWORD` và lựa chọn PostgreSQL 17 có pgvector. Để nâng cấp dữ liệu hiện có, hãy sử dụng quy trình migration có checksum bên dưới, đăng ký cap-ai và cấp phát các role cùng mật khẩu bị giới hạn trước khi đối soát ứng dụng, đồng thời sao lưu volume trước khi thay đổi image.

4. Các dịch vụ tự lấy token của riêng mình: `dxos-rag-worker`, `dxos-file-service` và `dxos-rag-dispatcher` là các realm client bảo mật có service account; mỗi client có một subject cố định tương ứng với một danh tính Core đang ở trạng thái ACTIVE. Realm nhập các client này ở trạng thái vô hiệu hóa cùng một secret được tạo tự động, vì vậy một ngăn xếp không có overlay này sẽ không thể tạo service token. `rag-up` khởi chạy Keycloak trước, sau đó bật ba client thông qua admin API bằng các giá trị `RAG_*_CLIENT_SECRET` từ `.env.rag`. Hostname công khai của Keycloak được cố định bằng `KEYCLOAK_HOSTNAME` (mặc định là `http://localhost:8085`) để các token được lấy qua mạng nội bộ vẫn chứa issuer mà các dịch vụ xác minh; giá trị này phải khớp với `OIDC_ISSUER_URL`. Worker, client ủy quyền của File Service và dispatcher làm mới token khi đã sử dụng 75% thời hạn hiệu lực, do đó không cần gia hạn thủ công hoặc tạo lại container. `PARSER_SERVICE_TOKEN` vẫn là một secret ngẫu nhiên riêng biệt có độ dài tối thiểu 32 ký tự; parser và worker dùng chung một private socket volume và UID 10001. API, worker và dispatcher kết nối bằng tài khoản DB bị giới hạn tương ứng.

5. Việc kích hoạt mô hình vẫn được thực hiện thủ công. Bổ sung `compose.models.yaml`, cung cấp các cờ schema, catalog và tệp thông tin xác thực, sau đó thực hiện các bước bên dưới. Đối với adapter đi kèm, cần chuẩn bị thêm các tệp riêng tư và bổ sung `compose.model-adapter.yaml` làm tệp `-f` cuối cùng ([adapter lưu trữ trên máy chủ đi kèm](#bundled-hosted-adapter-opt-in)). `make rag-up` không bao giờ tự bật mô hình hoặc khởi chạy adapter.

6. Trước khi bật các lời gọi mô hình, hãy xác nhận adapter có phạm vi giới hạn đáp ứng yêu cầu, nhập các ứng viên ở trạng thái vô hiệu hóa, hoàn tất quá trình đánh giá của Platform Admin và kích hoạt chính xác các phiên bản EMBEDDING và/hoặc LLM thông qua quy trình slot đã được kiểm toán của môi trường triển khai. Cấp phát các bản đánh giá xử lý bên dưới. Sau đó, bật `RAG_MODELS_QUALIFIED`, `RAG_MODELS_ENABLED` và `RAG_GLOBAL_MODEL_REGISTRY_ENABLED` cho API và worker rồi tạo lại các dịch vụ này. Agent chỉ phân giải các slot toàn cục. Nếu thiếu bản đánh giá, binding hoặc adapter, thao tác tương ứng sẽ không khả dụng. Các overlay tùy chọn không cung cấp slot đang hoạt động, thông tin quy kết consumer hoặc phê duyệt nhà cung cấp.

### Nâng cao: tự cung cấp danh tính

Sử dụng phương án này khi môi trường triển khai đã cấp token hoặc tự quản lý các Keycloak client.

- Đặt `RAG_WORKER_SERVICE_SUBJECT`, `RAG_FILE_SERVICE_SUBJECT` và `RAG_DISPATCHER_SERVICE_SUBJECT` thành các OIDC subject riêng biệt và đăng ký từng subject dưới dạng danh tính Core có trạng thái ACTIVE. Các realm client được cung cấp sẵn sử dụng những subject cố định trong `.env.example`.

- Cung cấp token có thể gia hạn và khớp audience trong các biến `RAG_WORKER_BEARER_TOKEN`, `RAG_FILE_BEARER_TOKEN` (cap-process tiếp nhận token này dưới tên `RAG_INTERNAL_BEARER_TOKEN`) và `RAG_DISPATCHER_BEARER_TOKEN`; hoặc để trống các biến này và thiết lập các cặp `RAG_*_CLIENT_ID` / `RAG_*_CLIENT_SECRET`. Token tĩnh đã được cấu hình sẽ được ưu tiên; nếu không, dịch vụ sẽ sử dụng client credentials. Token tĩnh phải được gia hạn trước khi hết hạn và các consumer sử dụng chúng phải được tạo lại.

- Cấu hình cùng một issuer, JWKS và audience trên các dịch vụ (`OIDC_ISSUER_URL`, `OIDC_JWKS_URL`, `OIDC_AUDIENCE`) cũng như endpoint token nội bộ (`OIDC_TOKEN_URL`). Đặt `KEYCLOAK_HOSTNAME` thành origin công khai của issuer đó để các token được yêu cầu thông qua hostname Keycloak nội bộ vẫn chứa issuer mà các dịch vụ xác minh.

- Sử dụng một `PARSER_SERVICE_TOKEN` ngẫu nhiên riêng biệt có độ dài tối thiểu 32 ký tự và các mật khẩu DB riêng biệt.

- Chỉ đặt `RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED` và `RAG_PARSER_QUALIFIED` sau khi đã xác minh các điều kiện tiên quyết đối với nguồn dữ liệu và parser. Đặt `RAG_METADATA_ENABLED=true` và bật `RAG_PACKAGE_ENABLED`, `RAG_PERSONAL_ENABLED` hoặc cả hai. Bật `RAG_PRIVATE_UPLOAD_ENABLED` đối với chức năng tải lên riêng tư.

- Chạy `docker compose -f compose.yaml -f compose.rag.yaml up -d --build` bằng tệp env của môi trường triển khai. Chọn một ứng dụng đã đăng ký và ở trạng thái `Ready` cho `AI_AGENT_PROCESSING_APP_ID`, đặt `RAG_AGENT_ENABLED=true`, sau đó chạy lại cùng lệnh. Khi mô hình bị tắt, chat sẽ trả về bằng chứng tìm kiếm từ vựng trong phạm vi chủ sở hữu.

## Lưu trữ và kích hoạt

Chỉ áp dụng `cap-ai/0004_models_vectors.sql` một cách tường minh bằng `scripts/migrate-rag.sh` sau khi pgvector đã được cấp phát. Đây là migration bổ sung sau migration package 0003; tuyệt đối không thay đổi checksum của các migration đã được áp dụng, ngoại trừ trường hợp phối hợp đặc biệt được ghi nhận trong [ADR 0010](../../adr/0010-model-operations-without-monetary-admission.md). Quá trình bootstrap bỏ qua 0004 trừ khi `RAG_MODELS_SCHEMA_ENABLED=true`; thiết lập này yêu cầu image có chứa extension tương ứng. Không có profile nào được bật hoặc phê duyệt nhà cung cấp nào được tạo sẵn. Các bảng kiểm soát nguồn, bản gốc, kiểm toán và tìm kiếm từ vựng vẫn được giữ lại khi mô hình bị vô hiệu hóa.

Registry toàn cục tùy chọn bổ sung `cap-ai/0005_model_registry.sql` sau 0004. Sử dụng `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope cap-ai --through 0005` để chủ động bật chức năng này (biến môi trường được chỉ định chứa connection URL). Runner áp dụng và tạo checksum riêng biệt cho 0004 và 0005. Quá trình bootstrap yêu cầu cả `RAG_MODELS_SCHEMA_ENABLED=true` và `RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true` đối với 0005; cả hai cờ schema đều mặc định là false. Những cờ schema này không bật mô hình runtime hoặc registry. Migration 0005 tạo bốn slot trống và không tạo ứng viên, profile được bật, phê duyệt nhà cung cấp hoặc binding nào. VLM và RERANKING không thể được binding trong R1.

### Đồng bộ lại trên một hệ thống đã cài đặt

`0004` và `0005` đã được đồng bộ lại ngay tại vị trí hiện có để phù hợp với baseline đã được phối hợp ([ADR 0010](../../adr/0010-model-operations-without-monetary-admission.md)). Cơ sở dữ liệu đã áp dụng các checksum mô hình trước đó không thể được nâng cấp trực tiếp tại chỗ: cả hai runner đều phát hiện xung đột trước khi thực thi bất kỳ domain SQL nào và từ chối tiếp tục; việc chỉnh sửa ledger hoặc bỏ qua kiểm tra checksum đều không được chấp nhận như một biện pháp khắc phục. Chủ sở hữu môi trường cài đặt phải xây dựng lại một cơ sở dữ liệu có tên trên phiên bản đã được phê duyệt, thực hiện bootstrap, nạp các khai báo `schema_version: 2` hợp lệ cùng các phê duyệt mô hình hiện hành, sau đó khởi chạy các binary tương ứng với cơ sở dữ liệu đó. Không khôi phục toàn bộ bản dump `cap_ai` vào schema đã được đồng bộ lại: thao tác này sẽ đưa các quan hệ và bản ghi liên quan đến thanh toán đã bị loại bỏ trở lại hệ thống, đồng thời chứa các bản ghi operation không còn tương thích. Thay vào đó, hãy tái tạo trạng thái mô hình — xuất bản lại các package, đăng ký lại nguồn dữ liệu, nhập lại ứng viên, phê duyệt lại chính sách xử lý và cấp lại quyền. Quy trình xây dựng lại có phạm vi giới hạn, bằng chứng đã được phê duyệt và khoảng thời gian bảo trì được mô tả trong [installation](../../operations/installation.md#rebuilding-an-existing-model-installation).

Tài khoản đăng nhập triển khai riêng biệt `dxos_ai_model_catalog` chỉ có thể thực thi `cap_ai.registry_import_candidates(jsonb)` trong registry. Tài khoản này không thể kích hoạt profile, thay đổi slot, chính sách hoặc quyền, cũng không thể đọc dữ liệu tri thức được bảo vệ. Cấp phát thông tin xác thực cho tài khoản thông qua deployment secrets; không có runtime API/worker nào sử dụng tài khoản đăng nhập này. `dxos_ai_gateway` được bổ sung quyền thực thi chỉ đọc đối với `cap_ai.registry_resolve_slot(text)`. Importer yêu cầu danh tính phiên catalog chính xác và từ chối các trường không xác định, giới hạn không hợp lệ cũng như xung đột phiên bản bất biến theo cách nguyên tử. Tập hợp ứng viên R1 của importer là HOSTED EMBED/GENERATE; hệ thống chỉ lưu các adapter key và tham chiếu đến biến môi trường chứa thông tin xác thực, không lưu endpoint hoặc giá trị thông tin xác thực. Digest khai báo do bên gọi cung cấp chỉ là metadata; các khai báo đã chuẩn hóa đầy đủ cũng được so sánh, vì vậy digest giống nhau không bao giờ được dùng để cấp quyền cho các định nghĩa xung đột.

Việc kích hoạt yêu cầu một thao tác triển khai của Platform Admin đã được đánh giá độc lập. Migration 0011 cung cấp thao tác này; các lệnh `dxos-model activate`, `deactivate`, `slots` và `list` ([tài liệu tham chiếu CLI](../../reference/cli/dxos-model.md)) là giao diện duy nhất để thực hiện thao tác đó. Xác thực Platform Admin, kiểm tra digest của ứng viên, mục đích sử dụng, giới hạn và adapter, sau đó ghi nhận kết quả đánh giá. Giao dịch quản trị bật profile đã được đánh giá, cập nhật phiên bản slot dự kiến để binding chính xác profile ID và revision đó, đồng thời ghi nhận actor và hành động trong `cap_ai.model_slot_events`. Nếu có revision đồng thời thay đổi, cần đánh giá lại slot hiện tại. Các tài khoản đăng nhập API, worker, catalog và Gateway không thể thực hiện thao tác này.

Các bảng mô hình lưu trữ các revision profile/policy bất biến, danh sách cho phép của policy, các thế hệ embedding độc lập và trạng thái operation bền vững. Revision policy/profile có thể bị vô hiệu hóa, nhưng định nghĩa và lịch sử allowlist của chúng không thể bị viết lại. Cấp phát từng revision policy/profile mới đã được phê duyệt thông qua các thao tác được đánh giá; ghi nhận phê duyệt có kiểm toán của Platform Admin và metadata đánh giá bất biến trong cùng một giao dịch cấp phát. Chính sách tài liệu và chính sách câu truy vấn được kiểm tra riêng biệt. Hoạt động xử lý APP hiện có tiếp tục sử dụng các chính sách truy vấn được lưu trữ trên máy chủ, gắn với ứng dụng và có `NON_PERSONAL` ở trạng thái bật. Migration 0008 bổ sung chức năng xử lý nguồn PERSONAL và truy vấn PERSONAL được đánh giá độc lập. Các consumer AGENT vẫn phải áp dụng các điều kiện lọc theo từng nguồn dữ liệu chuẩn tắc. Ngữ cảnh chứa nhiều nguồn vẫn phải tuân thủ các yêu cầu xử lý PERSONAL. JSON của request không thể tự khai báo phân loại hoặc lựa chọn thông tin xác thực/endpoint. Không sửa đổi một policy chỉ hoạt động cục bộ hiện có thành phê duyệt cho xử lý trên máy chủ; hãy tạo revision mới đã được đánh giá và binding tường minh các nguồn đủ điều kiện. Các phê duyệt bị vô hiệu hóa hoặc không khớp sẽ khiến việc đưa dữ liệu ra ngoài bị từ chối.

Sau khi kích hoạt một revision EMBEDDING mới, hãy giữ worker tiếp tục chạy. Mỗi lần polling `Worker.Once`, worker sẽ phân giải slot hiện tại và gọi `cap_ai.queue_embeddings` thông qua tài khoản đăng nhập worker đối với các phiên bản tài liệu đang hoạt động và đủ điều kiện, bao gồm cả những tài liệu đã được phân tích trước đó. Revision mới cần có allowlist policy xử lý đã được đánh giá riêng hoặc phê duyệt PERSONAL trước khi một job có thể được đưa vào hàng đợi. Việc đưa job vào hàng đợi có tính idempotent đối với phiên bản và danh tính profile chính xác. Sau đó, worker phân giải revision được ghim trong lease đã được fencing trước khi xây dựng generation. Hãy duy trì các tuyến xử lý của nhà cung cấp cũ cho đến khi tất cả lease đang chờ xử lý của chúng hoàn tất.

Các job EMBED ghim `profile@revision`, lexical run, chunk hash và dimensions. Chỉ một generation hoàn chỉnh mới được chuyển sang trạng thái READY; việc mất lease, xóa nguồn, xuất hiện phiên bản mong muốn mới hơn hoặc policy bị vô hiệu hóa sẽ ngăn thao tác ghi/kích hoạt. Xếp hạng cosine chính xác sẽ đưa các nguồn đang hoạt động và đủ điều kiện vào bộ nhớ trước khi xếp hạng. Không cài đặt ANN hoặc reranker. Quá trình hợp nhất sử dụng 50 kết quả đầu tiên trong mỗi danh sách, hằng số RRF là 60, thứ tự ổn định khi có kết quả bằng nhau, tối đa bốn chunk cho mỗi tài liệu và tối đa 20 kết quả. Tính đủ điều kiện của policy/profile nguồn được kiểm tra lại khi truy xuất vector. Thành viên role không bao giờ được nhúng vào vector.

## Cấp phát xử lý PERSONAL có kiểm toán

Migration `0008_personal_model_processing.sql` bổ sung giao diện phê duyệt vào kết nối triển khai `dxos_authz` hiện có. Cả thao tác phê duyệt và vô hiệu hóa đều yêu cầu một người dùng Core có trạng thái ACTIVE và được cấp quyền `role_operator` hiện hành trong DB. Quản trị viên ứng dụng, API, worker, Gateway và `dxos_ai_model_catalog` không thể phê duyệt hoạt động xử lý. Các hàm xác thực `session_user`, từ chối role bị thay thế và các tài khoản runtime có đặc quyền, đồng thời kiểm toán actor đã được xác minh và phiên triển khai. Trong đợt triển khai này không có giao diện quản lý. Người vận hành môi trường triển khai thực thi các hàm này thông qua kết nối `dxos_authz` được bảo vệ sau khi xác thực Platform Admin, bằng `psql` như minh họa bên dưới hoặc bằng lệnh `dxos-model policy approve|revoke` sử dụng `AI_MODEL_APPROVAL_DATABASE_URL` và token được bảo vệ ([tài liệu tham chiếu CLI](../../reference/cli/dxos-model.md)).

Đối với volume mặc định được giữ lại, trước tiên hãy sao lưu và kết nối bằng tài khoản quản trị cơ sở dữ liệu triển khai thông qua các biến môi trường libpq tiêu chuẩn (`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD`, `PGSSLMODE`). Đặt các biến `AI_DATABASE_PASSWORD`, `AI_WORKER_DATABASE_PASSWORD` và `AI_DISPATCHER_DATABASE_PASSWORD` thành các giá trị không rỗng, an toàn cho URL trong cùng môi trường được bảo vệ. Từ thư mục gốc của repository, chạy các lệnh sau trước khi đối soát ứng dụng. Thao tác upsert registry và tạo role có tính idempotent; chạy lại sẽ cập nhật ba mật khẩu này.

```sh
psql -X -v ON_ERROR_STOP=1 -f platform/postgres/capabilities/cap_ai.sql

psql -X -v ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY ARRAY['dxos_ai_api','dxos_ai_worker','dxos_ai_dispatcher'] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN', name);
    END IF;
    EXECUTE format('ALTER ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT', name);
  END LOOP;
END $$;
\getenv api_password AI_DATABASE_PASSWORD
\getenv worker_password AI_WORKER_DATABASE_PASSWORD
\getenv dispatcher_password AI_DISPATCHER_DATABASE_PASSWORD
ALTER ROLE dxos_ai_api PASSWORD :'api_password';
ALTER ROLE dxos_ai_worker PASSWORD :'worker_password';
ALTER ROLE dxos_ai_dispatcher PASSWORD :'dispatcher_password';
SQL
```

Sử dụng deployment secrets để cung cấp chính các mật khẩu này cho các dịch vụ runtime. Đối với phần lưu trữ mô hình, cần cấp phát thêm tài khoản đăng nhập bị giới hạn `dxos_ai_gateway` và mật khẩu của tài khoản này thông qua hoạt động quản trị triển khai trước khi áp dụng các migration mô hình; quá trình bootstrap trên volume mới sử dụng `08_runtime_passwords.sh`. Đặt `DATABASE_URL` trong môi trường được bảo vệ thành connection URL của tài khoản quản trị dành cho migration runner.

Đối với nâng cấp chỉ sử dụng FTS, lệnh `scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope cap-ai --through 0007` sẽ bỏ qua các migration tùy chọn 0004/0005. Các lựa chọn `--through 0004` và `--through 0005` được khai báo tường minh trước đó vẫn giữ nguyên ý nghĩa. Để cài đặt xử lý PERSONAL có kiểm toán, tìm kiếm kết hợp và quyền quản lý mô hình AGENT, hãy đặt cả ba cờ schema thành `true` trong môi trường migration: `RAG_MODELS_SCHEMA_ENABLED`, `RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED` và `RAG_AGENT_MODELS_SCHEMA_ENABLED`.

Sử dụng `--through 0010` để áp dụng các migration này hoặc `--through 0012` để bổ sung các hàm quản trị slot có kiểm toán, tài khoản đăng nhập `dxos_ai_model_admin` và các khóa quyền phê duyệt PERSONAL. Bootstrap runner sử dụng cùng các điều kiện tiên quyết. Cờ agent-model mặc định là `false` và cũng kiểm soát các migration phụ thuộc mô hình được bổ sung sau đó, nếu có. Không cờ nào trong số này kích hoạt profile, slot hoặc phê duyệt; migration 0008 không tạo sẵn phê duyệt và migration 0011 cũng không tạo sẵn sự kiện slot.

Đối tượng phê duyệt tối thiểu binding một revision provider/profile bất biến và mục đích sử dụng, một revision policy nguồn PERSONAL `LOCAL_ONLY` hiện có, cùng một phân loại truy vấn PERSONAL độc lập cho một ứng dụng ở trạng thái Ready. Đối tượng này ghi nhận bằng chứng đánh giá về thời gian lưu giữ, việc sử dụng dữ liệu cho mục đích huấn luyện và khu vực xử lý. Đây là một bản đánh giá xử lý, không phải thao tác viết lại policy phân tích cú pháp: quyền sở hữu nguồn và hoạt động phân tích `LOCAL_ONLY` vẫn không thay đổi. Mỗi nguồn được cung cấp đều phải có bản đánh giá tương ứng; một lời gọi EMBED chỉ phục vụ truy vấn cũng cần có bản đánh giá truy vấn phù hợp một cách độc lập. Giao diện SQL cố ý chỉ hỗ trợ PERSONAL/PERSONAL; các bản đánh giá APP tiếp tục tuân theo hợp đồng policy xử lý hiện có.

Cấp phát kết nối bằng deployment secrets thông qua các biến môi trường libpq tiêu chuẩn (`PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER=dxos_authz`, `PGPASSWORD` và `PGSSLMODE`). Không truyền mật khẩu hoặc connection URL trên dòng lệnh. Đặt `APPROVER_CORE_USER_ID` thành UUID Core của Platform Admin đã được xác thực. Khi các biến này đã được cung cấp, một thao tác đã qua đánh giá có dạng như sau:

```sh
psql -X -v ON_ERROR_STOP=1 -v approver="$APPROVER_CORE_USER_ID" <<'SQL'
SELECT cap_ai.approve_agent_processing(:'approver'::uuid, '{
  "profile_id":"reviewed-generator",
  "profile_revision":1,
  "provider_id":"reviewed-provider",
  "purpose":"GENERATE",
  "source_policy_id":"local-only",
  "source_policy_version":1,
  "source_classification":"PERSONAL",
  "query_app_id":"personal-library",
  "query_classification":"PERSONAL",
  "retention_evidence":"review-record/retention",
  "training_use_evidence":"review-record/training-use",
  "region_evidence":"review-record/processing-region"
}'::jsonb) AS approval_id;
SQL
```

Thay các danh tính và tham chiếu đánh giá ví dụ bằng profile bất biến, ứng dụng Ready và bằng chứng đã được đánh giá thực tế. EMBED cần có bản đánh giá riêng với `purpose: EMBED` và revision chính xác của embedding profile. UUID được trả về dùng để xác định bản đánh giá. Các định nghĩa và sự kiện kiểm toán đều bất biến. Nếu thử lại một bản đánh giá giống hệt trong khi bản đánh giá đó vẫn đang hoạt động, hệ thống sẽ trả về UUID hiện có, kể cả khi phản hồi trước đó bị thất lạc. Nếu phê duyệt lại sau khi đã thu hồi, hệ thống sẽ tạo một revision mới. Để thu hồi bản đánh giá, hãy sử dụng cùng kết nối được bảo vệ.

```sh
psql -X -v ON_ERROR_STOP=1 -v approver="$APPROVER_CORE_USER_ID" \
  -v approval="$PROCESSING_APPROVAL_ID" <<'SQL'
SELECT cap_ai.disable_agent_processing(
  :'approver'::uuid, :'approval'::uuid, 'Review withdrawn');
SQL
```

Thao tác vô hiệu hóa có tính idempotent và được kiểm toán một cách nguyên tử; revision đã bị vô hiệu hóa không thể được kích hoạt lại. Đặt `PROCESSING_APPROVAL_ID` thành UUID đánh giá được trả về trước khi chạy ví dụ vô hiệu hóa. Khi thu hồi toàn bộ quyền đối với một provider/profile, hãy vô hiệu hóa mọi bản đánh giá áp dụng, bao gồm cả các bản đánh giá EMBED/GENERATE được cấp riêng biệt. Không cần ghi trực tiếp vào bảng phê duyệt và kết nối này cũng không được cấp quyền thực hiện thao tác đó. Các bản ghi đánh giá chỉ chứa tham chiếu đến bằng chứng, không chứa thông tin xác thực, prompt hoặc văn bản nguồn. Gateway kiểm tra lại phê duyệt hiện hành sau khi xác nhận operation và ý định dispatch; worker cũng kiểm tra lại chính xác lease fence, source snapshot và quyền READ hiện tại của chủ sở hữu.

Migration `0009_personal_hybrid.sql` ghi nhận chủ sở hữu, quyền đối với nguồn và revision phê duyệt lưu trữ trên máy chủ chính xác trên từng job embedding và generation PERSONAL. Tìm kiếm kiểm tra quyền READ hiện tại của chủ sở hữu và phê duyệt nguồn trước khi xếp hạng vector chính xác. Một generation chỉ được xem là đủ điều kiện khi mọi chunk dự kiến đều có vector có thể sử dụng với số chiều đã được ghim. Nếu các collection được chọn không có generation READY đủ điều kiện, tìm kiếm sẽ trả về `LEXICAL_ONLY` cùng với `SEMANTIC_UNAVAILABLE` mà không gửi câu truy vấn đi.

Một phê duyệt mới sau khi thu hồi sẽ có revision mới. Worker đưa vào hàng đợi một bản dựng riêng cho source snapshot hiện được phê duyệt, kể cả khi nội dung đã phân tích và embedding profile không thay đổi. Mỗi bản dựng có danh tính operation bền vững riêng và các lần thử lại của bản dựng đó vẫn giữ nguyên danh tính. Tìm kiếm kết hợp loại trừ các generation gắn với snapshot đã bị thay thế và các generation PERSONAL trước 0009 không có snapshot được ghi nhận. FTS được cấp quyền vẫn khả dụng trong khi generation hiện tại đang được xây dựng.

Migration `0010_agent_model_authority.sql` kiểm tra ứng dụng hiện tại, role, snapshot READ và policy xử lý của từng nguồn trước khi tạo generation AGENT có căn cứ. Sử dụng hàm cấp phát có kiểm toán ở trên cho các bản đánh giá truy vấn toàn cục sau đây.

- Đối với EMBED chỉ phục vụ truy vấn trên máy chủ, hãy đánh giá chính xác revision của embedding provider và profile, đặt `purpose` thành `EMBED`, `query_app_id` thành ứng dụng đang được tìm kiếm và `query_classification` thành `PERSONAL`. Các phạm vi APP cũng cần có phê duyệt xử lý thông thường gắn với ứng dụng. Lặp lại bản đánh giá này cho từng ứng dụng đủ điều kiện mà chức năng embedding truy vấn toàn cục trên máy chủ được dự kiến sử dụng.

- Đối với AGENT GENERATE, hãy đánh giá chính xác revision của generation provider và profile, đặt `purpose` thành `GENERATE`, `query_app_id` thành `AI_AGENT_PROCESSING_APP_ID` và `query_classification` thành `PERSONAL`. Áp dụng bản đánh giá này ngay cả khi tất cả nguồn được chọn đều là APP. Mỗi nguồn PERSONAL vẫn cần có bản đánh giá policy nguồn tương ứng cho ứng dụng thư viện của nó. Mỗi nguồn APP vẫn cần có policy xử lý hiện có đã được phê duyệt.

Cả hai thao tác đều yêu cầu bản đánh giá hiện hành cho câu hỏi vì bất kỳ câu hỏi toàn cục nào cũng có thể chứa thông tin PERSONAL. Consumer xử lý cần có bản đánh giá truy vấn riêng, trong khi quyền READ và quyền xử lý nguồn được lấy từ từng nguồn thực tế.

## Cấu hình triển khai

API và worker tải cấu hình độc lập thông qua `models.FromEnvironment`. Theo mặc định, các chức năng này đều bị tắt. Để bật, tất cả các biến `RAG_MODELS_ENABLED`, `RAG_MODELS_QUALIFIED`, `RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED`, `RAG_PARSER_QUALIFIED` phải có giá trị `true`, đồng thời phải cấu hình `AI_GATEWAY_DATABASE_URL` và `RAG_MODEL_DEPLOYMENT_FILE`. Kết nối Gateway phải đăng nhập bằng tài khoản `dxos_ai_gateway`, không có quyền superuser hoặc `BYPASSRLS`. Tài khoản này thực thi các hàm policy và trạng thái operation nhưng không thể xuất các chunk, thay đổi quyền cấp hoặc chỉnh sửa policy. API và worker tiếp tục sử dụng các vai trò hiện có. Bootstrap chỉ chấp nhận `AI_GATEWAY_DATABASE_PASSWORD` khi có cấu hình schema tùy chọn. Lưu trữ các thông tin bí mật trong secrets của môi trường triển khai, tuyệt đối không đặt chúng trong manifest hoặc trình duyệt.

Tệp JSON triển khai riêng tư Plan05 hiện tại có các trường `providers`, `embed_profile` và `generate_profile`. Các tham chiếu profile chứa `id` và `revision`. Mỗi provider chứa `id`, `endpoint`, `credential_ref` (tham chiếu đến một biến môi trường) và `loopback_test`. Các profile và tham chiếu triển khai phải khớp với nhau. Bắt buộc sử dụng HTTPS; các yêu cầu chuyển hướng, thông tin xác thực trong URL và query string đều bị từ chối. Chỉ khi đặt rõ ràng `RAG_MODEL_TEST_MODE=true` mới cho phép sử dụng bộ điều hợp kiểm thử HTTP loopback. Điều này không đồng nghĩa với việc provider được phê duyệt để sử dụng trong môi trường production. Các profile `LOCAL` hiện có còn yêu cầu `RAG_LOCAL_MODELS_QUALIFIED=true`; không có mô hình cục bộ nào được đóng gói sẵn hoặc được xác nhận đủ điều kiện trước, và việc cung cấp dịch vụ cho mô hình cục bộ nằm ngoài phạm vi của harness mới. Chức năng tìm kiếm APP và PERSONAL chỉ dành cho chủ sở hữu sử dụng phương pháp kết hợp FTS và embedding trong giới hạn được xác định, cùng với các profile đã được xác nhận đủ điều kiện. Tìm kiếm hybrid PERSONAL yêu cầu migration 0009. Việc sử dụng embedding và generation được lưu trữ trên máy chủ cho PERSONAL đều phải có phê duyệt hiện hành, được kiểm toán riêng cho từng mục đích. Khi không có mô hình ngữ nghĩa hoặc mô hình chưa được xác nhận đủ điều kiện, hệ thống vẫn duy trì FTS đã được cấp quyền. Các operation mô hình chỉ dành cho PERSONAL luôn giữ nguyên ứng dụng thư viện đã đăng ký thực tế để phục vụ mục đích ghi nhận nguồn thực hiện, bất kể ngữ cảnh giao diện người dùng.

Bộ điều hợp HTTP nhận một chỉ thị hệ thống cố định, mô hình, mục đích sử dụng, dữ liệu đầu vào JSON có giới hạn và các giới hạn đã được cố định. Bộ điều hợp trả về các trường có giới hạn `text`, `vector`, `citation_ids`, `usage` và trường `status` tùy chọn; nếu xuất hiện trường không xác định, yêu cầu sẽ bị từ chối. Có thể sử dụng `X-Request-ID` tùy chọn để hỗ trợ việc ghi nhận operation. Đây là giao thức của bộ điều hợp triển khai, không phải tuyên bố về khả năng tương thích với API của một nhà cung cấp cụ thể. Trước khi sử dụng thực tế, cần xác nhận tokenizer, giới hạn ngữ cảnh, giới hạn đầu ra và thỏa thuận định giá với phía provider. Việc kiểm tra số token đầu vào sử dụng giới hạn byte mang tính thận trọng và một phần dung lượng cố định dành cho phần bao dữ liệu. Các chunk hoàn chỉnh sẽ bị loại bỏ thay vì cắt ngắn bảng để vừa với giới hạn. Hệ thống không cung cấp công cụ, thực thi SQL, truy xuất URL hoặc truyền dữ liệu theo luồng (streaming).

## Bộ điều hợp hosted được đóng gói sẵn (tùy chọn bật)

`compose.model-adapter.yaml` là một overlay tùy chọn dành cho bộ điều hợp được đóng gói sẵn: một tích hợp ranh giới Tier 2, có nhiệm vụ chuyển đổi yêu cầu `bounded-http-v1` hiện có thành một yêu cầu `POST` tương thích với OpenAI. Bộ điều hợp không sở hữu cơ sở dữ liệu, không nắm giữ danh tính ứng dụng và không áp dụng bất kỳ policy nào. Gateway vẫn là thành phần duy nhất có thẩm quyền điều phối yêu cầu, còn registry vẫn là thành phần duy nhất có thẩm quyền lựa chọn mô hình. Nếu không đưa overlay này vào, mọi bản cài đặt hiện có vẫn sử dụng đường dẫn provider qua endpoint bên ngoài và không khởi chạy bộ điều hợp nào; lệnh [`make rag-up`](../../operations/installation.md) không thay đổi và không bao giờ tự bật các lời gọi mô hình.

Overlay không công khai cổng trên máy chủ và không bổ sung route APISIX. `cap-ai` và `cap-ai-worker` truy cập bộ điều hợp tại `https://cap-ai-model-adapter:8443` thông qua mạng Compose riêng tư và xác thực bộ điều hợp bằng CA bundle có chứa cả các chứng chỉ gốc của hệ thống. Chỉ các chức năng `EMBED` và `GENERATE` thuộc loại `HOSTED` được phục vụ; `VLM`, `RERANKING`, streaming, tools và việc phục vụ mô hình cục bộ vẫn nằm ngoài phạm vi.

### Các tệp chủ sở hữu cần chuẩn bị

Chuẩn bị các tệp này bên ngoài Git, với `umask 077`. Không truyền bất kỳ giá trị nào dưới dạng đối số lệnh và không hiển thị cấu hình Compose sau khi phân giải ra terminal hoặc log: `docker compose config` sẽ in ra các giá trị môi trường.

| Tệp | Nội dung |
| --- | --- |
| `.env.providers` | Các khóa của nhà cung cấp dịch vụ bên ngoài, chỉ dành cho bộ điều hợp; mỗi khóa tương ứng với một `credential_ref` của route. Chỉ bộ điều hợp tải tệp này |
| `.env.models` | Chính xác nội dung `MODEL_ADAPTER_TOKEN=<random 32+ bytes>`, token nội bộ của Gateway |
| `model-adapter.json` | Khai báo route đã được xem xét và phê duyệt |
| `adapter-tls.crt`, `adapter-tls.key` | Chứng chỉ máy chủ và khóa riêng của bộ điều hợp |
| `adapter-ca-bundle.crt` | Các chứng chỉ gốc đáng tin cậy của hệ thống cùng với CA của bộ điều hợp |

```bash
umask 077
{ printf 'MODEL_ADAPTER_TOKEN='; openssl rand -hex 32; } > .env.models
read -rsp 'Embed provider API key: ' KEY; printf 'EMBED_PROVIDER_API_KEY=%s\n' "$KEY" > .env.providers; unset KEY
read -rsp 'Generate provider API key: ' KEY; printf 'GENERATE_PROVIDER_API_KEY=%s\n' "$KEY" >> .env.providers; unset KEY
```

Kiểm tra cặp tệp trước khi khởi chạy hệ thống. Bước kiểm tra trước khi khởi chạy (preflight) chỉ in tên biến và các nguyên nhân tĩnh, tuyệt đối không in giá trị:

```bash
python3 scripts/check_model_adapter_env.py \
  --provider-env .env.providers --internal-env .env.models
```

Công cụ sẽ từ chối tệp provider nếu tệp này khai báo `MODEL_ADAPTER_TOKEN` hoặc bất kỳ tên dành riêng nào (`MODEL_ADAPTER_*`, `AI_*`, `AI_MODEL_*`, `RAG_*`, `POSTGRES_*`, `OIDC_*`, `DATABASE_URL`, `PG*`, `SSL_CERT_FILE`), có tên biến bị lặp, tệp nội bộ không chứa chính xác một biến `MODEL_ADAPTER_TOKEN` có giá trị khác rỗng hoặc tệp cấp quyền truy cập cho group hay other. Bộ điều hợp tải tệp provider trước rồi mới tải tệp nội bộ, do đó token của chính môi trường triển khai sẽ được ưu tiên. Bước preflight có nhiệm vụ ngăn chặn xung đột này trước khi Compose âm thầm chấp nhận cấu hình.

### Khai báo route

Có hai route, mỗi route tương ứng với một mục đích và có danh tính provider riêng, không thể thay đổi. Các URL upstream, mã định danh mô hình và khóa bên dưới chỉ là giá trị giữ chỗ: hãy sử dụng endpoint chính xác đã được xem xét, mã định danh mô hình dạng literal và tham chiếu thông tin xác thực tương ứng. Các bí danh của router (`openrouter/auto`, `@preset/...`, `:nitro`) sẽ bị từ chối; riêng route `openrouter.ai` còn yêu cầu đối tượng `routing` đã được xem xét.

```json
{
  "schema_version": 1,
  "routes": [
    {
      "protocol_version": 1,
      "type": "openai-compatible",
      "base_url": "https://embed.example.com/v1",
      "credential_ref": "EMBED_PROVIDER_API_KEY",
      "models": [
        {
          "model_id": "example-embed-v1",
          "purpose": "EMBED",
          "dimension": 1536,
          "limits": {"max_input_tokens": 8191, "max_output_tokens": 1, "max_bytes": 65536, "timeout_ms": 30000}
        }
      ]
    },
    {
      "protocol_version": 1,
      "type": "openai-compatible",
      "base_url": "https://generate.example.com/v1",
      "credential_ref": "GENERATE_PROVIDER_API_KEY",
      "models": [
        {
          "model_id": "example-generate-v1",
          "purpose": "GENERATE",
          "limits": {"max_input_tokens": 64000, "max_output_tokens": 2048, "max_bytes": 262144, "timeout_ms": 60000}
        }
      ]
    }
  ]
}
```

Một route là cấu hình bất biến. Việc thay đổi base URL, tham chiếu thông tin xác thực, mô hình, mục đích sử dụng, dimension, bất kỳ giới hạn nào hoặc policy định tuyến đều làm thay đổi digest của route và do đó thay đổi đường dẫn được phục vụ. Vì vậy, những thay đổi này yêu cầu một catalog revision mới và một lần xem xét, phê duyệt mới. Việc xoay vòng giá trị bí mật phía sau một tham chiếu không thay đổi digest.

### Digest và đường dẫn trước khi tạo catalog

Xác định danh tính của từng route ngoại tuyến, trước khi catalog tồn tại:

```bash
bin/dxos-model validate --adapter-config model-adapter.json
```

```text
route digest=83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082 path=/v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call protocol_version=1 type=openai-compatible base_url=https://embed.example.com/v1 credential_ref=EMBED_PROVIDER_API_KEY
model example-embed-v1 purpose=EMBED dimension=1536 max_input_tokens=8191 max_output_tokens=1 max_bytes=65536 timeout_ms=30000
route digest=7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038 path=/v1/routes/7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038/call protocol_version=1 type=openai-compatible base_url=https://generate.example.com/v1 credential_ref=GENERATE_PROVIDER_API_KEY
model example-generate-v1 purpose=GENERATE max_input_tokens=64000 max_output_tokens=2048 max_bytes=262144 timeout_ms=60000
```

Các digest là thành phần của đường dẫn, không phải quyết định định tuyến: bộ điều hợp chỉ phục vụ từng route tại đường dẫn `/v1/routes/<digest>/call` tương ứng với chính route đó. Xem [Tài liệu tham chiếu CLI](../../reference/cli/dxos-model.md#route-only-validation).

### Khai báo catalog

Sử dụng đường dẫn đã xác định trong endpoint của từng provider. Hai provider ID giúp hai route hoạt động độc lập, còn các revision của profile chứa chính xác dimension và giới hạn lấy từ route:

```json
{
  "schema_version": 2,
  "providers": [
    {
      "id": "example-embed",
      "adapter": "bounded-http-v1",
      "endpoint": "https://cap-ai-model-adapter:8443/v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call",
      "credential_ref": "MODEL_ADAPTER_TOKEN"
    },
    {
      "id": "example-generate",
      "adapter": "bounded-http-v1",
      "endpoint": "https://cap-ai-model-adapter:8443/v1/routes/7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038/call",
      "credential_ref": "MODEL_ADAPTER_TOKEN"
    }
  ],
  "models": [
    {
      "adapter": "bounded-http-v1",
      "profile": {
        "id": "embed-primary",
        "revision": 1,
        "purpose": "EMBED",
        "execution": "HOSTED",
        "provider_id": "example-embed",
        "model_id": "example-embed-v1",
        "credential_ref": "MODEL_ADAPTER_TOKEN",
        "dimension": 1536,
        "limits": {"max_input_tokens": 8191, "max_output_tokens": 1, "max_bytes": 65536, "timeout_ms": 30000},
        "enabled": false
      }
    },
    {
      "adapter": "bounded-http-v1",
      "profile": {
        "id": "generate-primary",
        "revision": 1,
        "purpose": "GENERATE",
        "execution": "HOSTED",
        "provider_id": "example-generate",
        "model_id": "example-generate-v1",
        "credential_ref": "MODEL_ADAPTER_TOKEN",
        "limits": {"max_input_tokens": 64000, "max_output_tokens": 2048, "max_bytes": 262144, "timeout_ms": 60000},
        "enabled": false
      }
    }
  ]
}
```

`credential_ref` của provider là token nội bộ của Gateway dùng để truy cập bộ điều hợp; còn `credential_ref` của route chỉ định tên khóa của nhà cung cấp dịch vụ bên ngoài bên trong bộ điều hợp. Đối chiếu catalog với các route ở chế độ ngoại tuyến:

```bash
bin/dxos-model validate --catalog model-catalog.json --adapter-config model-adapter.json
```

```text
embed-primary@1 example-embed EMBED fb3a4c787dc2a7cce37bfbcdb7dea872a01ac9961f08a582f3d999c2f42a83aa /v1/routes/83ad328c6b4af2ecda6416cdf6f488dd062018548079fbb9684021917ae92082/call
generate-primary@1 example-generate GENERATE ac8d1b0c1de5dd293603c9a15d83e3f63900c581e78c8753c8abc5bb9e42de7c /v1/routes/7e26c5c3ab5c23ca79e0ecf4af17e7c6b3e0fc55fe44a17d10dd24cefa924038/call
```

Cột thứ ba là digest của khai báo bất biến mà quá trình xem xét, phê duyệt phải ghi nhận. Một candidate không được route cho phép chính xác (mô hình, mục đích sử dụng, dimension và các giới hạn không vượt quá ngưỡng cho phép) sẽ bị từ chối ngay tại bước này, trước khi có bất kỳ cơ sở dữ liệu nào được tạo.

### Xác thực TLS

Bộ điều hợp chỉ cung cấp TLS tại `cap-ai-model-adapter:8443`; SAN của chứng chỉ phải chứa tên này. Tạo một CA riêng cho bản cài đặt và không đưa CA đó vào danh sách tin cậy của chính bộ điều hợp: bộ điều hợp xác minh các endpoint của nhà cung cấp bên ngoài bằng các chứng chỉ gốc của hệ thống.

```bash
umask 077
openssl ecparam -name prime256v1 -genkey -noout -out adapter-ca.key
openssl req -x509 -new -key adapter-ca.key -sha256 -days 3650 \
  -subj '/CN=OpenDX-OS bundled model adapter CA' -out adapter-ca.crt
openssl ecparam -name prime256v1 -genkey -noout -out adapter-tls.key
openssl req -new -key adapter-tls.key -subj '/CN=cap-ai-model-adapter' -out adapter-tls.csr
printf 'subjectAltName=DNS:cap-ai-model-adapter\nextendedKeyUsage=serverAuth\n' > adapter-tls.ext
openssl x509 -req -in adapter-tls.csr -CA adapter-ca.crt -CAkey adapter-ca.key \
  -CAcreateserial -days 825 -sha256 -extfile adapter-tls.ext -out adapter-tls.crt
cat /etc/ssl/certs/ca-certificates.crt adapter-ca.crt > adapter-ca-bundle.crt
chown 10001:10001 adapter-tls.key adapter-tls.crt model-adapter.json adapter-ca-bundle.crt
chmod 0400 adapter-tls.key adapter-tls.crt model-adapter.json adapter-ca-bundle.crt
```

`capabilities/Dockerfile` chạy dịch vụ với UID 10001 và Compose gắn kết file secret bằng quyền sở hữu cùng chế độ truy cập của tệp trên máy chủ. Vì vậy, khóa, chứng chỉ và cấu hình phải thuộc quyền sở hữu của UID này và chỉ UID đó mới được phép đọc. CA bundle không phải thông tin bí mật, nhưng `cap-ai` và `cap-ai-worker` cũng chạy với UID 10001 nên phải có quyền đọc tệp này: nếu CA bundle chỉ cấp quyền `0600` cho operator, cả hai dịch vụ sẽ không thể xác minh bộ điều hợp. Tuyệt đối không dùng `chmod` để đặt quyền khóa riêng thành 644 và không thêm khóa này vào Git. Bundle được gắn kết ở chế độ chỉ đọc vào `cap-ai` và `cap-ai-worker` dưới dạng `SSL_CERT_FILE`; bundle phải giữ lại các chứng chỉ gốc của hệ thống (`cat /etc/ssl/certs/ca-certificates.crt ...`), vì nếu loại bỏ chúng, mọi kết nối TLS khác mà các dịch vụ này thực hiện đều sẽ bị lỗi. Cấu hình bundle rỗng hoặc chỉ chứa chứng chỉ của bộ điều hợp không được hỗ trợ: việc xác minh chứng chỉ không bao giờ bị vô hiệu hóa.

Xoay vòng chứng chỉ: thay thế `adapter-tls.crt`/`adapter-tls.key` (hoặc giá trị khóa của nhà cung cấp trong `.env.providers`), sau đó chỉ tạo lại các container bị ảnh hưởng bằng cùng các tệp Compose. Giữ nguyên `credential_ref` nếu chỉ xoay vòng giá trị bí mật. Xem xét và phê duyệt lại route nếu điều khoản xử lý dữ liệu, phạm vi tài khoản hoặc endpoint thay đổi.

### Lệnh gọi Compose

Thiết lập năm đường dẫn opt-in trong tệp env được bảo vệ (hoặc một tệp `--env-file` thứ hai), sau đó khởi chạy cùng một project với overlay được bổ sung:

```bash
docker compose --env-file .env --env-file .env.rag \
  -f compose.yaml -f compose.rag.yaml -f compose.models.yaml \
  -f compose.model-adapter.yaml up -d
```

Các biến `RAG_MODEL_ADAPTER_CONFIG_FILE`, `RAG_MODEL_ADAPTER_CREDENTIAL_ENV_FILE`, `RAG_MODEL_ADAPTER_TLS_CERT_FILE`, `RAG_MODEL_ADAPTER_TLS_KEY_FILE` và `RAG_MODEL_ADAPTER_CA_FILE` là bắt buộc đối với overlay. Nếu thiếu một biến, Compose sẽ dừng và hiển thị thông báo tĩnh chỉ rõ tên biến bị thiếu. Xác thực cùng danh sách tệp bằng `docker compose ... config --quiet`, lệnh này không in ra nội dung nào; tuyệt đối không kiểm tra cấu hình đã được kết xuất vì quá trình đó sẽ phân giải các giá trị bí mật.

### Tài khoản đăng nhập cơ sở dữ liệu và migration

Áp dụng `0012_personal_admin_authority_locks.sql` trước khi sử dụng CLI để thay đổi phê duyệt PERSONAL. Migration này giữ các hàng danh tính operator và membership trong suốt mỗi lần thay đổi, qua đó tuần tự hóa các thao tác thu hồi vai trò đồng thời. Migration sử dụng owner hiện có không có quyền đăng nhập và các quyền khóa hàng được giới hạn, được giới thiệu trong 0011.

Migration `0011_model_administration.sql` chỉ bổ sung các thành phần: những hàm slot có phạm vi hẹp và được kiểm toán, cùng tài khoản đăng nhập `dxos_ai_model_admin`; không có mật khẩu và không có profile, slot hoặc phê duyệt nào được tạo sẵn. Migration này tuân theo cơ chế opt-in của agent-schema hiện có, do đó một runner dừng tại `0010` vẫn tiếp tục dừng ở đó và không có mặc định nào thay đổi.

Cấp phát hai tài khoản đăng nhập triển khai mới bằng quyền database owner; đọc mật khẩu từ môi trường được bảo vệ thay vì ghi trực tiếp vào câu lệnh:

```sh
psql -X -v ON_ERROR_STOP=1 <<'SQL'
DO $$
DECLARE name text;
BEGIN
  FOREACH name IN ARRAY ARRAY['dxos_ai_model_catalog','dxos_ai_model_admin'] LOOP
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = name) THEN
      EXECUTE format('CREATE ROLE %I LOGIN', name);
    END IF;
    EXECUTE format('ALTER ROLE %I LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE NOREPLICATION NOINHERIT', name);
  END LOOP;
END $$;
\getenv catalog_password MODEL_CATALOG_DATABASE_PASSWORD
\getenv admin_password MODEL_ADMIN_DATABASE_PASSWORD
ALTER ROLE dxos_ai_model_catalog PASSWORD :'catalog_password';
ALTER ROLE dxos_ai_model_admin PASSWORD :'admin_password';
SQL
```

`dxos_ai_model_catalog` chỉ thực thi chức năng nhập candidate; tài khoản này không thể kích hoạt, thay đổi slot hoặc đọc dữ liệu tri thức được bảo vệ. `dxos_ai_model_admin` chỉ thực thi các hàm quản trị và xác thực chính xác session cùng vai trò `role_operator` hiện tại của Core. Các lệnh phê duyệt sử dụng tài khoản đăng nhập `dxos_authz` được bảo vệ hiện có cùng mật khẩu của tài khoản đó; không có lệnh CLI nào chạy bằng tài khoản owner.

Áp dụng schema với cả ba cờ schema, sau đó xác minh ledger:

```bash
RAG_MODELS_SCHEMA_ENABLED=true RAG_GLOBAL_MODEL_REGISTRY_SCHEMA_ENABLED=true \
RAG_AGENT_MODELS_SCHEMA_ENABLED=true \
  scripts/migrate-rag.sh --database-url-env DATABASE_URL --scope cap-ai --through 0012
```

Một bản cài đặt được giữ lại nhưng có checksum `cap_ai` xung đột với migration trước đó phải dừng: không chấp nhận chỉnh sửa ledger, bỏ qua checksum hoặc sử dụng lệnh reset. Thực hiện theo quy trình xây dựng lại riêng biệt đã được owner phê duyệt và chỉ định trong [tài liệu cài đặt](../../operations/installation.md#rebuilding-an-existing-model-installation); quy trình này tạo lại một cơ sở dữ liệu riêng và xác định lại trạng thái mô hình.

### Nhập, xác minh, xem xét và kích hoạt

Xuất từng DSN một từ môi trường được bảo vệ; CLI chỉ đọc đúng tài khoản đăng nhập mà lệnh đang cần và không bao giờ hiển thị giá trị:

```bash
export AI_MODEL_CATALOG_DATABASE_URL   # tài khoản đăng nhập catalog có quyền hạn chế
bin/dxos-model import --catalog model-catalog.json
bin/dxos-model validate --catalog model-catalog.json --adapter-config model-adapter.json
unset AI_MODEL_CATALOG_DATABASE_URL
```

Lệnh `import` chỉ khai báo các candidate đang ở trạng thái bị vô hiệu hóa. Sau đó, thực hiện một lời gọi tổng hợp (synthetic call) rõ ràng cho từng candidate đã được xem xét. Lệnh `verify` gửi dữ liệu đầu vào tổng hợp cố định đến route của bộ điều hợp được catalog chỉ định; lệnh này đọc `credential_ref` của candidate (token nội bộ của bộ điều hợp) từ môi trường được bảo vệ, không đọc khóa của nhà cung cấp bên ngoài:

```bash
bin/dxos-model verify --catalog model-catalog.json --profile embed-primary --revision 1 --json > verify-embed.json
bin/dxos-model verify --catalog model-catalog.json --profile generate-primary --revision 1 --json > verify-generate.json
```

Báo cáo JSON chứa danh tính profile, digest của khai báo, dimension và thông tin sử dụng tài nguyên trong giới hạn cho phép; không bao gồm nội dung phản hồi, endpoint hoặc khóa. Nếu xảy ra lỗi truyền tải sau khi yêu cầu có khả năng đã được gửi đi, lệnh sẽ thoát với mã 6 và tuyệt đối không gửi lại yêu cầu. Cần đối soát trạng thái trước khi thực hiện một lần thử khác đã được xem xét và phê duyệt.

Tạo bản ghi xem xét liên kết lần kiểm tra với một xác nhận độc lập của con người. Đối tượng `qualification` là báo cáo verify được giữ nguyên, còn `attestation_ref` là bản ghi xem xét, không phải bản sao của kết quả kiểm tra:

```json
{
  "declaration_digest": "fb3a4c787dc2a7cce37bfbcdb7dea872a01ac9961f08a582f3d999c2f42a83aa",
  "qualification": {"profile_id": "embed-primary", "revision": 1, "declaration_digest": "fb3a4c787dc2a7cce37bfbcdb7dea872a01ac9961f08a582f3d999c2f42a83aa", "purpose": "EMBED", "passed": true, "dimension": 1536, "citation_ids": [], "checked_at": "2026-11-24T10:00:00Z"},
  "attestation_ref": "review/2026-11-24/model-board/embed-primary"
}
```

Xác thực với tư cách Platform Admin mà không đưa token vào dòng lệnh, sau đó kích hoạt chính xác revision cần thiết. Kiểm tra `slots` trước: một bản cài đặt mới sẽ báo cáo `EMBEDDING slot_revision=0` và phải chỉ định rõ revision dự kiến, kể cả khi giá trị đó là 0.

```bash
export AI_MODEL_ADMIN_DATABASE_URL
bin/dxos-model slots --token-file operator.jwt
bin/dxos-model activate --slot EMBEDDING --profile embed-primary --revision 1 \
  --expected-slot-revision 0 --review embed-review.json --token-file operator.jwt
bin/dxos-model slots --token-file operator.jwt
unset AI_MODEL_ADMIN_DATABASE_URL
```

Thao tác thay đổi sẽ xác minh subject của token và vai trò `role_operator` hiện tại trong cùng một transaction, khóa slot, so sánh revision dự kiến, bật profile đã được phê duyệt, tăng revision của slot đúng một lần và ghi thêm sự kiện kiểm toán kèm bản xem xét. Nếu revision đã cũ, lệnh sẽ thoát với mã 4 và không thay đổi bất cứ điều gì. `LLM` liên kết với `generate-primary` theo cùng cách.

- Rollback là một thao tác được thực hiện rõ ràng với bản xem xét mới, không phải giảm revision: `bin/dxos-model activate --slot EMBEDDING --profile <older> --revision <n> --expected-slot-revision <current> --review <fresh-review> --rollback --token-file operator.jwt`. Revision của slot vẫn tăng.
- Deactivate hủy liên kết mà không xóa profile hoặc bất kỳ vector nào: `bin/dxos-model deactivate --slot EMBEDDING --expected-slot-revision <current> --reason 'review withdrawn' --token-file operator.jwt`.
- Các lệnh chỉ đọc (`list`, `inspect`, `slots`) sử dụng cùng tài khoản đăng nhập quản trị và xác thực operator; chúng không thực hiện thay đổi.

### Phê duyệt xử lý PERSONAL

Việc kích hoạt slot không đồng nghĩa với việc phê duyệt truyền dữ liệu cá nhân ra bên ngoài. Ghi nhận phê duyệt PERSONAL riêng biệt, có kiểm toán cho từng mục đích trên session `dxos_authz` được bảo vệ:

```json
{
  "profile_id": "embed-primary",
  "profile_revision": 1,
  "provider_id": "example-embed",
  "purpose": "EMBED",
  "source_policy_id": "local-only",
  "source_policy_version": 1,
  "source_classification": "PERSONAL",
  "query_app_id": "personal_library",
  "query_classification": "PERSONAL",
  "retention_evidence": "review/2026-11-24/retention",
  "training_use_evidence": "review/2026-11-24/training-use",
  "region_evidence": "review/2026-11-24/region"
}
```

```bash
export AI_MODEL_APPROVAL_DATABASE_URL   # tài khoản đăng nhập dxos_authz được bảo vệ
bin/dxos-model policy approve --file approval-embed.json --token-file operator.jwt
unset AI_MODEL_APPROVAL_DATABASE_URL
```

`query_app_id` phải là ứng dụng đã đăng ký thực sự và hiện đang ở trạng thái `Ready` đối với truy vấn; hãy xác nhận mã định danh ứng dụng đã triển khai thay vì mặc định cho rằng tên thư mục chính là mã định danh đó. Đối với các truy vấn AGENT, hãy sử dụng giá trị được cấu hình trong `AI_AGENT_PROCESSING_APP_ID`. `GENERATE` cần có bản xem xét riêng, với provider, revision profile và `purpose` riêng. Thu hồi một phê duyệt bằng lệnh `bin/dxos-model policy revoke --approval <uuid> --reason '<reason>' --token-file operator.jwt`; revision đã bị thu hồi không thể được kích hoạt lại. Việc xử lý APP `NON_PERSONAL` tiếp tục tuân theo quy trình được xem xét riêng ở trên và không phải là phê duyệt PERSONAL.

### Bật, xoay vòng và ngừng sử dụng route

Chỉ sau khi hoàn tất xác nhận đủ điều kiện, nhập catalog, kích hoạt và các phê duyệt bắt buộc, mới thiết lập `RAG_MODELS_QUALIFIED`, `RAG_MODELS_ENABLED` và `RAG_GLOBAL_MODEL_REGISTRY_ENABLED` cho `cap-ai` và worker, sau đó tạo lại các dịch vụ này. Nếu thiếu bất kỳ biến nào trong số đó, chức năng suy luận vẫn bị tắt; FTS đã được cấp quyền vẫn khả dụng.

Khi một route thay đổi, hãy thêm route mới và provider ID mới (mỗi provider ID chỉ có một endpoint), nhập revision mới đang bị vô hiệu hóa và thực hiện xem xét, phê duyệt. Sau đó, giữ cả route và ID cũ lẫn mới cho đến khi mọi embedding job và operation đã được ghim (pinned) và áp dụng fencing đều hoàn tất hoặc thất bại theo cơ chế fail-closed. Chủ động ngừng cấu hình cũ: xóa route tương ứng, nhập lại catalog, deactivate slot và xác nhận digest cũ không còn phân giải được. Tuyệt đối không viết lại khai báo provider cũ hoặc diễn giải lại các vector hiện có bằng một dimension mới.

Các tệp môi trường không được mã hóa và không phải là ranh giới bảo mật trước một operator có đặc quyền trên máy chủ hoặc container; chúng chỉ giúp giữ thông tin bí mật ngoài Git, image, đầu ra Compose đã phân giải và lịch sử shell.

## Khả năng tương thích với Global Registry

[Hợp đồng registry R1](../../reference/rag/api.md#global-model-registry-foundation-r1) xác định một giá trị mặc định riêng biệt là `RAG_GLOBAL_MODEL_REGISTRY_ENABLED=false`. Đây là một loader tùy chọn bật; khi ở trạng thái OFF, hệ thống sử dụng các tham chiếu profile cũ đã mô tả ở trên. Việc chuyển sang ON yêu cầu các migration registry bổ sung; cờ này không xác nhận provider đủ điều kiện và cũng không bỏ qua các điều kiện bật hiện có.

Khi registry ở trạng thái ON, các slot `ACTIVE` trong DB cung cấp chính xác các revision profile EMBEDDING/LLM. Cấu hình triển khai chỉ khai báo candidate cùng các tham chiếu bộ điều hợp/thông tin bí mật; các trường `embed_profile`/`generate_profile` cũ không thể ghi đè slot hoặc được sử dụng làm phương án dự phòng. Ứng dụng và người dùng không thể tự lựa chọn mô hình. Khi mô hình chưa được liên kết hoặc không khả dụng, hệ thống vẫn duy trì FTS đã được cấp quyền và các câu trả lời tùy chọn chỉ phục vụ tìm kiếm; nếu yêu cầu generation bắt buộc có bằng chứng, hệ thống trả về 503 `MODEL_UNAVAILABLE`. VLM được dành riêng nhưng chưa được liên kết, còn RERANKING vẫn bị vô hiệu hóa. Tuân theo hợp đồng chuẩn để biết quy trình cấp phát danh tính, vòng đời Platform Admin trong tương lai và tác động đến các thành phần sử dụng.

Đối với cuộc trò chuyện mới, việc lựa chọn nguồn của `AGENT` được thực hiện phía máy chủ trên tất cả membership vai trò hiện tại trong DB và các nguồn PERSONAL chỉ dành cho chủ sở hữu. Mỗi lượt hội thoại chỉ chấp nhận một câu hỏi. Các role claim, danh sách từ phía client và đề xuất ứng dụng từ LLM không cấp bất kỳ quyền hạn nào. Các route APP Hasura/capability hiện có tiếp tục giữ hành vi lựa chọn vai trò theo ADR 0007.

## Các operation bền vững và ghi nhận token

Nền tảng không áp đặt bất kỳ mức trần chi phí nào. Không có tài khoản ngân sách, khoản đặt trước, phân bổ, sổ ghi nhận mức sử dụng hoặc profile có định giá; cũng không có thành phần nào cần cấp phát trước khi gọi mô hình. Giới hạn chi tiêu thuộc về thỏa thuận với provider; các cơ chế kiểm soát của chính nền tảng là những giới hạn tài nguyên (`max_input_tokens`, `max_output_tokens`, `max_bytes`, `timeout_ms`), trạng thái bật của profile, thẩm quyền đối với slot, phê duyệt xử lý dữ liệu và công tắc ngắt khẩn cấp (kill switch).

Mỗi model operation duy trì chính xác một bản ghi `cap_ai.model_operations` bền vững cho operation key tương ứng. Chỉ model gateway, thông qua các hàm `cap_ai.claim_model_operation`, `cap_ai.begin_model_operation` và `cap_ai.finish_model_operation`, mới được phép tạo hoặc chuyển trạng thái bản ghi này. Bản ghi liên kết actor, ứng dụng được ghi nhận nguồn thực hiện, profile ID/revision bất biến, mục đích sử dụng và fingerprint của snapshot thẩm quyền cùng payload; không lưu phản hồi của mô hình hoặc văn bản nguồn. Quyền truy cập trực tiếp vào bảng và dispatch token bị thu hồi đối với các vai trò API, worker và catalog. Các operation key liên kết actor, profile, fingerprint nguồn/policy và hash của payload, nhờ đó một ý định gọi đồng thời hoặc được phát lại không thể gửi cùng một operation hai lần.

Đối với các model operation AGENT xuyên ứng dụng, hãy cấu hình `AI_AGENT_PROCESSING_APP_ID` thành một ứng dụng đã đăng ký thực sự và hiện ở trạng thái `Ready`; tên `AI_AGENT_BILLING_APP_ID` đã ngừng sử dụng sẽ không được đọc. Cấu hình bị thiếu hoặc không hợp lệ sẽ khiến lời gọi mô hình bị từ chối. Ứng dụng này xác định phạm vi ghi nhận nguồn thực hiện và phạm vi xem xét truy vấn: ứng dụng cần có bản xem xét truy vấn PERSONAL độc lập như đã mô tả ở trên, nhưng không cung cấp ngân sách và cũng không cấp quyền truy cập nguồn. Mỗi ứng dụng nguồn thực tế và mỗi nguồn PERSONAL đều phải vượt qua các bước kiểm tra READ và xử lý riêng. Xem [quy tắc ghi nhận nguồn thực hiện và trạng thái bền vững chuẩn](../../reference/rag/authorization.md#model-operation-attribution-and-durable-state). RAG overlay cung cấp consumer đã cấu hình cho cap-ai. Route APISIX công khai duy trì hành vi lựa chọn vai trò đối với các knowledge API hiện có.

Kiểm tra lại thẩm quyền sau khi claim, sau khi lưu ý định thực hiện và ngay trước khi gửi yêu cầu. Trạng thái có khả năng đã được gửi (`MAY_HAVE_SENT` hoặc `UNCERTAIN`) tuyệt đối không được gửi lại bởi cùng một lần thử hoặc bất kỳ lần thử nào sau đó. Một EMBED job không chắc chắn sẽ trở thành ingestion job ở trạng thái `FAILED` với `error_code` là `MODEL_CALL_UNCERTAIN`. Mã này được công khai, không phải giá trị riêng tư trong cơ sở dữ liệu: một caller hiện có quyền READ đối với tài liệu của job sẽ thấy mã này từ `GET /api/cap/ai/ingestions/{id}` (`{"status":"FAILED","error_code":"MODEL_CALL_UNCERTAIN"}`) vì route tuần tự hóa nguyên trạng `cap_ai.ingestion_metadata`. Những caller khác vẫn nhận được phản hồi ẩn `404 NOT_FOUND`, không có trạng thái và không có mã lỗi. Trạng thái job `FAILED` khác với trạng thái operation bền vững `UNCERTAIN`: job cho biết quá trình xây dựng embedding chưa hoàn tất, còn operation cho biết lời gọi có thể đã được gửi và tuyệt đối không được gửi lại. Danh tính operation của từng embedding chunk được giữ ổn định qua các lease fence, vì vậy sự cố sau khi gửi không thể tạo ra một lời gọi thứ hai tự động. Những lần thử mới được phê duyệt rõ ràng khác phải có operation identity riêng.

Các bộ đếm token chỉ phục vụ mục đích quan sát. Chúng chỉ được ghi nhận cùng với request ID tùy chọn từ provider và độ trễ khi operation thành công; các bản ghi `UNCERTAIN` và `FAILED_UNSENT` không có bộ đếm. Trong phiên bản nền tảng này không có reconciler, công cụ kiểm tra hóa đơn hoặc adapter quyết toán, cũng không có endpoint quản trị mức sử dụng công khai. Một bản ghi operation không cấp quyền READ đối với nội dung.

Một operation bị mắc kẹt cần được owner xem xét và xử lý, không phải tự động chuyển trạng thái. Index `model_operations_stuck` trên `(state, intent_at)` cung cấp các bản ghi không rõ trạng thái lâu nhất theo từng ứng dụng được ghi nhận nguồn thực hiện. Các vai trò gateway, API và worker không thể đọc bảng này, vì vậy owner phải chạy truy vấn bằng thông tin xác thực owner:

```sql
SELECT app_id,
       state,
       count(*) AS operations,
       min(intent_at) AS oldest_intent
FROM cap_ai.model_operations
WHERE state IN ('MAY_HAVE_SENT', 'UNCERTAIN')
GROUP BY app_id, state
ORDER BY oldest_intent;
```

`intent_at` chỉ có giá trị null đối với bản ghi `CLAIMED`, do đó mọi bản ghi được liệt kê đều đã commit intent. Owner xử lý các bản ghi cũ nhất dựa trên thỏa thuận với provider; nền tảng không gửi lại yêu cầu và cũng không thực hiện quyết toán chúng.

## API trả lời

`POST /api/cap/ai/answers` chấp nhận `{scope,query,max_citations?,require_generation?}`. Phản hồi: `{status,answer,results,citations,reason?,model_profile?,usage?}`. `answer` có giá trị null đối với `SEARCH_ONLY`/`NO_EVIDENCE`. Trạng thái có thể là `ANSWERED`, `SEARCH_ONLY` hoặc `NO_EVIDENCE`. Tối đa tám evidence chunk hoàn chỉnh được đưa vào generation. Mọi nguồn ngữ cảnh được cung cấp đều được kiểm tra sau khi mô hình trả về, kể cả các ngữ cảnh không được trích dẫn. Nếu bất kỳ nguồn nào trở nên không khả dụng, phản hồi sẽ không chứa văn bản cũ, tiêu đề, kết quả hoặc trích dẫn nào và có reason `AUTHORIZATION_CHANGED`. Các citation ID giả mạo khiến hệ thống từ chối trả lời với `UNSUPPORTED_ANSWER`. Việc xác thực ID chỉ xác lập nguồn gốc, không xác minh tính đúng đắn về mặt ngữ nghĩa.

Khi policy chặn hoặc xảy ra sự cố dịch vụ, hệ thống trả về kết quả chỉ phục vụ tìm kiếm trong phạm vi được cấp quyền, trừ khi `require_generation=true`. Trong trường hợp đó, hệ thống trả về 403 `POLICY_BLOCKED` hoặc 503 `MODEL_UNAVAILABLE`. Một lời gọi mô hình có khả năng đã được gửi sẽ trả về `SEARCH_ONLY` với reason `MODEL_CALL_UNCERTAIN` (HTTP 200, answer null, không có citation và không có usage), hoặc 503 `MODEL_CALL_UNCERTAIN` nếu `require_generation=true`. Không có phản hồi liên quan đến hạn mức sử dụng (quota). Khi không có bằng chứng, hệ thống không thực hiện lời gọi generation. Giao diện ứng dụng hiển thị câu trả lời có căn cứ với các trích dẫn được bảo vệ, trạng thái từ chối trả lời rõ ràng hoặc kết quả chỉ phục vụ tìm kiếm, đồng thời hủy ngữ cảnh cũ khi ứng dụng/vai trò thay đổi. Việc thiếu phê duyệt PERSONAL không bao giờ kích hoạt cơ chế dự phòng chuyển sang hosted. Lịch sử trò chuyện mới chỉ dành cho chủ sở hữu: các snapshot cũ vẫn có thể đọc được sau khi nguồn bị thu hồi theo quy trình thông thường, trong khi các bản gốc được liên kết/trích dẫn yêu cầu quyền READ hiện tại đối với nguồn. Phải xác thực lại quyền đối với lịch sử được tạo từ nguồn, bao gồm cả bản tóm tắt, trước khi thực hiện generation mới; lịch sử còn đọc được không đồng nghĩa với việc có thể tái sử dụng làm bằng chứng.

## Xác nhận đủ điều kiện

Chạy các bài kiểm thử unit của capability, kiểm thử tích hợp Go với PostgreSQL/pgvector thực tế và cờ `-p 1` (các fixture dùng chung DDL role ở phạm vi toàn cluster), các bài kiểm thử race tập trung, kiểm thử/build UI và `scripts/test-rag.sh models`. Runner models yêu cầu tất cả đầu vào của source-service cùng hai bộ điều hợp giả lập loopback được khai báo rõ ràng, test-only profile ID, Operator, dispatcher và các đầu vào Chromium/storage-state được liệt kê trong shell preflight. `scripts/integration/rag_model_fake.py` chỉ là một test spy dùng tạm; tuyệt đối không công khai các chức năng điều khiển của nó. Runner chỉ cấp phát policy fixture đã được xem xét cho test app mới và `rag-test-fake`, không bao giờ cấp cho một processor thực tế.

R11/R12/R17/R18/R19/R20 bao quát các vector có phạm vi, thẩm quyền cuối cùng mới nhất, trạng thái sẵn sàng độc lập, hạn chế không truyền dữ liệu PERSONAL hiện tại, các operation không chắc chắn có trạng thái bền vững với số lần gọi được giới hạn và chức năng trả lời. Những trường hợp kiểm thử hiện có này không chứng minh policy phê duyệt đã được sửa đổi.

Bộ kiểm thử tích hợp Go riêng biệt kiểm tra EMBED/GENERATE PERSONAL đã được phê duyệt, việc từ chối truyền dữ liệu nguồn/truy vấn chưa được phê duyệt, phê duyệt bị vô hiệu hóa hoặc không khớp và việc xem xét retention/training-use, thẩm quyền nhiều vai trò theo từng nguồn, snapshot owner-only được giữ lại nhưng không tái sử dụng ngữ cảnh đã bị thu hồi, cùng việc ghi nhận AGENT operation theo ứng dụng xử lý đã cấu hình. Các điều kiện kiểm tra nguồn/package R01–R16 trước đó vẫn là điều kiện tiên quyết. Một trường hợp kiểm thử gold giả lập có tính xác định đo lường khả năng truyền tải, luồng xử lý citation/provenance, ghi nhận token và độ trễ; trường hợp này không xác nhận tính chính xác của câu trả lời có căn cứ từ mô hình ngôn ngữ thực tế, khả năng chống injection, giới hạn tokenizer hoặc hóa đơn của provider. Bộ điều hợp LOCAL giả lập chỉ kiểm tra ranh giới điều phối dùng chung; không có bộ điều hợp phục vụ LOCAL nào được phát hành. Nếu thiếu phê duyệt thực tế từ provider/phân loại dữ liệu, việc bật hosted trong môi trường production vẫn bị chặn. Không thể đưa ra bất kỳ tuyên bố phát hành tổng thể nào theo S01–S08.

Chế độ registry đọc một catalog nghiêm ngặt từ `RAG_MODEL_DEPLOYMENT_FILE`:

```json
{
  "schema_version": 2,
  "providers": [
    {
      "id": "hosted",
      "adapter": "bounded-http-v1",
      "endpoint": "https://provider.example",
      "credential_ref": "MODEL_API_KEY"
    }
  ],
  "models": []
}
```

Mảng `models` rỗng cho phép runtime khởi động sau khi hoàn tất cấp phát triển khai. Để cấp phát candidate, mỗi mục model phải có `adapter: "bounded-http-v1"` và một đối tượng `profile` chứa `id`, `revision`, `purpose`, `execution`, `provider_id`, `model_id`, `credential_ref` và `limits` chỉ dành cho tài nguyên (`max_input_tokens`, `max_output_tokens`, `max_bytes`, `timeout_ms`); EMBED còn yêu cầu `dimension`. Các trường tiền tệ không thuộc schema version 2. Tuân theo hợp đồng chuẩn được liên kết để biết quy tắc xác thực. Quá trình khởi động không bao giờ nhập candidate. Runtime xác minh digest của khai báo bất biến với metadata của provider; thay đổi endpoint hoặc loopback yêu cầu một profile revision mới và phải thực hiện cấp phát lại. Việc đọc slot và đọc profile của embedding lease có fencing sử dụng các hàm gateway được cấp quyền hạn chế. Chỉ bật registry không mang lại trạng thái xác nhận đủ điều kiện cho production.

## Xác nhận đủ điều kiện rõ ràng cho hosted thực tế

Chỉ chạy `scripts/test-rag.sh real-models` trong một bản cài đặt dùng riêng đã được owner phê duyệt. Việc đặt `RAG_REAL_MODELS_OPT_IN=true` cho phép gửi các yêu cầu hosted tổng hợp, có khả năng phát sinh chi phí từ provider và thu hồi phê duyệt xử lý GENERATE được cung cấp khi kết thúc kiểm thử. Runner để phê duyệt đó ở trạng thái bị vô hiệu hóa. Chỉ khôi phục phê duyệt thông qua một lần phê duyệt mới được xem xét độc lập.

Sử dụng quy trình CLI ở trên để nhập, xác minh, xem xét và kích hoạt trước chính xác các revision EMBEDDING và LLM. Phê duyệt PERSONAL độc lập cho nội dung và truy vấn đối với cả hai mục đích bằng `policy approve`. Ghi lại các UUID phê duyệt được trả về. Cả hai phê duyệt phải bao quát ứng dụng thư viện chuyên dụng, policy nguồn `local-only@1`, các revision profile chính xác và processor thực tế tương ứng. Sử dụng cùng một `attestation_ref` không chứa thông tin nhạy cảm trong cả hai bản xem xét kích hoạt slot và cung cấp tham chiếu đó dưới dạng `RAG_REAL_MODELS_REVIEW_REF`. Một provider sử dụng OpenRouter còn cần được xem xét đối với processor nền được ghim và các thiết lập định tuyến.

Chuẩn bị một bản cài đặt riêng có ID ứng dụng thư viện cá nhân ở trạng thái Ready bắt đầu bằng `ragreal`. Cấp cho actor fixture A và B vai trò thư viện được cho phép, đồng thời giữ thư viện ở trạng thái rỗng. Actor A tải lên tài liệu tổng hợp. Actor B tuyệt đối không được có quyền truy cập vào tài liệu của A. Sử dụng các tiến trình File Service, scanner, parser, cap-ai và worker đang chạy với danh tính runtime cùng cấu hình tin cậy TLS đã được tài liệu hóa.

Cung cấp các biến môi trường sau thông qua môi trường kiểm thử được bảo vệ.

| Biến | Giá trị bắt buộc |
| --- | --- |
| `TEST_DATABASE_URL` | Tên cơ sở dữ liệu dùng riêng bắt đầu bằng `test_` hoặc kết thúc bằng `_test` |
| `RAG_TEST_AI_API_URL`, `RAG_TEST_FILE_API_URL` | Origin thực tế của các dịch vụ kiểm thử |
| `RAG_TEST_OIDC_TOKEN_A`, `RAG_TEST_OIDC_TOKEN_B` | Các session fixture đã xác thực hợp lệ và khác nhau |
| `RAG_REAL_TEST_APP`, `RAG_REAL_TEST_ROLE` | Ứng dụng thư viện `ragreal` chuyên dụng và vai trò được cho phép |
| `RAG_REAL_EMBED_PROFILE`, `RAG_REAL_EMBED_REVISION` | Profile EMBEDDING đang hoạt động và đã được xem xét chính xác |
| `RAG_REAL_GENERATE_PROFILE`, `RAG_REAL_GENERATE_REVISION` | Profile LLM đang hoạt động và đã được xem xét chính xác |
| `RAG_REAL_EMBED_APPROVAL`, `RAG_REAL_GENERATE_APPROVAL` | Các UUID phê duyệt PERSONAL thực tế đang được bật |
| `RAG_REAL_MODELS_REVIEW_REF` | Tham chiếu bản xem xét slot dùng chung, không chứa thông tin nhạy cảm |
| `RAG_REAL_MODEL_CLI` | Đường dẫn đến tệp thực thi `dxos-model` đã được build |
| `RAG_REAL_ADMIN_TOKEN_FILE` | Tệp access token của operator, chỉ dành cho owner và được CLI chấp nhận |
| `AI_MODEL_APPROVAL_DATABASE_URL` | Kết nối `dxos_authz` được bảo vệ đến cùng host, port và cơ sở dữ liệu dùng riêng như `TEST_DATABASE_URL` |
| `CLAMAV_ADDRESS`, `RAG_INTERNAL_AI_URL` | Scanner đang chạy và endpoint AI nội bộ của worker |
| `RAG_PARSER_QUALIFIED`, `RAG_SOURCE_QUALIFIED`, `RAG_METADATA_QUALIFIED` | Các điều kiện xác nhận đủ điều kiện của môi trường ingestion thực tế |

Preflight yêu cầu `OIDC_ISSUER_URL`, `OIDC_AUDIENCE` và `OIDC_JWKS_URL`, một CLI có thể thực thi và một tệp token mà owner có quyền đọc, không cấp quyền truy cập cho group hoặc other. Các ghi đè mục tiêu kết nối trong một trong hai DSN hoặc trong `PGHOSTADDR`, `PGSERVICE` và `PGSERVICEFILE` đều bị từ chối trước khi thực hiện công việc hosted. Giữ khóa của nhà cung cấp bên ngoài trong tệp secret của bộ điều hợp. Runner sử dụng service token và session phê duyệt được bảo vệ, đồng thời đọc metadata mô hình an toàn từ PostgreSQL.

```sh
RAG_REAL_MODELS_OPT_IN=true scripts/test-rag.sh real-models
```

Điều kiện kiểm tra sẽ tải Markdown mới thông qua File Service, chờ scanner/parser xử lý và tạo xong một vector generation tương ứng hoàn chỉnh, sau đó yêu cầu một tài liệu mà tìm kiếm từ vựng không tìm thấy phải xuất hiện trong kết quả truy xuất hybrid. Điều kiện này yêu cầu câu trả lời `BLUE-17` có căn cứ, kèm citation của chunk có thể truy cập và bằng chứng operation EMBED/GENERATE thành công. Hệ thống kiểm tra actor fixture bị từ chối truy cập và thu hồi phê duyệt GENERATE trước khi xác nhận không có yêu cầu generation nào khác được điều phối. Đầu ra cuối cùng chỉ chứa các danh tính an toàn của tài liệu, lần chạy, generation và profile. Lưu kết quả vào báo cáo xác nhận đủ điều kiện cục bộ trong `.scratch/`, kèm commit đã kiểm thử, lệnh thực thi, kết quả và bằng chứng định tuyến đã được xem xét.

Nếu thiếu điều kiện tiên quyết, lệnh thoát với mã 2 và trạng thái `NOT_RUN`. Lỗi truyền tải hoặc lỗi từ provider khiến lệnh thoát với mã 1 và không bao giờ được tự động thử lại. Một lần chạy thất bại có thể để lại tài liệu tổng hợp và trạng thái thu hồi phê duyệt. Kiểm tra trạng thái operation an toàn và chuẩn bị một fixture rỗng, biệt lập khác trước khi thực hiện lần xác nhận đủ điều kiện mới.

Một revision embedding trực tiếp thứ hai cần được xác nhận đủ điều kiện riêng do owner thực hiện. Kích hoạt candidate và phê duyệt đã được xem xét độc lập của revision đó, giữ cả hai route của bộ điều hợp ở trạng thái khả dụng, cho phép worker polling xây dựng generation mới hoàn chỉnh và xác minh chính xác profile revision cùng dimension trước khi tuyên bố chuyển đổi trực tiếp. Ghi nhận tiêu chí này là `NOT_RUN` khi không có revision thứ hai đã được xem xét và phê duyệt. Các bài kiểm thử cô lập revision có tính xác định bao quát lỗi hồi quy trong mã nguồn. Chỉ ghi nhận những tổ hợp provider/model thực sự vượt qua điều kiện kiểm tra trực tiếp.