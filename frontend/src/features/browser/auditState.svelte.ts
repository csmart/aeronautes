// The §50 audit trail as the drawer shows it: a filtered search over the
// whole trail with its export (§51), and the recent slice of the connected
// account's changes that mutations refresh after their effects. All data
// flows through the Api seam, so the same fixture-backed fake tests it.

import type { Api, AuditEventDTO } from "../../lib/api";

export class AuditState {
  #api: Api;
  // The account a recent read is for, decided by the owner at read time.
  #scope: () => string;

  constructor(api: Api, scope: () => string) {
    this.#api = api;
    this.#scope = scope;
  }

  /** Selected §50 query results; background Recent reads stay separate. */
  events: AuditEventDTO[] = $state([]);

  // §50 viewer filters + paging over the drawer's Changes tab.
  #filterBookmark = $state("");
  get filterBookmark(): string { return this.#filterBookmark; }
  set filterBookmark(value: string) {
    if (value !== this.#filterBookmark) this.#clearQuery();
    this.#filterBookmark = value;
  }
  #filterOp = $state("");
  get filterOp(): string { return this.#filterOp; }
  set filterOp(value: string) {
    if (value !== this.#filterOp) this.#clearQuery();
    this.#filterOp = value;
  }
  #filterResult = $state("");
  get filterResult(): string { return this.#filterResult; }
  set filterResult(value: string) {
    if (value !== this.#filterResult) this.#clearQuery();
    this.#filterResult = value;
  }
  #seq = 0;
  loaded = $state(false);
  loading = $state(false);
  moreLoading = $state(false);
  error: string | null = $state(null);
  moreError: string | null = $state(null);
  exhausted = $state(false);

  get canMore(): boolean {
    const last = this.events.at(-1)?.id ?? 0;
    return this.loaded && !this.loading && !this.moreLoading
      && this.error === null && !this.exhausted
      && Number.isSafeInteger(last) && last > 0;
  }

  #clearQuery() {
    // Filter edits must retire replies even when values change back before
    // another search or a Svelte update can observe the intermediate value.
    this.#seq++;
    this.events = [];
    this.loaded = false;
    this.loading = false;
    this.moreLoading = false;
    this.error = null;
    this.moreError = null;
    this.exhausted = false;
  }

  #requirePage(page: AuditEventDTO[], beforeId = 0, bookmarkId = "") {
    if (page.length > 50) throw new Error("audit page exceeds its limit");
    let previous = beforeId || Infinity;
    for (const event of page) {
      if (bookmarkId !== "" && event.bookmarkId !== bookmarkId) {
        throw new Error("audit page names another bookmark");
      }
      if (!Number.isSafeInteger(event.id) || event.id <= 0 || event.id >= previous) {
        throw new Error("audit page IDs do not advance");
      }
      previous = event.id;
    }
  }

  async search(more = false) {
    if (more && !this.canMore) return;
    const seq = ++this.#seq;
    const bookmarkId = this.filterBookmark;
    const operationPrefix = this.filterOp;
    const result = this.filterResult;
    const beforeId = more ? this.events[this.events.length - 1].id : 0;
    const ownsQuery = () => seq === this.#seq
      && bookmarkId === this.filterBookmark
      && operationPrefix === this.filterOp && result === this.filterResult;
    this.loading = !more;
    this.moreLoading = more;
    this.error = null;
    this.moreError = null;
    try {
      const page = await this.#api.auditSearch({ operationPrefix, result, bookmarkId, beforeId, limit: 50 });
      if (!ownsQuery()) return;
      this.#requirePage(page, beforeId, bookmarkId);
      this.events = more ? [...this.events, ...page] : page;
      this.loaded = true;
      this.exhausted = page.length < 50;
    } catch {
      if (!ownsQuery()) return;
      if (more) this.moreError = "More audit events are unavailable. Previously loaded results are shown.";
      else this.error = "Audit events are unavailable. Retry this search.";
    } finally {
      if (ownsQuery()) {
        this.loading = false;
        this.moreLoading = false;
      }
    }
  }

  exporting = $state(false);
  exportMessage = $state<string | null>(null);
  exportError = $state<string | null>(null);

  // Archive the trail under the viewer's current filters to a file the
  // operator chooses (§51). A cancelled dialog writes nothing; the whole
  // matching trail is written regardless of which page is on screen.
  async exportTrail() {
    if (this.exporting) return;
    this.exporting = true;
    this.exportError = null;
    this.exportMessage = null;
    try {
      const result = await this.#api.exportAudit({
        operationPrefix: this.filterOp,
        result: this.filterResult,
        bookmarkId: this.filterBookmark,
        beforeId: 0,
        limit: 0,
      });
      if (result === null) {
        this.exportMessage = "Export cancelled; nothing was written.";
      } else {
        const events = result.count === 1 ? "event" : "events";
        this.exportMessage = `Exported ${result.count} ${events} to ${result.directory}/${result.fileName}.`;
      }
    } catch {
      this.exportError = "The audit log could not be exported.";
    } finally {
      this.exporting = false;
    }
  }

  recentEvents: AuditEventDTO[] = $state([]);
  recentLoaded = $state(false);
  recentLoading = $state(false);
  recentError = $state(false);
  #recentSeq = 0;

  async loadRecent() {
    const seq = ++this.#recentSeq;
    const bookmarkId = this.#scope();
    this.recentLoading = true;
    this.recentError = false;
    try {
      const page = await this.#api.auditRecent(50, bookmarkId);
      if (seq !== this.#recentSeq) return;
      this.#requirePage(page);
      this.recentEvents = page;
      this.recentLoaded = true;
    } catch {
      // Mutations await this observation after their effects; a read failure
      // must not reject the already returned mutation or bypass its view guard.
      if (seq === this.#recentSeq) this.recentError = true;
    } finally {
      if (seq === this.#recentSeq) this.recentLoading = false;
    }
  }
}
