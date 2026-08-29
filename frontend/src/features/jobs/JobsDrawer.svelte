<!-- The §47 drawer, live: in-flight jobs with progress, rates and
  cancel, filterable by account (owner input 2026-08-29: multiple
  accounts in play need a filter), with the §50 audit trail behind a
  second tab. Polls while the app runs — faster when open or anything
  is active. -->
<script lang="ts">
  import { onMount } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { JobDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";

  let { browser }: { browser: BrowserState } = $props();

  let open = $state(false);
  let tab: "jobs" | "changes" = $state("jobs");
  let expanded: string | null = $state(null);
  let budgetSaving = $state(false);
  // §42 benchmark: typed-account consent inline.
  let benchOpen = $state(false);
  let benchConfirm = $state("");
  let benchError: string | null = $state(null);

  const TERMINAL = ["complete", "partial", "cancelled", "failed"];

  function isActive(job: JobDTO): boolean {
    return !TERMINAL.includes(job.state);
  }

  function progressOf(job: JobDTO): number | null {
    const planned = job.counters.objectsPlanned;
    if (planned <= 0 || !isActive(job)) {
      return null;
    }
    return Math.min(1, job.counters.objectsCopied / planned);
  }

  function timeOf(iso: string): string {
    return iso === "" ? "" : iso.replace("T", " ").replace(/(\.\d+)?Z$/, "");
  }

  async function cancel(job: JobDTO) {
    try {
      await browser.cancelJob(job.id);
    } catch {
      await browser.loadJobs();
    }
  }

  onMount(() => {
    void browser.loadJobs();
    void browser.loadAppSettings();
    const tick = setInterval(
      () => void browser.loadJobs(),
      1500,
    );
    return () => clearInterval(tick);
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
    if (!browser.appSettings) return;
    budgetSaving = true;
    try {
      await browser.saveAppSettings({ ...browser.appSettings, maxActiveJobs: value });
    } finally {
      budgetSaving = false;
    }
  }

  function settingsRows(job: JobDTO): [string, string][] {
    try {
      const parsed = JSON.parse(job.settings);
      return Object.entries(parsed)
        .filter(([, v]) => v !== "" && v !== 0)
        .map(([k, v]) => [k, String(v)]);
    } catch {
      return [];
    }
  }

  const bookmarkName = (id: string) =>
    browser.bookmarks.find((b) => b.id === id)?.displayName ?? "…";
</script>

<div class="drawer num">
  <button class="bar" onclick={() => (open = !open)} aria-expanded={open}>
    <span>
      {#if browser.activeJobs.length > 0}
        {browser.activeJobs.length} active
        {browser.activeJobs.length === 1 ? "job" : "jobs"}
      {:else}
        No active transfers
      {/if}
      {#if browser.auditEvents.length > 0}
        · {browser.auditEvents.length} recent changes
      {/if}
    </span>
    <span class="hint">{open ? "▾ hide" : "▸ jobs & changes"}</span>
  </button>

  {#if open}
    <div class="body">
      <div class="tabs" role="tablist">
        <button
          role="tab"
          class:active={tab === "jobs"}
          aria-selected={tab === "jobs"}
          onclick={() => (tab = "jobs")}>Jobs</button
        >
        <button
          role="tab"
          class:active={tab === "changes"}
          aria-selected={tab === "changes"}
          onclick={() => (tab = "changes")}>Recent changes</button
        >
        {#if tab === "jobs"}
          {#if browser.appSettings}
            <label class="filter" title="§38 whole-application active-job ceiling">
              <span>Active-job budget</span>
              <select
                value={String(browser.appSettings.maxActiveJobs)}
                disabled={budgetSaving}
                onchange={(e) => void saveBudget(Number(e.currentTarget.value))}
              >
                {#each [1, 2, 4, 8] as n (n)}
                  <option value={String(n)}>{n}</option>
                {/each}
              </select>
            </label>
          {/if}
          <button
            class="bench"
            title="Measure this cluster (§42): writes real objects to a temporary Aeronautes-owned container"
            disabled={browser.connectedBookmarkId === null}
            onclick={() => (benchOpen = !benchOpen)}
          >
            Benchmark…
          </button>
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
        {#if benchOpen}
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
              <span class="type-mono">{browser.account?.name}</span>. Type the account name to
              run:
            </span>
            <input class="type-mono" bind:value={benchConfirm} placeholder={browser.account?.name} />
            <button type="submit" disabled={benchConfirm !== (browser.account?.name ?? "")}>
              Run benchmark
            </button>
            {#if benchError}
              <em class="bad">{benchError}</em>
            {/if}
          </form>
        {/if}
        {#if browser.jobsError}
          <p class="empty">jobs unavailable: {browser.jobsError}</p>
        {:else if browser.jobs.length === 0}
          <p class="empty">No jobs yet — start a copy from the working list.</p>
        {:else}
          <div class="rows">
            {#each browser.jobs as job (job.id)}
              <div class="job">
                <span class="badge {job.state}">{job.state}</span>
                <span class="route type-mono" title="{job.source} → {job.destination}">
                  {job.source} → {job.destination}
                </span>
                <span class="accounts">
                  {bookmarkName(job.sourceBookmarkId)} → {bookmarkName(job.destBookmarkId)}
                </span>
                <span class="counts">
                  {job.counters.objectsCopied}{#if job.counters.objectsPlanned > 0}/{job.counters
                      .objectsPlanned}{/if}
                  obj · {formatBytes(job.counters.bytesCopied)}
                  {#if job.counters.objectsDeleted > 0}
                    · {job.counters.objectsDeleted} deleted
                  {/if}
                  {#if job.counters.objectsFailed > 0}
                    · <em class="bad">{job.counters.objectsFailed} failed</em>
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
                {#if isActive(job)}
                  <button class="cancel" onclick={() => cancel(job)}>Cancel</button>
                {:else}
                  <button
                    class="details"
                    onclick={() => (expanded = expanded === job.id ? null : job.id)}
                    aria-expanded={expanded === job.id}
                  >
                    {expanded === job.id ? "▾" : "▸"}
                  </button>
                {/if}
                {#if progressOf(job) !== null}
                  <div class="progress" style="--p: {(progressOf(job) ?? 0) * 100}%"></div>
                {/if}
                {#if job.error}
                  <p class="error">{job.error}</p>
                {/if}
                {#if expanded === job.id}
                  <div class="config num">
                    <span class="config-row"><span class="k">engine</span><span class="type-mono">{job.engine}</span></span>
                    <span class="config-row"><span class="k">verified</span><span>{job.counters.objectsVerified}</span></span>
                    {#each settingsRows(job) as [key, value] (key)}
                      <span class="config-row"><span class="k">{key}</span><span class="type-mono">{value}</span></span>
                    {/each}
                    {#if settingsRows(job).length === 0}
                      <span class="config-row"><span class="k">settings</span><span>defaults</span></span>
                    {/if}
                  </div>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
      {:else}
        <div class="rows">
          {#if browser.auditEvents.length === 0}
            <p class="empty">No mutations recorded yet.</p>
          {:else}
            {#each browser.auditEvents as event (event.id)}
              <div class="event">
                <span class="badge {event.result}">{event.result}</span>
                <span class="op type-mono">{event.operation}</span>
                <span class="resource type-mono">{event.resource}</span>
                <span class="summary">{event.summary}</span>
                <span class="when">{timeOf(event.at)}</span>
              </div>
            {/each}
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
</style>
