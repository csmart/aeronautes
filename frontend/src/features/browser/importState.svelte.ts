// Importing bookmarks from rclone.conf or clouds.yaml (ADR 0114). Go reads
// the file and keeps every secret; this state holds only the preview's rows,
// the operator's choice and the outcomes, and follows the verification Go
// runs in the background after the save.

import type { Api, ImportPreview, ImportResult } from "../../lib/api";

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** How often the dialog asks how verification is going. */
const followEveryMs = 500;

export class ImportState {
  #api: Api;
  // A save adds bookmarks and a verification flags some, which the rail
  // must show.
  #onChanged: () => void;
  #following: ReturnType<typeof setTimeout> | null = null;

  constructor(api: Api, onChanged: () => void) {
    this.#api = api;
    this.#onChanged = onChanged;
  }

  preview: ImportPreview | null = $state(null);
  chosen: number[] = $state([]);
  verify = $state(true);
  result: ImportResult | null = $state(null);
  busy = $state(false);
  error: string | null = $state(null);

  /** Ask for a file and read it; "cancelled" when none was chosen. */
  async pick(): Promise<"picked" | "cancelled" | "failed"> {
    this.stop();
    this.busy = true;
    this.error = null;
    this.result = null;
    try {
      const preview = await this.#api.previewImport();
      if (preview.token === "") return "cancelled";
      this.preview = preview;
      // Duplicates and skipped rows start unticked (ADR 0114 decision 6).
      this.chosen = preview.rows.filter((row) => row.state === "importable").map((row) => row.index);
      this.verify = true;
      return "picked";
    } catch (error) {
      this.preview = null;
      this.error = messageOf(error);
      return "failed";
    } finally {
      this.busy = false;
    }
  }

  toggle(index: number, on: boolean) {
    this.chosen = on ? [...new Set([...this.chosen, index])] : this.chosen.filter((i) => i !== index);
  }

  async save() {
    if (this.preview === null || this.chosen.length === 0 || this.busy) return;
    this.busy = true;
    this.error = null;
    try {
      this.result = await this.#api.saveImport(this.preview.token, [...this.chosen].sort((a, b) => a - b), this.verify);
      this.#onChanged();
      if (!this.result.done) this.#follow(this.result.token);
    } catch (error) {
      this.error = messageOf(error);
    } finally {
      this.busy = false;
    }
  }

  /** Stop following; Go's verification goes on and its flags still land. */
  stop() {
    if (this.#following !== null) clearTimeout(this.#following);
    this.#following = null;
  }

  #follow(token: string) {
    this.#following = setTimeout(async () => {
      try {
        const result = await this.#api.importStatus(token);
        if (this.#following === null) return;
        this.result = result;
        if (result.done) {
          this.#following = null;
          this.#onChanged();
          return;
        }
      } catch (error) {
        this.error = messageOf(error);
        this.#following = null;
        return;
      }
      this.#follow(token);
    }, followEveryMs);
  }
}
