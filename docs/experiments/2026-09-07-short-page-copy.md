# Mixed Copy under forced short listing pages

## Scope and oracle

During P22 native continuation work, the actual bound Copy method was exercised with the pinned rclone 1.74.3 and the native engine against two owned accounts on mock Swift and containerized Swift (SAIO).
The prototype starts from master `330ba3a2ea68e0dab998ab388d54bb852f284120` and changes only native continuation and its test harness; the runner and both engines' transfer logic are unchanged.
ADR 0021 requires complete native planning and fresh verification through an accepted empty page.
The wider P22 acceptance still requires execution and verification of the full intended namespace with both engines.

An owned reverse proxy caps every JSON listing of the selected source and destination containers to one row, preserves markers and returns the actual upstream Swift response.
The cap applies to rclone requests too.
Independent before/after namespace snapshots bypass the proxy.
These are controlled provider-compatibility scenarios, not evidence that standard Swift naturally returns short nonterminal pages.

## Observations

The three source names are `scope/a`, `scope/b%2F +日本語` and `scope/c\nnative`, where `\n` represents one literal newline.
Their bodies contain three, four and five bytes respectively; the first two names route through rclone and the last routes native.
Source and destination also contain out-of-scope sentinels.

The corrected native preview sees all three names, all twelve bytes and one native-routed object on both mock Swift and SAIO.
The original complete-Copy assertions fail on both fixtures: the job plans three objects but reports partial, with two copied, two verified and eight bytes copied.
The error names `b%2F +日本語` as missing from staging despite engine-reported success, then unverified.
The current aggregate failed counter is two for these two failure observations of one missing name; exact durable outcome accounting remains P13 work.
This is a detected omission, not evidence of complete engine execution.

In a separate copy of the same source and tests, removing only the proxy's limit replacement makes both complete-Copy assertions pass under the race detector.
That control still uses the proxy, actual bound service, actual pinned engines, the same names and independent snapshots.
It isolates the page cap from general mixed routing or literal-name failure; it does not yet prove which internal rclone operation caused the omission.

The author's raw evidence is preserved under `/tmp/aeronautes-remediation-20260905`: `p22-continuation-positive.json/.exit` contains the four passing read/preview cases and two failing complete-Copy cases; `p22-continuation-rclone-no-cap.json/.exit` contains the two passing uncapped Copy cases.
The uncapped scratch module preserves the original complete-Copy assertions; `p22-rclone-no-cap.py` records the single cap-removal change.
No failing run is credited in the final scenario-coverage result.

## Delivery decision

Continue the independently scoped native iterator fix, with complete native-routed new-name Copy and fresh verification under the global cap.
Keep the mixed-name preview, preserve the actual capped mixed Copy as a partial-outcome characterisation, and pair it with an uncapped mixed success control.
The partial characterisation earns no successful-Copy receipt.
Do not limit the cap to native requests, remove the observed mixed limitation, or claim that native success closes P22/DS-13.

After native continuation, investigate the smallest supported way for the pinned engine to consume the full selected set despite short pages.
Any engine/flag decision needs its own design update and independent review before implementation.
Its acceptance must cover both engines under the same cap and retain staging, credential isolation, literal names, conditional creation, fresh verification, and the existing Mirror/overwrite refusals.
Wider response/resource budgets, durable outcomes, external writers, ownership and the valuable-data release hold remain open.
