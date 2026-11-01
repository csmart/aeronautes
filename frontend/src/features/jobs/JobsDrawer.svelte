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
  const budgetValue = $derived(String(
    browser.appSettingsOperation === "set" || browser.appSettingsStatus === "unconfirmed"
      ? browser.appSettingsAttempt?.value ?? ""
      : browser.appSettings?.maxActiveJobs ?? "",
  ));
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
    browser.jobsFilter;
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
    // Operation-specific numerators: a delete job's progress is its
    // deletions, not a repurposed copied counter.
    const done =
      job.type === "delete" || job.type === "mirror"
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
    if (disposed || browser.jobsError || !browser.jobs.some((job) => job.id === jobId && isActive(job))) {
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
      await owner.loadJobs();
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
      await browser.loadJobs();
    } finally {
      if (!disposed) cancellationRefreshing = false;
    }
  }

  let rerunError: string | null = $state(null);

  async function rerun(job: JobDTO) {
    rerunError = null;
    try {
      await browser.rerunJob(job.id);
    } catch (e) {
      rerunError = e instanceof Error ? e.message : String(e);
    }
  }

  onMount(() => {
    const refresh = () => {
      void browser.loadJobs();
      void browser.loadRecoveryStatus();
    };
    refresh();
    // Reopening the drawer is not the explicit recovery read after a lost reply.
    if (browser.appSettingsAttempt?.state !== "unconfirmed") void browser.loadAppSettings();
    const tick = setInterval(refresh, 1500);
    return () => {
      disposed = true;
      clearInterval(tick);
    };
  });

  // ETA from the live rate and the remaining planned bytes.
  function etaOf(job: JobDTO): string {
    const rate = browser.jobRates[job.id] ?? 0;
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

  async function saveBudget(value: number) {
    if (disposed || !browser.canSaveAppSettings || !browser.appSettings) return;
    try {
      await browser.saveAppSettings({ ...browser.appSettings, maxActiveJobs: value });
    } catch {
      // BrowserState retains the unconfirmed attempt even if this drawer unmounts.
    }
  }

  // The download selection ceiling (ADR 0072): refused by the backend
  // outside 1,000..5,000,000 rather than clamped, so the control shows the
  // last observed value until a save is acknowledged.
  const ceilingValue = $derived(String(browser.appSettings?.downloadObjectCeiling ?? ""));
  let ceilingError: string | null = $state(null);
  async function saveCeiling(value: number) {
    if (disposed || !browser.canSaveAppSettings || !browser.appSettings) return;
    ceilingError = null;
    try {
      await browser.saveAppSettings({ ...browser.appSettings, downloadObjectCeiling: value });
    } catch (err) {
      ceilingError = err instanceof Error ? err.message : String(err);
    }
  }

  // The browser retention ceiling and the queue depth (ADR 0074 decisions
  // 1 and 2) take the ceiling's treatment: the backend refuses a value
  // outside the range rather than clamping it.
  const retentionValue = $derived(String(browser.appSettings?.browserRetentionCeiling ?? ""));
  let retentionError: string | null = $state(null);
  const queueDepthValue = $derived(String(browser.appSettings?.maxQueuedJobs ?? ""));
  let queueDepthError: string | null = $state(null);
  // The native client's per-host connection cap (ADR 0074 decision 4)
  // applies to sessions opened after the save.
  const connectionCapValue = $derived(String(browser.appSettings?.maxConnsPerHost ?? ""));
  let connectionCapError: string | null = $state(null);
  // Whether an acknowledged overwrite of a large object removes the segment
  // objects its manifest named once the replacement verifies (ADR 0081
  // decision 5). Defaults to removing them, which is what rclone's own
  // replacement does; the control is for the operator who shares segments
  // between manifests and cannot accept it.
  const reclaimChecked = $derived(browser.appSettings?.reclaimReplacedFragments ?? true);
  let reclaimError: string | null = $state(null);
  async function saveBound(patch: Partial<AppSettingsDTO>, report: (message: string | null) => void) {
    if (disposed || !browser.canSaveAppSettings || !browser.appSettings) return;
    report(null);
    try {
      await browser.saveAppSettings({ ...browser.appSettings, ...patch });
    } catch (err) {
      report(err instanceof Error ? err.message : String(err));
    }
  }

  function settingsRows(job: JobDTO): [string, string][] {
    try {
      const parsed = JSON.parse(job.settings);
      const rows: [string, string][] = Object.entries(parsed)
        .filter(([, v]) => v !== "" && v !== 0)
        .map(([k, v]) => [k, String(v)]);
      // The child's per-transfer buffers are the memory to reason about
      // (ADR 0074 decision 3): transfers × buffer size, when both are known.
      if (typeof parsed.transfers === "number" && parsed.transfers > 0 && typeof parsed.bufferSize === "string" && parsed.bufferSize !== "") {
        rows.push(["child buffers", `${parsed.transfers} × ${parsed.bufferSize}`]);
      }
      return rows;
    } catch {
      return [];
    }
  }

  const bookmarkName = (id: string) =>
    browser.bookmarks.find((b) => b.id === id)?.displayName ?? "…";

  const auditBookmarks = $derived.by(() => {
    const ids = new Set(browser.bookmarks.map((b) => b.id));
    for (const event of browser.auditEvents) {
      if (event.bookmarkId) ids.add(event.bookmarkId);
    }
    if (browser.auditFilterBookmark) ids.add(browser.auditFilterBookmark);
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
      {#if browser.jobsError}
        Job status unavailable
      {:else if browser.activeJobs.length > 0}
        {browser.activeJobs.length} active
        {browser.activeJobs.length === 1 ? "job" : "jobs"}
      {:else}
        No active transfers
      {/if}
      {#if browser.recentAuditError}
        · Recent changes unavailable
        {#if browser.recentAuditLoaded}({browser.recentAuditEvents.length} last loaded; stale){/if}
      {:else if browser.recentAuditLoading}
        · Loading recent changes
        {#if browser.recentAuditLoaded}({browser.recentAuditEvents.length} last loaded){/if}
      {:else if browser.recentAuditLoaded}
        · {browser.recentAuditEvents.length} last loaded recent changes
      {:else}
        · Recent changes not loaded
      {/if}
      {#if cancellationNotices.length > 0}
        · {cancellationNotices.length} cancellation {cancellationNotices.length === 1 ? "notice" : "notices"}
      {/if}
      {#if browser.recoveryStatusError}
        · Recovery status unavailable
      {:else if browser.recoveryStatus && browser.recoveryStatus.records > 0}
        · {browser.recoveryStatus.records} recovery {browser.recoveryStatus.records === 1 ? "record" : "records"} retained
      {/if}
    </span>
    <span class="hint">{open ? "▾ hide" : "▸ jobs & changes"}</span>
  </button>

  {#if open}
    <div class="body">
      {#if browser.interruptedDisclosuresError}
        <section class="interrupted" role="status">
          <p>Interrupted-job status is unavailable; whether an earlier run left jobs unfinished is unknown.</p>
        </section>
      {:else if browser.interruptedDisclosures && browser.interruptedDisclosures.total > 0}
        <section class="interrupted" aria-label="Interrupted jobs from a previous run">
          <p class="interrupted-head">An earlier run was interrupted. Its data and records are kept; nothing was cleaned up.</p>
          <ul class="interrupted-list">
            {#each browser.interruptedDisclosures.entries ?? [] as entry (entry.jobId)}
              <li>
                <span class="operation-state">
                  <span class="operation">{operationLabel(entry.type)}</span>
                  <button class="dismiss-interruption" onclick={() => void browser.dismissInterrupted([entry.jobId])}>Dismiss</button>
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
          {#if browser.interruptedDisclosures.total > (browser.interruptedDisclosures.entries ?? []).length}
            <p class="interrupted-more">{browser.interruptedDisclosures.total - (browser.interruptedDisclosures.entries ?? []).length} more not shown.</p>
          {/if}
          {#if browser.interruptedDismissError}
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
            void browser.searchAudit();
          }}>Recent changes</button
        >
        {#if tab === "jobs"}
          {#if browser.appSettings}
            <label class="filter" title="whole-application active-job ceiling">
              <span>Active-job budget</span>
              <select
                value={budgetValue}
                disabled={!browser.canSaveAppSettings}
                aria-describedby={`${id}-budget-status`}
                onchange={(e) => {
                  const value = Number(e.currentTarget.value);
                  e.currentTarget.value = budgetValue;
                  void saveBudget(value);
                }}
              >
                {#each [1, 2, 4, 8] as n (n)}
                  <option value={String(n)}>{n}</option>
                {/each}
              </select>
            </label>
            <label class="filter" title="the most objects one download selection may hold; a wider prefix is refused rather than cut short (1,000 to 5,000,000)">
              <span>Download ceiling</span>
              <input
                class="ceiling num"
                type="number"
                min="1000"
                max="5000000"
                step="1000"
                value={ceilingValue}
                disabled={!browser.canSaveAppSettings}
                aria-describedby={`${id}-ceiling-status`}
                onchange={(e) => {
                  const value = Number(e.currentTarget.value);
                  e.currentTarget.value = ceilingValue;
                  void saveCeiling(value);
                }}
              />
            </label>
            <span class="ceiling-status" id={`${id}-ceiling-status`} role="status">
              {#if ceilingError}{ceilingError}{:else if browser.appSettings}ceiling {browser.appSettings.downloadObjectCeiling.toLocaleString("en-US")} objects{/if}
            </span>
            <label class="filter" title="the most rows a browser column keeps loaded; at the ceiling the column keeps what it shows and loads no further page (1,000 to 5,000,000)">
              <span>Browser retention</span>
              <input
                class="ceiling retention num"
                type="number"
                min="1000"
                max="5000000"
                step="1000"
                value={retentionValue}
                disabled={!browser.canSaveAppSettings}
                aria-describedby={`${id}-retention-status`}
                onchange={(e) => {
                  const value = Number(e.currentTarget.value);
                  e.currentTarget.value = retentionValue;
                  void saveBound({ browserRetentionCeiling: value }, (message) => (retentionError = message));
                }}
              />
            </label>
            <span class="ceiling-status" id={`${id}-retention-status`} role="status">
              {#if retentionError}{retentionError}{:else if browser.appSettings}keeps {browser.appSettings.browserRetentionCeiling.toLocaleString("en-US")} rows{/if}
            </span>
            <label class="filter" title="the most jobs that may wait behind the active slots; a further start is refused with the count named (1 to 1,000)">
              <span>Queue depth</span>
              <input
                class="ceiling queue-depth num"
                type="number"
                min="1"
                max="1000"
                step="1"
                value={queueDepthValue}
                disabled={!browser.canSaveAppSettings}
                aria-describedby={`${id}-queue-depth-status`}
                onchange={(e) => {
                  const value = Number(e.currentTarget.value);
                  e.currentTarget.value = queueDepthValue;
                  void saveBound({ maxQueuedJobs: value }, (message) => (queueDepthError = message));
                }}
              />
            </label>
            <span class="ceiling-status" id={`${id}-queue-depth-status`} role="status">
              {#if queueDepthError}{queueDepthError}{:else if browser.appSettings}{browser.appSettings.maxQueuedJobs.toLocaleString("en-US")} waiting at most{/if}
            </span>
            <label class="filter" title="the most connections one session opens to a host; a burst of reads waits for a connection rather than opening one without bound; applies to sessions opened after the save (1 to 256)">
              <span>Connection cap</span>
              <input
                class="ceiling connection-cap num"
                type="number"
                min="1"
                max="256"
                step="1"
                value={connectionCapValue}
                disabled={!browser.canSaveAppSettings}
                aria-describedby={`${id}-connection-cap-status`}
                onchange={(e) => {
                  const value = Number(e.currentTarget.value);
                  e.currentTarget.value = connectionCapValue;
                  void saveBound({ maxConnsPerHost: value }, (message) => (connectionCapError = message));
                }}
              />
            </label>
            <span class="ceiling-status" id={`${id}-connection-cap-status`} role="status">
              {#if connectionCapError}{connectionCapError}{:else if browser.appSettings}{browser.appSettings.maxConnsPerHost.toLocaleString("en-US")} connections per host on the next connect{/if}
            </span>
            <label class="filter" title="when an acknowledged overwrite replaces a large object, remove the segment objects its manifest named once the replacement verifies; off keeps them, and a container that keeps versions keeps them either way">
              <span>Reclaim replaced segments</span>
              <input
                class="reclaim-fragments"
                type="checkbox"
                checked={reclaimChecked}
                disabled={!browser.canSaveAppSettings}
                aria-describedby={`${id}-reclaim-status`}
                onchange={(e) => {
                  const value = e.currentTarget.checked;
                  e.currentTarget.checked = reclaimChecked;
                  void saveBound({ reclaimReplacedFragments: value }, (message) => (reclaimError = message));
                }}
              />
            </label>
            <span class="ceiling-status" id={`${id}-reclaim-status`} role="status">
              {#if reclaimError}{reclaimError}{:else if browser.appSettings}{browser.appSettings.reclaimReplacedFragments ? "a replaced large object's segments are removed once the replacement verifies" : "a replaced large object's segments are kept"}{/if}
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
              value={browser.jobsFilter}
              onchange={(e) => {
                browser.jobsFilter = e.currentTarget.value;
                void browser.loadJobs();
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
        <div class="budget-status" id={`${id}-budget-status`} role="status" aria-busy={browser.appSettingsOperation !== null}>
          {#if browser.appSettingsOperation === "get"}
            <span>Loading settings…</span>
          {:else if browser.appSettingsStatus === "unavailable"}
            <span>Settings are unavailable.</span>
          {:else if browser.appSettingsStatus === "stale" || browser.appSettingsReadError}
            <span>Settings could not be refreshed; the last observation is stale.</span>
          {/if}
          {#if browser.appSettingsAttempt?.state === "pending"}
            <span>Saving budget {browser.appSettingsAttempt.value}…</span>
          {:else if browser.appSettingsAttempt?.state === "unconfirmed"}
            <span>Budget change to {browser.appSettingsAttempt.value} is unconfirmed.</span>
          {:else if browser.appSettingsAttempt?.state === "acknowledged"}
            <span>Budget change acknowledged.</span>
          {/if}
          {#if browser.appSettings}
            <span>Last observed budget: {browser.appSettings.maxActiveJobs}.</span>
          {/if}
          {#if browser.appSettingsStatus === "unconfirmed"}
            <span>Refresh settings before another change.</span>
          {:else if browser.appSettingsStatus === "current" && browser.appSettingsAttempt?.state === "unconfirmed"}
            <span>Current settings were read; the earlier save was not acknowledged.</span>
          {/if}
          <button disabled={browser.appSettingsOperation !== null} onclick={() => void browser.loadAppSettings()}>Refresh settings</button>
        </div>
        <div class="recovery" role="status" aria-busy={browser.recoveryStatusLoading}>
          {#if browser.recoveryStatusError}
            <p>Recovery status is unavailable. Retained counts are unknown.</p>
          {:else if browser.recoveryStatus}
            <p>
              {browser.recoveryStatus.records} recovery {browser.recoveryStatus.records === 1 ? "record" : "records"} retained across
              {browser.recoveryStatus.jobs} finished {browser.recoveryStatus.jobs === 1 ? "job" : "jobs"} in all accounts.
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
        {#if rerunError}
          <p class="empty bad" role="alert">{rerunError}</p>
        {/if}
        {#if browser.jobsError}
          <p class="empty">jobs unavailable: {browser.jobsError}</p>
        {:else if browser.jobs.length === 0}
          <p class="empty">No jobs in this view.</p>
        {:else}
          <div class="rows">
            {#each browser.jobs as job (job.id)}
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
                  {#if browser.jobRates[job.id] > 0 && isActive(job)}
                    {formatBytes(browser.jobRates[job.id])}/s
                    {#if etaOf(job)}· {etaOf(job)}{/if}
                  {:else}
                    {timeOf(job.finishedAt) || timeOf(job.startedAt)}
                  {/if}
                </span>
                <span class="row-actions">
                  {#if isActive(job)}
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
                  <div class="config num">
                    <span class="config-row"><span class="k">engine</span><span class="type-mono">{job.engine}</span></span>
                    {#if !job.counters.deletionAccountingIncomplete}
                      <span class="config-row"><span class="k">verified</span><span>{job.counters.objectsVerified}</span></span>
                    {/if}
                    {#each settingsRows(job) as [key, value] (key)}
                      <span class="config-row"><span class="k">{key}</span><span class="type-mono">{value}</span></span>
                    {/each}
                    {#if settingsRows(job).length === 0}
                      <span class="config-row"><span class="k">settings</span><span>defaults</span></span>
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
          <p role={browser.recentAuditError ? "alert" : "status"}>
            {#if browser.recentAuditError}
              Recent changes are unavailable.
              {#if browser.recentAuditLoaded}
                The last loaded slice of {browser.recentAuditEvents.length} events may be stale.
              {/if}
            {:else if browser.recentAuditLoading}
              Loading recent changes.
              {#if browser.recentAuditLoaded}
                Previously loaded slice: {browser.recentAuditEvents.length} events.
              {/if}
            {:else if browser.recentAuditLoaded}
              Last loaded recent slice: {browser.recentAuditEvents.length} events, up to 50 for the connected account.
            {:else}
              Recent changes have not been loaded.
            {/if}
          </p>
          <button disabled={browser.recentAuditLoading} onclick={() => browser.loadAudit()}>
            {browser.recentAuditError ? "Retry recent changes" : browser.recentAuditLoaded ? "Refresh recent changes" : "Load recent changes"}
          </button>
        </div>
        <div class="audit-filters num">
          <label for={`${id}-audit-bookmark`}>Bookmark</label>
          <select
            id={`${id}-audit-bookmark`}
            value={browser.auditFilterBookmark}
            onchange={(e) => {
              browser.auditFilterBookmark = e.currentTarget.value;
              void browser.searchAudit();
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
            value={browser.auditFilterOp}
            onchange={(e) => {
              browser.auditFilterOp = e.currentTarget.value;
              void browser.searchAudit();
            }}
          />
          <label for={`${id}-audit-result`}>Result</label>
          <select
            id={`${id}-audit-result`}
            value={browser.auditFilterResult}
            onchange={(e) => {
              browser.auditFilterResult = e.currentTarget.value;
              void browser.searchAudit();
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
          {browser.auditFilterBookmark === "" ? "All accounts" : `Bookmark: ${auditBookmarkLabel(browser.auditFilterBookmark)}`}
          · Operation prefix:
          <span class="type-mono">{browser.auditFilterOp === "" ? "any" : JSON.stringify(browser.auditFilterOp)}</span>
          · Result: <span class="type-mono">{browser.auditFilterResult === "" ? "any" : JSON.stringify(browser.auditFilterResult)}</span>
          · Names come from current bookmarks.
        </p>
        <div class="audit-status" role="status">
          {#if browser.auditError}
            <p role="alert">{browser.auditError}</p>
            {#if browser.auditLoaded}<p>Previously loaded results may be stale.</p>{/if}
          {:else if browser.auditLoading}
            <p>{browser.auditLoaded ? "Refreshing audit events. Previously loaded results are shown." : "Loading audit events."}</p>
          {:else if browser.auditMoreLoading}
            <p>Loading more audit events. Previously loaded results are shown.</p>
          {:else if browser.auditMoreError}
            <p role="alert">{browser.auditMoreError}</p>
          {:else if !browser.auditLoaded}
            <p>Audit events have not been loaded for these filters.</p>
          {:else}
            <p>Last loaded results for these filters.</p>
          {/if}
          <button disabled={browser.auditLoading} onclick={() => browser.searchAudit()}>
            {browser.auditError ? "Retry audit search" : browser.auditLoaded ? "Refresh audit search" : "Load audit events"}
          </button>
        </div>
        <div class="rows">
          {#if browser.auditLoaded && !browser.auditLoading && !browser.auditError && browser.auditEvents.length === 0}
            <p class="empty">No matching audit events.</p>
          {:else}
            {#each browser.auditEvents as event (event.id)}
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
            {#if browser.auditLoaded && browser.auditEvents.length > 0 && !browser.auditExhausted && !browser.auditError}
              <button class="more" disabled={!browser.auditCanMore} onclick={() => browser.searchAudit(true)}>
                {browser.auditMoreError ? "Retry audit page" : "Load more…"}
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

  .ceiling-status {
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

  .recovery-row {
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

  .rerun {
    font: inherit;
    font-size: var(--text-xs);
    padding: 0 var(--space-2);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: none;
    color: var(--color-accent);
    cursor: pointer;
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
