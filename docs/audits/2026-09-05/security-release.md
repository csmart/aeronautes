# Aeronautes security, platform, and release audit

Audit date: 2026-09-05.
Reviewed tree: `<repo>` at `cc6465ac69ec00756d9976bbf6cf7531b07da66d`.
This is the security, platform and release domain of the [full project audit](README.md).
No product source was changed for the audit.

The present design already declares a NO-GO for valuable-data release.
The evidence below supports retaining that decision.
The strongest new findings are reuse of a saved durable credential after an untrusted auth-endpoint edit, loss of destructive-recovery safety facts under SQLite's configured durability, and a deleted bookmark retaining a live authenticated session after keychain deletion fails.
The real pinned rclone also forwards a Swift token through a cross-host redirect, independently confirming an existing design warning.

## Scope and behaviour oracles

Read project instructions, `docs/REQUIREMENTS.md`, relevant portions of `docs/DESIGN.md`, ADRs 0006/0010/0011/0012, `docs/RELEASING.md`, workflows, packaging, services, persistence, authentication, secret storage, subprocess management, and relevant tests.
Applied the ECC security-review skill and the common coding-review skill with a desktop threat model: a hostile webview, malicious or misconfigured Swift endpoints, hostile object names and headers, competing clients, process/OS failure, native credential-store failures, and the user's existing local environment.
No claim is made that a webview compromise or malicious production endpoint was observed.

Principal oracles are DESIGN §§12, 26, 28.6, 50, 51, 55, 57.4, 61, and 65.10; K-SEC-001/002/003/005/006/007/008; K-XFER-014; and K-SEG-004.
These require backend authority over sensitive operations, write-only durable credentials, per-bookmark trust, equivalent native/engine TLS policy, isolated children, durable recovery provenance, and preservation of data when ownership or outcome is uncertain.
P1 means fix before valuable-data release; P2 means material lifecycle, supportability, or release work to schedule before general distribution.
Product choices already explicit in the design are separated from defects.

## Finding index

| ID | Priority | Finding | Evidence / confidence |
| --- | --- | --- | --- |
| SEC-01 | P1 | Editing an auth endpoint can send an existing durable key to an attacker | Direct service-level failing probe; high |
| SEC-02 | P1 | The real pinned child forwards its token through a cross-host storage redirect | Real v1.74.3 child failing probe; high |
| SEC-03 | P1 | WAL NORMAL allows recovery safety facts to disappear before destructive cleanup | Runtime PRAGMA plus modelled recovery/data-loss probe; high for mechanism, no actual power-cut experiment |
| SEC-04 | P2 | Failed keychain removal leaves a deleted bookmark authenticated | Direct service-level failing probe; high |
| SEC-05 | P2 | Returned storage URL skips part of the plaintext endpoint policy | Direct validation failing probe; high |
| SEC-06 | P1 | Child lifetime and cancellation guarantees differ across supported platforms | Source-verified; Windows/macOS runtime untested |
| SEC-07 | P1 | Sensitive bridge requests lack backend-held authority and narrowly scoped capabilities | Source-verified; existing design work, hostile webview assumed |
| SEC-08 | P2 | Audit failure and retention can erase the record of an applied mutation | Source-verified; existing accounting gap plus concrete retention behaviour |
| SEC-09 | P1 | Release artefacts are not gated by the required behavioural/native evidence | Workflow and script inspection; high |
| SEC-10 | P2 | Release binaries and packages do not identify their actual release | Build-source inspection; high |
| SEC-11 | P2, release gate | Pinned rclone contains 42 advisory IDs requiring exposure triage | Official offline binary scan; high inventory confidence, app exploitability not established by count |
| SEC-12 | P2 | Linux package dependencies do not enforce the engine version the app requires | Source-verified; exact supported distribution matrix unresolved |

## SEC-01 — saved credentials can be retargeted through the bridge

References: `internal/app/bookmarkservice.go:43`, `internal/bookmarks/bookmarks.go:110`, `internal/app/connections.go:40`, `internal/app/browseservice.go:27`, and `internal/swiftcontrol/authv1.go:52`.
`BookmarkService.Save` accepts a replacement auth URL and trust settings for an existing ID while a blank secret retains that ID's stored credential.
`Connect` subsequently loads that stored credential and authenticates against the replacement URL.
The normal editor advertises that a blank key keeps the existing key (`frontend/src/features/browser/BookmarkEditor.svelte:111`).

A compromised webview can enumerate bookmark IDs, save one with an attacker-controlled HTTPS auth URL and a blank secret, and invoke Connect.
The backend sends `X-Auth-Key` to the new endpoint even though no bridge method returns the key directly.
CSP does not constrain this Go-side network request.
This violates the intended hostile-frontend credential boundary in DESIGN §55 and K-SEC-002.
The attacker must already be able to invoke the bridge; this does not establish an initial injection vulnerability.

