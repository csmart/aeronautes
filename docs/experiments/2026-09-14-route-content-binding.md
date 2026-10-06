# Route content binding: what the native copy, upload and download routes accept

Follow-up to the [staged copy content record](2026-09-11-staged-copy-content.md), which bound one route and named the three it left open.
This record reproduces what those three accept, and is the evidence for [ADR 0045](../adr/0045-route-content-binding.md).

## What was asked

Finish-line item L05 requires that the enabled Copy, upload and download routes reject same-size corruption, missing or reordered segments and source or publication substitutions, or report an explicit unverified or refused outcome with uncertain residue retained.
[ADR 0042](../adr/0042-source-content-ladder.md) delivered that for the staged copy route only, and said so.
The question here is what the other three do.

## Method

A probe ran each route end to end through the transfer service against the two-account fixture on loopback port 18080, with a proxy in front of the account under test.
The proxy alters one body — the destination PUT for the two writing routes, the source GET for the download — by inverting every byte.
Length is preserved exactly, so every length comparison on the route still passes.
Nothing else about the route, the engine or the store is changed.

The proxy stands in for any cause of a same-size difference between what a route believed it moved and what the far side holds: a store that stored something else, a middlebox, a refilled buffer.
It is deliberately not a timing race: the reproduction is deterministic, and it does not depend on a listing lagging its object.

Names carrying an engine-reserved rune plan onto the native route, which is how the native cases are reached.

Three of the four cases carry a healthy control: the same proxy, in the same place, passing bytes through unchanged.
A control that failed would mean the fixture breaks transfers, and the corrupting case would then say nothing about the route.
The staged upload commit case has no control, so it is reported as a reproduction without one rather than as a controlled result.

An earlier draft of the probe compared the namespace oracle's SHA-256 object digest against an MD5 of the expected bytes.
Those two values can never be equal, so the guard that was supposed to prove the fixture had taken effect could not fire, and it would have reported success whether or not the bytes differed.
It was corrected before the recorded run: snapshot comparisons use the oracle's digest, and MD5 appears only where a Swift entity tag is meant.
The reason the other controls here can be trusted is that this one was found and is stated.

## What reproduced

Four intended failures, three controls passing, one run, exit 1.

| Case | Observed |
| --- | --- |
| Native copy of a dynamic large object source | destination holds different bytes of the same 8192, counted 1 copied |
| Native upload | store holds different bytes of the same 9728, counted 1 copied and 1 verified |
| Native download | placed file holds different bytes of the same 10240, counted 1 verified |
| Staged upload commit | store holds different bytes of the same 10240, counted 1 verified |

| Control | Observed |
| --- | --- |
| Native copy through the identity proxy | destination digest equals the source, 1 copied |
| Native upload through the identity proxy | stored digest equals the local file, 1 copied |
| Native download through the identity proxy | placed bytes equal the object, 1 verified |

## Why each one passes today

`nativeCopy` calls `verifyCopied`, which compares logical length always but compares entity tags only when the source and the destination are both plain.
A segmented source under the single-PUT limit is streamed into a plain destination, so the classes differ and the tag comparison is skipped.
The name is also stamped with a fingerprint derived from the source HEAD in the same PUT, before any content check, and later equality and skip decisions read that stamp back.

`verifyUpload` compares the live object's logical length against the local file's recorded size.
No digest of the local file is taken anywhere on that route.
`commitStagedUpload` admits the staged object on its logical length alone before copying it to the live name, so a difference introduced between staging and publication is not examined either.

The download route compares size on both paths and nothing else: `finishNativeDownload` checks the bytes written by `io.Copy` against the object's size, and `placeDownloaded` checks the staged file's size for a file the engine wrote.
Publication itself is a no-replace link through the held directory, and `publishVerified` already requires the published entry to name the same file that was checked; what is missing is that the check is only a size.

## Where the bytes already are

The staged copy route had to read the object back over the network, because by the time it could compare, the bytes were only in the store.
Most of these cases are not in that position: the native copy body, the native upload body and the native download body all stream through the process, and an engine-staged download file is already on local disk.
Those comparisons cost no object bytes on the network.

