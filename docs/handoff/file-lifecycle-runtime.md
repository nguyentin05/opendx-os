# File lifecycle and supporting services evidence

Scope: Task 7, `integration/backend-platform` based on `eb124a8`, disposable
Compose `opendx-integration` and PostgreSQL `opendx_integration`. Actual services:
SeaweedFS 3.80, ClamAV 1.4, APISIX 3.10.0 and Kestra 0.20.7. This qualifies the
following S06 assertions, with OCR deferred; it is not release acceptance.
Reproduction commands and private configuration are in
[integration setup](integration-setup.md#files-and-supporting-service-checks).

| Assertion | Result and observed behavior |
| --- | --- |
| Quarantine | PASS: actual scanner stopped; persisted attempts increase while status remains QUARANTINED/scanned_at null; download and submit 422; unchanged domain row, audit/event/receipt counts |
| Missing attachment | PASS: submit 422 with unchanged domain row and audit/event/receipt counts; focused live case 1.254s |
| Clean content | PASS: actual ClamAV CLEAN, exact uploaded/downloaded PDF bytes, SHA-256 and size match |
| Infected content | PASS: harmless EICAR in a PDF stream detected INFECTED; download/submit 422 with no command effects |
| Caller isolation | PASS: other workspace and wrong app role download 403; cross-app attachment command 404; cross-workspace FK attachment rejected by actual Hasura |
| Scan ownership | PASS: ordinary DB caller and Hasura DB connection lack CLEAN update grants; authenticated GraphQL mutation rejected; file remains quarantined |
| Replacement | PASS: backend DB cannot update identity/hash/app/name; PUT/PATCH has no replacement route; new upload gets new ID/key and quarantine, original CLEAN bytes/metadata unchanged; replacement rescanned |
| Scanner/storage recovery | PASS: real ClamAV and SeaweedFS stop/start, process worker restart, no volume deletion; storage outage returns 503 for prior clean download, pending remains unclean; then original pending identity/hash/bytes become CLEAN; prior clean and infected rows/objects survive |
| Upload scanner limit | PASS: real Go adapter scans a full 20 MiB object; explicit 25 MiB stream/file and 50 MiB expansion configuration; actual protocol readiness |
| OCR | NOT_RUN/deferred: unavailable; no OCR functionality claimed |

The seven originally named lifecycle tests passed together in 132.061s. A later
expanded run preserved all seven passes but exposed transient transport timeout
handling in the extra controlled-IP test. That test was corrected and passed
individually in 17.022s. The final expanded suite is reserved for the final handoff
verification; these separate results must not be presented as a full expanded
suite pass. File module database/race regressions passed 5.375s; actual
`TestRealSeaweedFSClamAV` passed 1.915s, with real storage, clean/EICAR and 20 MiB
scanning. `make test` passed all available Go modules and 36 Python tests; the UI
suite was skipped because dependencies were absent.

The original recovery RED identified a deployment defect: Docker changed the
process IP from 172.18.0.7 to 172.18.0.4, while APISIX continued forwarding to the
old address and returned 502. The pending row itself had recovered to CLEAN.
APISIX now uses a five-second DNS cache lifetime. Its gateway was recreated once
to load configuration before verification, never during recovery observation.

Natural restart need not change an IP. A separate controlled fault verified
project/network labels, chose an unused subnet address, reassigned only the
process endpoint from 172.18.0.4 to 172.18.0.101, and proved direct backend and gateway
original-byte downloads with the same gateway start timestamp. UTC evidence:
14:36:38.445 baseline, 14:36:38.652 reassignment, 14:36:46.064 both paths recovered,
14:36:53.615 original IP/aliases and both paths restored, 2026-11-07. This fault
preserved the same container and volumes and differs from an ordinary restart.
Requests poll boundedly through transient transport loss; the direct assertion
uses the actual container endpoint, independently of host port DNAT.

The latest natural recovery row is `555401db-005a-4c22-95e8-480d2906c2f4`, created
14:34:43.403851 UTC, CLEAN at 14:35:48.135352, attempt 6, unchanged SHA-256
`8b258fb894ea1f4f0b647489088184b88499b51cd4f9fa0ed49f167ffccc16fd`.
Private `file-recovery.json` contains the latest run's UTC operational evidence;
it is overwritten by a later file-test invocation, not an archive.

The supporting probe RED had 7 failures among 9 executable cases: both HTTP
endpoints incorrectly accepted 302/404/500 and scanner readiness was ignored.
After repair all 9 passed. Live strict probes also passed: actual S3 unauthenticated
403 XML AccessDenied, actual Kestra admin API 401, and actual ClamAV PONG. Requests
are bounded and redirects are not followed. Controlled HTTP servers verify exit
semantics only; they do not count as live services.

Kestra webhook authentication was verified separately against the actual route:
missing key 404 `Not Found`; wrong key 404 `Not Found: Webhook not found`, with
missing, valid or invalid Basic credentials. These calls created no actual Kestra
execution and no work/effect. Correct key with no Basic, and correct key with
invalid Basic, each returned 200 and produced an actual SUCCESS execution while
the backend deduplicated the original effect. The live case passed 12.879s.
**The secret webhook key authorizes delivery; Basic protects the admin API.**
Generic404 alone is never accepted as a supporting-service auth PASS. Signed
backend effect identity remains separately enforced. Alpha-only automatic
workflow qualification and beta NOT_READY remain unchanged.