`TestAuditRetargetCannotReuseSavedCredential` reproduces the actual Save → Connect sequence with an in-memory secret backend, the existing Swift test server, and a second loopback receiver.
Only synthetic fixture credentials were used.
The receiver obtained the previously saved key without credential reentry or native approval.

Remediation: bind each saved credential to its approved authentication origin and trust policy.
Treat changes to origin, account identity, or trust as a credential migration requiring credential reentry or a trusted native approval that is not forgeable by the webview.
Invalidate old sessions and bind queued operations to an immutable approved configuration generation.
Record the legitimate endpoint-migration policy in the security ADR.

Acceptance: direct bridge calls cannot retarget an existing key; origin/port/scheme/custom-CA/insecure transitions have explicit tests; legitimate approved migration works; stale queued operations cannot acquire the new identity; no key appears in errors, audit entries, logs, or frontend DTOs.

The locally reviewed [native HTTP message correction](../../experiments/2026-09-12-public-http-errors.md) stops provider reason text from supplying the public Message on normalized native Swift HTTP errors.
An owned HTTP/1.1 fixture reproduced a saved synthetic key in public Connect output; 51 maintained race-tested leaves pass after the correction, and removing its message guard restores the intended failure.
Local status wording and a fixed unknown-status fallback preserve status, classification, retryability and wrapped cause identity.
The cause can still contain provider text; network, unknown and joined-error paths, transaction headers, native credential authority/cleanup and wider P10 acceptance remain open.
This bounded evidence does not establish general redaction, native platform acceptance or valuable-data clearance; current-base verification and protected delivery remain required.

## SEC-02 — the pinned rclone follows a token-bearing redirect

References: `internal/swiftcontrol/tls.go:31`, `internal/swiftcontrol/session.go:74`, and `internal/transfer/rclone/runner.go:337`, `:394`, `:476`.
Native Swift requests refuse redirects, including a transport-level guard needed for the Swift dependency's internal redirect behaviour.
The subprocess byte path has no equivalent enforced policy.

`TestAuditPinnedChildRefusesTokenRedirect` invokes `Runner.CopyTo` using the official checksum-verified Linux/amd64 rclone v1.74.3.
A synthetic storage endpoint returns HTTP 302 to a receiver at a different hostname and port (`127.0.0.1` to `localhost`).
That receiver obtains the synthetic `X-Auth-Token`.
No durable API key is passed to the child, but the active token still grants account access.
This independently reproduces the existing §65.10 redirect concern and violates the native/engine security-policy equivalence.

Remediation: enforce a no-redirect policy on every engine request using a supported, tested engine mechanism, a reviewed upstream change, or a deliberately bounded alternative byte path.
An engine upgrade is not by itself evidence that Swift redirect behaviour is safe.
Do not relax trust or disable validation globally to make a transfer work.

Acceptance: real-child tests for 301/302/303/307/308, same-host and cross-host redirects, HTTPS to HTTP, and each shipped engine binary prove that no token reaches a redirect target.
Keep this test in the engine compatibility gate whenever the pin changes.

## SEC-03 — the configured database durability contradicts recovery's assumptions

References: `internal/db/db.go:35`, `internal/transfer/copy/executor.go:671`, `internal/app/transferservice.go:555`, `:577`, `internal/jobs/jobs.go:348`, and `internal/transfer/copy/verify.go:138`.
The database uses WAL and `synchronous(NORMAL)`.
The executor records a committing fact before a live DLO commit and relies on those facts to keep later recovery from reclaiming its segment prefix.
Those successful SQL commits are not all guaranteed to survive power loss or an OS crash with WAL NORMAL.
SQLite distinguishes that failure mode from an ordinary application-process crash; FULL adds the WAL synchronization needed for each transaction's power-loss durability. [SQLite synchronous documentation](https://www.sqlite.org/pragma.html#pragma_synchronous)

There is therefore a permitted state in which an older captured-prefix fact survives, later committing/committed facts do not, the remote live DLO commit succeeded, and the temporary staging manifest has already been removed.
Recovery sees no surviving commit fact and a 404 at the staging name, then deletes the prefix without proving that a live manifest cannot reference it.

`TestAuditDatabaseDurabilitySetting` confirms actual runtime settings `journal=wal synchronous=1`.
`TestAuditPowerLossMissingCommitFactPreservesLiveDLO` models the surviving-fact state and runs the actual litter sweeper against the existing mock Swift semantics.
The live DLO's logical content changes from 8 bytes to 0 after its segment is swept; the report says one segment swept with no kept prefix or warning.
This is a deterministic recovery-mechanism reproduction, not a claim that a kernel power loss was physically induced.
It is also distinct from the general concurrent reference-scan/exclusivity issue already tracked in the design.

