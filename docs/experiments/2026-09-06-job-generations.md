# Job credential lifetime validation

The behaviour oracle is accepted [ADR 0015](../adr/0015-credential-bindings.md): acquire a job's captured generation before durable creation, refuse binding changes, key-only replacement and deletion while the job uses that bookmark, and release only after the job and cancellation cleanup finish.
Cosmetic edits retain the binding, and every failed admission must release the pins it acquired.

## Delivered behaviour

Copy, Mirror, upload, download, deletion and benchmark admission acquire generation pins before their remote preflight and durable job creation.
Each distinct source or destination bookmark is pinned under the same coordinator used for credential publication and deletion.
The generation must still match the session acquired for admission, and its immutable reference joins the live-reference lease set.
Failure while acquiring a later bookmark releases earlier pins.
Multiple jobs retain independent pins, and repeated release of one pin cannot remove another job's protection.

Binding changes and key replacement refuse before preparing a new external write when a job already holds the bookmark.
Publication checks again after the external write: a job admitted while a key-store call waits keeps its original binding, and the unpublished candidate remains recorded as prepared work.
Cosmetic edits do not change the generation and can still succeed.
Deletion checks job pins before attempting to retire a reference or remove its bookmark.

Each service releases pins on admission failure, including failure to create a durable job and failure to create an upload recovery record after its job row exists.
Successful admission transfers ownership to the shared runner before the service returns.
The runner retains pins while queued, while executing, and through the entire job body's return, including cancellation cleanup and final audit writes.
Queued cancellation releases through the same outer cleanup even though it never enters the body.
Terminal job state and removal from the cancellation registry are deliberately insufficient evidence of pin release.

## Permanent checks and negative controls

Application regressions use real services, SQLite, the session secret store and HTTP fixtures.
An occupied execution slot keeps each of the six job types queued without another job's pins masking missing protection in the tested job.
Both source and destination are checked for Copy and Mirror.
Each queued job permits cosmetic editing, refuses key replacement and deletion with the job-in-use error, and permits replacement after cancellation has actually released its pins.
The successful replacement is the acknowledgement of release; a terminal row alone does not establish it.

A blocked engine factory proves protection before any durable job row exists, then fails and must release both bookmarks.
SQL triggers inject job creation failure in all six entry points and upload recovery-record failure after job creation.
A missing second bookmark must release the first admission pin.
An acknowledged engine barrier proves protection while running and while cancelled work waits for cleanup to finish.
A runner test makes the job terminal and removes its cancellation entry, then holds final body cleanup and verifies that credential changes still refuse until that cleanup returns.

Store checks cover independent pins, idempotent release, obsolete/missing/unavailable generations, missing credentials and publication while an external write is blocked.
The blocked-publication test verifies unchanged published configuration, intact original and candidate keys, and retained prepared work after refusal.

The original queued-job regression failed for key replacement in all six job types on the unchanged parent, including both bookmarks for Copy and Mirror.
Queued deletion already failed through the existing SQLite job foreign keys; that was not a reproduced deletion escape.
Separate disposable Go overlays remove the final publication recheck, release a pin before its body, or release it when cancellation is requested.
Each corresponding regression fails on the broken safeguard and passes in the implementation.
These are focused barrier and fault checks, not a complete generic fault harness.

## Verification results

The full core run used Go 1.27.1, the race detector, one uncached execution, both task-owned SAIO accounts and pinned rclone 1.74.3 including the engine matrix.
All 422 top-level tests passed across 19 packages, with zero failures or skips and 82.7% combined statement coverage.
The scenario report remains valid at 10 of 104 required cells, with 47 of 48 actions still having gaps; these regressions add no synthetic scenario credit.
Go build and vet pass, the production frontend build passes, and the pinned Wails v3.0.0-beta.15 binding generator produces no tracked changes.
The frontend is unchanged by this slice.

## Limits

Pins describe live jobs in this process; startup marks interrupted jobs failed rather than resuming queued execution.
Historical recovery records are not bound to a credential generation by this change, and backend-held plan capabilities remain separate work.
Existing job and benchmark history foreign keys can still prevent bookmark deletion after every live job has finished; this slice neither removes history nor changes its retention policy.
Held-session request revocation and the remaining library URL/token pairing gap are still open.
Physical key cleanup remains disabled until native authority and the entire live-reference set are established; job pins alone do not grant cleanup authority.
No native keychain, process-crash, power-loss or six-platform lifetime proof is claimed here.
The valuable-data release hold remains in place.
