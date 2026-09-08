<script lang="ts">
  import { onMount, untrack } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { JobDTO, JobItemDTO, JobItemPageDTO, JobItemTotalsDTO } from "../../lib/api";

  let { browser, job }: { browser: BrowserState; job: JobDTO } = $props();
  let page = $state<(JobItemPageDTO & { items: JobItemDTO[] }) | null>(null);
  let totals = $state<JobItemTotalsDTO | null>(null);
  let pageNumber = $state(1);
  let loading = $state(false);
  let error = $state(false);
  let pollError = $state(false);
  let polledState = $state<string | null>(null);
  let request = 0;
  let pollRequest = 0;
  let disposed = false;
  const terminal = (state: string) => ["complete", "partial", "cancelled", "failed"].includes(state);
  const currentState = $derived(terminal(job.state) ? job.state : polledState ?? job.state);
  const active = $derived(!terminal(currentState));

  async function load(cursor: string, number: number) {
    const seq = ++request;
    const id = job.id;
    page = null;
    totals = null;
    error = false;
    loading = true;
    pageNumber = number;
    try {
      const [items, counts] = await Promise.all([
        browser.jobItems(id, cursor, 100), browser.jobItemTotals(id),
      ]);
      if (disposed || seq !== request || id !== job.id) return;
      if (!items.items || items.status !== counts.status || !["available", "not_captured"].includes(items.status)) {
        throw new Error("inconsistent record availability");
      }
      page = { ...items, items: items.items };
      totals = counts;
    } catch {
      if (!disposed && seq === request && id === job.id) error = true;
    } finally {
      if (!disposed && seq === request && id === job.id) loading = false;
    }
  }

  // Completion can happen after a history job has left the live fifty-job window.
  async function pollState() {
    const seq = ++pollRequest;
    const id = job.id;
    try {
      const current = await browser.job(id);
      if (disposed || seq !== pollRequest || id !== job.id) return;
      if (!current) throw new Error("job unavailable");
      polledState = current.state;
      if (pollError) void load("", 1);
      pollError = false;
    } catch {
      if (disposed || seq !== pollRequest || id !== job.id) return;
      ++request;
      page = null;
      totals = null;
      loading = false;
      pollError = true;
    }
  }

  $effect(() => {
    job.id;
    active;
    untrack(() => void load("", 1));
    return () => { ++request; };
  });

  onMount(() => {
    const tick = setInterval(() => void pollState(), 1500);
    return () => {
      disposed = true;
      ++request;
      ++pollRequest;
      clearInterval(tick);
    };
  });
</script>

