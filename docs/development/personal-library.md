# Local personal document library

The personal library accepts owner-only CLEAN text PDFs and Markdown through the
existing File Service. Registration queues local extraction, structured chunking
and PostgreSQL lexical search. It requires no model key, pgvector or hosted
service. Personal `/answers` returns `SEARCH_ONLY`; required generation is denied.
Application library membership is checked against current Core authority on every
protected operation. Application administrators do not inherit another user's
private content.

## Runtime components and gates

Apply the checksum-verified Core and cap_ai migrations using
`python3 scripts/migrate_rag.py --help`. Fresh bootstrap uses the same SQL. Core
migration 0002 permits `text/markdown` only with `OWNER_ONLY`; existing ordinary
attachment MIME policy is unchanged. cap_ai 0002 adds run-scoped DocIR, chunks,
provenance links, simple FTS and fenced activation. Disable the feature to roll
back enablement; retain source restrictions and evidence rather than dropping
these tables or widening file permissions.

The API needs `RAG_METADATA_ENABLED=true`, `RAG_SOURCE_QUALIFIED=true`, then
`RAG_PERSONAL_ENABLED=true`, `RAG_METADATA_QUALIFIED=true` and
`RAG_PARSER_QUALIFIED=true`. All qualification flags default off. They are an
operator's assertion that the relevant live gates passed, not test results.
Configure `RAG_FILE_API_URL` for the independently protected File Service.
Never expose the parser/worker internal listeners through the public gateway.

Run `cap-ai-worker` with its dedicated `AI_WORKER_DATABASE_URL`,
`RAG_WORKER_SERVICE_SUBJECT`, `RAG_WORKER_BEARER_TOKEN`, `RAG_INTERNAL_FILE_URL`,
`PARSER_SOCKET` and `PARSER_SERVICE_TOKEN` (see its startup validation). It
also requires the three source/metadata/parser qualification flags. Give its
OIDC service subject exactly the deployment-configured worker identity. It has
two concurrent leases, 15-second heartbeats, bounded retries and no model client.
The narrow worker database role cannot read Core or write arbitrary cap_ai rows.

Build the parser separately:

```sh
docker build -f capabilities/Dockerfile.parser -t dxos-rag-parser:test capabilities
python3 scripts/integration/test_parser_sandbox.py
```

Run that image with `--network none --memory 512m --cpus 1 --pids-limit 64
--read-only --tmpfs /tmp:rw,noexec,nosuid,size=220m` and an explicitly private Unix
socket volume shared only with the worker. Set `PARSER_SOCKET` and
`PARSER_SERVICE_TOKEN`; match the latter with worker `PARSER_SERVICE_TOKEN`. Give the
parser no storage/database/OIDC/provider credentials. The service only receives
authenticated multipart metadata and source bytes; extraction runs in a child
process group with a hard deadline. Container memory/CPU/network restrictions
are deployment requirements, not properties of launching the binary alone.

## Qualified extraction envelope

