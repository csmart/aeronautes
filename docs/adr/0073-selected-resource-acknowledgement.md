# ADR 0073: the selected-resource acknowledgement completes as an accepted refusal

**Status:** Proposed on 23 September 2026 for the operator's decision; nothing here is accepted or implemented.
**Scope:** The selected-local-resource half of finish-line item L02 and the public-ACL acknowledgement it is required with, for the capabilities v1 keeps. The forged-force half stays deferred with the destructive paths.

## Problem

Finish-line item L02 requires, for v1, that the selected-local-resource half binds the download picker and that the public-ACL approval half guards container metadata editing ([finish line](../audits/2026-09-05/linux-finish-line.md), row L02).
[ADR 0041](0041-scoped-selection-capabilities.md) delivered the binding and named what it leaves open: "a trusted acknowledgement of account, container and prefix for selected local resources, presented outside the webview's control, remains open, as does the separate acknowledgement the forged force and public access-control cases need, and L02 does not close."
The limit is structural: `BrowseService.Connect` is a bridge method, so a page can connect to account B before opening the picker while still rendering account A, after which every binding check legitimately passes for B.
The binding cannot authenticate which account, container and prefix the operator believed they were selecting for.

The finish line expects this area to complete as a refusal rather than an implementation: "the connect action is reachable from the bridge, so the backend cannot authenticate which account the operator believed they were choosing for, and that acknowledgement can be neither built nor validated on this workstation", and "where an area completes that way, the refusal and its limits belong in the record for that area, and the capability stays described by what it refuses rather than by what it proves."

The public-ACL half has the same shape.
`MutateService` refuses a world-readable read ACL unless `ContainerEditDTO.AckPublic` is set (`internal/app/mutateservice.go:131-139`), and the source scopes that flag honestly: it guards against an operator accident, "NOT a hostile-webview control, since a compromised page can supply ackPublic itself".

## What v1 enforces today, which is the whole of what it claims

- A selection is bound at issue to one operation, one bookmark, the backend-resolved generation, one container and one prefix; the endpoint is observed before the blocking picker opens and again when it returns; preflight validates the token read-only, start consumes it atomically, an unstarted token expires after ten minutes, and each failed check has its own refusal (`internal/app/selections.go:73-80`; `internal/app/localtransferservice.go:162-200`, `:267`, `:482`).
- In a v1 build only the download consuming boundary is reachable; upload's is deferred under [ADR 0052](0052-v1-capability-gate.md).
- A world-readable read ACL is refused without the editor's acknowledgement, and since [ADR 0071](0071-container-settings-indicators.md) the browser shows the resulting exposure as a badge and a banner, so an ACL a page set behind the operator's back is visible on the next settings check.

## Decision, proposed

**Option A, accept the refusal for v1.**
v1 does not establish which account the operator intended for a selected directory or a public ACL.
The download capability is described by the six selection refusals and the two endpoint observations; container editing is described by the acknowledgement flag and the visible exposure.
The residual cases are exactly the ones the records already name: an account moved away and back inside the picker dialog (two observations, no connection epoch); a selection spent by an engine build that then fails; and a page that connects elsewhere before the picker opens, which only the operator's own reading of the bookmark rail would catch.
Acceptance is the operator's and belongs in the finish line in the operator's words; this record then changes status to Accepted and L02's v1 portion completes as an enforced refusal.

**Option B, present the acknowledgement outside the webview and validate it as a native journey.**
The backend already opens a native directory chooser for a download; it can title that dialog itself from the bookmark it resolved ("Download from <account>, <container>/<prefix> into…"), so the account the backend will bind is shown by a surface the page cannot draw.
For a public ACL, the backend can raise a native confirmation naming the container before applying the ACL and refuse unless that dialog answered yes.
Both are tier 1 under DESIGN §65.12 (authority and consent) and small in code; neither can be validated on this workstation.
The proof is a native journey on Linux, which is external acceptance the operator performs: open the picker and read its title; save a public ACL and answer the native dialog.
Until that journey is recorded, the refusal in option A still stands.

The author recommends A for v1 completion of L02, with B recorded as the closing design for the next release, or for v1 if the operator will run the native journey.

## What this does not establish

No finding closes by this record alone, and the valuable-data release hold is untouched.
The forged-force half stays deferred with the destructive paths, and the one-shot capability named at §65.10 / R-12 remains an alternative design for the public-ACL half.
