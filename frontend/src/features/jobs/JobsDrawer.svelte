<!-- The §47 drawer, live: in-flight jobs with progress, rates and
  cancel, filterable by account (owner input 2026-08-29: multiple
  accounts in play need a filter), with the audit trail behind a
  second tab. Polls on a fixed 1.5s interval while the app runs
  (adaptive cadence is §65.10 scope). -->
<script lang="ts">
  import { onMount } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { AppSettingsDTO, JobDTO, InterruptedDisclosureDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";
  import { v1Offers } from "../../lib/v1.svelte";
  import JobHistory from "./JobHistory.svelte";
  import JobItems from "./JobItems.svelte";

  let { browser }: { browser: BrowserState } = $props();
  const id = $props.id();

  let open = $state(false);
  let tab: "jobs" | "history" | "changes" = $state("jobs");
  let expanded: string | null = $state(null);
  // The budgets the service accepts (ADR 0105): one it would refuse is not
  // offered.
  const BUDGET_CHOICES = [1, 2, 3, 4];
  // While a budget save is unanswered, or its outcome is unknown and no read
  // has followed, the control shows the budget that save asked for;
  // otherwise what was last read. Other writes hold the settings too, a rail
  // resize or a restore, and say nothing about the budget.
  const budgetAsked = $derived.by(() => {
    const attempt = browser.settings.attempt;
    const unknown = attempt?.state === "unconfirmed" && browser.settings.status === "unconfirmed";
    return attempt?.state === "pending" || unknown ? attempt?.value : undefined;
  });
  const budgetValue = $derived(String(budgetAsked ?? browser.settings.document?.maxActiveJobs ?? ""));
  type CancellationNotice = {
    jobId: string;
    state: "pending" | "requested" | "unconfirmed";
  };
  const CANCELLATION_NOTICE_LIMIT = 50;
  let cancellationNotices = $state<CancellationNotice[]>([]);
  let cancellationRefreshing = $state(false);
  let disposed = false;
  // §42 benchmark: typed-account consent inline.
  let benchOpen = $state(false);
  let benchConfirm = $state("");
  let benchError: string | null = $state(null);

  const TERMINAL = ["complete", "partial", "cancelled", "failed"];

  $effect(() => {
    browser.jobs.bookmarkFilter;
    expanded = null;
  });

  function isActive(job: JobDTO): boolean {
    return !TERMINAL.includes(job.state);
  }

  function operationLabel(type: string): string {
    switch (type) {
      case "copy": return "Copy";
      case "upload": return "Upload";
      case "download": return "Download";
      case "delete_prefix": return "Delete prefix";
      case "empty_container": return "Empty container";
      case "delete_container": return "Delete container";
      case "delete_selected": return "Delete selected";
      case "delete_segments": return "Delete manifest + segments";
      default: return type || "Unknown operation";
    }
  }

  // Admission is read from the ledger, not the state alone: a recorded
  // execution is an attempt even from a queued state, so only a queued job that
  // attempted nothing claims nothing was attempted (ADR 0088 decision 5).
  function interruptionAdmission(e: InterruptedDisclosureDTO): string {
    if (e.attempted) return "Work had begun when the run ended; some of it may have completed.";
    // The records show no step; this does not by itself prove no request was
    // made, since a bulk sweep issues listings and container requests that are
    // not member steps, so the wording stays with what the ledger shows.
    if (e.interruptedState === "queued") return "Nothing is recorded as attempted; it had not left the queue.";
    return `Nothing is recorded as attempted; it reached ${e.interruptedState} when the run ended.`;
  }

  // The acknowledged item facts, positive first (ADR 0031 line 94): the copied
  // and verified members and their bytes stand beside the failed, refused and
  // inconclusive ones, never erased by them. An absent ledger is its own
  // statement, not a zero count (ADR 0088 decision 5).
  function interruptionItems(e: InterruptedDisclosureDTO): string {
    if (!e.itemsCaptured) return "No item ledger was kept for this job.";
    const f = e.items;
    const parts = [`${f.items} recorded ${f.items === 1 ? "member" : "members"}`];
    const add = (n: number, label: string) => {
      if (n > 0) parts.push(`${n} ${label}`);
    };
    add(f.copied, "copied");
    add(f.verified, "verified");
    add(f.failed, "failed");
    add(f.conflicts, "in conflict");
    add(f.refused, "refused");
    add(f.unresolved, "unresolved");
    add(f.unverified, "unverified");
    add(f.deleted, "deleted");
    add(f.unsettled, "unsettled");
    let summary = parts.join(", ") + ".";
    if (f.copiedBytes > 0) summary += ` ${formatBytes(f.copiedBytes)} copied.`;
    return summary;
  }

  // Every deletion job settles members and carries a separate deleted count;
  // the settled counter (objectsCopied) includes changed, refused and
  // unverified members, so it is never labelled "deleted" (ADR 0095).
  function isDeletionFamily(type: string): boolean {
    return type === "delete_selected" || type === "delete_prefix" || type === "empty_container"
      || type === "delete_container" || type === "delete_segments";
  }

  function progressOf(job: JobDTO): number | null {
    const planned = job.counters.objectsPlanned;
    if (planned <= 0 || !isActive(job) || job.counters.deletionAccountingIncomplete) {
      return null;
    }
    // A mirror copies and deletes, so its bar follows whichever count is
    // further along. A deletion's copied counter is already its settled
    // members (see isDeletionFamily), which is the progress its bar shows.
    const done =
      job.type === "mirror"
        ? Math.max(job.counters.objectsCopied, job.counters.objectsDeleted)
        : job.counters.objectsCopied;
    return Math.min(1, done / planned);
  }

  function timeOf(iso: string): string {
    return iso === "" ? "" : iso.replace("T", " ").replace(/(\.\d+)?Z$/, "");
  }

  function cancellationState(jobId: string) {
    return cancellationNotices.find((notice) => notice.jobId === jobId)?.state;
  }

  function cancelLabel(jobId: string): string {
    switch (cancellationState(jobId)) {
      case "pending": return "Requesting…";
      case "requested": return "Requested";
      case "unconfirmed": return "Retry cancellation";
      default: return "Cancel";
    }
  }

  function canCancel(jobId: string): boolean {
    if (disposed || browser.jobs.error || !browser.jobs.list.some((job) => job.id === jobId && isActive(job))) {
      return false;
    }
    const state = cancellationState(jobId);
    if (state === "pending" || state === "requested") return false;
    return state === "unconfirmed" || cancellationNotices.length < CANCELLATION_NOTICE_LIMIT;
  }

  function updateCancellation(jobId: string, state: CancellationNotice["state"]) {
    if (disposed) return;
    cancellationNotices = cancellationNotices.map((notice) =>
      notice.jobId === jobId ? { jobId, state } : notice);
  }

  async function cancel(jobId: string) {
    // Admission must be guarded before the next render disables the button.
    if (!canCancel(jobId)) return;
    const owner = browser;
    if (cancellationState(jobId)) updateCancellation(jobId, "pending");
    else cancellationNotices = [...cancellationNotices, { jobId, state: "pending" }];
    try {
      await owner.cancelJob(jobId);
      updateCancellation(jobId, "requested");
    } catch {
      if (disposed) return;
      updateCancellation(jobId, "unconfirmed");
      await owner.jobs.load();
    }
  }

  function dismissCancellation(jobId: string) {
    if (cancellationState(jobId) === "pending") return;
    cancellationNotices = cancellationNotices.filter((notice) => notice.jobId !== jobId);
  }

  async function refreshCancellationStatus() {
    if (disposed || cancellationRefreshing) return;
    cancellationRefreshing = true;
    try {
      await browser.jobs.load();
    } finally {
      if (!disposed) cancellationRefreshing = false;
    }
  }

  let actionError: string | null = $state(null);

  async function rerun(job: JobDTO) {
    actionError = null;
    try {
      await browser.rerunJob(job.id);
    } catch (e) {
      actionError = e instanceof Error ? e.message : String(e);
    }
  }

  // Only these jobs pause, and only before they are finishing (ADR 0104
  // decisions 1 and 7); the backend refuses the rest, so they are not offered.
  const PAUSABLE_TYPES = ["upload", "download", "copy"];
  const PAUSABLE_STATES = ["queued", "planning", "running"];

  function canPause(job: JobDTO): boolean {
    return PAUSABLE_TYPES.includes(job.type) && PAUSABLE_STATES.includes(job.state) && job.pause === "";
  }

  // A pause can be withdrawn before it takes effect as well as after. A job
  // that is finishing will reach no further unit, so a pause asked earlier
  // has nothing left to wait for and is not offered back as a Resume.
  function canResume(job: JobDTO): boolean {
    return (job.pause === "pausing" && PAUSABLE_STATES.includes(job.state)) || (job.state === "paused" && job.pause === "");
  }

  // A pause takes effect at the head of the job's next unit (ADR 0104
  // decision 1): a queued or planning job parks before it transfers anything,
  // and a running one at its next batch, or never if it is on its last.
  function pauseNote(job: JobDTO): string {
    if (job.pause === "pausing" && (job.state === "queued" || job.state === "planning")) {
      return "Pauses before it transfers anything.";
    }
    if (job.pause === "pausing" && job.state === "running") {
      return "Pauses when the current batch finishes, unless it is the last.";
    }
    if (job.pause === "resuming") return "Resumes when a job slot is free.";
    if (job.state === "paused") return "Paused. Nothing more is transferred until it is resumed.";
    return "";
  }

  // One request per job at a time: a second Resume sent before the list
  // refreshes would be refused as having nothing to withdraw. Another job's
  // request does not wait for this one. The guard is here and not the
  // button's disabled state, because disabling the focused button makes
  // WebKitGTK drop the keyboard focus.
  let pauseBusy: Record<string, boolean> = $state({});

  async function pauseOrResume(job: JobDTO, action: "pause" | "resume") {
    if (pauseBusy[job.id]) return;
    actionError = null;
    pauseBusy[job.id] = true;
    try {
      if (action === "pause") await browser.pauseJob(job.id);
      else await browser.resumeJob(job.id);
    } catch (e) {
      // The refusal names the job by its id, which the row does not show;
      // the row's own words say which job it was.
      const message = e instanceof Error ? e.message : String(e);
      actionError = `${operationLabel(job.type)} ${job.source} → ${job.destination}: ${message}`;
    } finally {
      delete pauseBusy[job.id];
    }
  }

  onMount(() => {
    const refresh = () => {
      void browser.jobs.load();
      void browser.jobs.loadRecoveryStatus();
    };
    refresh();
    // Reopening the drawer is not the explicit recovery read after a lost
    // reply, whichever setting that save was for.
    if (browser.settings.status !== "unconfirmed") void browser.settings.load();
    const tick = setInterval(refresh, 1500);
    return () => {
      disposed = true;
      clearInterval(tick);
    };
  });

  // ETA from the live rate and the remaining planned bytes.
  function etaOf(job: JobDTO): string {
    const rate = browser.jobs.rates[job.id] ?? 0;
    const remaining = job.counters.bytesPlanned - job.counters.bytesCopied;
    if (!isActive(job) || rate <= 0 || remaining <= 0) {
      return "";
    }
    const seconds = Math.round(remaining / rate);
    if (seconds < 90) {
      return `~${seconds}s`;
    }
    return `~${Math.round(seconds / 60)}m`;
  }

  // The numeric settings share one treatment: the service refuses a value
  // outside the range rather than clamping it, so each control shows the last
  // observed value until a save is acknowledged, and a refusal beside itself.
  type BoundedSetting = {
    field: "downloadObjectCeiling" | "browserRetentionCeiling" | "maxQueuedJobs" | "maxConnsPerHost";
    /** Names the control's status element. */
    slug: string;
    label: string;
    title: string;
    min: number;
    max: number;
    step: number;
    /** What the observed value means, in the operator's terms. */
    says: (value: string) => string;
  };
  const BOUNDED_SETTINGS: BoundedSetting[] = [
    {
      // ADR 0072.
      field: "downloadObjectCeiling", slug: "ceiling", label: "Download ceiling",
      title: "the most objects one download selection may hold; a wider prefix is refused rather than cut short (1,000 to 5,000,000)",
      min: 1000, max: 5_000_000, step: 1000,
      says: (value) => `ceiling ${value} objects`,
    },
    {
      // ADR 0074 decision 1.
      field: "browserRetentionCeiling", slug: "retention", label: "Browser retention",
      title: "the most rows a browser column keeps loaded; at the ceiling the column keeps what it shows and loads no further page (1,000 to 5,000,000)",
      min: 1000, max: 5_000_000, step: 1000,
      says: (value) => `keeps ${value} rows`,
    },
    {
      // ADR 0074 decision 2.
      field: "maxQueuedJobs", slug: "queue-depth", label: "Queue depth",
      title: "the most jobs that may wait behind the active slots; a further start is refused with the count named (1 to 1,000)",
      min: 1, max: 1000, step: 1,
      says: (value) => `${value} waiting at most`,
    },
    {
      // ADR 0074 decision 4: the native client's cap applies to sessions
      // opened after the save.
      field: "maxConnsPerHost", slug: "connection-cap", label: "Connection cap",
      title: "the most connections one session opens to a host; a burst of reads waits for a connection rather than opening one without bound; applies to sessions opened after the save (1 to 256)",
      min: 1, max: 256, step: 1,
      says: (value) => `${value} connections per host on the next connect`,
    },
  ];
  let boundedErrors: Partial<Record<BoundedSetting["field"], string | null>> = $state({});
  // Whether an acknowledged overwrite of a large object removes the segment
  // objects its manifest named once the replacement verifies (ADR 0081
  // decision 5). Defaults to removing them, which is what rclone's own
  // replacement does; the control is for the operator who shares segments
  // between manifests and cannot accept it.
  const reclaimChecked = $derived(browser.settings.document?.reclaimReplacedFragments ?? true);
  let reclaimError: string | null = $state(null);
  async function saveBound(patch: Partial<AppSettingsDTO>, report: (message: string | null) => void) {
    if (disposed || !browser.settings.canSave || !browser.settings.document) return;
    report(null);
    try {
      await browser.settings.save({ ...browser.settings.document, ...patch });
    } catch (err) {
      report(err instanceof Error ? err.message : String(err));
    }
  }

  type SettingRow = { key: string; value: string; origin: string };

  // The §45 record stores where each knob's value came from (origins);
  // the note turns that into plain words so a profile or calculated value
  // is not mistaken for one the operator set on this job (UI-09). A record
  // without origins — an older job, or an upload/download — shows no note.
  function settingOriginNote(origin: string | undefined): string {
    switch (origin) {
      case "override":
        return "set for this job";
      case "profile":
        return "from profile";
      case "recommendation":
        return "benchmark recommendation";
      case "calculated":
        return "calculated";
      case "default":
        return "default";
      default:
        return "";
    }
  }

  type SettingsView =
    | { kind: "rows"; rows: SettingRow[] }
    | { kind: "defaults" }
    | { kind: "unavailable" };

  // A job records its effective configuration as JSON, or "" when it carries
  // none (internal/jobs §45). Three states are distinguished so a record that
  // is absent or unreadable is never shown as if the job deliberately ran with
  // defaults (P28: empty versus unavailable must be distinct).
  function settingsView(job: JobDTO): SettingsView {
    if (job.settings.trim() === "") {
      return { kind: "unavailable" };
    }
    // Parsing and formatting stay under one fallback: a value that parses but
    // cannot be rendered (for example a field whose String() throws) is an
    // unreadable record, not a defaults run, and must not break the view.
    try {
      const parsed: unknown = JSON.parse(job.settings);
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
        return { kind: "unavailable" };
      }
      const fields = parsed as Record<string, unknown>;
      const origins = (fields.origins ?? {}) as Record<string, string>;
      const rows: SettingRow[] = Object.entries(fields)
        .filter(([k, v]) => k !== "origins" && v !== "" && v !== 0)
        .map(([k, v]) => ({ key: k, value: String(v), origin: settingOriginNote(origins[k]) }));
      // The child's per-transfer buffers are the memory to reason about
      // (ADR 0074 decision 3): transfers × buffer size, when both are known.
      if (typeof fields.transfers === "number" && fields.transfers > 0 && typeof fields.bufferSize === "string" && fields.bufferSize !== "") {
        rows.push({ key: "child buffers", value: `${fields.transfers} × ${fields.bufferSize}`, origin: "" });
      }
      return rows.length === 0 ? { kind: "defaults" } : { kind: "rows", rows };
    } catch {
      return { kind: "unavailable" };
    }
  }

  const bookmarkName = (id: string) =>
    browser.bookmarks.find((b) => b.id === id)?.displayName ?? "…";

  const auditBookmarks = $derived.by(() => {
    const ids = new Set(browser.bookmarks.map((b) => b.id));
    for (const event of browser.audit.events) {
      if (event.bookmarkId) ids.add(event.bookmarkId);
    }
    if (browser.audit.filterBookmark) ids.add(browser.audit.filterBookmark);
    return [...ids].filter((id) => id !== "");
  });

  function auditBookmarkLabel(id: string): string {
    if (id === "") return "No recorded bookmark";
    const name = browser.bookmarks.find((b) => b.id === id)?.displayName;
    return `${name || "Unavailable bookmark"} (${id})`;
  }
