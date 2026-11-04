# ADR 0099: levelled application log

**Status:** Accepted.
**Detail:** DESIGN §55 (release hardening); P28 "support diagnostics"; the operator's decision to provide selectable log levels in lieu of a diagnostics bundle.

## Context

Until now the application logged through the standard library `log` package: a handful of `log.Print`/`log.Printf` calls in `main` and the `internal/app` services, all at one implicit level, all to standard error.
That has two problems for a shipped desktop build.

A desktop launch has no console, so standard error is discarded.
The operational lines an operator would need to diagnose a problem — job crash recovery, audit-write failures, retention pruning, a terminal outcome that could not be saved — are written and then lost.

There is no way to ask for more or less detail.
Everything is always emitted, and there is no debug level to turn on when working out where a problem is.

The operator asked for selectable log levels — debug, info, warn, error — that can be enabled when diagnosing a problem, and chose this over a packaged "diagnostics bundle".
P28 names "support diagnostics"; the About dialog already shows the build identity, and that plus a levelled log is the agreed v1 surface.

A hard constraint applies: the log must never contain credentials.

## Decision

Add `internal/applog`, a small leaf package that both `main` and `internal/app` use in place of the standard library `log`:

- Four level functions — `Debugf`, `Infof`, `Warnf`, `Errorf` — with the same `printf` shape as the calls they replace, backed by `log/slog`.
- `Configure` reads two environment switches once at startup and installs the process logger:
  - `AERONAUTES_LOG_LEVEL` is `debug`, `info`, `warn`, or `error`: the lowest level written. Unset or unrecognised means `info`, which preserves the lines the application emitted before.
  - `AERONAUTES_LOG_FILE` is a path the log is appended to as well as written to standard error. This is how an operator captures a session on a desktop launch, where standard error is otherwise discarded. Unset means standard error only.
- The active logger is swapped atomically, so the one-time startup `Configure` cannot race a concurrent log call from a job goroutine.

### The log is auth-free by construction

No credential is ever passed to a log call, and that is a property of the call sites, not of a redactor:

- The authentication path (`internal/swiftcontrol`, Keystone/TempAuth) does not log.
- The transfer engine is handed only a per-session gateway key, never a durable secret (ADR 0003, ADR 0012, K-SEC-001), and the gateway strips auth-bearing headers from responses before the engine can see them.
- The operational lines carry job identifiers, container, prefix and object names, and error text from state, audit and retention work — never a token, a password, or a storage URL with embedded credentials.

A redacting writer was considered and rejected: it cannot know every secret shape, it gives false confidence, and it is the wrong place to enforce an invariant that the call sites already keep.
Adopting this package is therefore also a review checkpoint — a log call that would carry a credential is a defect to catch in review.

A log file is deliberately **not** written by default.
It is opt-in through `AERONAUTES_LOG_FILE`, so an ordinary launch keeps no on-disk log and raises no retention or growth question; a support flow enables the file for as long as it is needed.

## Consequences

- An operator, or a support request, can raise verbosity and capture a session with two environment variables, with no new UI and no diagnostics bundle.
- `main` and the `internal/app` services log through `applog`, and the standard library `log` import is removed from them. The line format changes from the `log` package's default to slog's `time=… level=… msg=…`, which also exposes the level; nothing parses these lines by format.
- Other packages, for example the transfer engine, can adopt `applog` incrementally to add debug-level detail where it helps. This ADR does not require that.
- The default level is `info`, so the lines the application emitted before stay visible; `debug` is additional detail turned on when needed.
