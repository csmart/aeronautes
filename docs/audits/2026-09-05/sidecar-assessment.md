# Sidecar advisory assessment — 9 September 2026

P31 and SEC-11 remain blocking.
This records a dependency assessment at application source `f88114958c7f5211b8953e0c757687146a4c4f5d`; it does not accept an engine update, an advisory disposition or valuable-data use.
The accepted pin remains v1.74.3.
The later application changes are not relabelled as a fresh application-binary scan.

## Scanner precision correction

The six exact accepted v1.74.3 sidecars each report the same 42 advisory IDs.
All were built with Go 1.26.4 and match the accepted binary manifest.
The six assessed v1.75.1 candidate sidecars each report one ID, [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932); the other 41 IDs are no longer reported in that snapshot, and none is newly reported.

These observations have module-version precision.
The pinned govulncheck v1.7.0 source falls back to advisory-provided package and function names when its binary reader cannot recover symbols; absent advisory function lists produce wildcard placeholders.
A nonempty function field therefore does not prove that the package or function is linked, called or reachable from Aeronautes.
The earlier audit's “symbol level” terminology described protocol fields and must not be used as symbol-presence evidence.
The raw earlier evidence remains preserved.

Each accepted-sidecar JSON stream has 566 finding messages: 43 module records, 53 package-field records and 470 function-field records.
Each candidate stream has 15 messages: one module record, seven package-field records and seven synthetic OpenPGP wildcard function-field records.
These are 42 and one distinct advisory IDs respectively, not 566 or 15 vulnerabilities.
JSON mode exits zero even when findings exist; the recorded text-mode baseline scan exits three.
The 42-to-one difference is not proof that 41 enabled vulnerable call paths were removed.

## Assessed candidate and provenance

