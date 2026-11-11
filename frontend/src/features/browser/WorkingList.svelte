<script lang="ts">
  import VirtualList from "../../components/VirtualList.svelte";
  import { breadcrumbs } from "../../lib/listing";
  import { formatBytes, formatCount } from "../../lib/format";
  import type { ListingRowDTO } from "../../lib/api";
  import { v1Offers } from "../../lib/v1.svelte";
  import type { BrowserState } from "./browserState.svelte";
  import FindPanel from "./FindPanel.svelte";

  let { browser }: { browser: BrowserState } = $props();

  let prefixInput: HTMLInputElement | undefined = $state();
  let virtualList: { focus: () => void } | undefined = $state();

  // focusList moves keyboard focus into the object list (screen-level
  // arrow shortcut, §4.4).
  export function focusList() {
    virtualList?.focus();
    if (browser.selectedIndex < 0 && browser.visibleEntries.length > 0) {
      browser.select(0);
    }
  }

  export function focusPrefixField() {
    prefixInput?.focus();
  }

  const crumbs = $derived(breadcrumbs(browser.prefix));
  const directoryLength = $derived(browser.prefix.lastIndexOf("/") + 1);
  const partialPrefix = $derived(browser.prefix.slice(directoryLength));

  function shortName(entry: ListingRowDTO): string {
    return entry.name.slice(directoryLength);
  }

  function applyPrefix() {
    const value = prefixInput?.value ?? "";
    browser.goTo(value);
  }

  function timestamp(iso: string): string {
    return iso === "" ? "—" : iso.replace("T", " ").replace(/(:\d\d)?Z$/, "");
  }
</script>

