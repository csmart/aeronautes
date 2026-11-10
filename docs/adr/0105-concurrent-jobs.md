# ADR 0105: up to four jobs run at once, and no job of ours deletes or overwrites another's stage

**Status:** Accepted for 0.5.0 (10 November 2026), delivered with this record's series.
The operator's decision is recorded: asked on 10 November 2026 for the budget the roadmap schedules, with the author's target of a ceiling of four and a default of two, they answered "sure, go with your target, we can revisit later".
**Scope:** How many transfer and deletion jobs run at once, what protects one running job from another of this application's own, what the stored setting becomes, and what the drawer offers.
It amends [ADR 0047](0047-single-active-job.md), which enforced one, and decision 5 of [ADR 0104](0104-pause-and-resume.md), which rested on it.
It changes neither the queue depth and the per-job units of [ADR 0074](0074-workload-bounds.md), the download destination reservation of [ADR 0078](0078-overlapping-download-destinations.md), the comparison before a replacement ([ADR 0081](0081-upload-overwrite-acknowledgement.md), [ADR 0085](0085-reobserved-destructive-identity.md), [ADR 0102](0102-versioning-matrix-fixed-point-and-archives.md)), the per-name deletion protocol ([ADR 0095](0095-prefix-deletion-by-the-per-name-protocol.md)), nor pause and resume.

## Context

ADR 0047 made one active job a hard ceiling on 14 September 2026 and called the decision deliberately reversible.
Its reason was that every interleaving of two jobs is a state some contract has to hold in, and the remaining safety work was easier to prove with one.
That work has since been done for the live namespace, and it was done for any other writer, not for one job at a time.
Every create on every route is conditional, an acknowledged replacement is compared at its fixed point against what the operator was shown, a deletion compares each name against its own listed row immediately before its request, and the container's versioning is read again before a replacement.
None of those asks whether the other writer is this application.

The roadmap says raising the budget was made safe by construction by ADR 0078.
That record covers overlapping local download destinations and nothing else, and it was written when download was the only job route the release kept.
A survey of the tree for this record, by reading and with nothing measured, found the runner correct for any capacity, the engine child and every spool private to its job, a bookmark's session safe to share, and the database written through one mutex with rows keyed by job.
It found one thing that only the single job protected.

A job's stage is ordinary objects.
An upload stages at `.aeronautes/stage/<job>/` at the root of its destination container, a copy at the same path under its destination prefix, and both put a large object's staged segments under that same path in `<container>_segments`.
The engine's gateway confines one job's child to its own stage, but a transfer's own publication and its reclamation of a replaced object's segments are not confined: an upload aimed at another job's staged manifest, which is a name the operator can browse to, would replace it and then reclaim the segments it named.
A deletion sweep is not confined: it lists its scope and deletes every row it lists, so a job that empties a container, deletes it, or deletes a prefix that covers the stage would delete the staged objects of an upload or a copy running beside it.
The finish line's residual L03 says so in as many words: "Enforced single-job execution removes this application as a source of a competing stage".
By reading, every commit fails closed on a missing staged object, so the likely end is a failed item and not a wrong object; the case not ruled out is a staged segment deleted around the moment its manifest is published, where the job would report an object verified whose segments are gone.

Three smaller things also assume one job.
The stored setting has been written as one by every save since ADR 0047, so a default of two would reach nobody who has ever moved the rail.
The drawer offered budgets the service refuses and showed the refusal as an unconfirmed save.
When this record was written no test in the tree ran two job bodies at the same moment.

## Decision

1. **Four jobs at most run at once, and two by default.**
   The ceiling stays a constant the runner enforces and the settings service refuses above, as ADR 0047 decisions 1 to 3 made it; only the number changes.
   The default is a constant of its own and is no longer the ceiling.
   A job beyond the budget queues as it does today.
   The queue is not ordered: a waiter is woken when a slot frees and whichever takes it first runs, so with several slots a later job may start before an earlier one.
