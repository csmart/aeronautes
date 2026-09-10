# Canonical name equivalence for download collisions

## Scope and oracle

This is a bounded prerequisite under the existing portable namespace policy, [K-XFER-011](../REQUIREMENTS.md), [DESIGN §27.5 and §65.9 item 13](../DESIGN.md), with the admission boundaries preserved by [ADR 0034](../adr/0034-download-destination-admission.md).
[DS-06](../audits/2026-09-05/data-safety.md) requires collision identity for complete paths and every ancestor, and its remaining open list names Unicode normalization and equivalence first.
The delivered [case-fold refusal](2026-09-09-download-path-case.md) and [portable component refusal](2026-09-10-portable-download-names.md) compare names through Go's Unicode lowercase, which already folds non-ASCII case: `Étude` and `étude` produce one identity today and are already refused as a collision.
What that relation does not fold is normalization form, and the two dimensions are independent: a precomposed upper-case spelling and a decomposed lower-case spelling of one name still produce different identities.

Two Swift names that differ only by Unicode normalization form are distinct byte sequences and therefore distinct today.
On a target filesystem that normalizes or compares without regard to normalization form, both spellings address one location, exactly as `Foo` and `foo` address one location on a case-insensitive filesystem.
The accepted policy is one namespace rejection rule for every target, so such a pair must refuse the complete selection rather than materialize differently per platform.

The scoped correction changes the collision identity alone.
It does not add a per-name refusal: a single name in any normalization form stays selectable, and only a collision between two selected names is refused.
Selected object names, local paths, diagnostic names and recorded identities keep their exact spelling; nothing is trimmed, normalized, escaped, encoded or renamed on any path that reaches the service, the executor, the destination or a record.

## Source finding

The preflight builds its collision identity with a Unicode lowercase of the relative name at `internal/transfer/local/local.go:194` for whole-path twins, `:277` for the file set and `:291` for the ancestor lookup.
The direct executor repeats the same identity at `internal/transfer/local/download_execute.go:52` and `:65`, before its first item or effect.
Both boundaries therefore treat canonically equivalent spellings as unrelated names.

## Before evidence on unchanged production

A probe against the owned mock, with the delivered production unchanged, seeded a safe control and the two normalization forms of one accented name below one prefix.
The precomposed stem is six bytes and the decomposed stem is seven bytes, so the two objects are distinct in Swift and distinct to every delivered check.

| Probe selection | Preflight items | Preflight problems |
| --- | ---: | ---: |
| Safe control, precomposed `étude.txt`, decomposed `étude.txt` | 3 | 0 |
| Safe control, precomposed file `étude`, decomposed ancestor `étude/child` | 3 | 0 |
| Safe control, `Foo`, `foo/child` | 3 items offered, refused | 1 |

The first row is the twin case: both spellings enter the plan, so the selection promises two paths that one normalizing destination cannot both preserve.
What happens later on such a destination is not exercised here, and placement publishes exclusively and records a conflict on an existing target, so a later refusal or partial effects are as consistent with this evidence as a lost object.
The demonstrated defect is the admission itself, against a whole-selection contract that requires refusal before any effect; no data-loss or record-loss outcome is claimed.
The second row is the ancestor case: the plan asks one normalizing location to be both a file and the directory holding a child.
The third row is the delivered case-fold control, refused as `file-vs-prefix`, which confirms the probe exercises the real boundary and that this slice must not disturb it.

Direct item validation accepts both spellings independently, so the executor boundary offers no separate protection.
The probe ran on Linux, where both spellings can be materialized side by side, which is precisely why a Linux-only execution test would miss the policy failure.

## Two collision relations, not one key

A single normalized key cannot carry the established relation, and the reason is structural rather than a matter of picking a better order.

The established lowercase equates the precomposed dotted capital I with a plain `i`, because Go maps that character to `i` with no mark.
Canonical equivalence equates the same character with the decomposed spelling, a capital I followed by a combining dot above.
The established lowercase then equates that decomposed spelling with a lowercase i followed by the same mark.
Any single equality key that preserves both must therefore, by transitivity, equate a plain `i` with `i` plus a combining dot above.
Those two are neither a lowercase pair nor canonically equivalent, and nothing here shows a target filesystem conflating them, so a single key necessarily refuses more than the relations contain.

Measured across Go's Unicode tables, one thousand four hundred and eighty-eight runes have a lowercase that differs from themselves.
A key built as canonical decomposition, then lowercase, then canonical decomposition again loses exactly one of those pairs on its own: the precomposed dotted capital I against a plain `i`.
That is a refusal the delivered identity makes today, so such a key used alone would remove an existing protection while adding a new one.

The correction therefore keeps two relations side by side and closes neither into the other.