Remediation: define which safety facts must be durably synchronized before remote side effects, then use FULL or an equally explicit proven durability protocol for those transactions.
Recovery must preserve data when provenance is incomplete; absence of a commit fact cannot itself establish absence of a live reference.
Include database/write/fsync failure handling in the recovery contract rather than treating a successful SQL call as unconditional durability.

Acceptance: lost-suffix journal scenarios, remote commit followed by local persistence failure, removed staging names, restart at every commit barrier, and failed cleanup all preserve the live object's bytes.
Add a process-crash test and a separately labelled power-loss durability test or storage fault model.
The fault/barrier harness belongs in this work because the correctness claim concerns the ordering of local and remote irreversible events.

## SEC-04 — partial bookmark deletion leaves a cached authenticated session

References: `internal/bookmarks/bookmarks.go:197` and `internal/app/bookmarkservice.go:93`.
The store commits deletion of the bookmark row and then deletes its credential from the external secret store.
If credential deletion fails, the service returns before invalidating the cached connection.
The bookmark is absent from listings while its session remains usable.
A retry by bookmark ID cannot simply repeat the missing-row deletion and recover the complete intended operation.

`TestAuditFailedCredentialRemovalInvalidatesDeletedBookmark` injects a secret-store Delete failure after a real authenticated connection exists.
The bookmark is then not found in SQLite, yet the cached session still completes an account request successfully.
This is an observable partial-failure lifecycle defect, rather than a claim that the OS keychain normally fails.
The reverse ordering on Save also deserves the same transaction design: it writes the new secret before the SQLite transaction, so a database failure can leave mismatched or orphaned external state (`bookmarks.go:110`).
The Save half was inspected but not separately fault-probed here.

Remediation: use an explicit multi-store lifecycle with recoverable tombstones or immutable credential references and compensation.
Invalidate authority whenever the logical bookmark is removed, even if external cleanup fails, and surface a recoverable cleanup state.
Coordinate edits and deletion with running jobs and session generations.

Acceptance: inject failure before and after each SQL/keychain operation; no deleted bookmark retains usable authority; orphaned keys are discoverable and retryable without reactivating the bookmark; existing credentials survive failed edits; users can distinguish complete deletion from pending credential cleanup.

## SEC-05 — returned storage endpoints do not receive the complete plaintext policy

References: `internal/bookmarks/bookmarks.go:367`, `:395`, and `internal/swiftcontrol/authv1.go:109`.
Entered URLs reject public plaintext HTTP and require explicit insecure opt-in for non-loopback private HTTP.
Returned storage URLs receive syntax checks and an HTTPS-auth to HTTP-storage downgrade check, but not the full endpoint policy.
A permitted HTTP loopback authentication endpoint can return a public HTTP storage URL without insecure opt-in.

`TestAuditLoopbackAuthCannotReturnPublicPlaintext` calls the current validator with loopback auth and `http://192.0.2.10/v1/AUTH_audit` and observes acceptance.
This probe checks policy directly; it does not send a token to that documentation address.
The realistic case is a local tunnel or development auth endpoint returning a storage URL outside the approved local exception.

Remediation: apply one endpoint/trust policy to entered, overridden, returned, and reauthenticated storage URLs, carrying the bookmark's explicit policy into the validator.
Acceptance: loopback exceptions remain loopback-scoped, every nonapproved plaintext target is rejected before token or object traffic, and a returned URL cannot silently widen the bookmark's accepted trust.

## SEC-06 — child lifetime guarantees are incomplete on macOS and Windows

References: `internal/transfer/rclone/childproc_linux.go`, `childproc_other_unix.go`, `childproc_windows.go`, `runner.go:547`, and `main.go:41`, `:66`, `:152`.
Linux creates a process group and requests parent-death SIGKILL.
Other Unix platforms create a group but lack parent-death coupling.
Windows uses immediate `Process.Kill`, with no equivalent job-object lifetime fence or graceful cleanup path.
The application instance lock prevents a second live application instance; it cannot prove that a previous parent's orphaned child has stopped writing.
Recovery may therefore overlap with a surviving writer after parent death on these platforms.
The application entry point also has no evident complete shutdown coordinator that waits for every job/child before database closure.

These are source-verified platform gaps already recognised by the design, not native macOS/Windows runtime reproductions from this Linux session.
Their importance is data integrity: cleanup and restart decisions require quiescence, rather than merely requesting cancellation.

