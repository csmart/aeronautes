<script lang="ts">
  import VirtualList from "../../components/VirtualList.svelte";
  import ContextMenu, { type MenuItem } from "../../components/ContextMenu.svelte";
  import { breadcrumbs } from "../../lib/listing";
  import { formatBytes, formatCount } from "../../lib/format";
  import type { ListingRowDTO } from "../../lib/api";
  import { v1Offers } from "../../lib/v1.svelte";
  import type { BrowserState } from "./browserState.svelte";
  import FindPanel from "./FindPanel.svelte";
  import { openSelectionDelete, singleRowDelete } from "./selectionDelete";

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

  const rowDelete = $derived(singleRowDelete(browser));
  // Copy and Download act on what is selected and say so (ADR 0108): the
  // selected objects and folders, the selected object or folder, or else the
  // folder or container being browsed.
  const source = $derived(browser.transferSource);
  const sourceWord = $derived(source.kind === "selected" ? "selected" : source.kind);

  // The row menu offers the toolbar's own actions for what is selected,
  // under the same conditions, so the two can never disagree.
  let menu: { x: number; y: number; entry: ListingRowDTO } | null = $state(null);

  function menuItems(entry: ListingRowDTO): MenuItem[] {
    // The toolbar's own refusals: nothing to act in, or a segment listing.
    const noScope = browser.containerName === null || browser.backstageFrom !== null;
    const items: MenuItem[] = [];
    if (entry.kind === "prefix") items.push({ label: "Open", run: () => browser.activate(entry) });
    if (v1Offers.download) {
      items.push({ label: `Download ${sourceWord}…`, disabled: noScope, run: () => browser.openTransfer("download") });
    }
    if (v1Offers.copy) {
      items.push({ label: `Copy ${sourceWord}…`, disabled: noScope, run: () => browser.openTransfer("copy") });
    }
    items.push({ label: "Copy name", run: () => void browser.copyText(entry.name) });
    if (rowDelete) items.push({ label: rowDelete.label, danger: true, run: () => openSelectionDelete(browser) });
    if (v1Offers.objectDelete && browser.chosenRows.length > 1) {
      items.push({ label: "Delete selected…", danger: true, run: () => browser.openSelectedDelete() });
    }
    // The prefix deletion's target is the selected folder only when that
    // folder is the one row selected; otherwise the dialog would act on the
    // folder being browsed or the container, under a label naming this row.
    if (v1Offers.bulkDelete && entry.kind === "prefix" && browser.selectedPrefix === entry.name) {
      items.push({ label: "Delete prefix…", danger: true, disabled: noScope, run: () => (browser.deleteOpen = "prefix") });
    }
    return items;
  }

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
        Settings of <span class="type-mono">{browser.containerName}</span> could not be read: {browser.containerDetailError}
      </div>
    {/if}
    {#if browser.backstageFrom}
      <div class="backstage-banner">
        <strong>Backstage:</strong> segments of
        <span class="type-mono">{browser.backstageFrom}</span> — objects here may be segments of
        larger logical objects; classification never authorizes deleting them.
      </div>
    {:else if browser.segmentStoreCaution}
      <!-- However the container was reached (ADR 0117 decision 2); the
        jump's notice above says more, so it stands alone. -->
      <div class="backstage-banner">{browser.segmentStoreCaution}</div>
    {/if}
    <!-- The path and, at its end, the listing's refresh as a small button
      named by its hover text, as the Containers column has it. -->
    <div class="path-row">
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
      <button
        class="tool"
        title="Refresh: re-read this listing from the server"
        aria-label="Refresh listing"
        aria-busy={browser.entriesRefreshing}
        disabled={browser.containerName === null || browser.entriesLoading || browser.entriesRefreshing}
        onclick={() => browser.refreshView()}
      >
        <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
          <path d="M13.25 8A5.25 5.25 0 1 1 11.71 4.29M11.71 1.79V4.29H9.21" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    </div>

    <div class="fields">
      <label class="field">
        <span class="field-label">Prefix</span>
        <input
          class="type-mono"
          bind:this={prefixInput}
          value={browser.prefix}
          placeholder="folder path, e.g. logs/2026/"
          onchange={applyPrefix}
        />
      </label>
      <label class="field">
        <span class="field-label">Filter</span>
        <input bind:value={browser.filter} placeholder="filter this list…" />
      </label>
      <button
        class="find"
        title="Search the whole container server-side"
        disabled={browser.containerName === null}
        onclick={() => (browser.findOpen = !browser.findOpen)}
      >
        Find in container
      </button>
    </div>

    <!-- Two rows by weight: the transfers with the quieter listing tool,
      and the deletions on a row of their own so they are never next to a
      transfer the operator meant to press. A button whose label follows the
      selection sits in a slot as wide as its widest label, and a button
      that comes and goes has a slot kept for it, so selecting a row moves
      neither the buttons nor the list below them. -->
    <div class="actions" role="group" aria-label="Actions">
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
        <span class="steady">
          <span class="sizer" aria-hidden="true">Download container…</span>
          <button
            class="find"
            title="Download it to a local directory"
            disabled={browser.containerName === null || browser.backstageFrom !== null}
            onclick={() => browser.openTransfer("download")}
          >
            Download {sourceWord}…
          </button>
        </span>
      {/if}
      {#if v1Offers.copy}
        <span class="steady">
          <span class="sizer" aria-hidden="true">Copy container…</span>
          <button
            class="find"
            title="Copy it to another account, container or folder"
            disabled={browser.containerName === null || browser.backstageFrom !== null}
            onclick={() => browser.openTransfer("copy")}
          >
            Copy {sourceWord}…
          </button>
        </span>
      {/if}
      <span class="gap"></span>
      <button
        class="find quiet"
        title="Count the objects under this prefix: streams the listing page by page and keeps no names"
        disabled={browser.containerName === null}
        onclick={() => (browser.countRunning ? void browser.cancelCount() : void browser.startCount())}
      >
        {browser.countRunning ? "Stop counting" : "Count objects"}
      </button>
      <span class="deletions">
      <!-- The selection's own deletion: one row's, known once the row has
        been read, or the selected set's. Never both, so they share a slot. -->
      <span class="steady">
        <span class="sizer" aria-hidden="true">Delete manifest…</span>
        {#if rowDelete}
          <!-- One selected row's own deletion, as the Delete key and the
            inspector open it, so a single file is deleted from here too. -->
          <button class="find danger" title="Delete only the selected row" onclick={() => openSelectionDelete(browser)}>
            {rowDelete.label}
          </button>
        {:else if v1Offers.objectDelete && browser.chosenRows.length > 1}
          <button
            class="find danger"
            title="Delete the selected objects, and each selected folder with everything in it, as one job"
            onclick={() => browser.openSelectedDelete()}
          >
            Delete selected…
          </button>
        {/if}
      </span>
      {#if v1Offers.bulkDelete}
        <!-- Each deletion is its own action (ADR 0121). Inside a folder only
          the folder's is offered, so a container's deletion is never one
          click from a prefix's; the container's two are at its top and in
          its menu. -->
        {@const noContainer = browser.containerName === null || browser.backstageFrom !== null}
        <span class="steady bulk">
          {#if browser.prefix === ""}
            <!-- At a container's top one selected folder swaps the two
              container actions for its own; the slot stays two wide. -->
            <span class="sizer" aria-hidden="true">Empty container…</span>
            <span class="sizer second" aria-hidden="true">Delete container…</span>
          {/if}
          <span class="bulk-buttons">
          {#if browser.prefix === "" && browser.selectedPrefix === ""}
            <button
              class="find danger"
              title="Delete everything in this container; the container and its settings stay"
              disabled={noContainer}
              onclick={() => (browser.deleteOpen = "empty")}
            >
              Empty container…
            </button>
            <button
              class="find danger"
              title="Delete everything in this container, then the container itself with its settings"
              disabled={noContainer}
              onclick={() => (browser.deleteOpen = "delete_container")}
            >
              Delete container…
            </button>
          {:else}
            <button
              class="find danger"
              title="Delete everything under this prefix; not only the selected objects"
              disabled={noContainer}
              onclick={() => (browser.deleteOpen = "prefix")}
            >
              Delete prefix…
            </button>
          {/if}
          </span>
        </span>
      {/if}
      </span>
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
        onContextMenu={(entry, _index, x, y) => (menu = { x, y, entry })}
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
  {#if menu}
    <ContextMenu
      x={menu.x}
      y={menu.y}
      label="Actions for {shortName(menu.entry)}"
      items={menuItems(menu.entry)}
      onClose={() => {
        menu = null;
        virtualList?.focus();
      }}
    />
  {/if}

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

  .path-row {
    display: flex;
    align-items: flex-start;
    gap: var(--space-2);
  }

  .breadcrumb {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    min-height: 1.8rem;
    flex-wrap: wrap;
    flex: 1;
    min-width: 0;
  }

  /* The Containers column's small tool button. */
  .tool {
    display: grid;
    place-items: center;
    flex: none;
    width: 1.6rem;
    height: 1.6rem;
    padding: 0;
    color: var(--color-accent-strong);
    background: var(--color-bg-card);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .tool:disabled {
    opacity: 0.5;
    cursor: default;
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
    margin: var(--space-2) 0 var(--space-2);
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    align-items: center;
    margin: 0 0 var(--space-3);
  }

  .actions .gap {
    width: var(--space-2);
  }

  /* A row of its own, at the right. Where the pane is too narrow for one
    line, the slot kept for the selection's deletion goes under the
    container's or the folder's, not above them as an empty line. */
  .actions .deletions {
    display: flex;
    flex-wrap: wrap-reverse;
    justify-content: flex-end;
    gap: var(--space-2);
    flex-basis: 100%;
  }

  /* A slot that is always as wide as the hidden label inside it, whatever
    the button there reads and whether or not there is one. */
  .steady {
    display: inline-grid;
  }

  .steady > * {
    grid-area: 1 / 1;
  }

  .steady .sizer {
    visibility: hidden;
    border-color: transparent;
  }

  /* Two labels wide, with the buttons at its right. */
  .steady.bulk {
    grid-template-columns: auto auto;
    column-gap: var(--space-2);
  }

  .steady.bulk > .sizer.second {
    grid-area: 1 / 2;
  }

  .steady.bulk > .bulk-buttons {
    grid-area: 1 / 1 / 2 / 3;
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }

  .actions .quiet {
    color: var(--color-text-secondary);
    border-color: var(--color-border);
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
  .sizer {
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