One case is in that position.
A staged upload that publishes an engine-segmented live object cannot be settled from a remote segment listing recombined against the live aggregate: a same-size remote segment replacement can leave the listing row and the derived aggregate unchanged, so the local file agrees with both while the remote bytes differ.
That is the counterexample ADR 0042 already disproved, and it does not become sound on a different route.
That case needs the same complete read, or an explicit unverified result.

Cheapness is not the same as sufficiency, and the fourth reproduction shows where the two part company.
`commitStagedUpload` reads the stage and then sends a separate live PUT, and the probe alters that PUT while leaving the stage intact.
A comparison that stops at the staged object therefore answers a question nobody asked: the stage was fine.
For that plain upload case specifically, the value the local digest must be compared against is the one the final live HEAD reports.
This is not a licence to defer the other checks until after publication: a segmented staged upload still reads and verifies the assembled stage before any committing fact or live manifest PUT, and a download still compares before it publishes anything.

### The lesson these three review rounds cost

The contract needed three rounds, and two of the corrections were the same error in different clothes.

The first draft proposed settling a segmented staged upload from a remote segment listing recombined against the live aggregate.
[ADR 0042](../adr/0042-source-content-ladder.md) had already disproved exactly that: a same-size replacement leaves both the listing row and the aggregate derived from it unchanged.
The second draft then bound the plain staged upload comparison to the staged object — while the fourth reproduction in this very record alters the separate live PUT and leaves the stage intact, so the proposed comparison would have passed on the case being reproduced.

Both are the same mistake: comparing something other than the thing being claimed.
A listing is not the content it describes, and a stage is not the live name.
The rule that generalises, and the one the next author on these routes will need, is that **every comparison must be bound to the boundary it claims** — the byte stream that was actually written, and the name that was actually published.
Reaching for the cheaper adjacent observation is the shortcut that keeps looking sound and keeps being wrong.

A second decision is worth carrying forward for its own sake.
Withholding the fingerprint until content is verified sounds like a strict improvement, but there is no way to bind that later write to the object that was checked: `UpdateObjectMetadata` preflights with a HEAD and then performs a replace-all POST, with no atomic content condition.
A replacement landing in between would receive a valid fingerprint while holding other bytes.
A stamp that cannot be bound to what it certifies is not a weaker proof, it is not a proof, so the stamp is dropped rather than moved.

## The asymmetry that cannot be engineered away here

The download stages and publishes by a no-replace link, so it can compare before publishing and refuse with the staged file retained.

Native copy and native upload publish with a single PUT at the live name.
A store that did not keep what was sent can only be detected after that PUT.
Under [ADR 0020](../adr/0020-recovery-containment.md) nothing is deleted or rewritten to repair it, so the honest outcome is an unresolved effect with the live name reported as residue.
Detecting a mismatch while the body is still being sent permits abandoning the request, but abandonment does not prove that no object was created, so only a refusal decided before the first byte can report that nothing was attempted.
Routing those two paths behind a staged name would close the gap; that is recommended separately and is not part of the proposed decision.

## What this record does not establish

It does not measure the engine's own checksumming, and does not rely on it.
Whatever the pinned engine verifies internally is a property of a third-party binary rather than evidence this project produced, and it does not cover the native routes or the commit copy at all.

Taking a fresh expectation at execution time is not a defence against a source that changed since preflight: these local preflight items retain no prior content identity, so there is nothing for a fresh observation to disagree with.

It reproduces same-size corruption only.
Missing and reordered segments, and whole-job source and publication substitution, are named by L05 and are not reproduced here; they remain open, and this record closes neither L05 nor DS-04.

No implementation is proposed by this record; ADR 0045 carries the decision, and no code changed.

## Evidence

Durable private packet `~/code/.aeronautes-work/20260913-claude/l05b/`, with `SHA256SUMS` beside the files.

| File | SHA-256 |
| --- | --- |
| `probe-before-verbose.log`, the run that produced the tables above including the control passes | `187bd272b58023e54e3d11bfb989af13ffd035d59f226c8fb1c2f88a5b29e73b` |
| `route_content_binding_probe_test.go.txt`, the inert probe source | `aea3910c2011ff61bd6ae9f160216963e235b1fc8ffd681dd60893d38ca31b61` |

The probe is archived rather than kept in the repository because it asserts behaviour that does not exist yet.
The two evidence files are cited by their own digests rather than by the packet's checksum file, because that file also covers the working state note and changes whenever it does.

