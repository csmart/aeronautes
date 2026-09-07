# Complete native listing response validation

The behaviour oracle is [ADR 0021](../adr/0021-complete-listing-pages.md), audit DS-13/W-13/R-16 and K-PERF-001/K-CON-001.
This record covers the first response slice on plan `4156b2a771df5cc20355625fc97b9c0490a09c8f`, whose parent is repaired master `c9d2d6bbafd16323ef6773f4ba6569e43422c9fd`.
Short-page continuation and marker validation remain the second slice.

## Failure and fix

The pinned library decoded one JSON value and ignored errors while draining the remaining response.
Its object date conversion could then replace an earlier decode error with a successful date parse.
Consequently, a complete JSON prefix inside a truncated HTTP message could appear complete, and invalid numeric fields could become accepted rows.

Container and object listing calls now share a listing-only reader.
It requires HTTP 200 without Content-Range, one JSON array with non-null typed rows, a complete closing bracket, and only JSON whitespace through the actual body EOF.
Known invalid status, root shape or suffix closes without draining an untrusted remainder.
Read, decode, context and close errors refuse the whole page; a primary parse/read error takes precedence over a later close error.
Object conversion starts only after response validation succeeds, and validates the entire timestamp before preserving the previous second precision.
The successful field mappings, literal names, subdirectories and SLO classification are retained.

The reader keeps a single decoded page and bounded read buffers.
It does not introduce a total response-byte or process-memory budget; P24 remains open.
The generic JSON reader, request authentication, retry policy and rclone are unchanged.

## Permanent tests and parent replay

`internal/swiftcontrol/listing_response_test.go` contains 52 response-table cases and two held-body cancellation cases through actual public sessions and loopback HTTP.
The table covers valid rows, empty arrays, whitespace, root shapes, null rows, invalid later rows, JSON suffixes, truncated Content-Length, 204/206, Content-Range, numeric type/fraction/overflow errors and invalid dates.
Successful controls check the literal hostile name, counts, bytes, hashes, timestamp, content type and SLO field.
Each malformed page returns no rows and performs one listing request without retry.
Cancellation is requested after the JSON array is flushed while the HTTP body is still incomplete.

The nested library adds 26 cases through its public `Connection.Containers` and `Connection.Objects` methods.
These cover one-byte delivery, read errors after valid JSON, close errors, cancellation before reading or at close, primary-error preservation, and prompt rejection without reading a known invalid remainder.
Every response body must close exactly once, and every failed library page must be nil.

Current tests replayed over the unchanged parent production source produce 25 deterministic response-table failures and six bound-service failures, with no setup failures, skips or races.
The final replay additionally catches the object held-body cancellation case: 32 failing and 30 passing leaf cases in total.
An earlier replay passed both held-body cancellation cases, so transport scheduling affects whether that parent defect is visible there; the 18 failing/8 passing nested-library cases provide deterministic direct lifecycle evidence.
The original four audit probes are also replayed unchanged: both response cases now pass, while both short-page cases still fail with the missing second name.

## Bound-service scenarios

Eight top-level system tests exercise two owned accounts on both mock Swift and SAIO.
Six tests each exercise five controlled malformed responses through the actual Browse container/object and Find methods: a short HTTP body, second JSON value, partial status, Content-Range and invalid bytes.
An owned reverse proxy modifies a real successful listing response and verifies that its intended fault was applied exactly once.
These are controlled provider/fault scenarios, not evidence that SAIO naturally emits malformed responses.

Browse refuses with an error, no rows and no continuation marker.
Find must first be admitted, then finish under the same scan id with an error, zero scanned rows and no matches.
Failed asynchronous Find receives no successful-start credit.
Checked Find shutdown and independent snapshots of both account namespaces run before any receipt earns credit.
The snapshots bypass the modifying proxy.

