<script lang="ts">
  import { untrack } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { JobDTO, JobHistoryDTO } from "../../lib/api";
  import JobItems from "./JobItems.svelte";

  let { browser }: { browser: BrowserState } = $props();
  let page = $state<(JobHistoryDTO & { jobs: JobDTO[] }) | null>(null);
  let pageNumber = $state(1);
  let loading = $state(false);
  let error = $state(false);
  let expanded = $state<string | null>(null);
  let request = 0;

  // History scopes predate the typed locations; quoting makes controls visible without parsing them.
  function quoted(value: string): string {
    return JSON.stringify(value).replace(/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}]/gu,
      (char) => `\\u{${char.codePointAt(0)!.toString(16)}}`);
  }

  async function load(cursor: string, number: number) {
    const seq = ++request;
    const filter = browser.jobs.bookmarkFilter;
    expanded = null;
    page = null;
    error = false;
    loading = true;
    pageNumber = number;
    try {
      const result = await browser.jobs.history(filter, cursor, 50);
      if (!result.jobs) throw new Error("history rows unavailable");
      if (seq === request && filter === browser.jobs.bookmarkFilter) page = { ...result, jobs: result.jobs };
    } catch {
      if (seq === request && filter === browser.jobs.bookmarkFilter) error = true;
    } finally {
      if (seq === request && filter === browser.jobs.bookmarkFilter) loading = false;
    }
  }

  $effect(() => {
    browser.jobs.bookmarkFilter;
    untrack(() => void load("", 1));
    return () => { ++request; };
  });
</script>

<section class="history" aria-label="Job history" aria-busy={loading}>
  <p>Retained jobs, newest first. History stays on the page you choose; refresh the first page to see newer jobs.</p>
  <nav aria-label="History pages">
    <button onclick={() => void load("", 1)}>First history page / refresh</button>
    <span aria-live="polite">History page {pageNumber}{loading ? " · loading…" : ""}</span>
    <button disabled={loading || !page?.nextCursor} onclick={() => void load(page!.nextCursor, pageNumber + 1)}>Next history page</button>
  </nav>
  {#if error}
    <p role="alert">Job history is unavailable or its cursor is no longer valid. Refresh the first page.</p>
  {:else if !loading && page}
    {#if page.jobs.length === 0}
      <p>No retained jobs on this page.</p>
    {:else}
      <div class="history-page">
        {#each page.jobs as job (job.id)}
          <article class="history-job">
            <button class="history-details" aria-expanded={expanded === job.id} onclick={() => (expanded = expanded === job.id ? null : job.id)}>
              {job.type} · {job.state} when page loaded · {job.createdAt} · {job.id}
            </button>
            <p><code>{quoted(job.source)} → {quoted(job.destination)}</code></p>
            {#if job.error}<p>{job.error}</p>{/if}
            {#if job.counters.deletionAccountingIncomplete}
              <p>Deletion accounting is incomplete. Final failure and unresolved totals are unavailable.</p>
            {/if}
            {#if expanded === job.id}
              {#key job.id}<JobItems {browser} {job} />{/key}
            {/if}
          </article>
        {/each}
      </div>
      {#if !page.nextCursor}<p>End of retained job history.</p>{/if}
    {/if}
  {/if}
</section>

<style>
  .history { font-size: var(--text-xs); }
  p { margin: var(--space-1) 0; }
  nav { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: baseline; }
  button { font: inherit; background: var(--color-bg-card); color: var(--color-text); border: 1px solid var(--color-border); border-radius: var(--radius-control); cursor: pointer; }
  button:disabled { opacity: 0.5; cursor: default; }
  .history-page { max-height: 32rem; overflow: auto; }
  .history-job { padding: var(--space-2); border-bottom: 1px solid var(--color-border); }
  /* A clearly different shade on every other entry tells neighbours apart,
    which the hairline alone did not (operator, 0.5.0 test run). */
  .history-job:nth-child(even) { background: var(--color-bg-inset); }
  code { white-space: pre-wrap; overflow-wrap: anywhere; unicode-bidi: isolate; }
  [role="alert"] { color: var(--color-danger); }
</style>