## A finding about the engine, stated at the strength the evidence supports

A comparison was written into the engine download path and then removed before delivery, because a broken-guard control disproved the evidence for it.
With that comparison deleted, the altered engine case still refused: nothing was staged and nothing was placed.
The refusal therefore came from the pinned engine, not from the added code.

The honest claim is narrow.
On the pinned engine version, with these fixtures, a same-size alteration of a downloaded body ends with no retained staged file and nothing published.
That is what was observed after the failure; absence afterwards does not establish that no local effect occurred at any point during the attempt.
That is evidence about this engine and these fixtures.
It is not an established contract: nothing here pins the engine's guarantee, enumerates the classes it covers, or fixes it against a version change.

Both directions matter.
If the engine's guarantee holds and can be pinned, it is stronger protection than a post-hoc comparison, because it refuses before any local effect exists at all.
If it does not hold — for a class not covered, or after a version bump — then the engine download path currently has no content binding whatever, and the absence would be silent.
Either way the current position is that this project has not established which it is.

A separate slice should pin it: reproduce the engine's refusal across the classes L05 names, record the version it was measured on, and decide whether to depend on it or to add the comparison behind a fixture that can prove the comparison works.
Until then the engine download path is recorded as unbound by this project's own evidence, rather than treated as protected.

### A worse defect than the one this slice set out to fix

This slice INTRODUCED a cleanup hazard that destroys data belonging to another writer, and it was caught in review before delivery rather than being something the route always handled.
It is recorded here at that weight because it is more serious than the unverified download it was written to prevent: a missing content check leaves a bad copy in place, while this deleted a file that was never ours to touch.

It is also the shape of defect this project keeps finding, in its purest form.
The record said the temporary was retained as residue.
The code deleted it.
A document describing a safety property the code does not have is worse than no document, because every later reader takes the property as established.

The first implementation of the mismatch branch set its error before capturing the written file's identity, so cleanup received nothing to compare against and unlinked whatever regular file held the temporary's name — the rejected bytes, and a replacement another writer had put there.
The first attempted fix scoped retention by whether the source's stated length agreed with the listing, which still left the unequal-length paths deleting that replacement.
Capturing the identity unconditionally then produced a third variant: a Stat that FAILS still leaves nothing to compare against, and cleanup again read that as permission to unlink any regular file.

Three revisions each destroyed another writer's file in a different way, and the first two failed for the same reason — both conditioned the protection on something about the source.
The reviewer's ruling is the sentence a future author needs, and it is more useful than any of the specific fixes:

> Preserving an existing rule that cleans up our own failed temporary does not permit deleting somebody else's file... do not use source-length agreement as the condition for protecting another writer.

The completed rule follows from it.
Capture the identity of what this call wrote on every path, successful or not.
An identity that could not be established is not a weaker identity but none at all, so the file is retained and reported as unresolved residue, and the failure to read it is joined with any earlier error rather than replacing it.
Cleanup never reads a missing identity as authority to unlink.
Retention of our own rejected bytes is the narrower promise, and an actual close failure keeps the existing cleanup contract where the identity IS known.

This is also why the comparison was removed rather than kept as defence in depth.
Code that cannot be shown to do anything is not a second layer; it is an unproven claim in a data-safety path, and the next reader would reasonably assume it was doing the work.

## Two explanations that were wrong, and what replaced them

Both were written to explain why this slice's retention is safe, and both explained something adjacent to the thing that mattered.

The first said cancellation is always decided before the comparison, so a cancelled item and a content mismatch could never be the same item.
That ordering does not exist in the code.
Cancellation is observed at particular boundaries, and a cancel arriving during a successful `Sync`, or during `Close` after the comparison has run, leaves the mismatch detected and its bytes retained.
An item can be both.
That is still consistent with the cancelled-delta contract, because that contract scopes its delta to its own staging boundaries and already permits surviving residue to remain unresolved — but the reason is that, not an ordering.

The second said the retention survives because `removeEmptyStageDirectory` cannot unlink a file.
True, and irrelevant here: the native temporary is a sibling of its target, chosen by `downloadTemporaryPath` and written through the held target parent, so no engine stage governs it, and `DownloadInto` returns before opening one when there are no engine items.
What preserves a rejected native temporary is this route's own deferred cleanup declining to run.