</script>

<div class="drawer num">
  <button class="bar" onclick={() => (open = !open)} aria-expanded={open}>
    <span aria-live="polite">
      {#if browser.jobs.error}
        Job status unavailable
      {:else if browser.activeJobs.length > 0}
        {browser.activeJobs.length} active
        {browser.activeJobs.length === 1 ? "job" : "jobs"}
      {:else}
        No active transfers
      {/if}
      {#if browser.audit.recentError}
        · Recent changes unavailable
        {#if browser.audit.recentLoaded}({browser.audit.recentEvents.length} last loaded; stale){/if}
      {:else if browser.audit.recentLoading}
        · Loading recent changes
        {#if browser.audit.recentLoaded}({browser.audit.recentEvents.length} last loaded){/if}
      {:else if browser.audit.recentLoaded}
        · {browser.audit.recentEvents.length} last loaded recent changes
      {:else}
        · Recent changes not loaded
      {/if}
      {#if cancellationNotices.length > 0}
        · {cancellationNotices.length} cancellation {cancellationNotices.length === 1 ? "notice" : "notices"}
      {/if}
      {#if browser.jobs.recoveryError}
        · Recovery status unavailable
      {:else if browser.jobs.recoveryStatus && browser.jobs.recoveryStatus.records > 0}
        · {browser.jobs.recoveryStatus.records} recovery {browser.jobs.recoveryStatus.records === 1 ? "record" : "records"} retained
      {/if}
    </span>
    <span class="hint">{open ? "▾ hide" : "▸ jobs & changes"}</span>
  </button>

  {#if open}
    <div class="body">
      {#if browser.jobs.interruptedError}
        <section class="interrupted" role="status">
          <p>Interrupted-job status is unavailable; whether an earlier run left jobs unfinished is unknown.</p>
        </section>
      {:else if browser.jobs.interrupted && browser.jobs.interrupted.total > 0}
        <section class="interrupted" aria-label="Interrupted jobs from a previous run">
          <p class="interrupted-head">An earlier run was interrupted. Its data and records are kept; nothing was cleaned up.</p>
          <ul class="interrupted-list">
            {#each browser.jobs.interrupted.entries ?? [] as entry (entry.jobId)}
              <li>
                <span class="operation-state">
                  <span class="operation">{operationLabel(entry.type)}</span>
                  <button class="dismiss-interruption" onclick={() => void browser.jobs.dismissInterrupted([entry.jobId])}>Dismiss</button>
                </span>
                <span class="route type-mono" title="{entry.source} → {entry.destination}">{entry.source} → {entry.destination}</span>
                <span class="interrupted-fact">{interruptionAdmission(entry)}</span>
                <span class="interrupted-fact">{interruptionItems(entry)}</span>
                {#if entry.recoveryRecords > 0}
                  <span class="interrupted-fact">
                    {entry.recoveryRecords} recovery {entry.recoveryRecords === 1 ? "record" : "records"} retained{#if entry.inconclusiveCommits > 0}, {entry.inconclusiveCommits} {entry.inconclusiveCommits === 1 ? "name" : "names"} with an inconclusive commit — not a failure{/if}.
                  </span>
                {:else if entry.placesLocalResidue}
                  <span class="interrupted-fact">This route keeps no recovery records, so none here is not a sign that nothing happened.</span>
                {/if}
                {#if entry.placesLocalResidue}
                  <span class="interrupted-fact">Files may remain in the folder you chose; what is there is yours to check.</span>
                {/if}
              </li>
            {/each}
          </ul>
          {#if browser.jobs.interrupted.total > (browser.jobs.interrupted.entries ?? []).length}
            <p class="interrupted-more">{browser.jobs.interrupted.total - (browser.jobs.interrupted.entries ?? []).length} more not shown.</p>
          {/if}
          {#if browser.jobs.dismissError}
            <p class="bad" role="alert">The dismissal did not happen; the notice remains.</p>
          {/if}
        </section>
      {/if}
      {#if cancellationNotices.length > 0}
        <section class="cancellation-feedback" aria-label="Cancellation requests">
          <ul class="cancellation-notices">
            {#each cancellationNotices as notice (notice.jobId)}
              <li class="cancellation-notice" aria-label="Cancellation request for job {notice.jobId}">
                <span class="type-mono">Job {notice.jobId}</span>
                {#if notice.state === "pending"}
                  <p role="status">Requesting cancellation; waiting for the request and status refresh.</p>
                {:else if notice.state === "requested"}
                  <p role="status">Cancellation requested. The job's reported state determines its outcome.</p>
                {:else}
                  <p role="alert" class="bad">Cancellation could not be confirmed. The job may still be running. Refresh status before retrying.</p>
                {/if}
                <div class="cancellation-actions">
                  {#if notice.state === "unconfirmed"}
                    {#if canCancel(notice.jobId)}
                      <button class="retry-cancellation" aria-label="Retry cancellation for job {notice.jobId}" onclick={() => cancel(notice.jobId)}>
                        Retry cancellation
                      </button>
                    {:else}
                      <span>Refresh status or select the job's account before retrying.</span>
                    {/if}
                  {/if}
                  {#if notice.state !== "pending"}
                    <button class="dismiss-cancellation" aria-label="Dismiss cancellation notice for job {notice.jobId}" onclick={() => dismissCancellation(notice.jobId)}>
                      Dismiss notice
                    </button>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
          {#if cancellationNotices.length >= CANCELLATION_NOTICE_LIMIT}
            <p role="status">Cancellation notice limit reached. Wait for a pending reply or dismiss a settled notice to make room for another job.</p>
          {/if}
          <p>Dismissing a notice only hides feedback; it does not stop a job or establish its outcome.</p>
          <button disabled={cancellationRefreshing} onclick={refreshCancellationStatus}>
            {cancellationRefreshing ? "Refreshing job status…" : "Refresh job status"}
          </button>
        </section>
      {/if}
      <div class="tabs" role="tablist">
        <button
          role="tab"
          class:active={tab === "jobs"}
          aria-selected={tab === "jobs"}
          onclick={() => (tab = "jobs")}>Jobs</button
        >
        <button
          role="tab"
          class:active={tab === "history"}
          aria-selected={tab === "history"}
          onclick={() => (tab = "history")}>Job history</button
        >
        <button
          role="tab"
          class:active={tab === "changes"}
          aria-selected={tab === "changes"}
          onclick={() => {
            tab = "changes";
            void browser.audit.search();
          }}>Recent changes</button
        >
        {#if tab === "jobs"}
          {#if browser.settings.document}
            <label class="filter" title="how many jobs run at once; a further start waits in the queue (1 to 4)">
              <span>Active-job budget</span>
              <select
                value={budgetValue}
                disabled={!browser.settings.canSave}
                aria-describedby={`${id}-budget-note ${id}-budget-status`}
                onchange={(e) => {
                  const value = Number(e.currentTarget.value);
                  e.currentTarget.value = budgetValue;
                  // The outcome is stated in the budget status below, which
                  // outlives this drawer; nothing is reported here.
                  void saveBound({ maxActiveJobs: value }, () => {});
                }}
              >
                {#each BUDGET_CHOICES as n (n)}
                  <option value={String(n)}>{n}</option>
                {/each}
              </select>
            </label>
            <span class="budget-note" id={`${id}-budget-note`}>
              each job brings its own engine settings, so a profile's transfers and limits apply per job, not in total
            </span>
            {#each BOUNDED_SETTINGS as setting (setting.field)}
              {@const observed = browser.settings.document[setting.field]}
              <label class="filter" title={setting.title}>
                <span>{setting.label}</span>
                <input
                  class="ceiling num"
                  type="number"
                  min={setting.min}
                  max={setting.max}
                  step={setting.step}
                  value={String(observed)}
                  disabled={!browser.settings.canSave}
                  aria-describedby={`${id}-${setting.slug}-status`}
                  onchange={(e) => {
                    const value = Number(e.currentTarget.value);
                    e.currentTarget.value = String(observed);
                    // The service takes whole numbers. Anything else would
                    // fail the bridge's decoding, which the window cannot
                    // tell from a lost reply, so it is not sent.
                    if (!Number.isInteger(value)) {
                      boundedErrors[setting.field] = `${setting.label} takes a whole number`;
                      return;
                    }
                    void saveBound({ [setting.field]: value }, (message) => (boundedErrors[setting.field] = message));
                  }}
                />
              </label>
              <span class="ceiling-status" id={`${id}-${setting.slug}-status`} role="status">
                {boundedErrors[setting.field] || setting.says(observed.toLocaleString("en-US"))}
              </span>
            {/each}
            <label class="filter" title="when an acknowledged overwrite replaces a large object, remove the segment objects its manifest named once the replacement verifies; off keeps them, and a container that keeps versions keeps them either way">
              <span>Reclaim replaced segments</span>
              <input
                class="reclaim-fragments"
                type="checkbox"
                checked={reclaimChecked}
                disabled={!browser.settings.canSave}
                aria-describedby={`${id}-reclaim-status`}
                onchange={(e) => {
                  const value = e.currentTarget.checked;
                  e.currentTarget.checked = reclaimChecked;
                  void saveBound({ reclaimReplacedFragments: value }, (message) => (reclaimError = message));
                }}
              />
            </label>
            <span class="ceiling-status" id={`${id}-reclaim-status`} role="status">
              {#if reclaimError}{reclaimError}{:else if browser.settings.document}{browser.settings.document.reclaimReplacedFragments ? "a replaced large object's segments are removed once the replacement verifies" : "a replaced large object's segments are kept"}{/if}
            </span>
          {/if}
          {#if v1Offers.benchmark}
            <button
              class="bench"
              title="Measure this cluster: writes real objects to a temporary Aeronautes-owned container"
              disabled={browser.connectedBookmarkId === null}
              onclick={() => (benchOpen = !benchOpen)}
            >
              Benchmark…
            </button>
          {/if}
        {/if}
        {#if tab === "jobs" || tab === "history"}
          <label class="filter">
            <span>Account</span>
            <select
              value={browser.jobs.bookmarkFilter}
              onchange={(e) => {
                browser.jobs.bookmarkFilter = e.currentTarget.value;
                void browser.jobs.load();
              }}
            >
              <option value="">All accounts</option>
              {#each browser.bookmarks as bookmark (bookmark.id)}
                <option value={bookmark.id}>{bookmark.displayName}</option>
              {/each}
            </select>
          </label>
        {/if}
      </div>

      {#if tab === "jobs"}
        <div class="budget-status" id={`${id}-budget-status`} role="status" aria-busy={browser.settings.operation !== null}>
          {#if browser.settings.operation === "get"}
            <span>Loading settings…</span>
          {:else if browser.settings.status === "unavailable"}
            <span>Settings are unavailable.</span>
          {:else if browser.settings.status === "stale" || browser.settings.readError}
            <span>Settings could not be refreshed; the last observation is stale.</span>
          {/if}
          {#if browser.settings.attempt?.state === "pending"}
            <span>Saving budget {browser.settings.attempt.value}…</span>
          {:else if browser.settings.attempt?.state === "unconfirmed"}
            <span>Budget change to {browser.settings.attempt.value} is unconfirmed.</span>
          {:else if browser.settings.attempt?.state === "refused"}
            <span>Budget change to {browser.settings.attempt.value} was refused: {browser.settings.attempt.reason}.</span>
          {:else if browser.settings.attempt?.state === "acknowledged"}
            <span>Budget change acknowledged.</span>
          {/if}
          {#if browser.settings.document}
            <span>Last observed budget: {browser.settings.document.maxActiveJobs}.</span>
          {/if}
          {#if browser.settings.status === "unconfirmed"}
            <span>Refresh settings before another change.</span>
          {:else if browser.settings.status === "current" && browser.settings.attempt?.state === "unconfirmed"}
            <span>Current settings were read; the earlier save was not acknowledged.</span>
          {/if}
          <button disabled={browser.settings.operation !== null} onclick={() => void browser.settings.load()}>Refresh settings</button>
        </div>
        <div class="recovery" role="status" aria-busy={browser.jobs.recoveryLoading}>
          {#if browser.jobs.recoveryError}
            <p>Recovery status is unavailable. Retained counts are unknown.</p>
          {:else if browser.jobs.recoveryStatus}
            <p>
              {browser.jobs.recoveryStatus.records} recovery {browser.jobs.recoveryStatus.records === 1 ? "record" : "records"} retained across
              {browser.jobs.recoveryStatus.jobs} finished {browser.jobs.recoveryStatus.jobs === 1 ? "job" : "jobs"} in all accounts.
            </p>
          {:else}
            <p>Checking recovery status…</p>
          {/if}
          <p>
            Remote residue is unverified. These counts describe records, not
            objects or failed transfers. Automatic recovery cleanup is unavailable.
          </p>
        </div>
        {#if v1Offers.benchmark && benchOpen}
          <form
            class="bench-form num"
            onsubmit={async (e) => {
              e.preventDefault();
              benchError = null;
              try {
                await browser.startBenchmark(benchConfirm);
                benchOpen = false;
                benchConfirm = "";
              } catch (err) {
                benchError = err instanceof Error ? err.message : String(err);
              }
            }}
          >
            <span>
              A benchmark writes real objects to a temporary container on
              <span class="type-mono">{browser.account?.name}</span>. It measures small native
              PUT/HEAD/DELETE throughput — an endpoint microbenchmark, not a transfer-path
              prediction. <label for={`${id}-benchmark`}>Type the account name to run:</label>
            </span>
            <input id={`${id}-benchmark`} class="type-mono" bind:value={benchConfirm} placeholder={browser.account?.name} />
            <button type="submit" disabled={benchConfirm !== (browser.account?.name ?? "")}>
              Run benchmark
            </button>
            {#if benchError}
              <em class="bad">{benchError}</em>
            {/if}
          </form>
        {/if}
        {#if actionError}
          <p class="empty bad" role="alert">{actionError}</p>
        {/if}
        {#if browser.jobs.error}
          <p class="empty">jobs unavailable: {browser.jobs.error}</p>
        {:else if browser.jobs.list.length === 0}
          <p class="empty">No jobs in this view.</p>
        {:else}
          <div class="rows">
            {#each browser.jobs.list as job (job.id)}
              <div class="job">
                <span class="operation-state">
                  <span class="operation">{operationLabel(job.type)}</span>
                  <span class="badge {job.state}">{job.state}</span>
                </span>
                <span class="route type-mono" title="{job.source} → {job.destination}">
                  {job.source} → {job.destination}
                </span>
                <span class="accounts">
                  {bookmarkName(job.sourceBookmarkId)} → {bookmarkName(job.destBookmarkId)}
                </span>
                <span class="counts">
                  {#if job.counters.deletionAccountingIncomplete}
                    Last confirmed progress: {job.counters.objectsCopied} processed · {job.counters.objectsFailed} reported failures
                  {:else}
                    {job.counters.objectsCopied}{#if job.counters.objectsPlanned > 0}/{job.counters
                        .objectsPlanned}{/if}
                    {#if isDeletionFamily(job.type)}
                      members settled
                    {:else}
                      obj · {formatBytes(job.counters.bytesCopied)}
                    {/if}
                    {#if job.counters.objectsDeleted > 0}
                      · {job.counters.objectsDeleted} deleted
                    {/if}
                    {#if job.counters.objectsFailed > 0}
                      · <em class="bad">{job.counters.objectsFailed} failed</em>
                    {/if}
                    {#if job.counters.overwritesRefused > 0}
                      · <em class="bad">{job.counters.overwritesRefused} {job.counters.overwritesRefused === 1 ? "overwrite" : "overwrites"} refused</em>
                    {/if}
                  {/if}
                </span>
                <span class="when">
                  {#if browser.jobs.rates[job.id] > 0 && isActive(job)}
                    {formatBytes(browser.jobs.rates[job.id])}/s
                    {#if etaOf(job)}· {etaOf(job)}{/if}
                  {:else}
                    {timeOf(job.finishedAt) || timeOf(job.startedAt)}
                  {/if}
                </span>
                <span class="row-actions">
                  {#if isActive(job)}
                    <!-- One button whose action changes, so keyboard focus stays
                      on it when a pause turns into a resume. -->
                    {#if canPause(job) || canResume(job)}
                      {@const action = canPause(job) ? "Pause" : "Resume"}
                      <button
                        class="pause-resume"
                        aria-label="{action} job {job.id}"
                        aria-disabled={pauseBusy[job.id] ? "true" : undefined}
                        onclick={() => pauseOrResume(job, action === "Pause" ? "pause" : "resume")}>{action}</button
                      >
                    {/if}
                    <button class="cancel" disabled={!canCancel(job.id)} onclick={() => cancel(job.id)}>
                      {cancelLabel(job.id)}
                    </button>
                  {:else if v1Offers.rerun && job.type === "copy"}
                    <button
                      class="rerun"
                      title="Start a fresh Copy after checking retained item records for unresolved effects and unverified copies; the new job replans the current scope"
                      onclick={() => rerun(job)}>Re-run</button
                    >
                  {/if}
                  <button
                    class="details"
                    aria-label="Item details for job {job.id}"
                    onclick={() => (expanded = expanded === job.id ? null : job.id)}
                    aria-expanded={expanded === job.id}
                  >
                    {expanded === job.id ? "▾" : "▸"}
                  </button>
                </span>
                {#if progressOf(job) !== null}
                  <div class="progress" style="--p: {(progressOf(job) ?? 0) * 100}%"></div>
                {/if}
                {#if pauseNote(job)}
                  <p class="pause-note">{pauseNote(job)}</p>
                {/if}
                {#if job.error}
                  <p class="error">{job.error}</p>
                {/if}
                {#if job.counters.deletionAccountingIncomplete}
                  <p class="error">Deletion accounting is incomplete. Final failure and unresolved totals are unavailable.</p>
                {/if}
                {#if !isActive(job) && job.recoveryRecords > 0}
                  <p class="recovery-row">
                    {job.recoveryRecords} recovery {job.recoveryRecords === 1 ? "record" : "records"} retained; remote residue is unverified.
                  </p>
                {/if}
                {#if expanded === job.id}
                  {@const view = settingsView(job)}
                  <div class="config num">
                    <span class="config-row"><span class="k">engine</span><span class="type-mono">{job.engine}</span></span>
                    {#if !job.counters.deletionAccountingIncomplete}
                      <span class="config-row"><span class="k">verified</span><span>{job.counters.objectsVerified}</span></span>
                    {/if}
                    {#if view.kind === "rows"}
                      {#each view.rows as row (row.key)}
                        <span class="config-row"><span class="k">{row.key}</span><span class="type-mono">{row.value}</span>{#if row.origin}<span class="origin">{row.origin}</span>{/if}</span>
                      {/each}
                    {:else if view.kind === "defaults"}
                      <span class="config-row"><span class="k">settings</span><span>defaults</span></span>
                    {:else}
                      <span class="config-row"><span class="k">settings</span><span>not recorded</span></span>
                    {/if}
                  </div>
                  {#key job.id}<JobItems {browser} {job} />{/key}
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      {:else if tab === "history"}
        <JobHistory {browser} />
      {:else}
        <div class="audit-recent">
          <p role={browser.audit.recentError ? "alert" : "status"}>
            {#if browser.audit.recentError}
              Recent changes are unavailable.
              {#if browser.audit.recentLoaded}
                The last loaded slice of {browser.audit.recentEvents.length} events may be stale.
              {/if}
            {:else if browser.audit.recentLoading}
              Loading recent changes.
              {#if browser.audit.recentLoaded}
                Previously loaded slice: {browser.audit.recentEvents.length} events.
              {/if}
            {:else if browser.audit.recentLoaded}
              Last loaded recent slice: {browser.audit.recentEvents.length} events, up to 50 for the connected account.
            {:else}
              Recent changes have not been loaded.
            {/if}
          </p>
          <button disabled={browser.audit.recentLoading} onclick={() => browser.audit.loadRecent()}>
            {browser.audit.recentError ? "Retry recent changes" : browser.audit.recentLoaded ? "Refresh recent changes" : "Load recent changes"}
          </button>
        </div>
        <div class="audit-filters num">
          <label for={`${id}-audit-bookmark`}>Bookmark</label>
          <select
            id={`${id}-audit-bookmark`}
            value={browser.audit.filterBookmark}
            onchange={(e) => {
              browser.audit.filterBookmark = e.currentTarget.value;
              void browser.audit.search();
            }}
          >
            <option value="">All accounts</option>
            {#each auditBookmarks as bookmark (bookmark)}
              <option value={bookmark}>{auditBookmarkLabel(bookmark)}</option>
            {/each}
          </select>
          <label for={`${id}-audit-operation`}>Operation prefix</label>
          <input
            id={`${id}-audit-operation`}
            class="type-mono"
            placeholder="operation prefix (transfer., delete.…)"
            value={browser.audit.filterOp}
            onchange={(e) => {
              browser.audit.filterOp = e.currentTarget.value;
              void browser.audit.search();
            }}
          />
          <label for={`${id}-audit-result`}>Result</label>
          <select
            id={`${id}-audit-result`}
            value={browser.audit.filterResult}
            onchange={(e) => {
              browser.audit.filterResult = e.currentTarget.value;
              void browser.audit.search();
            }}
          >
            <option value="">any result</option>
            <option value="applied">applied</option>
            <option value="conflict">conflict</option>
            <option value="verify_mismatch">verify_mismatch</option>
            <option value="failed">failed</option>
          </select>
        </div>
        <p class="audit-scope">
          {browser.audit.filterBookmark === "" ? "All accounts" : `Bookmark: ${auditBookmarkLabel(browser.audit.filterBookmark)}`}
          · Operation prefix:
          <span class="type-mono">{browser.audit.filterOp === "" ? "any" : JSON.stringify(browser.audit.filterOp)}</span>
          · Result: <span class="type-mono">{browser.audit.filterResult === "" ? "any" : JSON.stringify(browser.audit.filterResult)}</span>
          · Names come from current bookmarks.
        </p>
        <div class="audit-status" role="status">
          {#if browser.audit.error}
            <p role="alert">{browser.audit.error}</p>
            {#if browser.audit.loaded}<p>Previously loaded results may be stale.</p>{/if}
          {:else if browser.audit.loading}
            <p>{browser.audit.loaded ? "Refreshing audit events. Previously loaded results are shown." : "Loading audit events."}</p>
          {:else if browser.audit.moreLoading}
            <p>Loading more audit events. Previously loaded results are shown.</p>
          {:else if browser.audit.moreError}
            <p role="alert">{browser.audit.moreError}</p>
          {:else if !browser.audit.loaded}
            <p>Audit events have not been loaded for these filters.</p>
          {:else}
            <p>Last loaded results for these filters.</p>
          {/if}
          <button disabled={browser.audit.loading} onclick={() => browser.audit.search()}>
            {browser.audit.error ? "Retry audit search" : browser.audit.loaded ? "Refresh audit search" : "Load audit events"}
          </button>
          <button class="export" disabled={browser.audit.exporting} onclick={() => browser.audit.exportTrail()}>
            {browser.audit.exporting ? "Exporting…" : "Export log…"}
          </button>
          {#if browser.audit.exportError}
            <p role="alert">{browser.audit.exportError}</p>
          {:else if browser.audit.exportMessage}
            <p class="export-result">{browser.audit.exportMessage}</p>
          {/if}
        </div>
        <div class="rows">
          {#if browser.audit.loaded && !browser.audit.loading && !browser.audit.error && browser.audit.events.length === 0}
            <p class="empty">No matching audit events.</p>
          {:else}
            {#each browser.audit.events as event (event.id)}
              <div class="event">
                <span class="badge {event.result}">{event.result}</span>
                <span class="op type-mono">{event.operation}</span>
                <span class="resource type-mono">{event.resource}</span>
                <span class="summary">{event.summary}</span>
                <span class="when">{timeOf(event.at)}</span>
                <span class="audit-bookmark">{auditBookmarkLabel(event.bookmarkId)}</span>
                {#if event.transIds?.some((id) => id)}
                  <span class="trans">Transaction IDs: <span class="type-mono">{event.transIds.filter((id) => id).join(" ")}</span></span>
                {/if}
              </div>
            {/each}
            {#if browser.audit.loaded && browser.audit.events.length > 0 && !browser.audit.exhausted && !browser.audit.error}
              <button class="more" disabled={!browser.audit.canMore} onclick={() => browser.audit.search(true)}>
                {browser.audit.moreError ? "Retry audit page" : "Load more…"}
              </button>
            {/if}
          {/if}
        </div>
      {/if}
    </div>
  {/if}
</div>

<style>
  .drawer {
    background: var(--color-bg-pane);
    border-top: 1px solid var(--color-border);
  }

  .bar {
    width: 100%;
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--space-2) var(--space-4);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    background: none;
    border: none;
    cursor: pointer;
  }

  .hint {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .body {
    padding: 0 var(--space-4) var(--space-2);
  }

  .ceiling {
    width: 9em;
  }

  .ceiling-status,
  .budget-note {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .budget-status {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin: var(--space-2) 0;
    font-size: var(--text-sm);
  }

  .audit-recent, .audit-status, .audit-scope {
    margin: var(--space-2) 0;
    font-size: var(--text-sm);
    overflow-wrap: anywhere;
  }

  .audit-recent p, .audit-status p {
    margin: var(--space-1) 0;
  }

  .cancellation-feedback {
    border-bottom: 1px solid var(--color-border);
    margin-bottom: var(--space-2);
    padding-bottom: var(--space-2);
    font-size: var(--text-sm);
  }

  .cancellation-notices {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 12rem;
    overflow-y: auto;
  }

  .cancellation-notice {
    padding: var(--space-2) 0;
    overflow-wrap: anywhere;
  }

  .cancellation-feedback p {
    margin: var(--space-1) 0;
  }

  .cancellation-actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }

  .tabs {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    margin-bottom: var(--space-2);
  }

  .tabs [role="tab"] {
    font: inherit;
    font-size: var(--text-xs);
    padding: 2px var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-pill);
    background: none;
    color: var(--color-text-secondary);
    cursor: pointer;
  }

  .tabs [role="tab"].active {
    color: var(--color-text);
    background: var(--color-bg-card);
    border-color: var(--color-text-faint);
  }

  .filter {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: var(--space-1);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .filter select {
    font: inherit;
    font-size: var(--text-xs);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 1px var(--space-1);
  }

  .rows {
    max-height: 13rem;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .empty {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-faint);
  }

  .recovery {
    padding: var(--space-2);
    margin-bottom: var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    color: var(--color-text-secondary);
    font-size: var(--text-xs);
  }

  .recovery p {
    margin: 0;
  }

  .recovery-row,
  .pause-note {
    grid-column: 1 / -1;
    margin: 0;
    color: var(--color-text-secondary);
  }

  .job {
    display: grid;
    grid-template-columns: 6.5rem minmax(10rem, 1.4fr) minmax(7rem, 1fr) minmax(10rem, 1.2fr) auto auto;
    gap: var(--space-2);
    align-items: baseline;
    padding: 2px var(--space-2);
    font-size: var(--text-xs);
    border-radius: var(--radius-control);
  }

  .job:hover,
  .event:hover {
    background: var(--color-bg-card);
  }

  .operation-state {
    display: grid;
    gap: var(--space-1);
    min-width: 0;
    align-self: start;
  }

  .operation {
    overflow-wrap: anywhere;
    color: var(--color-text-secondary);
  }

  .route,
  .resource,
  .summary {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .accounts,
  .when {
    color: var(--color-text-faint);
  }

  .bad {
    color: var(--color-danger);
    font-style: normal;
  }

  .row-actions {
    display: flex;
    gap: var(--space-1);
    align-items: baseline;
  }

  .rerun,
  .pause-resume {
    font: inherit;
    font-size: var(--text-xs);
    padding: 0 var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: none;
    color: var(--color-accent);
    cursor: pointer;
  }

  .pause-resume[aria-disabled="true"] {
    color: var(--color-text-faint);
    cursor: progress;
  }

  .details {
    font: inherit;
    font-size: var(--text-xs);
    padding: 0 var(--space-1);
    border: none;
    background: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .config {
    grid-column: 1 / -1;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    padding: 2px var(--space-2) var(--space-1);
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
  }

  .config-row {
    display: flex;
    gap: var(--space-1);
  }

  .config-row .k {
    color: var(--color-text-faint);
  }

  .config-row .origin {
    color: var(--color-text-faint);
  }

  .audit-filters {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    margin-bottom: var(--space-2);
  }

  .audit-filters input {
    flex: 1;
    min-width: 0;
    font: inherit;
    font-size: var(--text-xs);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 1px var(--space-2);
  }

  .audit-filters label {
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
  }

  .audit-filters select {
    font: inherit;
    font-size: var(--text-xs);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  .more {
    font: inherit;
    font-size: var(--text-xs);
    padding: 2px;
    border: none;
    background: none;
    color: var(--color-accent);
    cursor: pointer;
    text-align: left;
  }

  .bench {
    font: inherit;
    font-size: var(--text-xs);
    padding: 2px var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-pill);
    background: none;
    color: var(--color-text-secondary);
    cursor: pointer;
  }

  .bench-form {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
    padding: var(--space-1) var(--space-2);
    border: 1px dashed var(--color-border);
    border-radius: var(--radius-control);
    margin-bottom: var(--space-2);
  }

  .bench-form input {
    font: inherit;
    font-size: var(--text-xs);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 1px var(--space-1);
  }

  .bench-form button {
    font: inherit;
    font-size: var(--text-xs);
    padding: 1px var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: var(--color-bg-card);
    color: var(--color-text);
    cursor: pointer;
  }

  .bench-form button:disabled {
    opacity: 0.5;
  }

  .cancel {
    font: inherit;
    font-size: var(--text-xs);
    padding: 0 var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: none;
    color: var(--color-danger);
    cursor: pointer;
  }

  .progress {
    grid-column: 1 / -1;
    height: 3px;
    border-radius: 2px;
    background: var(--color-bg-card);
    position: relative;
    overflow: hidden;
  }

  .progress::after {
    content: "";
    position: absolute;
    inset: 0;
    width: var(--p);
    background: var(--color-accent);
    transition: width 0.4s ease;
  }

  .error {
    grid-column: 1 / -1;
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-danger);
  }

  .event {
    display: grid;
    grid-template-columns: 6.5rem 11rem minmax(8rem, 1fr) minmax(8rem, 1.2fr) auto;
    gap: var(--space-2);
    align-items: baseline;
    padding: 2px var(--space-2);
    font-size: var(--text-xs);
    border-radius: var(--radius-control);
  }

  .audit-bookmark,
  .trans {
    grid-column: 1 / -1;
    overflow-wrap: anywhere;
    color: var(--color-text-secondary);
  }

  .badge {
    justify-self: start;
    padding: 0 var(--space-1);
    font-weight: var(--weight-semibold);
    border-radius: var(--radius-pill);
    border: 1px solid;
  }

  .badge.applied,
  .badge.complete {
    color: var(--color-success);
    background: var(--color-success-soft);
    border-color: var(--color-success);
  }

  .badge.queued,
  .badge.planning,
  .badge.running,
  .badge.paused,
  .badge.verifying {
    color: var(--color-accent);
    background: var(--color-bg-card);
    border-color: var(--color-accent);
  }

  .badge.conflict,
  .badge.verify_mismatch,
  .badge.partial,
  .badge.cancelled {
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-color: var(--color-warning);
  }

  .badge.failed {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-color: var(--color-danger);
  }

  .interrupted {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-2);
    border: 1px solid var(--color-warning);
    background: var(--color-warning-soft);
  }

  .interrupted-head {
    font-weight: 600;
    color: var(--color-warning);
  }

  .interrupted-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .interrupted-list li {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .interrupted-fact,
  .interrupted-more {
    font-size: var(--text-sm);
    color: var(--color-text-faint);
  }

  .dismiss-interruption {
    margin-left: auto;
  }
</style>