2. **At a live name, our own jobs are other writers to each other, under the contracts that already stand and with the windows those contracts state.**
   Two jobs that create one name each send a conditional create.
   The one whose request arrives after the other's object is live records a conflict; in the interval the store leaves between evaluating the condition and receiving the body both can be accepted, which every route already answers by reading the live name afterwards and recording what it finds.
   Two acknowledged replacements of one name each compare the name at their fixed point.
   One that compares after the other has published refuses, with both identities recorded; two that both compare before either publishes both publish and the later one stands, because a replacement carries no condition and ADR 0085 accepts the interval between the comparison and the effect for any writer.
   A transfer into a scope a deletion is sweeping: each listed name is deleted or kept by its own comparison immediately before its request, a name replaced between that comparison and the request is deleted as ADR 0085 states, and a name first written after the sweep's last listing stays.
   No new rule is added for these and none of those intervals is narrowed; a job's message that a destination changed externally now also covers a change made by another job of ours.
3. **No job of ours deletes or overwrites what another job of ours has staged.**
   The stage namespace is every name that has the components `.aeronautes/stage/` at its start or after a `/`.
   - **The stage namespace is not a destination.**
     An upload's preflight rejects a selection with a destination name in the stage namespace, and a copy refuses such an item when it plans it and records the refusal with its reason.
     No route publishes there, so no transfer replaces another job's staged object and no reclamation follows such a replacement.
     It is a fixed rule and not a lookup of who is running, so it holds whichever job starts first.
   - **A sweep and a stage do not overlap.**
     Every upload and every copy claims its stage root when it starts: the account, the destination container and `<container>_segments`, and the path `.aeronautes/stage/` under the destination prefix, which for an upload is the container root.
     Every sweep claims the account, the container and the prefix it lists: the deletion of a prefix, the emptying and the deletion of a container, for which the prefix is the container root, and the sweep of a dynamic large object's segment namespace that a manifest-with-segments deletion runs.
     A start is refused when its claim and a held claim of the other kind are on one account and one container and one path lies inside the other, and the refusal names the job that holds it and says to wait for it or cancel it.
   - **A deletion of named objects is checked when it starts.**
     The selected set, the manifest of a manifest-with-segments deletion and the reviewed entries of a static large object are fixed before the job starts, so such a job cannot reach a stage that begins later and holds no claim; it is refused at its start when one of its names lies under a held stage claim.
   - **Reclamation keeps a live job's stage.**
     When an acknowledged replacement reclaims the replaced object's old segments ([ADR 0081](0081-upload-overwrite-acknowledgement.md) decision 5), a segment that lies in the stage directory of a job that still holds its claim, in the container the segment is in, is kept and recorded as kept with that job named, and with every such job named when there are several, beside the shapes that decision already keeps.
     The reclaiming job is not excepted: a dynamic large object is inventoried by listing its prefix after the job has staged, so a prefix that covers the job's own stage directory would list the segments of the object it is about to publish.
   - **The account is compared by the rule the copy overlap guard already uses.**
     A claim records the account identifier of the storage URL its session resolved when the claim was taken.
     Two claims are on different accounts only when both identifiers are known and differ; a second bookmark, a host alias for one account and an identifier that could not be read all count as the same account, so the rule fails closed.
     A session keeps the account it first resolved, so a claim stays true for as long as it is held and is never re-read: a re-authentication that returns a storage URL with a different account is refused before its credentials are published, as [ADR 0100](0100-keystone-v3-authentication.md) already binds the region, and the session's requests then fail as an authentication failure until the operator reconnects.
     That comparison is exact, because Swift tells accounts apart by their exact bytes, and an account that cannot be read from either URL is the same only as the identical storage URL; the claims' own comparison ignores case, which can only refuse more.
     This adds a refusal and removes none: a TempAuth v1 re-authentication that returns the same account on another host is accepted as it is today, subject to the transport policy, and a Keystone v3 session keeps the stricter rule of ADR 0100, under which any change of the selected endpoint is refused whatever its identifier.
   The check and the claim are one step under the runner's lock, taken before the job row exists, so two racing starts cannot both win and a refused start leaves no row, no pin and no record.
   A claim is held until the job's whole body has returned, a paused job's included, and a queued job's from its admission, because its stage will exist as soon as it runs.
   Two transfers never conflict by their claims, and neither do two deletions.