The two successful Browse tests verify the owned container, literal object name, object fields, delimiter prefix and empty listing, with both namespaces unchanged.
They do not require account byte/count statistics to be immediately current after PUT; those counters are eventually consistent, and the public response-table controls test exact field mappings.
Each test emits at most one invocation-bound receipt per action/scenario.
The coverage reporter's top-level and duplicate-claim guards remain intact.

## Independent broken-guard controls

Each variant changes one safeguard in a disposable source copy and runs all 54 public-session and 26 library cases.
All fourteen variants fail intended assertions; none relies on a compilation error, timeout, panic, race, skipped case or failed fixture.
Every run reconciles its process exit and terminal test/package events.

| Removed safeguard | Public-session failures | Library failures |
| --- | ---: | ---: |
| HTTP 200 requirement | 2 | 2 |
| Content-Range refusal | 2 | 2 |
| Prompt root-shape refusal | 0 | 2 |
| Close-error propagation | 0 | 2 |
| Final context check | 0 | 2 |
| Initial context check | 0 | 2 |
| Complete timestamp fraction | 1 | 0 |
| Body EOF and suffix validation | 10 | 4 |
| Container decode error | 5 | 1 |
| Null container row | 2 | 0 |
| Container page-error return | 22 | 9 |
| Object decode error | 5 | 1 |
| Null object row | 2 | 0 |
| Object page-error return | 7 | 9 |

## Source provenance and full verification

`UPSTREAM.json` retains the exact v2.0.5 revision and module checksums and records the ordered `credential-pairing.patch`, `listing-response.patch` sequence.
The first patch is byte-identical to the parent.
The second contains the listing implementation and tests, plus corrections to the nested mock's empty object listing and DLO eventual-consistency fixture from null to an empty array.
The two application mock terminators are corrected the same way; explicit null-refusal tests remain.
These fixture changes preserve valid empty Swift JSON responses rather than allowing null in production.

`make verify-swift-source` reconstructs all 38 imported files from the checksum-verified upstream archive and both ordered patches.
All 15 checker tests pass, including missing, reordered, altered, extra and symlinked patch refusal and the existing module/source/checksum checks.
The required Go CI job retains this gate and the nested mock suite.

Final local verification:

- `go test -race -count=1 -timeout 10m -coverprofile=... -coverpkg=./internal/... -covermode=atomic -json ./internal/...` with both owned SAIO accounts and pinned rclone: 514 top-level tests, 1,041 leaf cases, 20 packages, no failures or races, 84.2% combined statement coverage.
  Only the separately gated `TestRcloneMatrix` is skipped.
- Nested `go test -race -count=1 .` with external Swift environment variables cleared: 129 passing top-level tests and 157 passing leaf cases.
  Its two existing unsupported symlink-feature tests skip.
- Scenario reconciliation from the complete final event stream, actual exit status and expected package set: valid process/stream/contract, 23/110 cells, 42/49 actions with gaps, SAIO configured.
- Internal vet, CGO-disabled internal build and Go formatting checks pass.
  Git whitespace checks exclude the recorded patch artifacts through `.gitattributes`, because unified-diff context lines require their leading spaces.
  The source gate still checks the applied code with `git apply --whitespace=error-all`.

Local raw evidence is under `/tmp/aeronautes-remediation-20260905/`: `p22-response-final-before-v3.*`, `p22-response-final-upstream-before-v2.*`, `p22-original-probes-response.*`, `p22-response-full-v3.*`, `p22-response-upstream-v2.*`, `p22-response-scenario-coverage.json` and `p22-response-controls/results.json`.
Earlier fixture/setup failures and invalid coverage receipts are excluded from final passing evidence.
The source tree contains the permanent tests and the checked patch needed to reproduce the change.

Independent local review and all five latest-head/current-master PR checks gate delivery, followed by a normal protected merge and all five master checks.
Frontend source is unchanged; its required pipeline jobs still run.
Short-page continuation, complete planning/verification across hidden tails, wider authority/durability/writer/ownership work and the valuable-data release hold remain open.
