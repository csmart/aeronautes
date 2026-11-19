<script lang="ts">
  import VirtualList from "../../components/VirtualList.svelte";
  import ContextMenu, { type MenuItem } from "../../components/ContextMenu.svelte";
  import { breadcrumbs } from "../../lib/listing";
  import { formatBytes, formatCount } from "../../lib/format";
  import type { ListingRowDTO } from "../../lib/api";
  import type { BrowserState } from "./browserState.svelte";
  import FindPanel from "./FindPanel.svelte";
  import NewFolderDialog from "./NewFolderDialog.svelte";
  import { openSelectionDelete, singleRowDelete } from "./selectionDelete";

  let { browser }: { browser: BrowserState } = $props();

  let prefixInput: HTMLInputElement | undefined = $state();
  let newFolderOpen = $state(false);
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
  const noContainer = $derived(browser.containerName === null || browser.backstageFrom !== null);
  // Rows are selected, other than one folder alone, whose own deletion is
  // the prefix deletion.
  const rowsSelected = $derived(browser.chosenRows.length > 1 || (browser.selectedEntry !== null && browser.selectedPrefix === ""));
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
    items.push({ label: `Download ${sourceWord}…`, disabled: noScope, run: () => browser.openTransfer("download") });
    items.push({ label: `Copy ${sourceWord}…`, disabled: noScope, run: () => browser.openTransfer("copy") });
    items.push({ label: "Copy name", run: () => void browser.copyText(entry.name) });
    if (rowDelete) items.push({ label: rowDelete.label, danger: true, run: () => openSelectionDelete(browser) });
    if (browser.chosenRows.length > 1) {
      items.push({ label: "Delete selected…", danger: true, run: () => browser.openSelectedDelete() });
    }
    // The prefix deletion's target is the selected folder only when that
    // folder is the one row selected; otherwise the dialog would act on the
    // folder being browsed or the container, under a label naming this row.
    if (entry.kind === "prefix" && browser.selectedPrefix === entry.name) {
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

  function objectCount(n: number): string {
    return `${formatCount(n)} ${n === 1 ? "object" : "objects"}`;
  }

  // A folder by its path, the container's top by the container's name.
  function placeName(prefix: string): string {
    return prefix === "" ? (browser.containerName ?? "") : prefix;
  }

  // What the application has to say about this place, as short marks whose
  // hover text is the whole sentence (ADR 0123 decision 2).
  type Mark = { label: string; tone: "danger" | "warning" | "quiet"; sentence: string };
  const marks: Mark[] = $derived.by(() => {
    const found: Mark[] = [];
    const settings = browser.openContainerSettings;
    const name = browser.containerName;
    if (settings?.publicRead) {
      const listing = settings.publicListing ? ", and can list its object names" : "";
      found.push({ label: "Public", tone: "danger",
        sentence: `Public: anyone with the URL can read objects in ${name} (read ACL ${settings.readAcl})${listing}.` });
    } else if (name && browser.containerDetailError) {
      found.push({ label: "Settings not read", tone: "quiet",
        sentence: `Settings of ${name} could not be read: ${browser.containerDetailError}` });
    }
    if (browser.backstageFrom) {
      found.push({ label: "Backstage", tone: "warning",
        sentence: `Backstage: segments of ${browser.backstageFrom} — objects here may be segments of larger logical objects; classification never authorizes deleting them.` });
    } else if (browser.segmentStoreCaution) {
      // However the container was reached (ADR 0117 decision 2); the
      // jump's mark above says more, so it stands alone.
      found.push({ label: browser.segmentStoreMark, tone: "warning", sentence: browser.segmentStoreCaution });
    }
    return found;
  });

  function timestamp(iso: string): string {
    return iso === "" ? "—" : iso.replace("T", " ").replace(/(:\d\d)?Z$/, "");
  }
</script>

<section class="pane">
  <header class="pane-head">
    <!-- The path, the marks for what the application has to say about
      this place, and the two tools pinned at the row's end, as the
      Containers column has its own. Nothing here adds a line, so the list
      below never moves under the pointer, and nothing after the tools can
      move them (ADR 0123). -->
    <div class="path-row">
      <nav class="breadcrumb num" aria-label="Path">
        <button class="crumb" onclick={() => browser.up()} title="Up" aria-label="Up one level">↑</button>
        <button class="crumb root" title={browser.containerName ?? undefined} onclick={() => browser.goTo("")}>
          {browser.containerName ?? "—"}
        </button>
        {#each crumbs as crumb (crumb.prefix)}
          <span class="crumb-sep">/</span>
          <button class="crumb" title={crumb.prefix} onclick={() => browser.goTo(crumb.prefix)}>
            {crumb.label === "" ? "␣" : crumb.label}
          </button>
        {/each}
        {#if partialPrefix !== ""}
          <span class="prefix-filter">Prefix: {JSON.stringify(partialPrefix)}</span>
        {/if}
      </nav>
      {#if browser.prefixCount}
        {@const count = browser.prefixCount}
        <span
          class="count-status num"
          role="status"
          title={`${objectCount(count.scanned)} in ${placeName(browser.countedPrefix)}` +
            (browser.countedAt ? ` · counted ${browser.countedAt.replace("T", " ").replace(/\.\d+Z$/, "Z")}` : "")}
        >
          {objectCount(count.scanned)} in <span class="type-mono">{placeName(browser.countedPrefix)}</span>
          {#if count.error}
            · count failed: {count.error}
          {:else if !count.done}
            so far…
          {/if}
        </span>
      {/if}
      <span class="marks">
        {#each marks as mark (mark.label)}
          <span class="mark {mark.tone}" title={mark.sentence} aria-label={mark.sentence} role="note">{mark.label}</span>
        {/each}
      </span>
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
      <button
        class="tool"
        title={browser.countRunning
          ? "Stop counting"
          : `Count the objects in ${placeName(browser.countTarget)}: streams the listing page by page and keeps no names`}
        aria-label={browser.countRunning ? "Stop counting" : "Count objects"}
        disabled={browser.containerName === null}
        onclick={() => (browser.countRunning ? void browser.cancelCount() : void browser.startCount())}
      >
        {#if browser.countRunning}
          <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><rect x="4" y="4" width="8" height="8" rx="1" fill="currentColor" /></svg>
        {:else}
          <span class="tool-glyph" aria-hidden="true">#</span>
        {/if}
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

    <!-- Two rows by weight: the transfers, and the deletions on a row of
      their own so they are never next to a transfer the operator meant to
      press. A button whose label follows the
      selection sits in a slot as wide as its widest label, and a button
      that comes and goes has a slot kept for it, so selecting a row moves
      neither the buttons nor the list below them. -->
    <div class="actions" role="group" aria-label="Actions">
      <button
        class="find"
        title="Upload local files to this prefix"
        disabled={browser.containerName === null || browser.backstageFrom !== null}
        onclick={() => (browser.localTransfer = "upload")}
      >
        Upload…
      </button>
      <button
        class="find"
        title="Create a folder here: an empty marker object that keeps it listed"
        disabled={browser.containerName === null || browser.backstageFrom !== null}
        onclick={() => (newFolderOpen = true)}
      >
        New folder…
      </button>
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
      <span class="deletions">
      <!-- One slot for the deletion that fits what is selected (ADR 0125
        decision 7): with rows selected only theirs, so the place being
        browsed is never one click from a deletion of the selection; with
        nothing selected the folder's, or at a container's top the
        container's two (ADR 0121). The slot is as wide as its widest
        content, so a selection moves nothing. -->
      <span class="steady bulk">
        {#if browser.prefix === ""}
          <span class="sizer" aria-hidden="true">Empty container…</span>
          <span class="sizer second" aria-hidden="true">Delete container…</span>
        {:else}
          <span class="sizer" aria-hidden="true">Delete selected…</span>
        {/if}
        <span class="bulk-buttons">
        {#if rowsSelected}
          {#if rowDelete}
            <!-- One selected row's own deletion, as the Delete key and the
              inspector open it, so a single file is deleted from here too. -->
            <button class="find danger" title="Delete only the selected row" onclick={() => openSelectionDelete(browser)}>
              {rowDelete.label}
            </button>
          {:else if browser.chosenRows.length > 1}
            <button
              class="find danger"
              title="Delete the selected objects, and each selected folder with everything in it, as one job"
              onclick={() => browser.openSelectedDelete()}
            >
              Delete selected…
            </button>
          {/if}
        {:else if browser.prefix === "" && browser.selectedPrefix === ""}
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
            title={browser.selectedPrefix
              ? "Delete the selected folder with everything in it"
              : "Delete everything in the folder being browsed"}
            disabled={noContainer}
            onclick={() => (browser.deleteOpen = "prefix")}
          >
            Delete prefix…
          </button>
        {/if}
        </span>
      </span>
      </span>
    </div>

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
        onEmptyPress={() => browser.clearSelection()}
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
        {browser.entriesSkipped > 0
          ? `rows ${(browser.entriesSkipped + 1).toLocaleString("en-US")} to ${(browser.entriesSkipped + browser.entries.length).toLocaleString("en-US")}`
          : `${browser.visibleEntries.length} rows`}{browser.entriesMarker !== "" ? " (more on server)" : ""}
        · {browser.selectionCount === 0 ? "none selected" : `${browser.selectionCount} selected`}
      </span>
      {#if browser.entriesAtCeiling}
        <div class="paging">
          <span class="retention-notice" role="status">
            {browser.entries.length.toLocaleString("en-US")} rows loaded, the retention ceiling; narrow the prefix, use Find, or count the prefix. The ceiling is set in Settings.
          </span>
          {#if browser.entriesMoreError}
            <span class="page-error" role="alert">Could not load the next page: {browser.entriesMoreError}</span>
          {/if}
          {#if browser.entriesSkipped > 0}
            <button class="more to-start" onclick={() => browser.entriesBackToStart()}>← Back to the start</button>
          {/if}
          <button
            class="more next-page"
            disabled={browser.entriesMoreLoading}
            aria-busy={browser.entriesMoreLoading}
            onclick={() => browser.nextEntriesPage().catch(() => {})}
          >
            {browser.entriesMoreLoading ? "Loading…" : `Next ${browser.retentionCeiling.toLocaleString("en-US")} →`}
          </button>
        </div>
      {:else if browser.entriesMarker !== "" || browser.entriesSkipped > 0}
        <div class="paging">
          {#if browser.entriesSkipped > 0}
            <button class="more to-start" onclick={() => browser.entriesBackToStart()}>← Back to the start</button>
          {/if}
          {#if browser.entriesMarker !== ""}
            {#if browser.entriesMoreError}
              <span class="page-error" role="alert">Could not load more objects: {browser.entriesMoreError}</span>
            {/if}
            <button
              class="more load-more"
              disabled={browser.entriesMoreLoading || browser.entriesError !== null}
              aria-busy={browser.entriesMoreLoading}
              onclick={() => browser.loadMoreEntries().catch(() => {})}
            >
              {browser.entriesMoreLoading ? "Loading…" : browser.entriesMoreError ? "Retry load more" : "Load more"}
            </button>
          {/if}
        </div>
      {/if}
    {:else}
      <span>{browser.containerName ? "listing…" : "no container open"}</span>
    {/if}
  </footer>
</section>

{#if newFolderOpen}
  <NewFolderDialog {browser} onClose={() => (newFolderOpen = false)} />
{/if}

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
    /* Gives way long before the path does, but keeps its number in sight;
      the whole sentence is its hover text. */
    flex: 0 1000 auto;
    min-width: 4.5rem;
    overflow: hidden;
    text-overflow: ellipsis;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    white-space: nowrap;
  }

  .retention-notice {
    padding: var(--space-1) var(--space-2);
    color: var(--color-text-faint);
    border: 1px dashed var(--color-border-strong);
    border-radius: var(--radius-control);
  }

  /* The marks sit at the row's end and are never cut: "Public" must be
    read whatever the width. */
  .marks {
    display: flex;
    flex: none;
    gap: var(--space-1);
    margin-left: auto;
  }

  .mark {
    flex: none;
    padding: 0 var(--space-1);
    font-size: var(--text-xs);
    line-height: 1.4rem;
    white-space: nowrap;
    border: 1px solid;
    border-radius: var(--radius-control);
    cursor: help;
  }

  .mark.danger {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-color: var(--color-danger);
  }

  .mark.warning {
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-color: var(--color-warning);
  }

  .mark.quiet {
    color: var(--color-text-faint);
    border-style: dashed;
    border-color: var(--color-border-strong);
  }

  .path-row {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  /* One line whatever the path, so a long name or a deep folder never
    makes the head taller: the path gives way first, each name ending in an
    ellipsis, then the count's text; the marks never do. */
  .breadcrumb {
    display: flex;
    align-items: center;
    gap: var(--space-1);
    min-height: 1.8rem;
    flex: 0 1 auto;
    min-width: 3rem;
    overflow: hidden;
  }

  .tool-glyph {
    font-size: var(--text-sm);
    font-weight: var(--weight-semibold);
    line-height: 1;
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
    flex: 0 1 auto;
    min-width: 1.5rem;
    overflow: hidden;
    text-overflow: ellipsis;
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
    flex: 0 1 auto;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    white-space: pre;
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

  /* A row of its own, at the left like the rows above it (ADR 0123
    decision 4). The container's or the folder's deletion comes first, so
    the slot kept for the selection's deletion, often empty, trails it. */
  .actions .deletions {
    display: flex;
    flex-wrap: wrap;
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

  /* Two labels wide, with the buttons at its left. */
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
    gap: var(--space-2);
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