4. **Reading another job's stage is not refused.**
   A download whose source scope covers a running job's stage downloads the staged objects it lists, as it already downloads the stage a crashed job left behind.
   A copy lists them too, and refuses each one whose destination name would fall in the stage namespace, under decision 3; a copy taken from inside a stage to an ordinary prefix copies them.
   They are unpublished copies, nothing of the staging job's is changed, and its own commit and verification are unaffected.
5. **A folder upload does not pick up this application's local staging.**
   The folder walk skips an entry named as a download stage or a download's partial file, `.aeronautes-stage-*` and `.aeronautes-part-*`, and counts it with the skipped entries it already discloses.
   Without this, a folder pick that contains an active download's destination would plan that download's half-written files as uploads; the content binding of [ADR 0080](0080-upload-content-binding.md) would refuse each as it changed, so this removes confusion, not a loss.
6. **A stored budget of one is cleared once, and an earlier choice is kept.**
   Since ADR 0047 every save of any setting has written the budget as one, so a stored one cannot be told from that forced value, and a migration removes it so that the default applies.
   A stored value from two to four is a choice an operator made before ADR 0047 and has not saved over since, and it is kept.
   A stored value above four reads as four and the row is left as written, as ADR 0047 decision 5 left it; a stored value below one reads as the default.
   An operator who chose one before ADR 0047 and has saved nothing since gets two and can choose one again, which is the one preference this loses.
   From then on a save stores what the operator chose, between one and four, and a restored backup's value is accepted in that range and refused outside it like any save.
7. **The settings surface states one range, and the drawer offers what will be accepted.**
   A save outside one to four is refused with one message that names the range.
   The drawer offers one, two, three and four, shows a refused save as refused with the service's reason, and says beside the control that each job brings its own engine settings.
   A settings read that fails at startup leaves the runner on the default and is logged, where today it is silent.
8. **What a job is given is per job, and the totals are not bounded.**
   Transfers, checkers, the request-rate and bandwidth limits and the buffers of the performance profile apply to each engine child, a deletion job has its own eight workers, and each job stages up to its own batch bound.
   With four jobs an endpoint sees up to four times one job's load, and nothing here caps the sum; the whole-application ceilings DESIGN §38.1 lists for workers and memory stay unbuilt, as ADR 0074 left them.
   The native session's connections per host stay capped per bookmark, shared by its jobs and by browsing.
9. **When the job store stops, every other job the runner holds is cancelled.**
   A record that cannot be written still stops the store for the session, and before this record the running job was the job whose record failed.
   With several running, a sibling whose items are already admitted would go on publishing them, because recording an outcome stays possible after the store stops, as it must for what has already happened.
   So the store, as it stops and before the failing write returns, cancels every other job whose body is running; each registers its cancel with the store when it is launched.
   It then tells the runner once, naming the job whose record failed, and the runner cancels every other job it holds, which reaches one that is starting, queued or parked.
   That job is the one whose write made the transition, decided with it under the store's lock: a sibling this stop cancels can fail its own final save in turn, and that later failure may supply the reason that stands but is neither named nor followed by a second notice.
   The runner's notice alone was not enough: it arrives on a goroutine of its own, and a review showed a sibling publish two files before it did.
   The job whose record failed is left alone: its own write has returned the error, and it ends on that account as it did when it was the only job, where a cancel arriving beside that error could change what it reports.
   Each transfer route ends as it does for a cancel, with one difference at cleanup: the engine child is ended, nothing further is published, and a request already sent completes and has its outcome recorded where a record can still be written.
   Once the store has stopped, no route removes anything from its stage, whichever job's record it was that failed.
   [ADR 0048](0048-cancelled-stage-removal.md) decision 7 already retains the stage when a job's required recording fails, so that the evidence is not destroyed with the record, and the store's stop is that failure for every job at once.
   A download cancelled for this reason therefore keeps its staged files and reports cleanup as not attempted, where an ordinary cancel removes them, and a native-routed item that is not yet placed keeps its temporary file, which is that item's stage, whether the cancel cut its read short or came as it closed; the cleanup boundary asks the store whether it has stopped, and no second way of stopping is added.
   The boundary this relies on is the check for cancellation that stands before each publication on every route; where a route lacks one, the implementation adds it, and the proof holds a job between two publications.
   A parked job is woken, asks the store at its boundary as a launch does, and ends as a lost record of its own ends it, with no further unit and its stage kept; a queued one never starts, as before.
   A transfer sibling's terminal result is still written, and no job's status can be read for the rest of the session, as the failing job's cannot.
   A deletion sibling cannot record what the cancel leaves untouched, because the store refuses every deletion record once it has stopped, and saved as finished it could not say which members were never requested; it is left open with its status unavailable and says why, as one whose own record was lost is, and the restart settles it; that includes a selected set cancelled while queued, whose members were admitted before it ran.

