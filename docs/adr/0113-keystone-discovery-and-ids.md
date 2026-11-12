# ADR 0113 — Keystone version discovery and identity by ID

**Status:** Proposed for 0.5.0 (12 November 2026), from findings K1 and K2 and the operator's decisions: version discovery limited to the same origin, and optional project and user IDs, both folded into 0.5.0 by the final-candidate plan.
**Scope:** how a Keystone v3 bookmark's Identity URL becomes the URL its token request is sent to; which Keystone identity a bookmark can name.
**Amends:** [ADR 0100](0100-keystone-v3-authentication.md), whose first cut is the password method with project scope by name.

## Context

A Keystone v3 bookmark today sends its token request to the Identity URL with `auth/tokens` appended, so the URL must already be the v3 endpoint.
A clouds.yaml `auth_url` usually names the Identity service's root, such as `https://identity.example.com:5000`, because OpenStack clients ask that root which API versions it offers and pick v3 themselves.
Pasted into Aeronautes, that root answers the token request with 404 and the bookmark cannot connect (K1).

ADR 0100 names the user and the project by name, each with its domain.
Some clouds hand their users a project ID and a user ID instead, and clouds.yaml often carries `project_id`; such an account cannot be described today (K2).
The vendored library already sends an ID when it is given one, so the gap is in Aeronautes' configuration, storage and editor.

## Decision

1. **An Identity URL ending in `/v3` is used as given.**
   A path whose last segment is `v3`, with or without a trailing slash, is the v3 endpoint, and no other request is made before the token request.
2. **Any other Identity URL is asked which versions it offers, once per session.**
   Before the session's first token request, Aeronautes sends one `GET` to the Identity URL as configured: no credentials and no token, the bookmark's own TLS rules, no redirect followed, a bounded response body and a bounded time.
   It reads the Keystone version document in either shape the Identity service sends: a list of versions (`versions.values`, normally with status 300) or a single version (`version`, when the URL is already a v3 root under another path).
   It takes the entry whose `id` starts with `v3.` and whose status is `stable` or `current`, and that entry's `self` link.
   The session keeps the URL it learned for every later authentication, including the implicit one after a 401; the bookmark keeps what the operator typed.
3. **The learned URL must have the same origin as the configured one.**
   The token request carries the password, so a version document cannot move it: the `self` link is used only when its scheme, host and port equal the configured URL's.
   Refusing redirects keeps the same rule for the document itself.
   There is no fallback to appending `/v3`, and no other host is tried.
4. **A URL that offers no v3 at its own origin is refused with words the operator can act on.**
   When the request fails, the document cannot be read, it names no stable v3, or its v3 link has another origin, connecting fails before any credential is sent, with an error saying that the Identity URL does not offer Keystone v3 at the same address and to give the v3 endpoint, for example `https://identity.example.com:5000/v3`.
   The error names the configured URL and not the learned link.
5. **A bookmark can name the user and the project by ID.**
   Two optional fields, User ID and Project ID, sit beside the names.
   A user is named by user ID, or by username with its user domain; a project by project ID, or by project name with its project domain.
   When an ID is given, the matching name and domain are not sent, because Keystone resolves an ID on its own and a name sent beside it could disagree.
   A v3 bookmark needs a user (username or user ID) and a project (project name or project ID) to be saved, as it needs a region today.
6. **The IDs are part of the bookmark's binding.**
   They are stored in two new nullable bookmark columns, carried in the backup (schema version 4; versions 1 to 3 still restore, with no IDs), shown and edited in the bookmark editor's Keystone section, and compared wherever the existing identity fields are, so changing an ID asks for the password again as changing a name does.

### Rejected alternatives

Appending `/v3` to any URL that lacks it, as the real-cloud test loader does: it is right for the common root URL but wrong for a URL naming another version, such as `/v2.0`, and it hides from the operator which endpoint is in use.

Using the path of a v3 link on another origin against the configured origin, for a service behind a proxy that advertises its internal address: it keeps the password at the configured origin, but it guesses that the proxy maps paths one to one, and a wrong guess fails later and less clearly than the refusal does.

## Consequences

- A clouds.yaml `auth_url` pasted as it is connects, at the cost of one unauthenticated request per session for a URL that does not end in `/v3`.
- A misconfigured or hostile Identity service cannot redirect the password to another address through its version document.
- An Identity service that offers v3 only on another host or port needs its v3 endpoint typed in full.
- Accounts given by ID can be bookmarked, and the import of clouds.yaml (F33) can carry `project_id` and `user_id` through.
- Application credentials, token authentication and trust or domain scope stay deferred, as ADR 0100 says.

## Tests

- A `/v3` URL, with and without a trailing slash, sends no discovery request.
- A root URL whose document lists v2.0 and a stable v3 connects through the v3 link; a single-version document does too.
- A v3 link on another host, scheme or port is refused before any token request, as are a redirect, a document with no stable v3, a malformed document and a failed request; the refusal names the configured URL.
- A later authentication on the same session, including after a 401, reuses the learned URL without asking again.
- With a user ID or project ID, the token request carries the ID and neither the matching name nor its domain; without them, it is unchanged.
- Saving a v3 bookmark with neither a username nor a user ID, or with neither a project name nor a project ID, is refused; a backup round trip keeps both IDs, and a version 3 backup restores with none.