Remediation: specify a cross-platform shutdown protocol; establish owned process lifetime before the child can write; implement the appropriate Windows job object and macOS lifetime strategy; distinguish cooperative cleanup from hard termination and reconcile the latter conservatively.
Acceptance: native tests on all six targets kill the parent at a child-write barrier and prove no subsequent child writes, safe recovery, proper cancellation result, and deterministic release of app locks and keychain resources.

Current disposition: the locally reviewed [Linux child-lifetime evidence](../../experiments/2026-09-12-linux-child-lifetime.md) exercises the pinned direct child at a write barrier on Linux amd64, including an already stopped child when its parent is killed.
An isolated controller verifies process identity and actual SIGKILL/reaping before another process acquires the instance lock and reconciles the retained records; later stage bytes and remote collateral remain unchanged.
Independent omission controls expose missing parent-death coupling and missing kill/reap, and the associated download correction preserves cancellation-only outcomes without hiding independent failures.
This scope still awaits combined-source verification and protected delivery; it does not establish descendant containment, complete application shutdown, keychain shutdown or native macOS/Windows lifetime guarantees.
SEC-06 and the valuable-data hold remain open.

## SEC-07 — the bridge still trusts frontend-held confirmation and broad capabilities

References: `internal/app/mutateservice.go:79`, `internal/app/segmentservice.go:193`, `:232`, `internal/app/selections.go:10`, `:37`, and `internal/app/localtransferservice.go:79`.
Public ACL acknowledgement and dangerous segment deletion confirmation are frontend-supplied booleans or strings.
They express UI intent but do not prove native user authorisation under the explicit hostile-webview model.
Native pickers are a good boundary against arbitrary supplied absolute paths, but their selection tokens persist for the application session, are retained up to a count limit, and lack expiry, consumption, and bookmark/operation binding.
A retained token can be used with a different requested bookmark.
The wider copy plan/execute authority problem is addressed together with this finding in P11 of the remediation plan.

Remediation: establish server-held, operation-specific plans and capabilities tied to account identity, selection, current facts, bounded lifetime, and permitted effects.
Use trusted native acknowledgement where the product requires protection against a compromised frontend, especially for public exposure, credential migration, destructive segment work, and selected local resources.
Recheck affected remote state at execution; ETag checks alone do not authenticate the user's requested scope.

Acceptance: adversarial direct bridge calls cannot skip approval or widen a capability to another account, local destination, object set, or action; expiry and replay are tested; stale plans fail visibly; native picker selections do not grant indefinite general filesystem access.

## SEC-08 — audit retention and failure handling do not meet the documented record contract

References: `internal/app/mutateservice.go:206`, `internal/app/jobrunner.go:79`, `internal/audit/audit.go:82`, and DESIGN §§50–51.
Mutation audit Append occurs after the remote action; failure is only written to stderr, while the user can still receive an applied result.
The audit store automatically deletes oldest rows beyond 10,000 during Append.
The current audit service exposes listing/pruning, but no implemented export-before-discard flow was found despite the design's retention requirement.
Job state persistence likewise has ignored errors and no central panic boundary; P12/P13 integrate that existing outcome-accounting gap.

This is a local support and recovery record, not a requirement for an immutable enterprise compliance ledger.
It still needs to distinguish an action that failed, one that happened but could not be recorded, and one whose outcome is unknown.
Silently pruning the only record impairs investigation of a destructive operation.

Remediation: design durable intent/outcome accounting and an explicit degraded state for record failures; preserve enough information to reconcile remote effects; implement the agreed export/retention policy and visible failures.
Acceptance: SQLite full/locked/error and audit-append failures cannot yield an unqualified success with no recoverable record; reaching retention bounds follows the agreed export policy; summaries remain typed and redacted.
Current substring-based rejection in audit summaries (`audit.go:53`, `:90`) should not be treated as a general secret-redaction system for arbitrary server errors or raw headers.

## SEC-09 — release builds do not prove the required release behaviour

References: `.github/workflows/release.yml:18`, `:41`, `:83`, `:131`, `.github/workflows/ci.yml:195`, and `scripts/webview-spike.sh:30`, `:86`.
The release workflow gates its six build targets on a Linux Go race suite and frontend checks/tests.
It does not itself require the real-rclone compatibility matrix, required SAIO contract run, native webview/socket smoke, or the native keychain/dialog/filesystem/process evidence described in the design.
Separate CI has SAIO infrastructure, but the real-rclone matrix is explicitly not enabled, and release `needs` edges do not establish those other outcomes for the exact release commit.
Producing a binary is not evidence of its native runtime behaviour.

