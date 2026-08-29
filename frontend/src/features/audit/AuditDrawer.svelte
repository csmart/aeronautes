<!-- The §47 drawer, Phase-3 shape: no long-running jobs exist yet, so it
  carries the §50 audit trail — every mutation visibly accounted for,
  expandable, newest first. Jobs and the bookmark filter arrive with
  Phase 5. -->
<script lang="ts">
  import type { BrowserState } from "../browser/browserState.svelte";

  let { browser }: { browser: BrowserState } = $props();

  let open = $state(false);

  function timeOf(iso: string): string {
    return iso.replace("T", " ").replace(/(\.\d+)?Z$/, "");
  }
</script>

<div class="drawer num">
  <button class="bar" onclick={() => (open = !open)} aria-expanded={open}>
    <span>
      No active transfers
      {#if browser.auditEvents.length > 0}
        · {browser.auditEvents.length} recent changes
      {/if}
    </span>
    <span class="hint">{open ? "▾ hide" : "▸ recent changes"} · jobs arrive with Phase 5</span>
  </button>

  {#if open}
    <div class="events">
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

  .events {
    max-height: 12rem;
    overflow-y: auto;
    padding: 0 var(--space-4) var(--space-2);
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .empty {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-faint);
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

  .event:hover {
    background: var(--color-bg-card);
  }

  .badge {
    justify-self: start;
    padding: 0 var(--space-1);
    font-weight: var(--weight-semibold);
    border-radius: var(--radius-pill);
    border: 1px solid;
  }

  .badge.applied {
    color: var(--color-success);
    background: var(--color-success-soft);
    border-color: var(--color-success);
  }

  .badge.conflict,
  .badge.verify_mismatch {
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-color: var(--color-warning);
  }

  .badge.failed {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-color: var(--color-danger);
  }

  .op {
    color: var(--color-text-secondary);
  }

  .resource {
    color: var(--color-text);
    overflow-wrap: anywhere;
  }

  .summary {
    color: var(--color-text-faint);
  }

  .when {
    color: var(--color-text-faint);
  }
</style>
