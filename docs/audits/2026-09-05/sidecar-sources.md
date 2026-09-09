# Candidate sidecar source collection — 9 September 2026

The 225 missing dependency sources from the [initial sidecar assessment](sidecar-assessment.md#remaining-openpgp-question) are now retained and verified.
All 252 distinct dependency module/version pairs in the six candidate binaries are available, together with the rclone v1.75.1 source at `687d264b689b8c49a67e2e52a8a5e0caa01c04ce`.
This closes the source availability gap only.
P31, SEC-11, the OpenPGP advisory disposition and valuable-data acceptance remain open; the accepted engine stays v1.74.3.

## Collection and integrity

Each new module archive and `go.mod` was checked against the exact candidate source's `go.sum`; the archive checksum also matches the corresponding embedded binary module sum.
Go 1.27.1 performed authenticated checksum-database verification, and the collector independently recomputed the archive and module-file h1 values before retaining the extracted files.
No candidate or dependency code, generator, application or test was executed by this collection.
The only compiled program was the existing standard-library-only import reader.

The new collection contains 225 archives, 52,000 regular extracted source files and 1,071,861,064 extracted source bytes.
The complete 253-root inventory, including the 27 previously retained dependency roots and the main source, contains 57,380 regular files.
All sources were retained, including tests, examples, platform-specific files and generator templates.
The final archival manifest covers 55,033 files in the new evidence bundle; this is a bundle count, not the complete source-file count.
The independent readback also verifies all 7,308 original evidence files and 899 files from the first attempt, without changing their bytes.
The [structured record](evidence/2026-09-09-sidecar-sources.json) identifies the collection, raw parser output, archival manifest, template sources and preservation checks.

Private GOPATH, module/build/temp caches and XDG configuration, cache and data roots were supplied explicitly.
Telemetry was disabled before the first Go command, ambient authentication and proxy settings were not inherited, and the effective Go settings were checked.
The module proxy had no direct fallback, and checksum verification remained enabled with the known `sum.golang.org` key.
This is environment and source-based containment evidence; no syscall trace was available.

## Import-reader result

The unchanged `go/parser` ImportsOnly reader recorded every one of the 46,307 Go-named files in the complete root inventory.
All 3,356 earlier parser records are unchanged.
It parsed import declarations from 46,302 files and retained five parse-error records.
ImportsOnly checks the package/import prefix, not the complete file syntax; other templates with valid prefixes can therefore be among the parsed records.
The reader process exited zero after writing those records; the enclosing census command correctly exited one because the contract rejects any parse error.
The failed strict census was not relabelled as successful, and its finish phase did not run.

All five failures are in `github.com/cloudflare/circl v1.6.4`:

| Template | Recorded parser location | Existing generated outputs |
| --- | --- | ---: |
| `kem/kyber/templates/pkg.templ.go` | 8:1, template directive before package | 6 |
| `kem/sike/templates/pkg.templ.go` | 14:9, template package identifier | 3 |
| `pke/kyber/templates/pkg.templ.go` | 12:9, template package identifier | 3 |
| `sign/dilithium/templates/acvp.templ.go` | 7:9, template package identifier | 3 |
| `sign/dilithium/templates/pkg.templ.go` | 7:1, template directive before package | 6 |

Their complete source contains literal template syntax and a `// +build ignore` header.
Four generator source files name them through `text/template.ParseFiles`; their fixed instances and path methods identify the 21 existing output files in the table.
Those output files are already present, hashed and parsed in the unchanged raw inventory.
Manual inspection accounts for all 31 template import declarations, including four interpolated paths and the conditional Dilithium imports.
Expanding only the source's fixed instance fields and conditions gives import sets matching all 21 retained outputs, including aliases, with 129 import occurrences.
Those declarations and fixed expansions name only standard-library or CIRCL packages.
The generators were read as source and were not run; matching regeneration or source-to-binary correspondence is not established.
The five original error records and their complete source bytes remain present rather than being excluded by an exception.
This assessment binds exactly the five recorded template hashes, four generators and their fixed instances; a changed input or additional parser error requires fresh assessment.

Among the parsed records, the same 46 imports reference the deprecated OpenPGP family, all from within that family itself.
Thirty-six are in non-test-named files and span nineteen files.
No external importer appears among the parsed records.
These observations do not turn the failed strict import census into a complete import, selected-build, initializer, call or binary-linkage assessment.
No “not linked”, “not reachable” or false-positive disposition is accepted for GO-2026-5932.

## Preserved failures and remaining work

The first attempt's initial setup failed because the checker expected `GOENV=off` to be reported literally, while the pinned Go implementation reports an empty disabled configuration filename.
That attempt's Go environment query exited zero, but its setup wrapper exited one; no reader build or download had yet run.
The correction matched the pinned implementation and preserved the failure.
The first download then failed at the explicitly configured proxy checksum endpoint with HTTP 404, before any pair was verified.
Read-only endpoint checks identified the canonical checksum service as available; a fresh attempt used that endpoint with the same verification key and exact module requests.
All 225 downloads in the fresh attempt succeeded before the separate parser failure described above.
The archive, checksum, source identity and parser guards were not weakened.

The next evidence step is selected-build and exact source-to-artifact analysis, retaining the strict parser failure and this separate bounded template assessment.
The unexplained modified-source metadata in both Linux candidate binaries, six-target compatibility, application/native-library/npm coverage and every enabled-path advisory disposition remain outstanding.
The [P31 acceptance criteria](remediation-plan.md#acceptance-gates-and-completion), all 46 findings, 35 work packages and seven release gates remain unchanged.
