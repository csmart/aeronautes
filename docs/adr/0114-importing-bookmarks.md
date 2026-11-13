# ADR 0114 — importing bookmarks from rclone and OpenStack configuration

**Status:** Accepted for 0.5.0 (12 November 2026), delivered with this record's series, from finding F33 and the operator's decisions of 12 November: one import for rclone.conf and clouds.yaml, secrets to the secret store as usual, verification after import by default, a failed verification saved and flagged, duplicates skipped by default, and no secret in any error.
**Scope:** reading another tool's configuration file into bookmarks; the preview, the save and the verification that follows it; the flag a failed verification leaves on a bookmark.
**Amended by:** [ADR 0118](0118-duplicating-naming-and-keyless-bookmarks.md), for where the import is offered (decision 1) and the name each bookmark is saved under (decision 7).
**Relies on:** [ADR 0113](0113-keystone-discovery-and-ids.md) for an Identity root URL and for users and projects named by ID, and [ADR 0100](0100-keystone-v3-authentication.md) for what a Keystone bookmark may name.

## Context

An operator who already uses rclone or the OpenStack clients has their Swift accounts written down in `rclone.conf` or `clouds.yaml`, often with the key or password beside them.
Typing each one again into the bookmark editor is slow and invites mistakes.

Both files hold secrets, so reading them is a secret-handling path: the key or password must reach the secret store and nothing else, as a typed one does (K-SEC-001 to K-SEC-003, §55).
rclone stores a Swift key in plain text: its `key` option is marked sensitive, not a password option, so it is never obscured (rclone v1.74.3, `backend/swift/swift.go`).
F33 assumed an obscured key might need decoding; it does not, and no value is guessed to be obscured.

## Decision

1. **One action reads either file.**
   "Import bookmarks…" sits beside backup and restore in the Settings view and opens the native file picker.
   Go reads the picked file, at most 1 MiB, and decides its kind from its content: a YAML document with a top-level `clouds` map is a clouds.yaml, and a file of INI sections is an rclone config.
   A file rclone has encrypted is refused as a whole, saying it is encrypted and Aeronautes does not decrypt it; no password is asked for.
   It is encrypted when its first line that is neither blank nor a comment is `RCLONE_ENCRYPT_V0:`, so a comment neither hides an encrypted file nor refuses a plain one.
   Anything else is refused as not a configuration Aeronautes can import.
2. **rclone: every section with `type = swift`.**
   `auth` is the auth URL, `user` the username, `user_id` the user ID, `key` the secret, `domain` the user domain, `tenant` and `tenant_id` the project, `tenant_domain` the project domain (falling back to `domain`, as the library does), `region` the region, and `storage_url` a TempAuth bookmark's storage override.
   The auth version is the one rclone itself would use: `auth_version` when it is set, otherwise the library's own reading of the auth URL, which looks for the text `v3`, then `v2`, then `v1` anywhere in it.
   This is deliberately not ADR 0113's last-segment test: an rclone remote whose URL contains none of the three could not authenticate in rclone, so it is skipped rather than imported to be discovered.
   Version 2 is skipped; versions 1 and 3 make TempAuth and Keystone v3 bookmarks.
3. **clouds.yaml: every cloud using the password method, with `secure.yaml` beside it merged over it, as the OpenStack clients do.**
   A cloud uses the password method when its `auth_type` is absent, `password` or `v3password`; any other `auth_type` is skipped.
   It is Keystone v2, and skipped rather than saved as v3, when `identity_api_version` is 2 or its auth URL's last path segment begins with `v2`.
   `auth.auth_url`, `auth.username` or `auth.user_id`, `auth.password` (the secret), `auth.project_name` or `auth.project_id`, `auth.user_domain_name` and `auth.project_domain_name` (each falling back to `auth.domain_name`) and `region_name` make a Keystone v3 bookmark.
   The keys are read as the OpenStack client's loader reads them (openstacksdk, `config/loader.py`): `tenant_name`, `tenant_id` and the hyphenated spellings such as `project-name` and `user-domain-name` are accepted, and `domain_name` and `domain_id` fill whichever user and project domain keys are absent.
   The auth URL is kept as written, so a root URL is discovered under ADR 0113.
   Only `secure.yaml` in the same folder is read, and only to complete the clouds the picked file names.
