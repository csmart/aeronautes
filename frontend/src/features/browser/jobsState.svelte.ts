// The jobs drawer's data (§46/§47): the filtered job list with the
// throughput each poll derives, the recovery status, the ADR 0088
// interrupted-job notice with its dismissal, and the per-job reads the item
// pages make. Starting and following a job stays with the view that started
// it. All data flows through the Api seam, so the same fixture-backed fake
// tests it.

import { messageOf } from "../../lib/messageOf";
import type { Api, InterruptedDisclosuresDTO, JobDTO, RecoveryStatusDTO } from "../../lib/api";

export class JobsState {
  #api: Api;

  constructor(api: Api) {
    this.#api = api;
  }

  list: JobDTO[] = $state([]);
  /** Bookmark filter for the drawer; "" = all accounts (§47 owner input). */
  bookmarkFilter = $state("");
  error: string | null = $state(null);
  recoveryStatus = $state<RecoveryStatusDTO | null>(null);
  recoveryLoading = $state(false);
  recoveryError = $state(false);
  #recoverySeq = 0;
  // ADR 0088: the interrupted-job notice. Null while unknown; a page with
  // total 0 means there is nothing to disclose. The dismiss error is kept
  // apart from the read error because a failed dismissal (decision 7) must
  // say so without claiming the read itself is unavailable.
  interrupted = $state<InterruptedDisclosuresDTO | null>(null);
  interruptedError = $state(false);
  dismissError = $state(false);
  #interruptedSeq = 0;
  // Per-job throughput (bytes/s) computed from successive poll deltas.
  rates: Record<string, number> = $state({});
  #lastPoll: Record<string, { bytes: number; at: number }> = {};

  async loadRecoveryStatus(): Promise<void> {
    const seq = ++this.#recoverySeq;
    this.recoveryLoading = true;
    try {
      const status = await this.#api.recoveryStatus();
      if (seq === this.#recoverySeq) {
        this.recoveryStatus = status;
        this.recoveryError = false;
      }
    } catch {
      if (seq === this.#recoverySeq) {
        this.recoveryStatus = null;
        this.recoveryError = true;
      }
    } finally {
      if (seq === this.#recoverySeq) {
        this.recoveryLoading = false;
      }
    }
  }

  // The ADR 0088 notice read. An unavailable read is recorded as an error,
  // never as an empty page, so the banner shows the status as unknown rather
  // than silently saying there was nothing to disclose.
  async loadInterrupted(): Promise<void> {
    const seq = ++this.#interruptedSeq;
    try {
      const page = await this.#api.interruptedDisclosures(0);
      if (seq === this.#interruptedSeq) {
        this.interrupted = page;
        this.interruptedError = false;
      }
    } catch {
      if (seq === this.#interruptedSeq) {
        this.interrupted = null;
        this.interruptedError = true;
      }
    }
  }

  // Dismissal records only that the operator was told (ADR 0088 decision 3);
  // a failed write leaves the notice standing and says so (decision 7).
  async dismissInterrupted(jobIds: string[]): Promise<void> {
    this.dismissError = false;
    try {
      await this.#api.dismissInterrupted(jobIds);
    } catch {
      this.dismissError = true;
      return;
    }
    await this.loadInterrupted();
  }

  #seq = 0;
  #loadedFilter: string | null = null;

  /** The job a dialog's "Show in jobs" asked the drawer to open on. */
  revealJob: string | null = $state(null);

  async reveal(jobId: string): Promise<void> {
    // A filter on another account would hide the very job asked for.
    if (this.bookmarkFilter !== "" && !this.list.some((job) => job.id === jobId)) {
      this.bookmarkFilter = "";
    }
    await this.load();
    this.revealJob = jobId;
  }

  async load() {
    const seq = ++this.#seq;
    const filter = this.bookmarkFilter;
    if (filter !== this.#loadedFilter) {
      this.list = [];
      this.rates = {};
      this.error = null;
      this.#loadedFilter = filter;
    }
    try {
      const jobs = await this.#api.jobs(filter, false, 50);
      if (seq !== this.#seq || filter !== this.bookmarkFilter) return;
      const now = Date.now();
      const rates: Record<string, number> = {};
      for (const job of jobs) {
        const moved = job.counters.bytesCopied;
        const last = this.#lastPoll[job.id];
        if (last && now > last.at && moved >= last.bytes) {
          rates[job.id] = ((moved - last.bytes) * 1000) / (now - last.at);
        }
        this.#lastPoll[job.id] = { bytes: moved, at: now };
      }
      this.list = jobs;
      this.rates = rates;
      this.error = null;
    } catch (error) {
      if (seq === this.#seq && filter === this.bookmarkFilter) this.error = messageOf(error);
    }
  }

  history(bookmarkId: string, cursor: string, limit: number) {
    return this.#api.jobHistory(bookmarkId, cursor, limit);
  }

  async get(jobId: string, bookmarkId: string) {
    const job = await this.#api.job(jobId, bookmarkId);
    if (!job) return job;
    // ADR 0128 keeps finished history after both bookmark links are cleared.
    const matchesAccount = bookmarkId
      ? job.sourceBookmarkId === bookmarkId || job.destBookmarkId === bookmarkId
      : !job.sourceBookmarkId && !job.destBookmarkId
        && ["complete", "partial", "cancelled", "failed"].includes(job.state);
    if (job.id !== jobId || !matchesAccount) {
      throw new Error("job does not match the requested account and identity");
    }
    return job;
  }

  items(jobId: string, cursor: string, limit: number, bookmarkId: string) {
    return this.#api.jobItems(jobId, cursor, limit, bookmarkId);
  }

  itemTotals(jobId: string, bookmarkId: string) {
    return this.#api.jobItemTotals(jobId, bookmarkId);
  }
}
