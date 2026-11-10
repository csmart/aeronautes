# ADR 0090 — common container settings as tick boxes over the values they stand for

**Status:** Accepted (2026-10-27) — the operator's request of 26 October 2026, recorded as decision 27 in the [Linux finish line](../audits/2026-09-05/linux-finish-line.md); the model change and the editor change ship in the same range as this record.
**Scope:** How the container editor presents the common settings (world read, public listing, static web) and how the Go container model classifies the `Web-*` metadata family.
It changes neither the public-read acknowledgement and its native confirmation ([ADR 0073](0073-selected-resource-acknowledgement.md), §19.1), the settings badges ([ADR 0071](0071-container-settings-indicators.md)), the bridge signatures, nor the exclusion of CORS and TempURL metadata from the editable surface.
[ADR 0101](0101-tempurl-key-slots-and-cors-editing.md) decision 6 later lifted the exclusion of CORS, which is now ordinary editable metadata; the TempURL key slots stay out of the metadata editors and have their own operation there.

## The request

Testing the Linux build on 26 October 2026 the operator asked, of the configure-container panel: "it's only k/v metadata values, can we have some buttons to tick to turn on read only, web, common things people might do? and then that's reflected below in the actual metadata (and vice versa, if someone adds the right metadata it ticks the boxes), maybe separated by a line?"; and "I don't want people to need to know the exact syntax to make a container world readable and enable web".

§19.1 already names the presets (private, public read, public list and read) and §19.2 requires that structured and raw editing round-trip without silently changing semantics.
The panel had the raw material but not the presets: the read ACL editor shows entry chips and a raw line, and the metadata editor shows key/value rows.

## Two surfaces, one of them closed

World read and public listing are entries of the read ACL header, which the editor already writes.
Static web is the `X-Container-Meta-Web-*` family: `Web-Index` names the object a directory serves as its page, `Web-Listings: true` serves a listing where there is none, `Web-Error` and `Web-Listings-CSS` refine them.
`buildContainerDetail` folded that family into a `StaticWeb` presence flag and kept the keys out of `ContainerDetail.Meta`, `ContainerEdit.Meta` excluded them by contract, and the editor showed "Static web: configured" with nothing to edit.

That closure also hid a defect.
The metadata key validator admits `Web-Index`, so a row typed into the editor was POSTed and written, and the verify read then classified the key out of `Meta` and reported `metadata "Web-Index" missing after write`: a `verify_mismatch` banner over a write that had succeeded.

## Decision

1. The `Web-*` family is ordinary editable container metadata.
   `ContainerDetail.Meta` carries the keys, `ContainerEdit.Meta` sets and removes them through the same per-key delta as any other key (§17.2), and `StaticWeb` stays a derived flag for the row badge and the inspector's summary line.
   CORS stays excluded because its values are header-shaped and multi-valued, and TempURL keys stay excluded because they are secrets (§22, K-SEC-006).
   The contract for callers changes with it: a working copy built from the detail carries the web keys along, and an edit whose `Meta` omits them removes them, as for any key.
   The system test that proved an unrelated edit leaves `Web-Index` in place now carries the key in its working copy, which is what the editor does.
2. The editor offers three tick boxes above the metadata and ACL editors, separated by a rule: anyone can read objects (`.r:*`), anyone can list object names (`.rlistings`, offered only beside world read because alone it grants nothing), and serve as a static website (`Web-Index: index.html` and `Web-Listings: true` as the defaults).
   Each tick is derived from the value it stands for, so a hand-typed `.r:*` or a hand-added `Web-Error` row ticks its box; ticking rewrites only the entries or keys that tick stands for and leaves every other entry and row as it was.
   The exact header values stay in view in the ACL editor and the metadata rows, as §19 requires.
3. The world-read tick does not replace consent.
   The editor's acknowledgement checkbox and the backend's native confirmation stand as they are; the editor's predicate becomes the exact-entry rule the gate and the badges use (`.r:*` after trimming, in any comma position), so the tick, the acknowledgement and the badge agree.
   The old regular expression demanded an acknowledgement for a referrer grant such as `.r:*.example.com`, which the backend never treated as world read.
4. Static web serves anonymous requests, so when the website tick is on without world read the editor says so.
   It does not tick world read on the operator's behalf: making a container public stays the operator's own act.

## Maintained proof

- Go: the detail carries `Web-Index` in `Meta` and still reports `StaticWeb`; an edit that keeps the key in its working copy leaves it, one that drops it removes it, while CORS and TempURL survive both; the literal metadata round trip carries the key on the mock and the real fixture.
- Frontend, pure: the ACL forms of ADR 0071 (`.r:*`, `.r:*,.rlistings`, the spaced form, the denial beside world read, `.r:*.example.com`, `.r:*x`, a bare `.r:`, `.rlistings` alone, `account:user`, empty) read back as world read and listing or not; each rewrite changes only its own entries and reads back as the tick that was set; static web is on for any `Web-*` key in any case and the defaults are added once.
- Frontend, component: ticking world read writes `.r:*` and Save still refuses without the acknowledgement; unticking it drops `.rlistings` with it and keeps other grants; a hand-typed ACL drives the ticks and a referrer grant demands no acknowledgement; the website tick adds the two rows to the metadata editor and the payload, unticking removes them, and a hand-added `web-error` row ticks the box; the panel opens with the ticks the current values imply.

## Limits

The website tick writes the two defaults; the error page and the listing style sheet remain hand-typed rows.
The editor cannot know whether the cluster runs the staticweb middleware, since the capabilities read does not expose it; as with versioning under an unknown `/info`, the cluster decides.
Nothing here changes a mutation's admission, a gate, or the valuable-data hold.