1. The established lowercase relation, computed exactly as it is today and left untouched, so every pair refused today is refused by construction.
2. A canonical caseless relation: canonical decomposition through the strict helper, then that same lowercase, then canonical decomposition again.

Two selected names collide when their exact bytes differ and either relation holds between them.
The second relation subsumes plain canonical equivalence, because canonically equivalent names share a decomposition and therefore share every later step, and it additionally covers pairs that differ in both dimensions at once, such as a precomposed upper-case spelling against a decomposed lower-case one.
The first relation is what carries the precomposed dotted capital I against a plain `i`, which the second loses on its own.

Measured against the same one thousand four hundred and eighty-eight pairs, the two relations together lose none.

The rule is the pairwise union of those two relations and is deliberately not the transitive closure of that union.
One consequence is recorded rather than hidden: a plain `i` and `i` followed by a combining dot above are permitted together, because neither relation holds between them, even though a chain through the precomposed character would join them.
Nothing else is added: `resume` and `résumé` keep separate identities, a ligature stays distinct from its two letters because compatibility equivalence is excluded, and `ß` stays distinct from `ss` because the established lowercase does not fold it.

Two separate contracts follow, and they must not be confused.
The strict canonical helper, used for the decomposition steps, must agree with canonical equivalence exactly, so it must never report as equivalent a pair that is not.
Neither collision relation carries that oracle, because both deliberately collide case-only pairs that are not canonically equivalent.

Both relations are applied at all five existing sites and nowhere else, so whole-path twins, the file set and the ancestor lookup gain the second relation at both the preflight and the direct boundary.

## Dependency route

Canonical composition is not available in the Go standard library, and it cannot be approximated safely.
Stripping combining marks would refuse `resume` against `résumé`, which are distinct on every filesystem, and refusing every name that carries a combining mark would refuse entire scripts that use them correctly.
An unsound approximation in either direction is worse than the current gap, so the correction needs a normalization implementation.

`golang.org/x/text` version 0.39.0 is already a required module in this project's graph, through `github.com/wailsapp/wails/v3` version 3.0.0-beta.16.
It is absent from `go.sum` only because no current build path imports one of its packages.
Adopting `golang.org/x/text/unicode/norm` therefore promotes an already-selected module to a direct requirement rather than admitting a new upstream project, which is the same shape as the reviewed promotion of `golang.org/x/sys`.
Selection in the module graph is not the same as reachability, and no package of it is compiled into this application today, so its packages and sums enter the build for the first time here and receive review on that footing.

The alternative is an in-tree canonical equivalence built from the Unicode data files.
That is a large table-bearing addition with its own correctness surface and its own review burden, and it duplicates a maintained implementation the graph already carries.
This plan proposes the promotion and asks review to settle it before any code is written, because importing the module adds executable packages and sums to this application's build.
The author of record favours the promotion on two grounds: the module is already selected in the graph through the toolkit, so the version in question is one the project already carries rather than a new upstream choice, and a hand-written canonical composition would put a correctness risk in the one place this slice cannot afford one, since the whole premise is that two spellings are one name.

## Strict equivalence is not a bare library call

The library's normal forms are stream-safe, and that is not the same relation as canonical equivalence.
In the proposed version, a run of more than thirty non-starters is broken by an inserted combining grapheme joiner, so two inputs that differ by a real joiner can share one normal form.
A probe against the exact cached module bytes shows it: with a base letter followed by thirty-one combining acutes, and the same letter followed by thirty acutes, a real joiner and one more acute, the inputs differ at sixty-three and sixty-five bytes, and both the composed and the decomposed forms of the two are equal.
Those two names are not canonically equivalent, so a bare comparison of normal forms would refuse a genuinely distinct pair and break this plan's collision-only contract.

The implementation must therefore provide a named canonical helper whose result agrees with strict canonical equivalence, not an unexamined call into the library.
Its dedicated tests must cover long combining runs with and without a pre-existing joiner, composition and decomposition, canonical reordering of marks, Hangul syllables and singleton decompositions, and must assert that the helper never reports as equivalent a pair that is not canonically equivalent.
That assertion belongs to the helper alone; the combined collision relation is separately required to keep colliding the case-only pairs the delivered identity already refuses.
Refusing long runs, or refusing combining marks, is not an acceptable substitute, because either would contradict the collision-only policy stated above.

## Required observations

The implementation must show all of the following, and each refusal must happen before any marker, staging, job, item, audit or filesystem effect.

Refused, as a whole selection, at both the preflight and the direct boundary: a precomposed and a decomposed spelling of one file name; the same pair below a shared parent; a precomposed file against a decomposed ancestor; a decomposed file against a precomposed ancestor; the same conflict expressed through a directory marker; and a conflict whose two members are separated by an unrelated sorting name.
Each refusal must name the exact selected spelling it refuses and must not report a rewritten or normalized name.

