// The application settings document (§38/§40) as the window edits it, the
// bookmark rail width it also persists, and its backup and restore (ADR
// 0098). One operation runs at a time so a late read never replaces a newer
// acknowledged write; the rail width waits for whatever is in flight. All
// data flows through the Api seam, so the same fixture-backed fake tests it.

import type { Api, AppSettingsDTO, RestoreResultDTO } from "../../lib/api";

export class SettingsState {
  #api: Api;
  // What the owner does once a restore has finished: a restore may add
  // bookmarks, which the rail must show.
  #onRestored: () => void;

  constructor(api: Api, onRestored: () => void) {
    this.#api = api;
    this.#onRestored = onRestored;
  }

  document: AppSettingsDTO | null = $state(null);
  status = $state<"unavailable" | "current" | "stale" | "unconfirmed">("unavailable");
  operation = $state<"get" | "set" | null>(null);
  readError = $state(false);
  attempt = $state<{ value: number; state: "pending" | "acknowledged" | "unconfirmed" } | null>(null);
  get canSave(): boolean {
    return this.operation === null && this.status === "current" && this.document !== null;
  }

  async load() {
    // A late read must never replace a newer acknowledged settings write.
    if (this.operation !== null) return;
    this.operation = "get";
    this.readError = false;
    try {
      this.document = await this.#api.appSettings();
      this.status = "current";
    } catch {
      this.readError = true;
      if (this.status !== "unconfirmed") {
        this.status = this.document ? "stale" : "unavailable";
      }
    } finally {
      this.#endOperation();
    }
  }

  async save(settings: AppSettingsDTO) {
    if (!this.canSave) {
      throw new Error("Refresh settings before making another change.");
    }
    const request = { ...settings };
    this.operation = "set";
    this.attempt = { value: request.maxActiveJobs, state: "pending" };
    try {
      this.document = await this.#api.saveAppSettings(request);
      this.status = "current";
      this.attempt = { value: request.maxActiveJobs, state: "acknowledged" };
    } catch (error) {
      this.status = "unconfirmed";
      this.attempt = { value: request.maxActiveJobs, state: "unconfirmed" };
      throw error;
    } finally {
      this.#endOperation();
    }
  }

  /** The latest rail width asked for while a settings read or write was in
   * flight; written against the document that operation leaves behind. */
  #pendingRailWidth: number | null = null;

  /** The bookmark rail's width persists in the settings document but is
   * chrome rather than a budget: it takes the one write path without the
   * drawer's attempt tracking, which names the active-job budget, and a
   * failure costs nothing but persistence, since the width already applies
   * to this window and the next drag writes again. A width asked for while
   * another settings operation is running waits for it, so the last width
   * the operator chose is the one stored. */
  async saveRailWidth(px: number) {
    if (this.operation !== null) {
      this.#pendingRailWidth = px;
      return;
    }
    if (this.status !== "current" || !this.document) return;
    this.operation = "set";
    try {
      this.document = await this.#api.saveAppSettings({ ...this.document, railWidthPx: px });
      this.status = "current";
    } catch {
      // Not persisted; the window keeps the width it shows.
    } finally {
      this.#endOperation();
    }
  }