### Why these shapes

Refusing the start, and not filtering staged names out of each sweep, was chosen because the refusal is one check in one place the runner already owns, while a filter would have to be right in the sweep, the selected set, the segment job and every later deletion, and would leave a sweep reporting a container emptied that still holds another job's stage.
Refusing every deletion on a container a transfer writes into was considered and not taken: deleting a few unrelated objects while an upload runs is ordinary, and only the stage is unprotected.
Checking a transfer's destination names against the stages held at that moment was considered and not taken: a job that starts later would have to be checked against names an earlier copy has not yet listed, while the fixed rule needs no such knowledge and gives up only the ability to publish into a staging area.
Letting a running sibling carry on after the store stops, and stopping it only at its next admission, was considered and is what the tree did before this record; a reviewer's run showed an upload publish all three of its files after another job's terminal save had stopped the store.
Skipping `.aeronautes/stage/` in every listing was considered and not taken: a crashed job's stage is residue the operator must be able to see, delete, download and copy.
Refusing a reader whose source covers a stage was considered and not taken: it would stop a container being downloaded while it is uploaded to, to prevent copies that harm nothing.
Keeping the stored value was considered and not taken: it would make the default a value no existing installation ever sees.
A whole-application worker budget was considered and left where ADR 0074 left it: it is a design of its own, and the operator chose the count first.

## Records this amends

- ADR 0047: the ceiling is four and the default two; its decisions on where the ceiling is enforced, what settings report and the resize regression stand with those numbers, and its decision that a stored value above the ceiling is left as written stands, beside the one clearing decision 6 makes.
- ADR 0104 decision 5, "One job still runs at a time": a paused job is still not running, and every item is still checked at its own commit; the argument that a pause interleaves no more than cancelling and starting again is replaced by decisions 2 and 3 here.
  A resumed job takes any free slot.
- DESIGN §38.1, finish-line residual L03 and decision 16, and the roadmap's line crediting ADR 0078 are brought to this record.
- ADR 0074, ADR 0078 and ADR 0088 name the single slot in passing and are qualified; [ADR 0011](0011-typed-service-surface.md) calls the queue first-in first-out, which the runner has never been.
- [ADR 0044](0044-segment-deletion-namespace.md) lists another job's active staging as open; for this application's own jobs decision 3 closes it, and an outside writer stays as that record leaves it.

## Required proof

