# What the shutdown barrier test's failure does and does not establish — 10 October 2026

`TestShutdownChildRealSwift/cancel` has failed intermittently on hosted CI for weeks, and the owed action was to fix it once a hosted occurrence named its cause.
Run 38053430368's first attempt — the resulting-master run for a change that touched two markdown files and nothing else — is an occurrence, and it does **not** name the cause.
It yields one fact, and successive drafts of this record each claimed more than that fact carries, so what follows separates the fact from the reading of it.

```
shutdown_linux_test.go:203: timed out waiting for real child partial bytes;
  last seen: file.bin.0b4b4656.partial: 32768 bytes, want at least 65536
```

## What the evidence establishes

The last observation the wait recorded before giving up was a file of 32768 bytes where 65536 were required.
That is the whole of it.

**Its contents were not compared, and an earlier draft of this record said they were.**
The old wait was a `switch` whose first arm matched a short file, logged the size and `continue`d, so the content comparison in the arm below it was never reached for a 32768-byte file.
An incorrect file of that size would have produced the identical line.
So nothing here establishes that those bytes were a prefix of the object at all.

## What it does not establish, although a first draft of this said otherwise

**That there was no growth across the sixty seconds.** The witness is replaced on every poll, so the retained line is the last observation and not a history. The file may have grown earlier and stalled, or stalled from the first write; nothing here distinguishes those.

**That the unflushed bytes were in the child's buffer.** The child's nominal buffer is 16 MiB by default, which the source supports, but the hosted events carrying a `16Mi` setting belong to other tests in that run and say nothing about this child's occupancy. The job's scoped gateway also sits between the proxy and the child and copies under its own write deadline.

**That no deadline would have helped.** That followed from the no-growth claim, which is not established.

## The leading unresolved alternative, which the test could not report

The helper process that hosts the child is capped at **forty seconds** by its own `-test.timeout=40s` and at forty-five by its context, while this wait runs for **sixty**.
So a timeout here can mean the producer was already gone, and the failing path recorded neither the helper's exit nor its log — which is why this occurrence cannot distinguish "the bytes were stuck" from "the writer had been killed twenty seconds earlier".
Reconciling those deadlines is owed. Reporting is not, and the wait's failure now carries the tail of the helper's log and states the configured deadline relationship — which is a prompt for the reader, not a determination: it does not record whether the helper actually exited, and a future occurrence still has to be read rather than trusted.

## What was changed, and why it does not depend on the history

The decision to stop requiring a complete flush rests on the pipeline's shape rather than on this occurrence.
`downloadCancelBodyProxy` truncates each read so a fixed number of bytes leaves the proxy, then holds the response body **open** and never ends it.
How much of that release has reached the disk at a chosen instant is therefore a property of buffering between the proxy and the file, which this test neither controls nor can flush.
Requiring it was requiring something no contract offers, whatever happened in any particular run.

So "the barrier was exact" became two assertions, each checked where it is decided: the release is exact at the barrier, which is the only party that knows how much it handed downstream; and what reached the disk is a non-empty prefix of the object, no longer than that release.

The retention comparison afterwards had to change with it, and this is the part a first version of the fix got wrong.
Accepting a short prefix at the earlier snapshot makes the terminal comparison racy in the other direction: the remaining released bytes can reach the disk between that snapshot and the moment the child actually stops, so a whole-snapshot equality then fails on a schedule that is entirely correct.
That interleaving was identified from the source rather than reproduced, and no fast local run reaches it.
The comparison is now a named oracle that allows the partial to have grown while requiring everything else to be identical — it never shrinks, never exceeds the release, stays a true prefix, keeps its mode, and the entry set and the sentinel are unchanged — with a deterministic test over twelve schedules, including that one.
