# Malformed native listing identities

This is the before-fix evidence for [ADR 0029](../adr/0029-listing-identity-decoding.md), a listing prerequisite for the [Copy item-record migration](../adr/0028-transfer-item-records.md).
The probes ran against reviewed source `7d61bc7a4109655d0e18c8bc7eefaf431d7a7eed`, whose tree exactly matches the subsequent [PR58 master delivery](2026-09-08-transfer-item-storage.md#delivery) at `17cbe69058aa20fe1d0d6330c64f5103f1228e58`.
They add external test files through Go overlays and leave every application Go source and SQL migration unchanged.
These are deliberately failing expected-behaviour probes, archived as inert text until the separately reviewed implementation adds maintained regressions.

## Session boundary

The real session's container, object and pseudo-directory iterators consume controlled local HTTP listing responses.
Every page starts with a valid control row, followed by the selected name case.
The expected failure is rejection of the whole malformed page with zero published rows and one request.
The controls must preserve both exact names and advance to the terminal empty page.

| Final session probe | Leaf cases | Expected failures | Valid controls passed | Skips |
| --- | ---: | ---: | ---: | ---: |
| Three listing kinds, nineteen name cases each | 57 | 33 | 24 | 0 |

The eleven malformed cases per listing kind cover an invalid byte, a stray continuation byte, an overlong encoding, encoded surrogate bytes, an out-of-range code point, a truncated rune and five unpaired/reversed surrogate-escape forms.
All are accepted under replacement-character names by the current decoder.
The eight valid controls cover literal and escaped U+FFFD, literal and escaped supplementary characters, ordinary Unicode, a combining sequence, a literal backslash escape and embedded zero/newline characters.
All valid controls preserve their expected names.
Checking only the already decoded Go string cannot distinguish the malformed cases from legitimate names containing U+FFFD.

## Actual Copy boundary

The final actual-service probe runs eight cases on each of owned mock Swift and two-account SAIO.
A response proxy modifies only the selected source container's first `scope/` JSON listing page, after receiving a complete bounded successful response from that provider.
It changes the exact quoted name token of an existing fixture object; the independent snapshot client bypasses that proxy.
The underlying selected objects have valid names, including a replacement character or supplementary character and a newline that selects the existing native route.
The fixture does not require Swift to store malformed names or claim that Swift naturally emits malformed responses.

| Final actual Copy probe | Leaf cases | Expected failures | Valid controls passed | Skips |
| --- | ---: | ---: | ---: | ---: |
| Mock Swift and two-account SAIO | 16 | 10 | 6 | 0 |

The five faults on each provider are an invalid raw byte, a lone high surrogate, a lone low surrogate, a repeated `name` member and an equivalent escaped duplicate member.
The three valid controls are a literal replacement character, an escaped replacement character and a valid surrogate pair.
All malformed cases should fail before any destination mutation or publication of the earlier valid row from that same page.
Instead, every case currently reports `complete`, plans/copies/verifies two native-routed objects, reports fifteen planned/copied bytes and zero failed objects, and performs two destination PUTs.
The valid controls produce those same expected successful totals and preserve the exact independently expected content digests.
Ordinary and escaped duplicate identity fields are currently resolved by the legacy decoder's later value.

Every case checks the complete job counters, terminal time/state, public versus durable job result, terminal audit operation/result and source bookmark/resource authority.
The expected retained recovery record names the destination bookmark/container and this job's generated staging prefix, with one job and one record in the recovery status.
The snapshots cover every container under each harness's owned namespace prefix, including unselected objects, another job's staging sentinel and shared DLO/SLO aliases and segments.
They verify no source mutation and no unselected destination changes; failed cases separately require zero destination changes at all.
This is complete owned-fixture scope, not observation of unrelated containers elsewhere in either account.

All sixteen cases then close the parent database and open it in a separate child, compare job/audit/recovery state before and after `FailInterrupted`, and verify that the reopen issued no remote request.
An explicit final witness confirms the reopen and subsequent collateral comparisons completed in every case, including the expected failures.
No unexpected test diagnostics were present.

## Evidence and replay

The [session probe](probes/2026-09-08-listing-fidelity_test.go.txt) is byte-identical to the executed source, with SHA-256 `2799ac6ccff98602ba18cfd0ef2f51e08af795db4b5eb9ca63af68b8c96d9df0`.
The [final actual Copy probe](probes/2026-09-08-copy-listing-fidelity_test.go.txt) is byte-identical to the executed source, with SHA-256 `9ec60924459ddfbfaf92a32362f2ec7841455e58f446a3ea686c209abb49cf59`.
Both contain synthetic fixture data only.

The recorded Go 1.27.1 commands use `-json -race -count=1 -timeout=10m` and external overlays into `internal/swiftcontrol/listing_fidelity_probe_test.go` or `internal/systemtest/copy_listing_fidelity_probe_test.go`.
The selected tests are `TestListingFidelityProbe` and `TestCopyListingFidelityProbe(Mock|RealSwift)` respectively.
Replay the system probe only with owned mock/two-account fixtures configured as in the existing system-test setup.
The original before-fix runs exit one because their required refusal assertions fail; the six actual-service controls exit successfully at the subtest level.
Earlier development versions without duplicate fields or the final exact-outcome/reopen witness are separate runs and are not added to these totals.

The installed Go 1.27.1 standard library was also checked independently with ten raw-value examples.
Raw JSON capture preserved its input bytes, and strict validation rejected malformed Unicode and ordinary/escaped duplicate members while accepting the valid Unicode and escaped-backslash controls.
Differently cased member names remain distinct to that syntactic validator; this does not certify legacy struct-field alias semantics.
The [standard validator contract](https://pkg.go.dev/encoding/json/jsontext#Value.IsValid) supplies the proposed implementation primitive, subject to design and implementation review.

## Limits and next work

This evidence establishes controlled native listing/Copy failures and valid-name controls.
It does not establish rclone's independent JSON parsing, a complete per-effect ledger, strong verification, immutable remote identity or authority to retry.
The child check is an orderly database reopen, not full application restart, process kill, orphan-child control, fsync or power-loss evidence.
No common fault/barrier framework, scenario-coverage credit or valuable-data clearance is claimed.

Review ADR 0029, implement raw-row validation through a third checked Swift-source patch, maintain these regressions and add lifecycle/unknown-field/duplicate/negative controls.
Then complete the separately reviewed Copy adapter, item-access UI, interrupted-result projection and unresolved-rerun refusal, followed by local upload/download migrations.
The wider P12/P13/P22 work and valuable-data release hold remain open.