Refused as well, to prove the established relation survives and the second one reaches what it claims: a non-ASCII case-only pair such as `Étude` against `étude`; a capital I with a combining dot above against a lowercase i with the same mark; the precomposed dotted capital I against a plain `i`, which the second relation loses and the first carries; the precomposed dotted capital I against its decomposed spelling; and a mixed pair differing in both dimensions, a precomposed upper-case spelling against a decomposed lower-case one, which only the second relation carries.
Each of those five must appear in the twin matrix and in the ancestor matrix, in both ancestor directions, at both the preflight and the direct boundary, and each test must state which relation is expected to hold so a pair cannot pass by accident through the simpler one.

Admitted, and recorded as the bounded consequence of not closing the union: a plain `i` beside `i` followed by a combining dot above.

One further check is required and is not a matter of chosen examples.
For every rune whose lowercase differs from itself, the correction must still collide that rune with its lowercase, so the test walks the whole range rather than a selected list.

Passing controls, which must keep their exact plan and their exact recorded identities: a single precomposed name alone; a single decomposed name alone; `resume` beside `résumé`; a ligature name beside its two-letter spelling; and the delivered case-fold and portable-name fixtures, which must keep their present outcomes unchanged.
A safe first member must precede every refused case, so that a refusal that leaked an effect would be visible as a materialized first member.

## Required proof

Before evidence must be reproduced on unchanged production through overlays that retain the actual exits, showing the intended failures at both boundaries.
At least three narrow broken-guard overlays must each fail only their own boundary: one removing the preflight twin identity, one removing the preflight ancestor identity, and one removing the direct executor identity.
The complete internal race suite must pass with its inventory reconciled against the delivered name count, alongside vet, gofmt, the Windows and macOS vets and internal builds, and a module tidy that leaves the promoted requirement exact.
Actual service evidence must cover the engine, native and marker routes on both owned providers, with complete local and remote deltas, full job, item and audit facts, and the distinction between direct invocation and returned-child witnesses preserved.

## Implemented correction and regressions

The correction adds one file holding the two identities and the strict canonical helper, and changes the five existing sites to consult both relations.
The preflight keeps one group map per relation and reports one problem per collision group, emitting the lowercase groups first so an existing case collision keeps its exact kind and wording; a group only the second relation finds is reported as a name-form collision.
The ancestor lookup consults both file maps and flags by the selected file, so one file conflicting under both relations is reported once.
The direct executor repeats both relations before its first item or effect, exactly where it repeated the single one.
No boundary, ordering, exact-byte duplicate check or selected spelling changed, and `golang.org/x/text` moved from a graph selection to a direct requirement, adding one line to the module file and two to its checksums.

The helper carries its own battery: precomposed against decomposed, canonical reordering, Hangul, a singleton, a nested decomposition, the precomposed dotted capital I against its decomposed spelling, and, as pairs that must stay distinct, names that merely look alike, a same-class mark reordering, a compatibility ligature, a preserved real joiner, and a thirty-one mark run against the same run broken by a real joiner.
Every fixture that must be distinct is asserted to differ in bytes first, because a decomposed sequence typed as a literal is normalized on its way into a source file and would otherwise compare one string with itself.

The boundary matrix carries all five pairs as twins and again as ancestors, and each ancestor pair appears in both directions, so the selected file and its required ancestor exchange spellings rather than relying on item order, which does not exchange those roles.
Every collision case also records which single relation is expected to carry it, and the test asserts that membership: a case declared for the caseless relation fails if the lowercase relation already holds between its two names, so no case can pass through the unintended relation.
The accepted controls are the distinct look-alikes, sharp s against double s, a ligature against its letters, a lone decomposed spelling, a lone precomposed spelling, a shared parent, and the admitted plain `i` beside `i` with a combining dot.

Accepted controls assert the whole outcome rather than admission alone: the exact report, routed counts and engine calls, per-item admission, execution and verification observations with staging identity, every owned entry unchanged, each selected name present with its full bytes, the permitted directories, and nothing else.
At the service layer they additionally assert job state and counters, item totals and identities, audit facts and history counts, the local delta and the per-route source reads.

They assert all of that because an earlier revision did not, and the gap was invisible until it was attacked directly.
Those controls checked only that admission returned without error and that the selected names appeared in the plan, which cannot distinguish a real download from an executor that admits everything and then does nothing.
A mutant that keeps every admission check and returns an empty successful report passed the entire matrix at that revision, two hundred and twenty leaves in the transfer package and all twenty-one mock service leaves; there was no real-service no-op observation for that revision.
The lesson generalises beyond this slice: a control that names only the absence of a refusal is not a control, and the cheapest way to find that out is to write the implementation that would satisfy it and require the control to fail.

## Complete verification