4. **A user or a project named both by name and by ID is imported as its own client's request resolves.**
   Keystone resolves a user or a project by its name when a request carries both (Keystone 30.0.0, `auth/plugins/core.py` and `auth/core.py`), so what matters is what each client sends.
   The OpenStack client sends only the ID of either (keystoneauth1, `identity/v3/password.py` and `base.py`), so a clouds.yaml cloud given both is imported by ID.
   rclone's library sends only the project's ID, but the user's name, domain and ID together (`auth_v3.go`), which Keystone resolves by the name; so an rclone project given both is imported by ID, and an rclone user given both is imported by name and domain, without the ID.
   Either way the bookmark reaches the account its source reached, and the verification that follows the import (decision 8) checks it.
5. **What Aeronautes does not offer is skipped with a fixed reason, never imported differently.**
   A source is skipped, and the preview says why, when it takes its credentials from the environment (`env_auth`); uses Keystone v2, application credentials, a pre-made token or another `auth_type`; names an endpoint interface other than public; names by ID the domain of a user or project that it names by name (a domain ID beside a user or project ID is not needed and is ignored); names a user by name with no user domain, which neither client completes and Keystone refuses; names a clouds.yaml project by name with no project domain, which the OpenStack client does not complete either (an rclone project's domain falls back to the user domain and then to `Default`, as its library sends); has no region for Keystone v3; sets a storage override for Keystone v3; has an auth version neither stated nor readable from the URL; or trusts other than the system's certificate authorities (`verify` false, `insecure` true, a `cacert`, or a client `cert` and `key`).
   An absent `verify`, or `verify` true, is the system default and is imported.
   Because of these skips, the save's `Default` fill for a blank domain (ADR 0100) never supplies a domain an imported source did not send.
   Each reason is a fixed phrase and never quotes a value from the file.
6. **The preview holds no secret.**
   The parsed entries stay in Go behind a one-use token; the frontend receives, for each, its name, kind, endpoint, user, project, region, whether a secret was found, and one state: importable, already a bookmark, or skipped with its reason.
   An entry is already a bookmark when an existing one has the same auth URL, the same user (username or user ID) and the same project (name or ID).
   Importable entries are ticked, and the others are not; the operator may tick one that is already a bookmark to import it as a second.
7. **Saving goes through the ordinary bookmark save.**
   The ticked entries are saved one by one with the store's own validation, the source's name as the display name, and the secret handed to the secret store exactly as a typed one is; an entry with no secret is saved without a credential, like a restored one, and asks for it on first use.
   The token is spent by the save, and a second save with it is refused.
8. **Verification follows the save, on by default, without blocking.**
   "Verify after import" is ticked by default.
   When it is, each saved bookmark that has a secret is verified in the background, two at a time: authenticate and read the account, both read-only, through the path the editor's Verify uses.
   The dialog shows each row's result as it arrives and can be closed at any time; the summary lists verified, did not verify with its reason and an Edit button, and not verified because no secret was found.
9. **A failed verification is saved and flagged.**
   The bookmark keeps its configuration and its secret, and gains a flag with a plain reason chosen by the kind of failure (for example "the identity service refused these credentials"), never the error's own text, which can carry a server's words.
   The rail tile and the editor show the flag until the bookmark next connects successfully or is verified successfully in the editor, which clear it.
   The flag is not part of the credential binding and is not carried in a backup.

## Consequences

- Accounts in rclone.conf or clouds.yaml become bookmarks without retyping, with their secrets only in the secret store.
- A source Aeronautes cannot reproduce exactly is skipped and named, so no bookmark connects somewhere other than its source did.
- A failed verification does not lose the work of the import: the bookmark is there to fix.
- Bookmarks gain one nullable column for the flag.
- Encrypted rclone configs, application credentials, domains named only by ID (including the common `default` ID) and imported TLS settings stay out; each can be added later as its own decision.

## Tests

- Each reader against fixture files: every mapped field, both auth versions, the version read from the URL in the library's order, the `secure.yaml` merge, the domain fallbacks, a name and an ID together for each source and each side, a domain ID beside an ID, `verify` true imported, a name with no domain skipped for each source and an rclone project falling back to the user domain, and each skip reason; the encrypted marker after a comment is refused, a comment naming the marker is not, and a file of neither kind is refused; a file over the size bound is refused before it is parsed.
- No preview row, skip reason, error or log line contains a secret from the fixture.
- An entry that matches an existing bookmark is not ticked; a ticked one is saved as a second bookmark.
- A save stores each secret in the secret store and none in SQLite; an entry without a secret is saved without a credential; a reused token is refused.
- Verification runs at most two at a time, stores a flag with the fixed reason on failure, and stores none on success; a later successful connect, and a successful verify in the editor, clear the flag.
