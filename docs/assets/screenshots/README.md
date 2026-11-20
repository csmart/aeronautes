# Screenshots

The main README uses `06-inspector-dlo-0.5.0.png`, a desktop capture supplied during preparation for 0.5.0 on 20 November 2026.
It shows the current container browser, DLO inspector, segment table and metadata controls.
The capture retains its original dimensions and development-build label.

The numbered captures below are historical development screenshots against containerised Swift (SAIO, two accounts), taken headless at 1280×800 with the E2E rig.
They record earlier behaviour and are not a feature list for the current release: Mirror is unavailable in 0.5.0, and Copy now follows the store and transfer engine's outcomes rather than the old staging protocol.

- `01-browser.png` — four-pane browser: bookmarks, containers with
  backstage classification, working list, object HEAD detail.
- `02-inspector-dlo.png` — the DLO inspector: logical vs listed size,
  exact manifest, segment table, backstage jump, manifest-only delete.
- `03-copy-preview.png` — cross-account Copy preview: honest §28.4
  counts and the stage-and-commit overwrite disclosure.
- `04-mirror.png` — Mirror armed by typed confirmation: the would-delete
  set, and the delete half's rules stated in the dialog.
- `05-jobs-drawer.png` — the jobs drawer after a completed cross-account
  copy, with the active-job budget and benchmark controls.

For automated captures, the smoke/E2E tooling (`scripts/smoke.sh`) shows the pattern.
Keep the capture's date and provenance explicit when adding or replacing screenshots.