<section class="item-records" aria-label="Retained item records" aria-busy={loading}>
  <h4>Retained item records</h4>
  <p>Last observed job state: {currentState}.</p>
  <p>Names and locations are quoted; escapes expose invisible characters. Expand a name to inspect its full locations.</p>
  {#if active}
    <p role="status">Job still active. Records are incomplete and may change. Refresh to see newly saved facts.</p>
  {/if}
  <nav aria-label="Item pages">
    <button onclick={() => void load("", 1)}>First item page / refresh</button>
    <span aria-live="polite">Item page {pageNumber}{loading ? " · loading…" : ""}</span>
    <button disabled={loading || pollError || !page?.nextCursor} onclick={() => void load(page!.nextCursor, pageNumber + 1)}>Next item page</button>
  </nav>
  {#if error || pollError}
    <p role="alert">Item records are unavailable. Retained outcomes and counts cannot be confirmed.</p>
  {:else if !loading && page?.status === "not_captured"}
    <p>Item records were not captured for this job. Its displayed result does not establish the missing item outcomes.</p>
  {:else if !loading && page?.status === "available" && totals}
    <dl class="totals">
      <div><dt>Recorded items</dt><dd>{totals.items}</dd></div>
      <div><dt>Planned</dt><dd>{totals.planned}</dd></div>
      <div><dt>Admitted</dt><dd>{totals.admitted}</dd></div>
      <div><dt>Failed names</dt><dd>{totals.failed}</dd></div>
      <div><dt>Conflicts</dt><dd>{totals.conflicts}</dd></div>
      <div><dt>Refused</dt><dd>{totals.refused}</dd></div>
      <div><dt>Unresolved effects</dt><dd>{totals.unresolved}</dd></div>
      <div><dt>Unverified copies</dt><dd>{totals.unverified}</dd></div>
      <div><dt>Items with diagnostics</dt><dd>{totals.problems}</dd></div>
    </dl>
    <p>These counts overlap; do not add them together. While active, pages and counts are separate snapshots.</p>
    {#if page.items.length === 0}
      <p>{pageNumber === 1 ? "No item records have been saved for this recorded job." : "No further item records on this page."}</p>
    {:else}
      <div class="item-page">
        {#each page.items as item (item.id)}
          <article class="item-record">
            <details>
              <summary><code>{item.keyDisplay}</code></summary>
              <dl class="locations">
                <dt>Source ({item.source.kind})</dt><dd><code>{item.source.display}</code></dd>
                <dt>Destination ({item.destination.kind})</dt><dd><code>{item.destination.display}</code></dd>
                {#if item.staging}
                  <dt>Staging ({item.staging.kind})</dt><dd><code>{item.staging.display}</code></dd>
                {/if}
              </dl>
              <p>Planned: {item.planned ? "yes" : "no"} · Admitted: {item.admitted ? "yes" : "no"} · Listed bytes (advisory): {item.listedBytes}</p>
            </details>
            <p>
              {#if item.admitted && (!item.execution || item.execution.effect === "unresolved")}<span class="problem">Unresolved effects. </span>{/if}
              {#if item.execution?.result === "copied" && item.verification?.result !== "verified"}<span class="problem">Unverified copy. </span>{/if}
              {#if !item.admitted && !item.execution && item.verification}Comparison only; no execution admission.{/if}
            </p>
            {#each [["Execution", item.execution], ["Verification", item.verification]] as const as [label, observation]}
              <p class="observation">
                <strong>{label}:</strong>
                {#if observation}
                  {observation.result.replaceAll("_", " ")} · effects: {observation.effect.replaceAll("_", " ")}
                  {#if observation.result === "copied"} · {observation.copiedBytes} reported copied bytes{/if}
                  {#if observation.code} · {observation.stage}: {observation.code}{/if}
                  {#if observation.httpStatus} · HTTP {observation.httpStatus}{/if}
                {:else}
                  No observation recorded.
                {/if}
              </p>
            {/each}
          </article>
        {/each}
      </div>
      {#if !page.nextCursor}<p>End of saved item records.</p>{/if}
    {/if}
  {/if}
</section>

<style>
  .item-records { grid-column: 1 / -1; border: 1px solid var(--color-border); border-radius: var(--radius-control); padding: var(--space-2); font-size: var(--text-xs); min-width: 0; }
  h4, p { margin: var(--space-1) 0; }
  nav, .totals { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: baseline; }
  button { font: inherit; background: var(--color-bg-card); color: var(--color-text); border: 1px solid var(--color-border); border-radius: var(--radius-control); cursor: pointer; }
  button:disabled { opacity: 0.5; cursor: default; }
  .totals div { display: flex; gap: var(--space-1); }
  dd { margin: 0; }
  .item-page { max-height: 24rem; overflow: auto; }
  .item-record { padding: var(--space-2); border-top: 1px solid var(--color-border); }
  summary { cursor: pointer; overflow-wrap: anywhere; }
  .locations { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: var(--space-1) var(--space-2); }
  code { white-space: pre-wrap; overflow-wrap: anywhere; unicode-bidi: isolate; }
  .problem, [role="alert"] { color: var(--color-danger); }
</style>