The separate §55 spike script, `scripts/webview-spike.sh` (`make spike`), can exit successfully when Xvfb is missing.
Its socket-check command swallows `ss` errors; a missing inspection tool can therefore be mistaken for the absence of a listener.
The CI smoke runs `scripts/smoke.sh` instead, which fails closed when Xvfb, xdotool, import, curl or python3 is missing.
That smoke only skips its transfer step if the pinned rclone is unavailable; CI explicitly fetches the pin first.
That is acceptable only as an explicitly reported developer skip, not a successful mandatory release assertion.

Remediation: make release eligibility an explicit exact-commit evidence gate including deterministic scenarios, required Swift integration, real engine matrix, actual production bundle smoke, and each platform's native contracts.
Retain the representative deployed-cluster acceptance required by §57.4 as a separately recorded owner/deployment gate.
Required smoke tools must fail or report a machine-readable nonpassing skip when unavailable.
Acceptance: remove a required tool or deliberately break a contract and verify release is blocked; every artefact carries the source/evidence identity; tagged builds cannot bypass those conditions through separate workflow topology.

## SEC-10 — release identity is not stamped

References: `main.go:29`, `internal/app/app.go:31`, `build/linux/Taskfile.yml:29`, `build/darwin/Taskfile.yml:28`, `build/windows/Taskfile.yml:33`, `build/config.yml:14`, `build/linux/nfpm/nfpm.yaml:5`, `build/darwin/Info.plist:15`, and `build/windows/info.json`.
`main.version` defaults to `dev`, with a comment promising a release linker stamp.
The release build flags never set `-X main.version=...` and explicitly disable VCS stamping.
Package and native resource versions are hard-coded to `0.1.0`.
The tag-triggered workflow does not propagate the tag into these values.
Consequently the About dialog and package resources cannot reliably identify which release or fix is installed.

Remediation: derive application version, package/resource versions, source revision, and dirty status from one validated release input; stamp the binary and generate packaging metadata consistently.
Acceptance: build two fixture tags and verify About, binary metadata, macOS resources, Windows resources, RPM/DEB metadata, filenames, and recorded source revision agree and differ as expected.
Record checksums and build provenance with the release evidence.