Before evidence came from Go overlays that restore the two production files to their unchanged state while keeping the final tests, so the failures are the tests meeting real production.
In the transfer package one hundred and twenty leaves failed and one hundred and sixty passed; at the service layer fifteen failed and twenty-one passed.
The passing sets are the accepted controls and the established case shapes that unchanged production already refuses, which are the controls proving the fixtures reach the real boundary.

Four narrow mutants separate the guards from each other and from the effects, and their failures do not overlap.
Removing the caseless twin groups failed eight preflight leaves and no direct leaf; removing the caseless ancestor lookup failed sixteen preflight leaves and no direct leaf; removing both caseless checks from the direct executor failed ninety-six direct leaves and no preflight leaf.
None of those three touched an accepted control.
A fourth mutant keeps every admission check and then returns an empty successful report: it failed fifty-six leaves, every one an accepted control and no refusal leaf at all.
That mutant is the reason the accepted controls assert effects; before they did so it passed the entire matrix.

The complete internal suite ran under the race detector at three-way package parallelism against the owned two-account service, and passed with no failure and no skipped test: twenty packages and three thousand seven hundred and eighteen distinct test names.
Three hundred and eighty-eight of those are added by this slice, leaving three thousand three hundred and thirty before it, which matches the inventory recorded by the preceding portable component slice.
Both providers are covered without skips: thirty-six leaves on the owned mock and thirty-six against the real service, across three routes and twelve shapes each.

One earlier attempt at that suite is recorded rather than discarded: run against the real service without an explicit package limit, the system-test package reached Go's default ten-minute limit and the package failed, so that attempt is not evidence of a pass.
The reported run carries an explicit twenty-five-minute package limit, matching the lane's own budget.

The raised budget in the pipeline does not reach a developer machine, and the distinction is worth stating plainly because it is easy to miss.
The lane sets its own package limit in the workflow, while a local run takes Go's default ten minutes per package unless the invocation passes one.
With the real service configured, the recorded local run's system-test package exceeded that default.
Runtime depends on the host, its load and the test selection, so the full real-service gate must pass an explicit package limit matching the workflow and retain its actual exit; the completion of a wrapper is not a pass.

Formatting, vet on Linux, Windows and macOS, the Windows and macOS internal builds and a module tidy that leaves both module files unchanged all pass.

## Protected delivery

The delivered range is the two logical commits between master 5c64de37a9f1c3c1 and the reviewed head ea9bfe63b9af7152: the sequence plan and the implementation with its recorded results.
Independent review returned four corrective verdicts on the plan and one on the implementation before approving each, then approved the rebase onto that base separately; the base carried the two investigation records delivered just before, neither of which touches a file in this range.
The module files promote the text module from an indirect to a direct requirement in one line and two checksums, which review accepted with the implementation.

Its check run 34528444762 completed at the first attempt with all five required jobs successful.
The frontend job reported zero errors and zero warnings from svelte-check and passed twenty-nine test files carrying one thousand one hundred and thirty tests, untouched by this range, which moves no frontend byte.
The race-detector Go job recorded twenty-four successful package executions across twenty-three distinct tested packages with no failures.
The containerized Swift integration job passed three thousand seven hundred and sixteen test results with no failures and the same two skipped cases as the preceding deliveries, the rclone matrix case and the keychain round trip, measured combined Go statement coverage of 87.3 per cent against the seventy-five per cent floor, and recorded scenario coverage of forty of one hundred and sixteen cells and thirty-eight of fifty-two actions with gaps, reported valid with the owned service configured and coverage incomplete.
That job ran for 25.2 minutes, past the twenty-five-minute job cap that applied before the [raised time budget](2026-09-08-integration-time-budget.md#2026-09-10-job-cap-cancellation-and-correction) and inside the current one, which is a further measurement in that budget's favour.
The headless smoke job landed three of three objects and passed.
The run retained both coverage badges, the 438,218-byte scenario coverage artifact and the 6,109,681-byte smoke artifact.

The pull request then left draft state and was merged by rebase pinned to the reviewed head object.
Master became 235e7d9877f3f85e, whose tree is identical to the reviewed head's tree, and both commits kept their patch identity, message, author identity and intermediate tree.
The resulting master check run 34533436985 concluded success with the same five required jobs at the first attempt.
The pipeline exercised Linux runners only, so native acceptance on the other platforms remains unproven.

## Remaining limits

This slice settles canonical equivalence for collisions and nothing else.
Compatibility equivalence, leading spaces, component and path length limits, directory-only aliases and rclone encoding collisions stay open, and each remains named in DS-06.
The rule makes no claim about which normalization form a given filesystem stores or compares, only that canonically equivalent spellings must not both be promised.
No native macOS or Windows acceptance is established, because the pipeline exercises Linux runners only.
P18, P19, P20, P30 and the valuable-data hold remain open.
