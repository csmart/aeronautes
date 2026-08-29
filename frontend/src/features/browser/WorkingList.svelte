<script lang="ts">
  import VirtualList from "../../components/VirtualList.svelte";
  import { breadcrumbs } from "../../lib/listing";
  import { formatBytes } from "../../lib/format";
  import type { ListingRowDTO } from "../../lib/api";
  import { suspectedSegmentStore, type BrowserState } from "./browserState.svelte";

  let { browser }: { browser: BrowserState } = $props();

  let prefixInput: HTMLInputElement | undefined = $state();

  export function focusPrefixField() {
    prefixInput?.focus();
  }

  const crumbs = $derived(breadcrumbs(browser.prefix));

  function shortName(entry: ListingRowDTO): string {
    return entry.name.slice(browser.prefix.length);
  }

  function applyPrefix() {
    const value = prefixInput?.value ?? "";
    browser.goTo(value === "" || value.endsWith("/") ? value : value + "/");
  }

  function timestamp(iso: string): string {
    return iso === "" ? "—" : iso.replace("T", " ").replace(/(:\d\d)?Z$/, "");
  }
</script>

<section class="pane">
  <header class="pane-head">
    <nav class="breadcrumb num" aria-label="Path">
      <button class="crumb" onclick={() => browser.up()} title="Up" aria-label="Up one level">↑</button>
      <button class="crumb root" onclick={() => browser.goTo("")}>
        {browser.containerName ?? "—"}
      </button>
      {#each crumbs as crumb (crumb.prefix)}
        <span class="crumb-sep">/</span>
        <button class="crumb" onclick={() => browser.goTo(crumb.prefix)}>
          {crumb.label === "" ? "␣" : crumb.label}
        </button>
      {/each}
    </nav>

    <div class="fields">
      <label class="field">
        <span class="field-label">Prefix</span>
        <input
          class="type-mono"
          bind:this={prefixInput}
          value={browser.prefix}
          placeholder="server-side prefix…"
          onchange={applyPrefix}
        />
      </label>
      <label class="field">
        <span class="field-label">Filter</span>
        <input bind:value={browser.filter} placeholder="filter loaded rows…" />
      </label>
      <button class="find" title="Search the whole container server-side (Phase 1)">
        Find in container
      </button>
    </div>

    <div class="columns num" aria-hidden="true">
      <span class="col-name">Name</span>
      <span class="col-size">Size</span>
      <span class="col-modified">Last modified</span>
    </div>
  </header>

  <div class="list">
    {#if !browser.containerName}
      <div class="empty">
        {#if !browser.account}
          <p class="empty-title">Not connected</p>
          <p class="empty-teach">Choose a bookmark on the rail, or add one, to browse.</p>
        {:else}
          <p class="empty-title">Select a container</p>
          <p class="empty-teach">
            A container is a namespace with its own storage policy and access controls.
          </p>
        {/if}
      </div>
    {:else if browser.entriesLoading}
      <div class="empty"><p class="empty-title">Listing…</p></div>
    {:else if browser.entriesError}
      <div class="empty">
        <p class="empty-title">Listing failed</p>
        <p class="empty-teach error-text">{browser.entriesError}</p>
      </div>
    {:else if browser.visibleEntries.length === 0}
      <div class="empty">
        {#if browser.filter !== ""}
          <p class="empty-title">No loaded rows match the filter</p>
        {:else if browser.prefix !== ""}
          <p class="empty-title">Nothing under this prefix</p>
          <p class="empty-teach">
            A prefix exists because objects live beneath it — or because an explicit
            marker object was created. No directory is required.
          </p>
        {:else if suspectedSegmentStore(browser.containerName)}
          <p class="empty-title">Empty segment store</p>
          <p class="empty-teach">
            You are backstage. Objects here may be parts of larger logical objects.
          </p>
        {:else}
          <p class="empty-title">Empty container</p>
        {/if}
      </div>
    {:else}
      <VirtualList
        items={browser.visibleEntries}
        rowHeight={36}
        selected={browser.selectedIndex}
        onSelect={(i) => browser.select(i)}
        onActivate={(entry) => browser.activate(entry)}
      >
        {#snippet row(entry: ListingRowDTO, _index: number, isSelected: boolean)}
          <div class="entry" class:selected={isSelected}>
            {#if entry.kind === "prefix"}
              <svg class="icon folder" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <path
                  d="M1.75 3.5 h4 l1.5 1.75 h7 v7.25 h-12.5 Z"
                  fill={entry.markerBacked ? "currentColor" : "none"}
                  stroke="currentColor"
                  stroke-width="1.3"
                />
              </svg>
              <span class="name">{shortName(entry)}</span>
              <span class="size num">—</span>
              <span class="modified num">—</span>
            {:else}
              <svg class="icon" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <path
                  d="M3.5 1.75 h6 l3 3 v9.5 h-9 Z M9.5 1.75 v3 h3"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.3"
                />
              </svg>
              <span class="name">
                {shortName(entry)}
                {#if entry.sloListed}
                  <span class="badge">SLO</span>
                {/if}
              </span>
              <span class="size num">{formatBytes(entry.bytes)}</span>
              <span class="modified num">{timestamp(entry.lastModified)}</span>
            {/if}
          </div>
        {/snippet}
      </VirtualList>
    {/if}
  </div>

  <footer class="pane-foot num">
    {#if browser.containerName && !browser.entriesLoading}
      <span>
        {browser.visibleEntries.length} rows{browser.entriesMarker !== "" ? " (more on server)" : ""}
        · {browser.selectedIndex >= 0 ? "1 selected" : "none selected"}
      </span>
      {#if browser.entriesMarker !== ""}
        <button class="more" onclick={() => browser.loadMoreEntries()}>Load more</button>
      {/if}
    {:else}
      <span>{browser.containerName ? "listing…" : "no container open"}</span>
    {/if}
  </footer>
</section>

<style>
  .pane {
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--color-bg-pane);
    border-right: 1px solid var(--color-border);
  }

  .pane-head {
    padding: var(--space-3) var(--space-3) 0;
  }

  .breadcrumb {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    min-height: 1.8rem;
    flex-wrap: wrap;
  }

  .crumb {
    padding: 0.1rem var(--space-1);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    background: none;
    border: none;
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .crumb:hover {
    color: var(--color-accent-strong);
    background: var(--color-bg-card);
  }

  .crumb.root {
    font-weight: var(--weight-semibold);
    color: var(--color-text);
  }

  .crumb-sep {
    color: var(--color-text-faint);
  }

  .fields {
    display: flex;
    gap: var(--space-2);
    align-items: end;
    margin: var(--space-2) 0 var(--space-3);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .field:first-child {
    flex: 1.4;
  }

  .field:nth-child(2) {
    flex: 1;
  }

  .field-label {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .field input {
    width: 100%;
    box-sizing: border-box;
    padding: var(--space-1) var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  .field input.type-mono {
    font-family: var(--font-mono);
  }

  .find {
    padding: var(--space-1) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-accent-strong);
    background: var(--color-bg-card);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-control);
    cursor: pointer;
    white-space: nowrap;
  }

  .columns {
    display: grid;
    grid-template-columns: 1fr 7rem 11rem;
    gap: var(--space-2);
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    border-bottom: 1px solid var(--color-border);
  }

  .col-size,
  .col-modified {
    text-align: right;
  }

  .list {
    flex: 1;
    min-height: 0;
  }

  .entry {
    display: grid;
    grid-template-columns: auto 1fr 7rem 11rem;
    gap: var(--space-2);
    align-items: center;
    height: 100%;
    box-sizing: border-box;
    padding: 0 var(--space-2);
    margin: 0 var(--space-1);
    border-radius: var(--radius-control);
    cursor: default;
  }

  .entry:hover {
    background: var(--color-bg-card);
  }

  .entry.selected {
    background: var(--color-selection-bg);
    box-shadow: inset 0 0 0 1px var(--color-selection-border);
  }

  .icon {
    color: var(--color-text-faint);
  }

  .icon.folder {
    color: var(--color-accent);
  }

  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: pre; /* hostile names: trailing/repeated spaces stay visible */
  }

  .badge {
    margin-left: var(--space-1);
    padding: 0 var(--space-1);
    font-size: var(--text-xs);
    font-weight: var(--weight-semibold);
    color: var(--color-accent-strong);
    background: var(--color-accent-soft);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-pill);
  }

  .size,
  .modified {
    text-align: right;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .empty {
    display: grid;
    place-content: center;
    height: 100%;
    text-align: center;
    padding: var(--space-5);
  }

  .empty-title {
    margin: 0;
    font-weight: var(--weight-medium);
    color: var(--color-text-secondary);
  }

  .empty-teach {
    margin: var(--space-2) 0 0;
    max-width: 28rem;
    font-size: var(--text-sm);
    color: var(--color-text-faint);
  }

  .error-text {
    color: var(--color-danger);
  }

  .pane-foot {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--space-1) var(--space-3);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    border-top: 1px solid var(--color-border);
  }

  .more {
    padding: 0 var(--space-2);
    font: inherit;
    font-size: var(--text-xs);
    color: var(--color-accent-strong);
    background: none;
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-pill);
    cursor: pointer;
  }
</style>
