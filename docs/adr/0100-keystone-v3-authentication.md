# ADR 0100: Keystone v3 authentication by wrapping the vendored built-in

**Status:** Accepted.
**Detail:** DESIGN §9.2 / §26.2 (the Swift session layer); road-to-1.0.0 decision 31 (Keystone v3 in 0.5.0); K-SEC-001 (the engine never speaks Keystone).

## Context

0.5.0 adds a Keystone v3 bookmark auth type beside TempAuth v1.
A bookmark names its auth version; the session authenticates with it and then browses, inspects and edits metadata.
The transfer engine never speaks Keystone (K-SEC-001): it is handed a bearer token and a storage URL by the app session, so a Keystone bookmark drives both browse and transfers through the same token contract, with no browse-first limitation.

The existing v1 authenticator (`internal/swiftcontrol/authv1.go`) is Aeronautes' own, preset on the wire connection in place of ncw/swift's built-in.
It is hand-written because the built-in v1 offers none of four things we need, and two of them can only be enforced from inside the auth exchange:

- the §26.2 token lifetime (the built-in v1 implements no `Expires()`);
- the storage-URL override that must survive re-auth;
- the `ValidateStorageURL` gate, so an HTTPS login cannot hand its token to an `http://` endpoint (§12.3);
- the consistent `(token, URL, expiry)` snapshot, published under the wire library's auth lock so a credential read cannot race an implicit 401 re-auth (review R-06).

The v1 protocol is a GET with two headers, so reimplementing it was cheaper than carrying a patch to the vendored library.
Keystone v3 is the opposite: a JSON POST to `/auth/tokens`, a token in a response header, and a service catalog to parse.
That is substantial, security-adjacent protocol code we would rather not own, or test only against our own mock.

## Decision

Reuse ncw/swift's built-in Keystone v3 authenticator for the protocol, and wrap it.

`guardedAuth` (`internal/swiftcontrol/authv3.go`) delegates `Request` and `Response` to the built-in, then — after the built-in has parsed the reply and selected the catalog's object-store endpoint — runs `ValidateStorageURL` on that endpoint and publishes the credential snapshot.
The two Aeronautes invariants that must live in the auth exchange, the storage-URL gate and the R-06 snapshot, are therefore the only new auth code; the request, response and catalog parsing stay the library's well-exercised code.

The built-in v3 authenticator type is unexported, so a caller that leaves `Connection.Auth` nil and sets `AuthVersion=3` gets it but no handle to wrap.
A one-line, behaviour-preserving export, `NewV3Authenticator`, is added to the vendored source as a registered patch, `v3-authenticator-export.patch`, recorded in `third_party/ncw-swift/UPSTREAM.json` and verified by `scripts/check-swift-source.py` exactly like the existing patches.
The operator chose to carry it through the existing in-repo vendored-patch mechanism for now; migrating this dependency to a standalone pinned fork, and offering the minimal export upstream, are tracked as later work rather than part of this slice.

`v1Auth` and `guardedAuth` share one `credentialSnapshot` primitive, so the concurrency-sensitive snapshot logic cannot drift between the two paths.

### Rejected alternatives

A hand-written v3 authenticator, as v1 is: it would own around 150 lines of Keystone request, response and catalog code, security-adjacent and tested only against a mock we wrote.
We prefer the library's protocol here, exactly as the rest of the codebase reuses rclone for moving bytes and keeps only the thin, safety-critical seams native.

Letting the library authenticate with no wrapper (`Auth=nil`, `AuthVersion=3`) and validating afterwards in `Session`: this loses the R-06 snapshot, because the consistent triple must be captured inside `Response` under the library's unexported auth lock, and it forks `Session`'s credential reads into a separate v3 path.

### No storage-URL override for v3

The override is a v1/TempAuth workaround for a single returned `X-Storage-Url`.
Keystone's catalog already distinguishes endpoints by interface (public, internal, admin) and region, so the endpoint is chosen there rather than overridden.
`New` rejects a storage-URL override when `AuthVersion=3`.

### Binding the storage region

A Keystone catalog can advertise object-store endpoints in several regions.
The built-in selects the first public endpoint matching the connection's region, so with no region configured a multi-region catalog is ambiguous — and, worse, an implicit 401 re-authentication against a reordered catalog can select a *different* region, silently moving operations and a metadata write to a namespace the operator never reviewed.
So `New` requires an explicit `Region` for v3, which pins the selection, and `guardedAuth` binds the endpoint chosen at first authentication and refuses any later authentication that selects a different one.
Neither the TLS gate nor the credential snapshot establishes *which* namespace is in use; this does.

### Token expiry

The built-in v3 authenticator reads the token's `expires_at`, so §26.2 gets the server's own statement for free; `guardedAuth` forwards it.

## Scope

The first cut is the password method with project scope: user, password, user domain, project and project domain.
The application-credential and token methods, trust scoping and domain-scoped tokens are deferred.
The built-in already supports them, so widening later adds `Config` and DTO fields and tests, not protocol code.

## Testing

The committed tests drive the real built-in parser against a mock Keystone endpoint (`/v3/auth/tokens`) in `internal/swifttest`: a happy path that validates and captures the catalog endpoint and then reads the account with the issued token, the storage-URL gate refusing a public `http://` catalog endpoint, a bad-credentials failure, and the two `New` guards.
Because the protocol is the library's and only the gate and snapshot are ours, a mock is the right instrument for the committed tests.
An independent oracle — a real Keystone stood up next to the SAIO, or a throwaway cloud Keystone wired into a pipeline — remains the follow-up for fixture faithfulness and end-to-end acceptance.

## Consequences

A Keystone v3 bookmark authenticates through the app session and works for browse, inspect, metadata and, through the token contract, engine transfers.
The v1 authenticator keeps its behaviour but now shares the snapshot primitive.
The vendored library carries one more registered patch, which the provenance gate verifies on every CI run.
The app and frontend layers — the bookmark DTO fields and the auth-type selector — follow in later slices.
