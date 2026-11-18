# ADR 0124 — what the store says it does not offer is greyed, with the reason

**Status:** Accepted on 18 November 2026 for 0.5.0, on the operator's decision of that day: "we should check /info on the bookmark and based on the enabled features, offer them or not ... some may not offer tmp urls, in which case we grey it out so people don't think they can do it but it won't work".
Tier 3 under DESIGN §65.12: it disables editors the store would ignore, and changes no request or refusal on a store that offers them.
**Scope:** the inspector's editors whose effect needs a feature of the store that its `/info` names: TempURL keys, the static website preset and container quotas.
The versioning modes are already gated this way and are not changed.

## What exists

DESIGN §10 reads `/info` when a bookmark connects and says capabilities should drive what is offered, TempURL among its examples.
The connection keeps the snapshot, the create dialog uses its storage policies, and the container editor greys out a versioning mode the store's `versioned_writes.allowed_flags` do not name.
Nothing else is gated.
A store without the TempURL feature still offers the account's and each container's TempURL keys, which it stores as metadata and never uses, so an operator can set a key and believe that signed links will work.
The static website preset and the quota fields are the same: the store keeps the metadata and does nothing with it.

## The behaviour oracle

- The operator's decision of 18 November 2026, quoted above.
- DESIGN §10: an absent feature and an unreadable `/info` are different states; "Unknown is not Unsupported", so a store whose `/info` cannot be read is offered everything and decides for itself.
- Swift's `/info`: each middleware that is installed names itself with a top-level key, `tempurl`, `staticweb` and `container_quotas` among them.

## Decision

1. **A feature the store's `/info` does not name is greyed where it is offered.**
   With `/info` read and no `tempurl` key, the account's and the container's TempURL keys are shown, with any key slots that are set, but cannot be changed.
   With no `staticweb` key, "Serve as a static website" cannot be ticked or unticked.
   With no `container_quotas` key, the two quota fields cannot be changed.
2. **Each greyed control says why.**
   Its hover text, and a line under the section, say that this store's `/info` does not list the feature, so the store would keep the setting and not act on it.
3. **An unread `/info` greys nothing.**
   When `/info` could not be read, every control is offered as today, under the note the container editor already shows that nothing is gated.
4. **The snapshot is the one taken at connection.**
   The backend sends the three features' states, supported, unsupported or unknown, with the storage policies and versioning flags it already sends; a reconnection reads them again.

## Why these shapes

**Hiding the controls** was considered and not taken: a setting that is already present on the container, a key or a quota written while another proxy served it, would then be invisible.
Greyed, it is shown and the reason is beside it.

**Refusing the change in the backend** was considered and not taken: the store accepts the write, the operator may know that another proxy in front of the same cluster has the feature, and the window's job here is to say what this store advertised, not to forbid metadata.

## Required proof before implementation is accepted

Tier 3.

- Go, service: the capabilities the window reads carry `tempurl`, `staticweb` and `container_quotas` as supported when `/info` names them, unsupported when `/info` was read without them, and unknown when `/info` was not read.
- Frontend, component: each of the three is greyed with its reason when unsupported, and offered when supported or unknown.
- Browser journey on the mock, whose `/info` names neither `staticweb` nor `container_quotas`: the static website preset and the quota fields are greyed, with the reason.

## What this does not do

It does not gate actions the core proxy serves, such as CORS, access lists and metadata, or any transfer or deletion.
It does not probe a feature by trying it.
