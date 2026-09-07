# P23 literal-input reproductions — 7 September 2026

These observations prepare the P23 sequence in DESIGN §65.11 after the delivered P22 work.
They do not change production code or close UI-04, UI-07 or UI-14.

## Original frontend probes on current master

The source is master `c7b669e422465754e9674f51e3e364986c905277`.
A disposable copy of its tracked frontend uses the same lockfile and installed dependencies as the prior frontend verification.
The preserved original audit probes are copied without assertion changes; only their absolute imports are redirected to this current source.
Four P23 cases are selected and the eight other archived cases are unselected, not successful evidence.

| Original probe | Observed assertion failure |
| --- | --- |
| Preserve destination prefix bytes in Copy preview | `"  object prefix  "` is sent as `"object prefix"` |
| Send the exact server prefix | `"external"` is sent as `"external/"` |
| Retain or explicitly reject `__proto__` metadata | The key is absent and no error is reported |
| Report added keys independently of `Object.prototype` | `constructor` is not reported as added |

All four selected tests fail their intended assertions, with process exit 1 and no setup failures.
The raw record is `/tmp/aeronautes-remediation-20260905/p23-original-before.json`, `.log` and `.exit`.
`p23-original-probes-inputs.json` records the source commit, original/copied probe hashes and the import-only change.
`p23-prepare-probes.py` records the disposable source/dependency setup.

These are frontend component/helper observations, not evidence of a destructive remote Mirror execution.
Mirror currently supports comparison preview and refuses every start.
Transfer requests still require an empty or slash-terminated prefix, so the first probe demonstrates altered input even though its literal unslashed prefix is unsupported for execution.
The correction must surface that existing validation error rather than silently converting a whitespace-only prefix into root scope.

## Independent metadata-header observations

The [Swift API reference](https://docs.openstack.org/api-ref/object-store/#create-or-update-object-metadata) defines metadata names as case-insensitive ASCII HTTP token names and documents underscore-to-hyphen conversion.
The [Swift WSGI header adapter](https://github.com/openstack/swift/blob/master/swift/common/swob.py) maps header names through an environment representation that cannot distinguish underscores from hyphens.
This documents an identity limitation; the exact running fixture must still be observed independently.

A raw HTTP probe creates one uniquely named Aeronautes-owned container on the local SAIO account, then five owned plain objects with known bytes and an unrelated `Keep` metadata sentinel.
Each object receives a metadata POST, an independent HEAD and a GET compared to the known source bytes.
Three container metadata POSTs are also followed by independent HEAD observations.

| Resource | Submitted key | Observed result |
| --- | --- | --- |
| Object | `constructor` | `Constructor` retains the submitted value and the sentinel |
| Object | `toString` | `Tostring` retains the submitted value and the sentinel |
| Object | `__proto__` | HTTP 202, but the submitted key is absent; only the sentinel remains |
| Object | `a_b` | HTTP 202, but the submitted key is absent; only the sentinel remains |
| Object | `A-B` | `A-B` retains the submitted value and the sentinel |
| Container | `constructor` | `Constructor` retains the submitted value |
| Container | `__proto__` | HTTP 204, but the submitted key is absent; earlier `Constructor` remains |
| Container | `a_b` | HTTP 204, but the submitted key is absent; earlier `Constructor` remains |

This fixture drops underscore-bearing headers rather than returning renamed keys.
Do not generalise its observed dropping to every deployment or claim that literal existing `__proto__` metadata was erased by the application.
Both the documented normalization and this fixture's dropping violate exact preservation of an entered underscore-bearing key, supporting explicit refusal before mutation.
The probe leaves every object's known bytes unchanged and verifies deletion of its owned objects and container afterwards.
It does not exercise the bound application mutation methods, DLO preservation, concurrency or whole-account collateral safety.

The script and raw observations are `/tmp/aeronautes-remediation-20260905/p23-metadata-wire-probe.py` and `p23-metadata-wire-observations.json`.
The record includes all eight header observations, five known-body digests and successful cleanup verification, without credentials.
The maintained application tests must separately prove complete working-copy serialization, supported-key round trips and pre-mutation refusal with independent namespace snapshots.