- Settings: the default is two on an empty store; five is refused naming the range; after the migration a store that held one reads two, one that held three reads three, one that held sixteen reads four with its row unchanged, and one that held zero reads two; a restored value of three is kept.
- The runner admits two bodies and queues the third under the default, and admits four under the ceiling whatever a caller asks for.
- Two uploads of different content to one name, both running, one held at its commit until the other has published: the held one records a conflict and the name holds the publisher's bytes; on the mock and on the owned Swift.
- The stage namespace: an upload whose selection would publish a name under `.aeronautes/stage/` is rejected by its preflight, including one aimed at a running job's staged manifest; a copy whose source holds such a name refuses that item, records it and copies the rest.
- The claim, on each pairing at the service boundary on the mock: an emptying, a container deletion and a covering prefix deletion are refused while an upload or a copy into that container is queued, running or paused, on the container and on its segments container, with the job named; a selected set or a segment deletion that names an object under the stage is refused; a prefix deletion beside the stage, a selected set of unrelated names and a deletion on another container start; the reverse, an upload or a copy refused while a sweep holds its claim, and started while a deletion of named objects runs; a refused start leaves no job row; the claim is gone when the job's body has returned, and held while it is paused.
- The account: two bookmarks whose storage URLs differ in host and name one account conflict, an identifier that cannot be read conflicts, and two accounts whose identifiers are known and differ do not; a TempAuth v1 re-authentication that returns the same account on another host is accepted, and one that returns a different account, one differing only in case included, is refused before any request uses its credentials; the existing Keystone v3 refusal of any endpoint change stands.
- Two starts that conflict, raced: exactly one wins.
- Reclamation: a replaced manifest whose recorded segments lie in the stage directory of another job that holds its claim keeps them and records them as kept with that job named, names both when two jobs hold them, and removes them when no such claim is held; a dynamic predecessor whose prefix covers the replacing job's own stage is not replaced, and a name in a live job's own directory is kept whichever job asks.
- Two downloads into sibling directories, both held in their engines together, both complete with their own stages removed.
- The store stopping: a terminal save made to fail for one job while a sibling upload is held in its engine with every item admitted; the sibling is cancelled, publishes nothing, keeps its stage and has what it observed recorded; the same run without the failure publishes every file; a sibling held between two publications publishes no second one; a sibling released after the stop still publishes nothing while the runner's notice is held back. The runner told that one job's record failed cancels its sibling and leaves that job to run to its own end. A parked job ends without a further unit. A prefix sweep and a segment deletion stopped beside another job's lost record, and a queued selected set whose settlement the stop interrupts, are left open and say why. A later stop is neither named nor told.
- The same for a sibling download held in its engine after files have reached its stage: it places nothing, keeps every staged file, reports cleanup as not attempted and has what it observed recorded, and an ordinary cancel of the same download removes them as today; a native read ended by such a cancel keeps its partial file, and one whose read completed and whose cancel came at close keeps its whole temporary, where an ordinary cancel removes it.
- Shutdown with two running jobs and one paused returns, and each row is left cancelled or interrupted.
- The folder walk skips a download stage directory and a partial file and counts them as skipped.
- The drawer: the four choices, a refused save shown as refused, and the sentence beside the control.

## Limits

Nothing here was measured: the cost in control-plane latency of four jobs sharing one bookmark's connections, the write throughput of several jobs recording at once, and whether a full batch still finishes inside a token's life when jobs share bandwidth are stated as unknown, and the default of two is the operator's to revisit.
Two jobs at the default double what one job asks of an endpoint, with no back-off.
A download, and a copy taken from inside a stage, can read another job's staged objects, as decision 4 says.
Two deletions over one scope run together, and each records as changed or absent what the other removed.
The intervals decision 2 names are not narrowed: two acknowledged replacements of one name can both publish, and a create can be accepted beside another in the store's own window.
When the store stops, a request a sibling has already sent completes, and its outcome is recorded only where a record can still be written.
A cluster that hands the same credentials a different account on re-authentication stops that session's work until the operator reconnects; none is known to, and the alternative is a job acting on an account its claim does not cover.
Decision 3 knows one account from another by the identifier in the storage URL, so two accounts that share an identifier on different clusters are treated as one and a deletion on one waits for a transfer on the other.
Reclamation keeps the old segments of an object that a still-running job published earlier in its own run, because they lie in that job's stage directory; they are recorded as kept and are removed by nothing until the operator deletes them.
Copying or uploading into a staging area is refused for every job, so a crashed job's stage can be downloaded and deleted and can no longer be copied under its own names.
A deletion made from the inspector is not a job and is not checked against a stage, as it is not today.
The claim is on the stage root and not on one job's own directory, so a sweep that would only remove the stage a finished or crashed job left beside a running job's is refused until that job ends.