The pattern is the same one this record already documents twice: verifying a claim next to the one being made.
The first time it deleted another writer's file, and these two only put false sentences in a decision record — but a decision record is what the next author reasons from.

## The case continuous integration caught that the authoring host did not

The maintained retention case for a content mismatch combined with a close failure passed
here and failed in two independent integration jobs on the same head.

It was not a missed leaf.
The case ran locally and passed by luck.
`os.SameFile` compares device and inode, and the fixture removed the temporary and created
the replacement at the same name, so the kernel was free to hand the new file the inode it
had just released — after which the check reports "still ours" for somebody else's file.
On this host that happened in twelve of five hundred remove-and-recreate cycles; on the
runner it happened every time.
Two hundred and three hundred count local reruns did not surface it.

The first lesson drawn from this was wrong and is corrected here, because the wrong version
would have thrown away the thing that worked.

It is tempting to conclude that a fixture must never remove and recreate at the same path.
That sequence is exactly what found a real product defect, and it should be kept.
Reuse of the freed inode is genuinely observable: this host produced it during a
five-hundred-cycle probe, and independent review reproduced it too, confirming that the
corrected code preserves the replacement after a reuse while the old branch deletes it.
Those are first observations, not frequency estimates, and neither says how often the
integration runner will do it.

The real lesson is about controls rather than fixtures.
**Establish which condition a control actually exercises.**
The mutation that removes this retention was first pointed at the replacement case, where
the damage only appears if the freed inode happens to be reused, so it could not fire
reliably and was written off as unavoidably probabilistic. That was a selection error: the
same mutation deterministically breaks the existing `source_close` case, which loses its
retained temporary, and `source_close_cleanup`, which then records a cleanup attempt that
must not happen.

So keep the adversarial sequence for what it finds, and complement it with a deterministic
check of the same property, rather than treating an unreliable control as the best available.

The correction to the product removes the dependency rather than making it more likely to
hold: a rejected download no longer reaches the identity check at all.

## What this probe reproduces now, measured 6 October 2026

The tables above are what the probe measured on 14 September 2026 and are left as they
were. This section records what the same probe, byte for byte, does at later heads. It is
rerun rather than edited: each rerun checks the archived file's SHA-256 against the digest
cited above before placing it, places it only for the run, removes it afterwards, and
re-measures the source clean.

| Case | 14 September 2026 | Now | Closed at |
| --- | --- | --- | --- |
| Native copy of a dynamic large object source | reproduced: 1 copied over different bytes | stopped reproducing | `10a98f6a`, [ADR 0083](../adr/0083-native-copy-content-binding.md) decision 1, [ADR 0084](../adr/0084-held-manifest-copy.md) and ADR 0045's native-copy obligations |
| Native upload | reproduced: 1 copied and 1 verified | stopped reproducing | `27434353`, ADR 0045's native-upload obligation |
| Staged upload commit | reproduced: 1 verified | stopped reproducing | `27434353`, ADR 0045's plain-commit obligation |
| Native download | reproduced: 1 verified over different bytes | cannot reproduce | `c9c8a381`, [ADR 0045](../adr/0045-route-content-binding.md)'s download delivery |

Each of the three that stopped reproducing did so with its own precondition still holding,
which is what distinguishes a closed gap from a broken fixture. The probe deletes the
declared entity tag on the way to the store, so the store has nothing to enforce and the
altered bytes are published: the destination holds different bytes of the same length in
every one of the three, and the probe says so in its own output before deciding. What
changed is that the route now asks afterwards and reports, so no intended-failure assertion
is reached. The three healthy controls pass in the same runs, so the fixture still carries
bytes.

The download case is different in kind, and what the rerun establishes about it is narrower
than the other three. It stops at its own prerequisite — "nothing was placed, so the probe
cannot speak to placement" — and that `ReadFile` failure is a `t.Fatalf`, so the case never
reaches its verified-counter assertion and never inspects a diagnostic or a retained
temporary. What the rerun shows is therefore only this: **the case no longer reaches its
intended placement assertion.** It shows neither an open content defect nor, by itself, why
nothing was placed.