<section class="pane">
  <header class="pane-head">
    {#if browser.openContainerSettings?.publicRead}
      <div class="public-banner" role="alert">
        <strong>Public:</strong> anyone with the URL can read objects in
        <span class="type-mono">{browser.containerName}</span>
        (read ACL <span class="type-mono">{browser.openContainerSettings.readAcl}</span>){browser.openContainerSettings.publicListing ? ", and can list its object names" : ""}.
      </div>
    {:else if browser.containerName && browser.containerDetailError}
      <div class="settings-notice" role="status">
        Settings of <span class="type-mono">{browser.containerName}</span> could not be checked: {browser.containerDetailError}
      </div>
    {/if}
    {#if browser.backstageFrom}
      <div class="backstage-banner">
        <strong>Backstage:</strong> segments of
        <span class="type-mono">{browser.backstageFrom}</span> — objects here may be segments of
        larger logical objects; classification never authorizes deleting them.
      </div>
    {/if}
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
      {#if partialPrefix !== ""}
        <span class="prefix-filter">Prefix: {JSON.stringify(partialPrefix)}</span>
      {/if}
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
      <button
        class="find"
        title="Search the whole container server-side"
        disabled={browser.containerName === null}
        onclick={() => (browser.findOpen = !browser.findOpen)}
      >
        Find in container
      </button>
      <button
        class="refresh"
        title="Re-read this listing from the server"
        aria-label="Refresh listing"
        aria-busy={browser.entriesRefreshing}
        disabled={browser.containerName === null || browser.entriesLoading || browser.entriesRefreshing}
        onclick={() => browser.refreshView()}
      >
        Refresh
      </button>
      <button
        class="find"
        title="Count the objects under this prefix: streams the listing page by page and keeps no names"
        disabled={browser.containerName === null}
        onclick={() => (browser.countRunning ? void browser.cancelCount() : void browser.startCount())}
      >
        {browser.countRunning ? "Stop counting" : "Count objects"}
      </button>
      {#if v1Offers.copy}
        <button
          class="find"
          title="Copy this container/prefix to another account or container"
          disabled={browser.containerName === null || browser.backstageFrom !== null}
          onclick={() => (browser.transferOpen = true)}
        >
          Copy to…
        </button>
      {/if}
      {#if v1Offers.upload}
        <button
          class="find"
          title="Upload local files to this prefix"
          disabled={browser.containerName === null || browser.backstageFrom !== null}
          onclick={() => (browser.localTransfer = "upload")}
        >
          Upload…
        </button>
      {/if}
      {#if v1Offers.download}
        <button
          class="find"
          title="Download this prefix to a local directory"
          disabled={browser.containerName === null || browser.backstageFrom !== null}
          onclick={() => (browser.localTransfer = "download")}
        >
          Download…
        </button>
      {/if}
      {#if v1Offers.bulkDelete}
        <button
          class="find danger"
          title="Bulk delete: empty this prefix or container, or delete the container — NOT the selected object"
          disabled={browser.containerName === null || browser.backstageFrom !== null}
          onclick={() => (browser.deleteOpen = true)}
        >
          Delete scope…
        </button>
      {/if}
      {#if v1Offers.objectDelete && browser.chosenRows.some((row) => row.kind === "object")}
        <button
          class="find danger"
          title="Delete the selected objects as one job: an object listed with a size is checked against its listing, a zero-byte or SLO-listed row is read again first; prefix rows are left out"
          onclick={() => browser.openSelectedDelete()}
        >
          Delete selected…
        </button>
      {/if}
    </div>

    {#if browser.prefixCount}
      {@const count = browser.prefixCount}
      <p class="count-status num" role="status">
        {formatCount(count.scanned)} objects under <span class="type-mono">{browser.containerName}/{browser.prefix}</span>
        {#if count.error}
          · count failed: {count.error}
        {:else if !count.done}
          so far · {formatCount(Math.round(count.objectsPerSecond))}/s…
        {:else if browser.countedAt}
          · counted {browser.countedAt.replace("T", " ").replace(/\.\d+Z$/, "Z")}
        {/if}
      </p>
    {/if}
    {#if browser.findOpen}
      <FindPanel {browser} onClose={() => (browser.findOpen = false)} />
    {/if}

    <div class="columns num" aria-hidden="true">
      <span class="col-name">Name</span>
      <span class="col-size">Size</span>
      <span class="col-modified">Last modified</span>
    </div>
  </header>

  <div class="list">
    {#if !browser.containerName}
      <div class="empty">
        {#if browser.connecting}
          <p class="empty-title" role="status">Connecting to {browser.connectingName}…</p>
        {:else if !browser.account}
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
        {:else if (browser.containerDetail?.class ?? browser.container?.class) !== "normal"}
          <p class="empty-title">Empty segment store</p>
          <p class="empty-teach">
            You are backstage. Objects here may be segments of larger logical objects.
          </p>
        {:else}
          <p class="empty-title">Empty container</p>
        {/if}
      </div>
    {:else}
      <VirtualList
        bind:this={virtualList}
        ariaLabel="Objects"
        items={browser.visibleEntries}
        rowHeight={36}
        selected={browser.selectedIndex}
        rowSelected={(entry) => browser.isChosen(entry)}
        onSelect={(i, mode) => (mode === "toggle" ? browser.toggleRow(i) : mode === "range" ? browser.extendTo(i) : browser.select(i))}
        onActivate={(entry) => browser.activate(entry)}
      >
        {#snippet row(entry: ListingRowDTO, _index: number, isSelected: boolean)}
          <div class="entry" class:selected={isSelected}>
            {#if entry.kind === "prefix"}
              <svg class="icon folder" viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <path
                  d="M1.75 3.5 h4 l1.5 1.75 h7 v7.25 h-12.5 Z"
                  fill="none"
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
              <span class="name">{shortName(entry)}{#if entry.sloListed}{" "}<span class="badge">SLO</span>{/if}</span>
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
        · {browser.selectionCount === 0 ? "none selected" : `${browser.selectionCount} selected`}
      </span>
      {#if browser.entriesAtCeiling}
        <span class="retention-notice" role="status">
          {browser.entries.length.toLocaleString("en-US")} rows loaded, the retention ceiling; narrow the prefix, use Find, or count the prefix. The ceiling is set in Settings.
        </span>
      {:else if browser.entriesMarker !== ""}
        <div class="paging">
          {#if browser.entriesMoreError}
            <span class="page-error" role="alert">Could not load more objects: {browser.entriesMoreError}</span>
          {/if}
          <button
            class="more"
            disabled={browser.entriesMoreLoading || browser.entriesError !== null}
            aria-busy={browser.entriesMoreLoading}
            onclick={() => browser.loadMoreEntries().catch(() => {})}
          >
            {browser.entriesMoreLoading ? "Loading…" : browser.entriesMoreError ? "Retry load more" : "Load more"}
          </button>
        </div>
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

  .count-status {
    margin: 0 0 var(--space-2);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .public-banner {
    margin: 0 0 var(--space-2);
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-control);
  }

  .retention-notice {
    padding: var(--space-1) var(--space-2);
    color: var(--color-text-faint);
    border: 1px dashed var(--color-border-strong);
    border-radius: var(--radius-control);
  }

  .settings-notice {
    margin: 0 0 var(--space-2);
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-text-faint);
    border: 1px dashed var(--color-border-strong);
    border-radius: var(--radius-control);
  }

  .backstage-banner {
    margin-bottom: var(--space-2);
    padding: var(--space-1) var(--space-2);
    font-size: var(--text-xs);
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border: 1px solid var(--color-warning);
    border-radius: var(--radius-control);
    overflow-wrap: anywhere;
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
    white-space: pre;
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

  .prefix-filter {
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }

  .fields {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    align-items: end;
    margin: var(--space-2) 0 var(--space-3);
  }

  .field input {
    min-width: 9rem;
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

  .find.danger {
    color: var(--color-danger);
    border-color: var(--color-danger);
  }

  .find,
  .refresh {
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

  .refresh:disabled {
    opacity: 0.5;
    cursor: default;
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
    flex-wrap: wrap;
    gap: var(--space-1);
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

  .more:disabled {
    cursor: wait;
    opacity: 0.6;
  }

  .paging {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
    max-width: 100%;
  }

  .page-error {
    color: var(--color-danger);
    overflow-wrap: anywhere;
  }
</style>
