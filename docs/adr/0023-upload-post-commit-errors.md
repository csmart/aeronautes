# ADR 0023 — report upload errors after commit

**Status:** Proposed for independent local review under the authorised audit remediation plan.
**Scope:** The first local-upload P12/P21 error-handling slice after ADR 0020 containment.

## Problem and oracle

The [current-master fault reproductions](../experiments/2026-09-08-upload-recovery.md) show that local upload reports `complete` without a visible error when its committed recovery record is refused or its staging DELETE fails.
The rejected committed record also fails to prevent the subsequent staging DELETE.
The live bytes and earlier recovery records survive these probes; the old DS-10 recovery-row clearing is already contained by P04.

ADR 0020 requires failed persistence to reduce authority and retain uncertainty.
DESIGN §28.6 requires durable commit facts and explicit errors, while the job counters must continue to describe the data that actually passed the existing verification.
A cleanup or bookkeeping error after a verified live write must not turn that copied object into a fictitious failed transfer or silently complete the whole job.

## Decision

Keep per-object transfer failures separate from issues observed after the live commit.
The local report will carry an independent post-commit issue count and a bounded sample containing the affected live name, stage and error.
Count every observed issue independently of the sample limit; do not rebuild the total from the sample length.
This internal report does not add a bridge method or database schema.

When the DLO committed-record callback returns an error, record that issue and prohibit staging deletion for the affected object.
Read-only live verification may still run; a successful existing verification still contributes the copied/verified object and byte counts.
If verification also fails, retain both the transfer failure and the bookkeeping issue without adding the bookkeeping issue to `ObjectsFailed`.
Later objects retain their own existing before-commit admission requirements; this slice grants no new write authority or recovery retry.

After successful live verification, handle a staging DELETE error explicitly, retain the recovery facts and report the exact attempted staging name in the issue text.
An authoritative not-found response means that attempted staging name is already absent and does not create an additional cleanup issue.
Other errors remain unresolved, including a failed response after a deletion may have landed; do not retry deletion or claim the staging object necessarily exists.
This does not establish exclusive ownership or make ordinary transfer-time deletion safe against outside aliases; P15 retains that separate boundary.

A local job with these issues is `partial` and carries a visible error summary even when every selected object's live bytes passed verification.
Keep copied/verified/byte counts from those successful checks, and reserve `ObjectsFailed` for the existing transfer-failure channel.
Preserve an issue summary alongside a primary run error or cancellation; do not hide the earlier issue behind the later terminal reason.
The existing retained-record counts remain counts of records, not physical residue or issue counts.

No recovery row is cleared and no automatic reclamation is restored.
The broader persistence of exact identity outcomes, audit atomicity, terminal-write failure handling, power-loss proof, historical authority, verification strength and six-platform guarantees remain P12/P13/P16 and release work.

## Implementation and verification

1. Add and exercise the local report/error handling, then the service's terminal summary, in one coherent implementation commit.
   The shared local report must leave successful downloads and native upload behaviour unchanged.
2. Maintain the current bound-service reproductions on mock Swift and SAIO with the actual pinned rclone.
   Cover successful plain/DLO uploads, rejected committing and committed records, first/last staging DELETE failures, absent staging names and landed deletions whose response fails.
   Observe exact live/staging bytes, manifests and segments, both owned account namespaces, local files, recovery facts and the job/audit result independently.
3. Reopen the recorded database in an actual child test process after execution and checked close; distinguish that process evidence from the initial in-process second-handle observation and from power-loss tests.
   Retained facts and visible terminal issues must survive without remote recovery activity.
4. Exercise more than 200 post-commit issues so the total cannot silently truncate with the displayed sample.
   Include a mixed transfer failure and post-commit issue, plus cancellation or a primary error after an issue.
5. Restore each ignored error independently and require the maintained tests to fail.
   Also catch deletion after a rejected committed record, issue-count truncation, hidden terminal messages and incorrect transfer counters.
6. Require appropriate full suites, independent local approval, all five checks on the current PR head and base, protected merge and all five resulting master checks.

This is a bounded DS-10 follow-up, not closure of P12, P13, P21 or the valuable-data release hold.
