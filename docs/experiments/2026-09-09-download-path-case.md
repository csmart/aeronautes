# Download case-folded file and ancestor conflicts

## Scope and oracle

This is a bounded prerequisite under the existing portable namespace policy, [K-XFER-011](../REQUIREMENTS.md), [DESIGN §27.5 and §65.9 item 13](../DESIGN.md), with the admission boundaries preserved by [ADR 0034](../adr/0034-download-destination-admission.md).
A selected ordinary file `Foo` and a selected file `foo/child` require the same case-folded local identity to be both a file and a directory.
An ordinary file `Foo` and a zero-byte directory marker `foo/` have the same conflict.
The complete selection must be refused before service job/engine admission and before the direct executor's first item or effect, even if the current filesystem can represent the distinct raw spellings.

The scoped correction applies the existing comparison identity to planned files and every required ancestor at both boundaries.
Selected object names, local paths, diagnostic names and recorded identities keep their exact spelling.
No normalization, escaping, directory-only alias or reserved-name policy is added.

## Source finding and planned reproduction

The source inspection found that preflight lowercases complete names but stores and looks up file ancestors with raw case-sensitive keys.
Direct executor entry repeats the same division.
The inspected boundaries are [PreflightDownload](../../internal/transfer/local/local.go) and [Download](../../internal/transfer/local/download_execute.go) at the approved parent `dffe875e0cb5890d8c485085f787484ef4e0b597`.
For the pair above, the complete-name keys `foo` and `foo/child` differ, and the raw ancestor `foo` does not match the planned file `Foo`.
An existing-state filesystem check cannot discover a conflict between two still-absent planned entries.
This paragraph describes a static finding; runtime reproduction and outcomes will be recorded only after the maintained producers run.

Use dedicated owned selected, outside-sibling and unrelated local fixtures, both owned remote namespace scopes and full source payload bytes.
Add a safe first item `000-safe.bin` or marker `000-safe/` before the conflicting pair so per-item refusal or early materialization cannot satisfy the test.
Keep marker conflicts mixed with an ordinary blocking file; a pair of markers is not this file-versus-directory case.
Cover ordinary engine files, native files selected under the existing unsafe-prefix route and directory markers on mock Swift and real Swift.
Exercise immediate and deeper ancestors, nonadjacent names and direct input ordering in focused local tests.

## Required observations

Service cases call both PreflightDownload and StartDownload through an actual picker-issued token.
A refusal requires rejecting preflight output, an empty returned job id and refusal error, unchanged job/recovery/item/observation/transfer-audit counts, no selected content GETs, no local delta and unchanged owned remote namespaces and write trace.
Listings and marker-classification metadata reads remain permitted planning work.
When the baseline or a broken control unexpectedly returns a job id, wait for it and independently read public and durable job state, complete item pages and matching audit, preserving the failing safety assertion.
A truthful partial result or late collision is not successful preflight refusal.

Direct cases construct exact plans with real mock source payloads and the existing local engine harness, with and without recorders.
They require refusal before a non-nil report, admission, execution, verification or progress callbacks, source requests, Copy invocation and local effects.
The app's existing engine-construction spy separately proves service refusal before engine setup, with a safe control that reaches the configured sentinel.
The system stage callback runs after Copy returns; it remains a returned-Copy witness, not a direct child-start counter.
Remote snapshots cover the two owned namespace scopes, not entire accounts.

Passing controls preserve two distinct children under one exact parent, a compatible directory marker plus child and disjoint file/subtree paths.
For accepted controls, inspect complete file bytes and permitted local deltas independently of exact final job, item and audit facts.
Preserve the existing whole-leaf case-fold, exact-case ancestor and selected-root-symlink regressions.

## Verification and retained evidence

First run the maintained safety assertions against unchanged production and retain their actual failing exits alongside passing controls.
Record the exact source/test hashes, commands, full raw output and provider configuration without secrets.
After the bounded correction, require the same assertions to pass and narrowly broken preflight/direct ancestor guards to fail their relevant boundary checks.
Then run the relevant compatibility checks and complete inventory, scenario and coverage verification under the existing finite budgets.
Independent local review is required for every logical commit and after any authorised final-base rewrite, before protected PR delivery and all five resulting master checks.
No runtime result, test count, coverage, native platform outcome or delivery is claimed by this plan.

## Remaining limits

Portable namespace rejection and native filesystem acceptance are separate claims.
These fixtures need no symlink or concurrent actor and do not establish stable selected-root authority, race-safe placement or cleanup ownership.
Unicode equivalence, reserved names, forbidden characters, rclone encoding collisions and representative native target evidence remain wider P19 work.
P18, P19, P20, P30 and the valuable-data hold remain open.
