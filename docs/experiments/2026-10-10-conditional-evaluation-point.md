# Where the create conditional is evaluated — 10 October 2026

[ADR 0085](../adr/0085-reobserved-destructive-identity.md)'s conditional-create restoration shipped with an admission: the store evaluates `If-None-Match: *`, and where that evaluation falls relative to a request body was measured nowhere, so a create landing after it was not covered.
The decision turns on it. If the conditional were evaluated when the publication commits, that one transition's window would close; if it is evaluated earlier, the conditional removes only the dependence on how old our own observation is.
This is that measurement, and it settles the question in the weaker direction.

No application code ran. A probe spoke HTTP over a socket so the framing and the timing were its own, in its own container, removing what it created.
The evidence is retained privately in `.aeronautes-work/20261010-conditional-evaluation/` with `SHA256SUMS` and `HOST.md`: the probe, the six-run log, the last run's JSON, and the fixture's `/info`.

- Fixture: the owned SAIO, Swift `2.38.2.dev23`, one storage policy `1replica`.
- Six consecutive runs, each exercising both `Content-Length` and `Transfer-Encoding: chunked`, giving twelve observations of the contested case and twelve of each control — thirty-six in all.
- The contested case sends a PUT carrying `If-None-Match: *` whose body arrives in two pieces, and creates the same name from a second connection while the first body is still arriving.

## The conditional is evaluated before the body completes

| Case | Observations | Result |
| --- | --- | --- |
| `If-None-Match: *` onto a name that already existed | 12 of 12 | 412 |
| `If-None-Match: *` onto an absent name, two-piece body, no competitor | 12 of 12 | 201 |
| `If-None-Match: *` onto an absent name, **created by another writer while the body was arriving** | 12 of 12 | **201** |

These are status codes, and the uncontested control's *bytes* were read back in the final run's two cases only — the six-run log retains each control's status, not its body, and the probe's JSON is overwritten per run.
The contested case is different: its surviving bytes are in the log for all twelve, which is what the second table rests on.

The first two controls matter as much as the case: they establish that the conditional was genuinely sent and genuinely honoured in every run, and that splitting the body does not by itself change the outcome.
Against that, the contested PUT was never refused. The name existed well before the request completed, and the store still answered 201.

So the conditional is evaluated early, and a create landing after that evaluation is not refused.
[ADR 0085](../adr/0085-reobserved-destructive-identity.md)'s restoration keeps exactly the value its comment now claims and no more: the refusal stops depending on the age of our own observation, and the window does not close.
On the native upload route, which streams a whole local file after the headers, the uncovered interval is the length of that upload.

## A 201 is not evidence that the published bytes are at the name

| Which bytes the name held afterwards | Observations |
| --- | --- |
| the other writer's | 11 of 12 |
| the conditional creator's | 1 of 12 (chunked) |

This was not the question and is the more useful answer.
In eleven of twelve the conditional creator received `201 Created` and the other writer's object is what remained at the name; in one it was the other way round, so this is a race rather than a rule and neither outcome may be relied on.
What the timings bound, and nothing beyond it: across the twelve observations the surviving object's `X-Timestamp` fell 2.00 to 3.58 ms after the conditional request's start, while the competitor's own PUT *returned* 18 to 26 ms after that same start, so the competitor began somewhere inside that interval; the conditional request itself ran 2018 to 2029 ms.
A timestamp assigned early with the newest write kept would explain the split, and that reading is an inference these runs do not establish — they measure neither the server's internal ordering nor any dependence of the winner upon it.
What they do establish is that both outcomes occur under this collision, which is why it is recorded as a race and why neither may be relied on.

What this means for the upload routes is already in the code rather than owed to it.
`verifyUpload` asks the live name what it holds after the publication exists, and for a plain publication it requires a plain object whose length *and* entity tag equal the local file's, so the surviving stranger's object fails that comparison and the item is reported unresolved and unverified rather than copied.
Its own comment had already drawn the line this measurement lands on: the store is the only party that can *prevent* such a publication, and this is the party that can *report* it.
The store did not prevent it here, and the report is what the operator gets.

## What is still not measured

Whether an acknowledged overwrite — a PUT with no conditional, which is what [ADR 0081](../adr/0081-upload-overwrite-acknowledgement.md) accepts — behaves the same way under a concurrent write at the same name.
Nothing here measures that, and the same race is plausible for it; the difference is only that no conditional is in play, so there is no expectation to disappoint.
