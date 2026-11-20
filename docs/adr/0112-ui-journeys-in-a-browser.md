# ADR 0112 — UI journeys in a browser

**Status:** Proposed on 12 November 2026, from the operator's standing rule of the same day that automated journeys come before further features and that the 0.5.0 tag waits for both the real-cloud journeys and browser-driven UI journeys.
**Scope:** a test-only way to drive the real frontend and the real services from a browser: the build, the stand-ins for native dialogs, the state it keeps, the accounts it uses, how it runs and what it prints.
**Relates to:** [docs/journeys.md](../journeys.md) (layer 1, the service journeys), [ADR 0109](0109-where-aeronautes-keeps-its-files.md) (where the application keeps its files), K-SEC-001 to K-SEC-003.

## Context

The real-cloud journeys (layer 1) call the services the buttons call, so they find what the services do wrong and nothing the frontend does wrong: a dialog that sends the wrong request, a button that never enables, a disclosure that is missing.
The frontend's own tests run components against a fake API and never reach a real service.
Wails 3.1.0 has no automation hook into its webview, but it has a server mode: built with the `server` tag, the application serves the same frontend and the same bound services to an ordinary browser over HTTP on localhost, with events over a WebSocket.
A trial on this tree built and served the real frontend that way, and kept its state where the environment pointed it.
What server mode cannot do is show a native dialog: the file and folder pickers and the two native confirmations (an acknowledged overwrite and a world-readable ACL) need stand-ins.

## Decision

1. **A test-only build of the real application.**
   `make uijourneys` builds the application with the tags `server` and `uijourney`.
   The `uijourney` tag swaps exactly the native seams `main.go` already injects (every picker, the transfer, audit and backup ones among them, the two confirmers and the secret store opener) for test stand-ins, in a build-tagged file beside a `!uijourney` file holding today's desktop seams; nothing else differs from the shipped application.
   The `uijourney` build serves on 127.0.0.1 only, at a port of its choosing, and refuses to start when `WAILS_SERVER_HOST` names any other host, because that variable exists to widen the listen address and this process holds real credentials; the runner clears it.
   No release build carries either tag, and a default-build test asserts the stand-ins are not compiled in.
2. **The stand-ins are scripted over a control listener the test runner owns.**
   The `uijourney` build opens a second listener on 127.0.0.1 with a random port and a random token, and refuses any request without the token.
   It prints one startup line, the only exception to decision 7, carrying the two loopback ports and the token and nothing else; the runner reads it and never repeats the token.
   Through it the runner sets the folder or files the next picker returns, queues the answer the next confirmation gives, creates the run's bookmarks, and asks for teardown.
   A picker or confirmation with nothing scripted fails, so a journey cannot proceed on an answer nobody gave.
3. **State and secrets stay with the run.**
   The runner launches the build with `HOME` and the XDG directories pointed at a fresh temporary directory, so settings, the database and the lock are created there (ADR 0109); the operator's files are never opened.
   The `uijourney` build opens the session secret store directly; it never calls the default opener, which probes the OS keychain.
4. **Credentials never pass through the browser or the runner's script.**
   Real-cloud bookmarks are created through the control listener by Go, which reads the clouds.yaml entries with `internal/realcloud`'s selection, validation and redaction rules, so no password reaches Node, the page, a Playwright trace or a screenshot.
   The bookmark editor itself is driven only against the mock, with a fixed test key.
5. **Two targets, one suite.**
   The same journeys run against the in-repository mock (`cmd/mockswift`), which needs no credentials, and against the two real clouds when a clouds.yaml is given, under layer 1's safety rules: every container is one the run created, through `internal/journeys`' ownership record held by the control listener, and teardown removes only those.
   The mock target runs in the required CI smoke job alongside the native desktop smoke.
   Real-cloud journeys stay local only; CI refuses a `CLOUDS` path and uses no cloud credentials.
6. **Playwright drives a real browser.**
   The suite lives in `uijourneys/` with its own pinned `package.json`, apart from the frontend's dependencies, and drives Chromium through Playwright, finding controls by role and accessible name as the operator would, not by test-only attributes.
7. **Output names no provider, host, account or secret.**
   Apart from decision 2's startup line, every line of output names only a step, labelled `mock`, `ceph` or `swift`, and the private directory: never a provider, host, account, credential, or the token again.
   Tracing is off; a failure keeps a screenshot and the page's text in that private directory under the user cache directory, beside layer 1's private files.
   The application's own log goes to a private file, as in layer 1.

## Consequences

- The frontend and the services are tested together through the shipped bridge, at the cost of one extra build and a Node dependency used only by this suite.
- The stand-ins are the one place the UI journeys differ from the shipped application, and they replace only what has no browser equivalent.
- Native dialogs themselves stay untested by this suite, as they were.

## Local Swift fixture extension — 20 November 2026

The operator wants practical GUI coverage to be complete while provider credentials and runs stay local.
The mock cannot prove version archives or quota enforcement, while the existing local SAIO offers those behaviours without a billable account.
The optional SAIO target and the existing archive journeys are implemented.
Quota and version-setting widget coverage follows separately.

8. **An optional existing local SAIO target, using the same suite.**
   Setting `AERONAUTES_SAIO_AUTH_URL` opts into the `saio` browser project alongside the mock; selecting `--project=saio` runs that project alone.
   Go reads this endpoint and the existing integration variables `AERONAUTES_SAIO_USER` and `AERONAUTES_SAIO_KEY`, with the integration fixture's defaults `test:tester` and `testing`, and saves a TempAuth bookmark without sending its key to the page.
   The Playwright child receives only a boolean SAIO selector; the launcher removes the SAIO endpoint and credential variables from that child's environment, while the Go application retains them.
   A request for this target without its endpoint fails; there is no fallback to a provider account or clouds.yaml.
   It uses the existing owned-container record and teardown, never starts, restarts or recreates SAIO, and never clears the account as a whole.
   Application restart/crash journeys run only on the mock project when both the clouds path and `AERONAUTES_SAIO_AUTH_URL` are absent, since restarting loses the control listener's ownership record.
   Setting the SAIO endpoint disables those journeys for the whole run, including its mock project; leaving it unset preserves the existing mock-only restart coverage.
   The existing version-archive journeys also run on SAIO, and new quota and version-setting journeys use the actual store rather than adding those semantics to the mock.
   Output may use the target label `saio` under decision 7's same private-evidence rules; native substitutions remain unchanged.
   This adds no hosted job and does not replace the representative-cloud release gate or authorise provider credentials in GitHub.

## Tests

- Default build: the `uijourney` stand-ins are absent (a test that fails if the desktop seams are not the ones wired).
- The control listener refuses a request without the token and a picker or confirmation with nothing scripted.
- Journeys, on the mock and both real clouds: create a bookmark and connect (mock only); browse a container; upload a folder with a segmented file and see the large-object disclosure; upload a name again with the overwrite confirmation acknowledged; download it back; edit object metadata; make a container public with the confirmation and private again; delete objects, a prefix and the container through the typed confirmations; switch between the two clouds.
- The 0.5.0 tag waits for these journeys only. The copy matrix, container and account metadata beyond the public-read step, and the settings backup follow later and do not hold the tag.
- The `uijourney` build refuses a non-loopback `WAILS_SERVER_HOST`, and the startup line carries only the two ports and the token.