That the native download route compares before publishing and refuses with its temporary
retained is established by separate evidence, not by this rerun: `finishNativeDownload` sets
`contentMismatch` and skips cleanup before returning its failure, and the maintained cases in
`internal/transfer/local/download_binding_test.go` assert the failed and unverified result
with the bytes retained. A reader who takes the rerun's failure line as an open gap has it
backwards, but the reason to say so is that source and those tests, not the absent file.

A probe case that can no longer reach its own subject should be retired on its next revision.
The archived file is deliberately not edited here, because it is cited by digest and a rerun
of an edited probe would prove nothing.

So the probe's exit stays non-zero while that one case remains in it. The exit code is not
the result; each case's own output is.

## What L05 stands at, 6 October 2026

Same-size corruption, which is what this probe reproduces, is now reported or refused on the
three writing routes it covers, and the reruns are the evidence for those three. That is the
whole of what they establish. It is narrower than L05, and L05 is not closed by it.

What this probe never reproduced is not the same as what remains unverified, and an earlier
draft of this section ran the two together. The probe covers same-size corruption only. The
cases below are outside its scope, and where coverage exists it is named rather than assumed:

- **Missing and reordered segments** are refused on the native download route by
  `TestNativeDownloadRefusesReorderedAndMissingRuns`, and on the copy routes by construction,
  because a dynamic source's ladder is recombined against the aggregate its HEAD reports and
  a stale, truncated, extended or reordered listing does not reproduce it. This probe does not
  exercise either, so it is not the evidence for them.
- **Source and publication substitution** is refused on the native download route by
  `TestNativeDownloadRefusesASubstitutedSource` and
  `TestNativeDownloadRefusesASubstitutedPublication`. Whole-job substitution — a source
  container swapped under a running job — is named by L05 and is not covered anywhere; it
  remains open.
- **The engine download path is bound**, which an earlier draft of this section got wrong by
  restating this record's September sentence under an October date.
  [ADR 0070](../adr/0070-engine-download-content-binding.md) was accepted and implemented on
  23 September 2026 and says in its own status line that it closes the gap ADR 0045 recorded
  and ADR 0060 left unbound: `engineExpectation` requires the captured source identity,
  matches it, and builds the ladder the held staged bytes are compared against before
  publication, with the maintained proof and mutants in the
  [engine-binding record](2026-09-22-engine-download-binding.md).
- **A path that lies consistently to both the write and the read it is checked by.** Nothing
  local can catch that, and no route here claims to. Still open, permanently.
- **A source that changed before it was observed.** This limit belongs to the upload routes,
  whose items carry no prior content identity, so a fresh observation has nothing to disagree
  with. It does NOT describe the download routes, and an earlier draft of this bullet drew
  their boundary in the wrong place — one step too early, which understated what they refuse.

  A download captures a source identity for every planned item in the transfer-set walk at
  Start, which is a later and firmer thing than the preview the interface showed before it.
  A replacement arriving between that capture and the later check **is refused**: the native
  route matches the opened object's own detail against the capture before it creates a
  temporary and reports substitution with nothing published, which
  [ADR 0060](../adr/0060-native-download-content-binding.md) requires and
  `TestNativeDownloadRefusesASubstitutedSource` holds by replacing the source with
  same-length bytes after preflight and requiring both the refusal and the absent file.
  `engineExpectation` covers the same interval on the engine route and refuses an item whose
  identity was never captured.

  What is genuinely not covered is stated by ADR 0060 in its own words: the decision does not
  freeze the source after `OpenObject`, and does not freeze the published name after the
  confirmation pass. So the window is after the execution-time observation, not after Start.

  The static-large-object caveat is narrower than that draft implied too. A listing row
  carrying no SLO entity tag costs the one comparison that would detect a **same-size**
  substitution from that listing; the captured length still has to agree, and the content is
  still compared against the source's ladder as it streams.

Two routes also differ in what their check can do, and the difference matters more than the
checks having the same shape. A download compares before it publishes, so its check is a
gate: nothing reaches the live name. A native copy, a native upload and a plain staged
commit publish at the live name in one request, so theirs is a report: the bytes are already
there, the item is failed and unverified, and the residue is retained under
[ADR 0020](../adr/0020-recovery-containment.md). The declared entity tag on the PUT is the
only thing that prevents those three, and it is defeated by any path that removes the
header — which is precisely the path this probe models.