  /** Every settings operation ends here so a rail width that waited for it
   * is written against the document it left behind. */
  #endOperation() {
    this.operation = null;
    if (this.#pendingRailWidth !== null) {
      const px = this.#pendingRailWidth;
      this.#pendingRailWidth = null;
      void this.saveRailWidth(px);
    }
  }

  backingUp = $state(false);
  backupMessage = $state<string | null>(null);
  backupError = $state<string | null>(null);

  // Write the operator's configuration — settings and bookmarks, no
  // credentials — to a file they choose (ADR 0098). A cancelled dialog
  // writes nothing; passwords are re-entered after a restore.
  async backUp() {
    if (this.backingUp) return;
    this.backingUp = true;
    this.backupError = null;
    this.backupMessage = null;
    try {
      const result = await this.#api.exportSettings();
      if (result === null) {
        this.backupMessage = "Backup cancelled; nothing was written.";
      } else {
        const marks = result.bookmarkCount === 1 ? "bookmark" : "bookmarks";
        this.backupMessage = `Backed up settings and ${result.bookmarkCount} ${marks} to ${result.directory}/${result.fileName}. Passwords are not included; re-enter them after a restore.`;
      }
    } catch {
      this.backupError = "Your settings could not be backed up.";
    } finally {
      this.backingUp = false;
    }
  }

  restoring = $state(false);
  restoreMessage = $state<string | null>(null);
  restoreError = $state<string | null>(null);

  // Restore the operator's configuration from a backup file they choose
  // (ADR 0098): replace the settings and add any bookmarks not already
  // present, leaving existing ones untouched. Credentials are never
  // restored, so each account's password is re-entered the next time it is
  // used. A cancelled dialog changes nothing.
  async restore() {
    if (this.restoring) return;
    // A settings write in flight (a rail resize sends the whole document)
    // would otherwise race the restore and could persist a stale document
    // over it. Hold the settings operation for the whole restore so such a
    // write waits, then refresh the cached settings before releasing it so
    // the waiting write applies against the restored document, not the old
    // one (ADR 0098; review db:991 finding 1).
    if (this.operation !== null) {
      this.restoreError = "Settings are busy; try the restore again in a moment.";
      return;
    }
    this.restoring = true;
    this.restoreError = null;
    this.restoreMessage = null;
    this.operation = "set";
    try {
      const result = await this.#api.importSettings();
      if (result === null) {
        this.restoreMessage = "Restore cancelled; nothing was changed.";
        return;
      }
      // Settings were replaced on the backend; read them back so a later
      // whole-document write cannot undo the restore.
      try {
        this.document = await this.#api.appSettings();
        this.status = "current";
      } catch {
        this.status = this.document ? "stale" : "unavailable";
      }
      this.restoreMessage = this.#restoreMessage(result);
    } catch (error) {
      this.restoreError =
        error instanceof Error && error.message ? error.message : "Your settings could not be restored.";
    } finally {
      // Releasing here flushes any rail width asked for during the restore,
      // written against the refreshed document.
      this.#endOperation();
      this.restoring = false;
      this.#onRestored();
    }
  }

  // #restoreMessage turns a restore result into one honest line: what landed,
  // what remains when it did not finish, and that passwords are re-entered on
  // use (ADR 0098; review db:991 finding 3).
  #restoreMessage(result: RestoreResultDTO): string {
    // Settings replace first; if they could not be confirmed the restore
    // stopped there, so report that and nothing else.
    if (!result.settingsApplied) {
      return result.detail ? `${result.detail}.` : "The restore could not be applied; nothing changed.";
    }
    const added = `${result.bookmarksCreated} ${result.bookmarksCreated === 1 ? "bookmark" : "bookmarks"}`;
    const skipped =
      result.bookmarksSkipped > 0
        ? ` ${result.bookmarksSkipped} already present ${result.bookmarksSkipped === 1 ? "was" : "were"} left unchanged.`
        : "";
    let message = `Restored your settings and added ${added}.${skipped}`;
    // Entries whose commit could not be confirmed are not counted as failed:
    // the row may be durable, so they are reported separately with reload
    // guidance, while only entries known not to be restored invite a re-run.
    const notRestored =
      result.bookmarksTotal - result.bookmarksCreated - result.bookmarksSkipped - result.bookmarksUnresolved;
    if (notRestored > 0) {
      message += ` ${notRestored} of ${result.bookmarksTotal} not yet restored`;
      message += result.detail
        ? ` (${result.detail}); re-run the restore to finish.`
        : "; re-run the restore to finish.";
    }
    if (result.bookmarksUnresolved > 0) {
      message += ` ${result.bookmarksUnresolved} could not be confirmed; reload when local state is available.`;
    }
    return `${message} Re-enter each account's password the next time you use it.`;
  }
}