Current disposition: [PR71](https://github.com/csmart/aeronautes/pull/71) delivered one validated build identity, native/package resource generation and checksum/provenance checks, including the scoped Windows LF correction.
The [maintained evidence and protected delivery](../../experiments/2026-09-09-release-identity.md#protected-delivery-and-hosted-artifacts) covers two disposable fixture releases, all five PR/master checks, six actual unsigned hosted artifacts, both Windows clean-checkout witnesses and the downloaded Linux binary's command-line identity.
The preceding unstamped-source description remains the audit-time before evidence.
Native About, installed packages/upgrades, Windows/macOS runtime observations, signing and full P33 release acceptance remain open.

## SEC-11 — the engine pin has a substantial untriaged advisory inventory

References: `internal/transfer/rclone/runner.go:29`, `internal/transfer/rclone/manifest.txt`, `scripts/fetch-rclone.sh`, `.github/dependabot.yml`, and the dependency evidence below.
The exact official Linux/amd64 engine selected by the repository is rclone v1.74.3, built with Go1.26.4.
An official `govulncheck` binary scan against the downloaded public Go vulnerability database reports 42 distinct advisory IDs, across nine modules plus the standard library.
The [9 September precision correction and six-target assessment](sidecar-assessment.md) establish module-version fallback for these reports; their function fields do not prove binary linkage or enabled callers.
The application Go source scan being clear does not cover the separate engine binary.
The [preserved engine advisory inventory](evidence/rclone-advisories.json) records the 42 IDs, scanner provenance and original JSON checksum.
The full reported traces remained in the audit scratch directory; the exact binary scan is reproducible from the command below.

This count is not 42 confirmed exploitable Aeronautes vulnerabilities.
The general-purpose engine contains unused servers and backends, and binary scanning does not establish enabled-command/flag reachability.
For example, the local-encoding traversal advisory requires nondefault encoding and explicitly excludes the demonstrated path under default encoding; Aeronautes has a fixed argument surface and excludes ambient rclone configuration. [Rclone local-encoding advisory](https://github.com/rclone/rclone/security/advisories/GHSA-7p4m-qxvv-g567)
The metadata-permission issue requires metadata restoration; a reported local-backend finding alone does not prove this app enables that mode. [Rclone metadata advisory](https://github.com/rclone/rclone/security/advisories/GHSA-945v-v9p3-v5xw)
The TLS KeyUpdate advisory describes a malicious client against a server, so it must not be presented as a confirmed Swift-client exploit solely from a generic TLS finding. [Go TLS advisory](https://pkg.go.dev/vuln/GO-2026-6090)
Conversely, the old standard-library URL parser used by HTTP code has a published resource-exhaustion issue that warrants actual redirect/input reachability analysis. [Go URL advisory](https://pkg.go.dev/vuln/GO-2026-6218)

The maintenance finding is that the manual pin is excluded from automatic dependency updates and the compatibility measurements are not a required behavioural CI gate, while known fixes now exist beyond that pin.
The audit subsequently ran the existing Linux/amd64 matrix; its observations and non-gating semantics are recorded in [verification.md](verification.md#completed-local-two-account-validation).
Before shipping, every retained advisory needs an evidence-backed disposition for the exact Swift/local surfaces and shipped binary.
An immediately reachable security defect should be promoted to P1; this audit already supplies an independent P1 redirect reproduction in SEC-02.

Remediation: inventory and scan all six exact sidecars, triage enabled paths and flags, select a patched engine/toolchain version, and pass the full compatibility/security matrix before changing the pin and both checksum layers.
Decide how the project handles an urgent engine security update when distribution packaging has not caught up.
Do not remove checksum enforcement, allow arbitrary version mismatch, or assume a newer binary fixes the app's separate contracts.
Acceptance: retained advisory dispositions name their reachability evidence; the chosen pin passes the real-engine redirects, staging/commit, cancellation, path, TLS, and fault tests on all targets; update ownership and latency are documented.

Current evidence: all six accepted v1.74.3 sidecars retain 42 reported IDs; the six assessed v1.75.1 candidate artifacts report one at the same module-version precision.
The [complete comparison and OpenPGP follow-up](sidecar-assessment.md) retain the unexplained Linux modified-source metadata, incomplete source closure, unperformed full candidate compatibility gate and pending native/application/npm scopes.
On 12 September 2026 [the dispositions record](../../experiments/2026-09-12-advisory-dispositions.md) brought the 42-row proposal into the repository for review, every row keeping its draft class with its source citations, and [the release evidence record](../../experiments/2026-09-12-release-evidence.md#offline-rescan-covering-the-added-text-dependency) reran the three application scan scopes offline at the 9 September database snapshot with the added text dependency in scope and zero findings, which says nothing about advisories published later.
No candidate pin or advisory disposition is accepted, and SEC-11/P31 and the valuable-data release hold remain blocking.

## SEC-12 — Linux package installation does not guarantee a usable engine

References: `build/linux/nfpm/nfpm.yaml:24`, `:31`, `:39`, `.github/workflows/release.yml:70`, `internal/transfer/rclone/runner.go:205`, and `docs/RELEASING.md:29`.
Linux packages depend on unversioned distribution `rclone`, and Linux tarballs do not bundle the pinned sidecar.
The application refuses any engine version other than v1.74.3.
A successful package installation or ordinary distribution upgrade can therefore leave transfers unavailable.
The design deliberately tracks Fedora's engine pin, so this is an unresolved distribution compatibility contract rather than a reason to disable validation.
The public Fedora package page checked on this date lists 1.74.3 for Fedora 44 and a newer Rawhide build, illustrating that package availability varies by release. [Fedora rclone packages](https://packages.fedoraproject.org/pkgs/rclone/rclone/)
No current Ubuntu version is asserted here; primary package-page retrieval was incomplete.

Remediation: choose a supported distribution/version envelope with satisfiable exact dependencies, a managed private sidecar, or another explicit tested distribution policy.
Document install, upgrade, and repair behaviour and align the security-update policy from SEC-11.
Acceptance: clean installation and ordinary upgrade on every advertised Linux release either provide the accepted engine or reject the unsupported configuration before presenting transfer capability; version drift has an actionable diagnostic.

Current disposition: [the release evidence record](../../experiments/2026-09-12-release-evidence.md#native-journeys-on-linux-amd64) observed the version refusal natively in the production binary on Linux amd64, with both versions named before any job existed; the distribution envelope, installation, upgrade and rollback remain open.

## Previously recorded credential/session work still applies

The separate W-05/W-15 atomic credential/session problem remains part of the plan; SEC-01 does not replace it.
`internal/swiftcontrol/session.go:135` and `:149` read URL and token through separate snapshots, and `internal/app/transferservice.go:295` assembles those separate values into a child remote.
`ForceReauth` at `session.go:157` clears the underlying connection and `authedAt`, but `RefreshIfStale` at `:117` preferentially uses the retained advertised expiry when one exists.
That can avoid the forced refresh of a rejected token.
The earlier DESIGN §65.10 review records the reproduction and wider concurrent-refresh concern; this audit rechecked the source but did not rerun that historical race probe.
P10 must cover rejected-token invalidation and atomic URL/token/expiry/trust/generation acquisition, with deterministic simultaneous native/child refresh tests and queued bookmark changes.

## Positive findings and explicit product decisions

- SQLite stores public bookmark metadata and credential-presence state, while credential values are held behind a separate secret-store interface.
  There is no ordinary bridge getter for stored credentials.
  OS-keychain probing performs a write/read/delete check and the fallback session backend is visible to the user.
  The pinned macOS go-keyring implementation uses `/usr/bin/security -i` and stdin for secret material; an argv-secret leak was not found there.
- The engine gets a short-lived token through an allowlisted environment, an empty isolated configuration, a fixed argument surface, bounded retries, and explicit positional separation.
  It receives no durable API key.
  The runner rejects live Swift destination names and enforces staged writes.
  Sidecar checksums fail closed and multi-remote trust identity is checked instead of globally widening TLS acceptance.
- Native Swift handling validates endpoint syntax, refuses HTTPS downgrade, blocks redirects, and redacts temporary-URL metadata fields.
  These protections are valuable even though SEC-02/05 identify equivalence gaps.
- The bridge binds typed services, not raw engine/store factories; service inventory tests check the boundary.
  Frontend assets are local and the CSP restricts scripts and connections; no remote CDN or raw HTML-rendering pattern was identified in this bounded inspection.
- Database writes are serialized, migrations have tests, and single-instance file locking exists across the platforms.
  CI actions and the SAIO image are pinned, upstream engine archives and embedded binaries have checksum checks, and Dependabot covers Go, npm, and Actions.

Unsigned pre-release artefacts are an explicit owner decision in `docs/RELEASING.md:10` and DESIGN §65.6.
This report does not reclassify that accepted prototype decision as an accidental vulnerability or invent permission to configure signing.
Signing/notarization, provenance, and user-facing installation trust remain distribution work before a broader release policy is claimed.

The encrypted-file vault ADR is proposed and requires an owner choice; the visible session-only fallback is safer than silently writing plaintext credentials.
Missing vault support is a suitability gap on keychainless Linux, not evidence of a stored-key leak.
Flatpak portability, native portal/keychain behaviour, and supported deployment coverage remain explicitly gated work.
No updater is implemented; manual update can be an acceptable product choice if versioning, compatibility, rollback, and user communication are specified.

Additional release/support work to record without treating every omission as a vulnerability: a public security-reporting policy and ownership; explicit least-privilege release workflow permissions (the retrieved repository default is read-only, with workflow PR approval disabled); complete third-party notice inventory and packaging; privacy-conscious support bundles; backup/restore semantics for SQLite and external credential references; and refusal or a deliberate policy when an older app opens a database with future migrations.
The current migration code does not provide an explicit future-schema compatibility refusal (`internal/db/migrate.go:37`).
No legal compliance conclusion is drawn from the notice inventory alone.

## Verification and reproducibility

### Direct behavioural probes

The [portable reproduction instructions](probes/README.md), [four security probe sources](probes/manifest.json), and [original log](evidence/security-probes.log) are preserved with this audit.
The original Go overlay run used `-race -count=1` and exited 1 with five intended invariant failures and one passing SQLite-setting observation.
It produced no race-detector report.
Local mock server permissions were approved; only synthetic credentials and temporary state were used.
The verified rclone executable SHA256 was `9700aa1273ac73d6d0833c43ba63fe830516422cb131960b8c1a24ced789cba0`.
The preserved combined probe suite was rerun successfully in the sense of reproducing its expected failures; it is distinct from the passing [normal baseline](verification.md).

### Go advisory scans

Scanner: official `golang.org/x/vuln` v1.7.0 `govulncheck` installed in `<audit-scratch>/audit-bin`.
Application analysis toolchain: Go1.27.1.
To avoid submitting a private dependency graph, downloaded the complete public Go vulnerability database source archive and ran the scanner against a local flat OSV directory.
Only `data/osv/GO-*.json` entries were extracted; no source archive code was executed.
Source archive: `https://codeload.github.com/golang/vulndb/tar.gz/refs/heads/master`, downloaded 2026-09-05.
Archive source commit from its Git archive metadata: `7106aff44bfd324e2f3ec6b0f0927ee03a835325`.
Archive SHA256: `e557c53412bd9a01ef9f4908af35c5625e63d1adb6e048452847edcf9d09df2e`.
Local database: `<audit-scratch>/go-vuln-flat`, containing 4,378 OSV entries.
The source OSV entries use placeholder modified timestamps, so the scanner's `db_last_modified: 0001-01-01T00:00:00Z` is not a claim about freshness; the downloaded archive commit and checksum identify the evidence snapshot.

Common application scan command:

```sh
env PATH=<audit-scratch>/go/bin:/usr/bin:/bin \
  GOCACHE=<audit-scratch>/go-cache \
  GOMODCACHE=<audit-scratch>/go-mod \
  GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  <audit-scratch>/audit-bin/govulncheck \
  -db file://<audit-scratch>/go-vuln-flat \
  -show verbose ./internal/...
```

Linux/amd64 `./internal/...`: 19 root packages and 12 modules including the main module, no findings, exit 0.
With `GOOS=darwin GOARCH=arm64`, `./internal/...`: 19 root packages and 13 modules, no findings, exit 0.
With `GOOS=windows GOARCH=amd64`, `./...`: 21 root packages and 17 modules including main/Wails, no findings, exit 0.
Cross-OS source analysis is not native execution.
No-advisory results are bounded by this database snapshot, loaded package graph, and scanner analysis; they do not certify memory safety, application logic, native WebKit/WebView2 libraries, or all build-tool dependencies.

Exact engine binary scan command:

```sh
env GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  <audit-scratch>/audit-bin/govulncheck \
  -db file://<audit-scratch>/go-vuln-flat \
  -mode=binary \
  <audit-scratch>/rclone-pin/rclone
```

Text output exited 3 and reported 42 advisory IDs at symbol level.
Repeating with `-json` persisted `<audit-scratch>/rclone-govulncheck.json`; JSON mode exited 0 as expected and contains 566 finding messages over 42 unique IDs, not 566 separate vulnerabilities.
The scanner config records `scan_level=symbol` and `scan_mode=binary`.
Only this Linux/amd64 sidecar was binary-scanned in this audit.

### npm limitation and primary advisory spot checks

The lockfile has 157 package entries; relevant pinned versions inspected include Svelte 5.57.0, Vite 8.2.2, Vitest 4.1.11, and Wails runtime 3.0.0-beta.15.
The initial sandboxed `npm audit --json --package-lock-only` could not reach the registry.
Automatic approval review rejected the network-enabled retry because it would submit the project's dependency metadata to npm without explicit permission.
An anonymous attempt to establish that this exact lockfile was already public returned 404.
Permission was requested; none had arrived when this report was written, and no network-enabled retry was made.
A fresh full npm advisory submission therefore remains unperformed.
A later authorised GitHub check confirmed Dependabot alerts are enabled and returned zero open alerts for this repository.
That is useful evidence about GitHub's existing dependency analysis, not a replacement for a fresh complete npm scan or proof that every dependency is safe.
See the [remote evidence record](verification.md#retrieved-github-evidence).

Public primary-source spot checks found the inspected Svelte, Vite, and Vitest versions beyond the fixed versions of the following specific advisories: [Svelte DOM clobbering](https://github.com/sveltejs/svelte/security/advisories/GHSA-rcqx-6q8c-2c42), [Vite Windows filesystem deny bypass](https://github.com/vitejs/vite/security/advisories/GHSA-fx2h-pf6j-xcff), and [Vitest advisory](https://github.com/vitest-dev/vitest/security/advisories/GHSA-82fw-gwwq-j7x9).
Those checks cover named published advisories only and cannot replace the complete graph scan.

## Remediation order and harness fit

1. Preserve the valuable-data release hold and make backend authority, credential-origin binding, and endpoint/redirect policy concrete before widening features.
   Turn SEC-01/02/05 probes into permanent regressions during their respective fixes.
2. Define durability, outcome accounting, recovery ownership, and child quiescence as one consistent contract, then fix them in reviewable slices.
   Include SEC-03/04 failure cases and native child lifetime tests.
3. Extend the fault/barrier harness specifically for local safety-fact writes, remote live commits, removed staging names, failed audit writes, keychain partial operations, parent death, and child termination.
   Label deterministic simulated faults, process crashes, OS/power-loss models, real engines, and native platforms separately so a passing mock cannot stand in for a stronger claim.
4. Triage and update the pinned engine under the real compatibility matrix, align Linux distribution policy, and complete the remaining npm check only with the pending permission or a fully local advisory database.
5. Implement release identity, exact-commit evidence gating, retention/export and support recovery, then run clean install/upgrade/native acceptance on advertised targets and a representative deployed Swift system.

The harness work belongs in these findings because multiple release-blocking invariants can only be judged at failure boundaries.
It should support the defined safety contracts; a broader harness redesign that has no bearing on these acceptance checks can remain a separate workstream.

## Untested limits

This domain audit did not run real desktop windows, macOS or Windows binaries, native keychain/portal dialogs, a physical power-loss experiment, signing/notarization, installer upgrade tests, a real production SwiftStack account, broad fuzzing, or penetration testing against a deployed app.
It did not scan real user secrets or inspect private keychain contents.
The companion audit documents cover the full application baseline, UI, data safety, harness breadth and completed local SAIO run.
This report deliberately distinguishes direct reproductions from source-derived risks, public advisory inventory, and unresolved product decisions.
