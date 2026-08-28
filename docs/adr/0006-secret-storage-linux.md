# ADR 0006 — Secret storage on Linux: the opt-in encrypted vault implementation

**Status:** Proposed — must be resolved before bookmarks can persist secrets to a vault
(the SecretStore interface, OS Secret Service backend, and session-only store do not wait on this)
**Detail:** `DESIGN.md` §12.1; requirements K-SEC-003, K-SEC-004

## Question to resolve

Which established, reviewed implementation/format backs the opt-in passphrase-encrypted local
vault on Linux (the common keychain-absent case: sway/headless/SSH/Flatpak)?

Required properties (§12.1): authenticated encryption; a memory-hard password KDF; atomic writes;
strict file permissions; a versioned on-disk format; a password-change/migration path; no
plaintext in SQLite; **no custom cryptographic primitives**. Unlocked once per session; never
silent; never the default when a real keychain exists.

## Candidates to evaluate

- `filippo.io/age` (scrypt recipient) wrapping a small versioned secrets file;
- an established Go keyring library's encrypted-file backend, if its format and maintenance pass
  review;
- any other reviewed AEAD-file format meeting every property above.

## Decision

_Unresolved. Record the chosen implementation, the on-disk format version, the KDF parameters,
and the migration story here, then flip Status to Accepted._