| Adapter | Pinned dependency | License | Supported envelope |
| --- | --- | --- | --- |
| Markdown AST | [goldmark 1.7.8](https://github.com/yuin/goldmark/tree/v1.7.8) | MIT | UTF-8 headings, paragraphs/lists, fenced code and labelled pipe tables; remote/file links never fetched |
| Text PDF layout | [Poppler utilities 25.12.0-r0, Alpine 3.23](https://pkgs.alpinelinux.org/packages?branch=v3.23&name=poppler-utils) | GPL-2.0-or-later | Text rows and simple aligned tables, page/bounding-box provenance; no embedded images or ambiguous layouts |
| Local tokenizer | `unicode-rune-v1` in this repository | Apache-2.0 | Unicode scalar count, independent of any model tokenizer |

The worker rejects other adapter versions. The container pins the APK version;
requalification is required before changing it. Gold fixtures in
`capabilities/internal/knowledge/docir/testdata/` are original generated documents:
Markdown/Vietnamese text, an aligned approval table on PDF page 4, an image-only
PDF and a 201-page limit fixture. The table relation `>= 10,000,000 VND → Director`
is retained in labelled rows and citations. This is a small regression corpus,
not a claim of arbitrary PDF layout or Vietnamese semantic recall. Simple FTS
uses NFC normalization and exact lexical terms, without stemming or translation.

Limits are 20 MiB source, 200 pages, 120 seconds, 512 MiB container memory,
200 MiB decoded output, 25 MP images, 50,000 elements and 20,000 chunks. The
current recipes reject all PDF images before decoding. Scans return
`OCR_UNAVAILABLE`; ambiguous layout or malformed input fails without activation.
Markdown image alt text is authored text, not OCR evidence; image targets are
never fetched. No OCR/VLM, extracted images or derived asset writer is enabled. Asset routes
only serve exact AVAILABLE artifacts from an active COMPLETE run; these text-only
recipes produce none. No abandoned files are created by these recipes, and no
destructive retention sweep is scheduled.

`chunk-v1` uses a 512-rune paragraph target and 768-rune hard maximum. Logical
table rows retain column labels/units; oversized rows retain short identifying
cells and warn. Code splits at line boundaries; overlong lines fail. Each chunk
stays within one element/section, so overlap is zero (within the 64-rune ceiling).
This conservative recipe favors provenance over packing unrelated small blocks.
COMPLETE runs are immutable; a new fenced attempt can replace a failed candidate
without erasing the old eligible active version.

See [third-party notices](../../LICENSES/third_party/native-rag-parser.md). Images
containing Poppler require the applicable GPL notices and corresponding source
when distributed; no parser image is published by this development workflow.

## Browser integration

The shell reads `/api/auth/me` and `/api/auth/apps` with a real bearer token.
The latter now includes the current `knowledge` declaration, including a valid
capability-only library with an empty `tables` array. Exactly one enabled library
is selected. Its held allowed role is used independently of the ambient app role.
Ordinary domain lists continue to use registry-declared columns and Hasura.

Supply `window.DXOS_CONFIG = {issuer, clientID}` before the UI bundle for a
public OIDC client with allowed redirect URIs and PKCE S256 enabled. Production
requires HTTPS and explicit configuration; only localhost has the development
Keycloak default. The access token stays in memory, expires locally, and is
cleared on sign-out. Only transient PKCE verifier/state uses sessionStorage.
There is no token paste field, provider/key setup or browser storage credential.

The library uploads privately, polls actual scan status, registers only CLEAN
sources, displays queued/ready/error state, and supports search, protected
citations, original download and logical delete. Reupload creates a new source;
the version replacement API remains revision checked. Switching user/app/role
cancels requests and suppresses stale callbacks even if the transport ignores
cancellation. Refresh permissions reloads the current registry session.

## Verification and limits

Run the module tests and UI checks listed in [verification](verification.md).
Use `-tags=integration` and a disposable PostgreSQL 17 `TEST_DATABASE_URL` for
knowledge/files database tests, including real process crash/retry, stale-fence
activation and source/role revocation. Local fake File Service/authority tests
are not evidence of real Hasura/scanner/OIDC acceptance.

`scripts/test-rag.sh personal` runs the source and metadata gates first, then
local parser/worker A/B same-byte ingestion, protected guessed routes,
`SEARCH_ONLY`, and a real Playwright Chromium browser flow. It requires all
source/metadata variables in [source qualification](../contracts/security-and-acceptance.md#native-private-source-qualification),
`RAG_TEST_AI_API_URL`, `RAG_TEST_UI_URL`, and `RAG_TEST_UI_STORAGE_A`/`_B` paths
to Playwright storage-state files containing existing IdP sessions for the same
distinct A/B fixture users as the API tokens. Install Playwright Chromium in the
test environment. These files and credentials are private test inputs, never
source files. Use a disposable installation; runners create test sources and
change fixture memberships during revocation probes. A missing prerequisite is
`NOT_RUN`, never PASS. No live gate should be reported as satisfied from this
page or from container/unit evidence alone.