The assessment selected the [official v1.75.1 release](https://github.com/rclone/rclone/releases/tag/v1.75.1), published on 4 September 2026, as a candidate above the prior advisory-derived v1.75.0 floor.
All six official ZIPs match the signed upstream SHA-256 manifest and GitHub release asset digests.
Their embedded source revision is `687d264b689b8c49a67e2e52a8a5e0caa01c04ce`, matching the signed release tag.
The [structured evidence](evidence/2026-09-09-sidecars.json) retains every candidate archive/binary hash, build metadata summary, raw scan hash and per-advisory comparison.

| Target | Candidate Go version | Main module | Modified source metadata | SBOM module entries | Reported IDs |
| --- | --- | --- | --- | ---: | ---: |
| Linux amd64 | 1.26.8 | v1.75.1+dirty | true | 246 | 1 |
| Linux arm64 | 1.26.8 | v1.75.1+dirty | true | 246 | 1 |
| macOS amd64 | 1.26.8 | v1.75.1 | false | 247 | 1 |
| macOS arm64 | 1.26.8 | v1.75.1 | false | 246 | 1 |
| Windows amd64 | 1.26.8 | v1.75.1 | false | 244 | 1 |
| Windows arm64 | 1.26.8 | v1.75.1 | false | 244 | 1 |

Both signed Linux binaries report modified source trees; that modification is unexplained.
Clean metadata on the other four binaries does not establish reproducible source-to-binary correspondence either.
GnuPG validated the manifest signature with the fingerprint published in the [upstream signing procedure](https://rclone.org/release_signing/), `FBF737ECE9F8AB18604BD2AC93935E02FF3B54FA`.
The manifest uses a DSA/SHA-1 signature over SHA-256 file digests; no algorithm override or trust assignment was used.
The annotated tag signature was separately validated with key `E3B358DC858FB307F48170B9CB0DBEBC5F32C81D`.
These observations are not independent build attestations, native code-signing or installation results.
The candidate binaries were inspected as nonexecutable data and were not invoked in this assessment.
The earlier [P09 candidate token-redirect control](../../DESIGN.md) remains separate historical evidence; this assessment does not repeat it or treat it as the complete P31 compatibility gate.

The candidate database archive was retrieved at 18:05:51–18:05:53 UTC on 9 September 2026 and names Go vulnerability database commit `c1fc9ed8a61223797c096857356d32f4f36ca0dc`, with 4,379 OSV records.
The accepted-sidecar assessment used the earlier retained commit `fc5d44d2e19322760d54ba05819057cfbfe803c2`, with 4,378 records.
The only database difference is the new Crossplane advisory GO-2026-6302; no existing record changed or disappeared, and neither compared sidecar inventory reports it.
Placeholder OSV modification dates and `db_last_modified=0001-01-01` do not establish freshness; the recorded retrieval times and archive hashes bound this comparison.

## Complete reported-ID comparison

Every row is a report comparison, with no accepted enabled-path disposition.
A candidate non-report does not clear the still-accepted v1.74.3 binary.

| Advisory | Baseline | Candidate targets reporting | Observed candidate affected-module versions |
| --- | --- | --- | --- |
| [GO-2026-4961](https://pkg.go.dev/vuln/GO-2026-4961) | 6/6 | 0/6 | `golang.org/x/image v0.45.0` |
| [GO-2026-4970](https://pkg.go.dev/vuln/GO-2026-4970) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | 6/6 | 0/6 | `golang.org/x/net v0.57.0`; `stdlib go1.26.8` |
| [GO-2026-5051](https://pkg.go.dev/vuln/GO-2026-5051) | 6/6 | 0/6 | `github.com/cloudsoda/go-smb2 v0.0.0-20260701064823-d8c5600d73b8` |
| [GO-2026-5061](https://pkg.go.dev/vuln/GO-2026-5061) | 6/6 | 0/6 | `golang.org/x/image v0.45.0` |
| [GO-2026-5062](https://pkg.go.dev/vuln/GO-2026-5062) | 6/6 | 0/6 | `golang.org/x/image v0.45.0` |
| [GO-2026-5066](https://pkg.go.dev/vuln/GO-2026-5066) | 6/6 | 0/6 | `golang.org/x/image v0.45.0` |
| [GO-2026-5158](https://pkg.go.dev/vuln/GO-2026-5158) | 6/6 | 0/6 | `go.opentelemetry.io/otel v1.44.0` |
| [GO-2026-5774](https://pkg.go.dev/vuln/GO-2026-5774) | 6/6 | 0/6 | `github.com/go-chi/chi/v5 v5.3.1` |
| [GO-2026-5775](https://pkg.go.dev/vuln/GO-2026-5775) | 6/6 | 0/6 | `github.com/go-chi/chi/v5 v5.3.1` |
| [GO-2026-5777](https://pkg.go.dev/vuln/GO-2026-5777) | 6/6 | 0/6 | `github.com/go-chi/chi/v5 v5.3.1` |
| [GO-2026-5841](https://pkg.go.dev/vuln/GO-2026-5841) | 6/6 | 0/6 | `github.com/klauspost/compress v1.19.0` |
| [GO-2026-5856](https://pkg.go.dev/vuln/GO-2026-5856) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | 6/6 | 6/6 | `golang.org/x/crypto v0.56.0` |
| [GO-2026-5942](https://pkg.go.dev/vuln/GO-2026-5942) | 6/6 | 0/6 | `golang.org/x/net v0.57.0`; `stdlib go1.26.8` |
| [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) | 6/6 | 0/6 | `golang.org/x/text v0.41.0` |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-6061](https://pkg.go.dev/vuln/GO-2026-6061) | 6/6 | 0/6 | `google.golang.org/grpc v1.84.0-dev.0.20260723093437-b6eac429d7b6` |
| [GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-6091](https://pkg.go.dev/vuln/GO-2026-6091) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-6181](https://pkg.go.dev/vuln/GO-2026-6181) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6182](https://pkg.go.dev/vuln/GO-2026-6182) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6183](https://pkg.go.dev/vuln/GO-2026-6183) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6184](https://pkg.go.dev/vuln/GO-2026-6184) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6185](https://pkg.go.dev/vuln/GO-2026-6185) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6186](https://pkg.go.dev/vuln/GO-2026-6186) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6187](https://pkg.go.dev/vuln/GO-2026-6187) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6188](https://pkg.go.dev/vuln/GO-2026-6188) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6189](https://pkg.go.dev/vuln/GO-2026-6189) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6190](https://pkg.go.dev/vuln/GO-2026-6190) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6191](https://pkg.go.dev/vuln/GO-2026-6191) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6195](https://pkg.go.dev/vuln/GO-2026-6195) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6196](https://pkg.go.dev/vuln/GO-2026-6196) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6197](https://pkg.go.dev/vuln/GO-2026-6197) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6199](https://pkg.go.dev/vuln/GO-2026-6199) | 6/6 | 0/6 | `github.com/rclone/rclone v1.75.1`; `github.com/rclone/rclone v1.75.1+dirty` |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | 6/6 | 0/6 | `stdlib go1.26.8` |
| [GO-2026-6222](https://pkg.go.dev/vuln/GO-2026-6222) | 6/6 | 0/6 | `golang.org/x/image v0.45.0` |
| [GO-2026-6303](https://pkg.go.dev/vuln/GO-2026-6303) | 6/6 | 0/6 | `golang.org/x/crypto v0.56.0` |
| [GO-2026-6354](https://pkg.go.dev/vuln/GO-2026-6354) | 6/6 | 0/6 | `golang.org/x/crypto v0.56.0` |
| [GO-2026-6355](https://pkg.go.dev/vuln/GO-2026-6355) | 6/6 | 0/6 | `golang.org/x/crypto v0.56.0` |

## Remaining OpenPGP question

GO-2026-5932 covers the deprecated `golang.org/x/crypto/openpgp` family and lists no fixed version.
Upgrading x/crypto alone does not close it.
The source follow-up inspected the signed-tag rclone source and 27 of the 252 distinct embedded dependency module/version pairs; 225 pairs remain uninspected.
The standard Go parser read imports from 3,356 Go files with no parse errors.
All 46 deprecated-family import edges, including tests, occur within that family itself; 36 edges in non-test-named files span 19 files.
No external importer was found within the inspected set.

The actual self-update and inspected Proton backend/bridge/API/SRP calls use the maintained `github.com/ProtonMail/go-crypto` family.
The wrapper selects fixed version/copy/copyto Swift/local routes, but a backend-name argument does not close startup initializers or shared callers.
The import reader is not a selected-build graph, type-checked call graph, linker map or runtime trace.
Missing source coverage, build conditions, initialization and exact source-to-artifact correspondence prevent a “not linked”, “not reachable” or false-positive disposition.
GO-2026-5932 remains unknown.

## Other dependency evidence and limits

The frozen application source scans reported zero findings for Linux amd64 internal packages, Darwin arm64 internal packages and Windows amd64 all packages, with default tags and tests excluded.
The Linux/macOS scopes omit the Wails entry package, and none is a current six-target application-binary or native-library scan.
The versionless local Swift replacement is excluded from version matching by the scanner; a separate integrity check verifies its 38 files against upstream plus three recorded patches, which does not establish advisory reachability.
No advisory in that database snapshot names the Swift package, a bounded inventory observation only.

The npm lockfile has 156 entries plus the root, with 155 marked as development dependencies.
Those installation flags do not establish which compiler/framework code appears in shipped assets.
The offline npm audit returns before advisory retrieval, so its zero result is excluded from security conclusions.
The rejected registry submission was not retried; a complete approved submission or fully local advisory source, final asset inventory and native-library assessment remain required.

The source-download setup had one recorded limitation: a private module cache without a private GOPATH likely updated the default user Go checksum-database checkpoint.
Its timestamp falls within the producer window, but no before-state hash proves attribution.
No candidate was installed there, and no restoration or further download was attempted after discovery; future source downloads must isolate both caches.
The initial sandbox download/key-import failures and every actual producer result remain retained separately from successful later checks.

## Next required work

1. Obtain reviewed enabled-path dispositions from sufficient exact source/binary evidence, retaining the OpenPGP and any other unknowns.
2. Review any proposed pin/design change, then pass the real-engine redirect, TLS, name, staging/placement, retry, cancellation and large-object compatibility gate on every supported target.
3. Preserve checksum and version refusal; a mismatch escape hatch cannot substitute for candidate acceptance.
4. Complete the application/native-library/npm scopes and the Linux installation/upgrade policy, with exact artifact identity and required native evidence.

The [P31/SEC-11 acceptance criteria](remediation-plan.md#acceptance-gates-and-completion), P18/P30 authority and child-lifetime work, and the valuable-data release hold remain unchanged.
The private frozen source records are `p31-dependencies-f8811495-20260910`, `p31-candidate-v1.75.1-20260910` and `p31-openpgp-v1.75.1-20260910` under the release-identity evidence directory.
Their manifests, scanner identity and database hashes are retained in the structured record.
