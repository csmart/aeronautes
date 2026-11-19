<script lang="ts">
  import type { BrowserState, ContainerMode } from "./browserState.svelte";
  import type { ContainerRowDTO, ContainerSettingsDTO } from "../../lib/api";
  import { formatBytes, formatCount } from "../../lib/format";
  import ContextMenu, { type MenuItem } from "../../components/ContextMenu.svelte";

  let { browser, onCreate }: { browser: BrowserState; onCreate: () => void } = $props();

  // A container row's menu opens the container and then the toolbar's own
  // action for it, under the toolbar's conditions; an action waits for the
  // open and runs only if that container is the one now open.
  let menu: { x: number; y: number; name: string } | null = $state(null);
  let menuFrom: HTMLElement | null = null;

  // Containers that are only named like segment stores stay in the list,
  // since the name is a guess, but behind one press, so that nobody walks
  // into them by accident (ADR 0117 decision 1).
  let possibleBackstageOpen = $state(false);

  async function inContainer(name: string, run: () => void) {
    await browser.openContainer(name);
    if (browser.containerName === name) run();
  }

  function menuItems(name: string): MenuItem[] {
    return [
      { label: "Open", run: () => void browser.openContainer(name) },
      { label: "Upload…", run: () => void inContainer(name, () => (browser.localTransfer = "upload")) },
      { label: "Download container…", run: () => void inContainer(name, () => (browser.localTransfer = "download")) },
      { label: "Copy container…", run: () => void inContainer(name, () => (browser.transferOpen = true)) },
      { label: "Copy name", run: () => void browser.copyText(name) },
      // Two actions, never one that chooses between them inside its dialog:
      // deleting also destroys the container's settings (ADR 0121).
      { label: "Empty container…", danger: true, run: () => void inContainer(name, () => (browser.deleteOpen = "empty")) },
      { label: "Delete container…", danger: true, run: () => void inContainer(name, () => (browser.deleteOpen = "delete_container")) },
    ];
  }

  function openMenuAt(event: MouseEvent, name: string) {
    event.preventDefault();
    menuFrom = event.currentTarget as HTMLElement;
    menu = { x: event.clientX, y: event.clientY, name };
  }

  function menuKey(event: KeyboardEvent, name: string) {
    if (event.key !== "ContextMenu" && !(event.shiftKey && event.key === "F10")) return;
    event.preventDefault();
    menuFrom = event.currentTarget as HTMLElement;
    const box = menuFrom.getBoundingClientRect();
    menu = { x: box.left + 24, y: box.bottom, name };
  }

  const modes: { id: ContainerMode; label: string }[] = [
    { id: "logical", label: "Logical" },
    { id: "all", label: "All" },
    { id: "backstage", label: "Backstage" },
  ];

  // The mode buttons are one tablist over the container list below them.
  // The selected tab is its only tab stop, and the horizontal arrows and
  // Home/End move focus and select the mode they land on (a mode is a local
  // regrouping of loaded containers, so selecting on focus costs nothing).
  // Exactly those four keys are consumed: the window-level browser handler
  // returns on a prevented event, so they never move the working list,
  // while ArrowUp/ArrowDown keep their §4.4 owner.
  const id = $props.id();
  const panelId = `${id}-panel`;
  const tabId = (mode: ContainerMode) => `${id}-tab-${mode}`;
  let tabButtons: HTMLButtonElement[] = $state([]);
  // The focus moved by a key must stay visible: script focus does not
  // inherit the keyboard focus indication on every engine, and the DOM
  // typings do not yet name the option that requests it.
  const visibleFocus: FocusOptions & { focusVisible?: boolean } = { focusVisible: true };

  function onTabKeydown(event: KeyboardEvent, index: number) {
    let next: number;
    switch (event.key) {
      case "ArrowRight": next = (index + 1) % modes.length; break;
      case "ArrowLeft": next = (index - 1 + modes.length) % modes.length; break;
      case "Home": next = 0; break;
      case "End": next = modes.length - 1; break;
      default: return;
    }
    event.preventDefault();
    browser.mode = modes[next].id;
    tabButtons[next]?.focus(visibleFocus);
  }

  // Rows report entering and leaving the visible area, so the settings of
  // the rows on screen can be read (ADR 0110 decision 2). One observer
  // watches every row, since a page can hold thousands; with no root it
  // still accounts for the list's own scrolling. A document without
  // IntersectionObserver reports nothing.
  let observer: IntersectionObserver | null = null;
  const rowNames = new WeakMap<Element, string>();
  function visibility(): IntersectionObserver | null {
    if (observer || typeof IntersectionObserver === "undefined") return observer;
    observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        const name = rowNames.get(entry.target);
        if (name !== undefined) browser.containerOnScreen(name, entry.isIntersecting);
      }
    });
    return observer;
  }
  function onScreen(node: HTMLElement, name: string) {
    rowNames.set(node, name);
    visibility()?.observe(node);
    return {
      update(next: string) {
        rowNames.set(node, next);
      },
      destroy() {
        observer?.unobserve(node);
        browser.containerOnScreen(rowNames.get(node) ?? name, false);
      },
    };
  }
</script>

{#snippet badge(label: string, tone: string, detail: string)}
  <span class="setting-badge {tone}" title={detail}>{label}</span>
{/snippet}

{#snippet settingsBadges(settings: ContainerSettingsDTO, remembered: boolean)}
  <!-- Every badge is a positive fact one HEAD reported (ADR 0071); the
       tooltip carries the exact header value and when it was observed.
       Remembered settings from an earlier connection are muted and say so
       (ADR 0110 decision 4). -->
  {@const observed = remembered
    ? `last seen ${settings.observedAt}, not read under this connection`
    : `observed ${settings.observedAt}`}
  {#if settings.publicRead}
    {@render badge(settings.publicListing ? "public + listing" : "public", remembered ? "muted" : "danger", `read ACL ${settings.readAcl} · ${observed}`)}
  {/if}
  {#if (settings.referrerGrants?.length ?? 0) > 0}
    {@render badge("referrer", remembered ? "muted" : "warning", `read ACL ${settings.readAcl} · ${observed}`)}
  {/if}
  {#if settings.sharedWrite}
    {@render badge("shared write", remembered ? "muted" : "warning", `write ACL ${settings.writeAcl} · ${observed}`)}
  {/if}
  {#if settings.staticWeb}
    {@render badge("web", remembered ? "muted" : "neutral", `static web configured · ${observed}`)}
  {/if}
  {#if settings.versioningMode !== "off" && settings.versioningMode !== ""}
    {@render badge("versioned", remembered ? "muted" : "neutral", `versioning ${settings.versioningMode}${settings.versioningArchive ? ` → ${settings.versioningArchive}` : ""} · ${observed}`)}
  {/if}
  {#if settings.quota}
    {@render badge("quota", remembered ? "muted" : "neutral", `quota set · ${observed}`)}
  {/if}
  {#if settings.cors}
    {@render badge("cors", remembered ? "muted" : "neutral", `CORS configured · ${observed}`)}
  {/if}
{/snippet}

{#snippet containerRow(container: ContainerRowDTO, subdued: boolean)}
  <button
    class="container-row"
    class:subdued
    class:selected={browser.containerName === container.name}
    onclick={() => browser.openContainer(container.name)}
    oncontextmenu={(event) => openMenuAt(event, container.name)}
    onkeydown={(event) => menuKey(event, container.name)}
    use:onScreen={container.name}
  >
    <span class="container-name">
      {container.name}
      {#if browser.mode === "backstage" && container.class !== "normal"}
        <span class="class-chip">{container.class}</span>
      {/if}
      {#if container.settings}
        {@render settingsBadges(container.settings, false)}
      {:else}
        {#if container.lastSeen}
          {@render settingsBadges(container.lastSeen, true)}
        {/if}
        {#if browser.settingsErrors[container.name]}
          <span class="setting-badge muted" title={`settings could not be read: ${browser.settingsErrors[container.name]}`}>not read</span>
        {/if}
      {/if}
    </span>
    <span class="container-detail num">
      {formatBytes(container.bytes)} · {formatCount(container.count)} objects
    </span>
  </button>
{/snippet}

<section class="pane">
  <header class="pane-head">
    <div class="title-row">
      <h2 class="pane-title">Containers</h2>
      {#if browser.account}
        <!-- Three small actions, each named by its hover text: the words
          did not fit the column's width beside its title. -->
        <div class="tools">
          <button class="tool" title="Create container" onclick={onCreate}>+</button>
          <button
            class="tool"
            title="Refresh: re-read the container list from the server"
            aria-label="Refresh containers"
            aria-busy={browser.containersLoading}
            disabled={browser.containersLoading}
            onclick={() => browser.loadContainers()}
          >
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
              <path d="M13.25 8A5.25 5.25 0 1 1 11.71 4.29M11.71 1.79V4.29H9.21" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>
          {#if browser.settingsCheck?.running}
            <button class="tool" title="Stop the scan" aria-label="Stop the scan" onclick={() => browser.stopSettingsCheck()}>
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
                <rect x="4" y="4" width="8" height="8" rx="1" fill="currentColor" />
              </svg>
            </button>
          {:else}
            <button
              class="tool"
              title="Scan: read every loaded container's access and feature settings, one request each"
              aria-label="Scan containers"
              disabled={browser.account === null || browser.containers.length === 0}
              onclick={() => browser.checkSettings()}
            >
              <!-- A tag: what a scan adds to each row. -->
              <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
                <path d="M2.5 2.5h5l6 6-5 5-6-6z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" />
                <circle cx="5.4" cy="5.4" r="1" fill="currentColor" />
              </svg>
            </button>
          {/if}
        </div>
      {/if}
    </div>
    {#if browser.settingsCheck}
      <p class="check-status" role="status">
        scanned {browser.settingsCheck.checked} of {browser.settingsCheck.total}{browser.settingsCheck.running ? "…" : browser.settingsCheck.stopped ? " (stopped)" : ""}
      </p>
    {/if}
    <div class="modes" role="tablist" aria-label="Container mode">
      {#each modes as mode, index (mode.id)}
        <button
          role="tab"
          id={tabId(mode.id)}
          class="mode"
          aria-selected={browser.mode === mode.id}
          aria-controls={panelId}
          tabindex={browser.mode === mode.id ? 0 : -1}
          bind:this={tabButtons[index]}
          onclick={() => (browser.mode = mode.id)}
          onkeydown={(event) => onTabKeydown(event, index)}
        >
          {mode.label}
        </button>
      {/each}
    </div>
    <input
      class="container-filter"
      type="search"
      bind:value={browser.containerFilter}
      placeholder="filter containers…"
      aria-label="Filter containers"
      disabled={!browser.account}
    />
  </header>

  <div class="rows" role="tabpanel" id={panelId} aria-labelledby={tabId(browser.mode)}>
    {#if browser.connecting}
      <!-- Switching bookmarks clears the old account before the new one
        answers; without this the column read "Not connected" meanwhile. -->
      <p class="disconnected" role="status">Connecting to {browser.connectingName}…</p>
    {:else if !browser.account}
      <p class="disconnected">Not connected.</p>
    {:else if browser.containersLoading && browser.containers.length === 0}
      <!-- A re-read keeps the rows it is about to replace on screen; only a
        list with nothing to show says it is listing. -->
      <p class="disconnected" role="status">Listing…</p>
    {:else if browser.containersError}
      <p class="error callout danger">{browser.containersError}</p>
    {:else}
      {#each browser.containerGroups.main as container (container.name)}
        {@render containerRow(container, false)}
      {/each}
      {#if browser.containerFilter.trim() !== "" && browser.containerGroups.main.length === 0 && browser.containerGroups.suspected.length === 0}
        <p class="disconnected" role="status">
          {#if browser.containerGroups.hiddenBackstage > 0}
            <!-- Segment containers are loaded but this view hides them. -->
            Only segment containers hidden in this view hold "{browser.containerFilter.trim()}"; the All and Backstage views show them.
          {:else}
            No loaded container's name holds "{browser.containerFilter.trim()}"{browser.containersMarker !== "" ? "; more containers can be loaded below" : ""}.
          {/if}
        </p>
      {/if}

      {#if browser.containerGroups.suspected.length > 0}
        <h3 class="group-label">
          <button
            class="group-toggle"
            aria-expanded={possibleBackstageOpen}
            onclick={() => (possibleBackstageOpen = !possibleBackstageOpen)}
          >
            <span aria-hidden="true">{possibleBackstageOpen ? "▾" : "▸"}</span>
            Possible backstage · {browser.containerGroups.suspected.length}
          </button>
        </h3>
        {#if possibleBackstageOpen}
          {#each browser.containerGroups.suspected as container (container.name)}
            {@render containerRow(container, true)}
          {/each}
        {/if}
      {/if}

      {#if browser.containersAtCeiling}
        <p class="retention-notice" role="status">
          {browser.containers.length.toLocaleString("en-US")} containers loaded, the retention ceiling; further containers are not loaded. The ceiling is set in Settings.
        </p>
      {:else if browser.containersMarker !== ""}
        {#if browser.containersMoreError}
          <p class="error callout danger" role="alert">Could not load more containers: {browser.containersMoreError}</p>
        {/if}
        <button
          class="load-more"
          disabled={browser.containersMoreLoading}
          aria-busy={browser.containersMoreLoading}
          onclick={() => browser.loadMoreContainers().catch(() => {})}
        >
          {browser.containersMoreLoading ? "Loading…" : browser.containersMoreError ? "Retry load more" : "Load more…"}
        </button>
      {/if}
    {/if}
  </div>

  {#if browser.account && browser.mode === "logical" && (browser.containerGroups.hiddenBackstage > 0 || browser.containerGroups.suspected.length > 0)}
    <footer class="pane-foot">
      {#if browser.containerGroups.hiddenBackstage > 0}
        <div>{browser.containerGroups.hiddenBackstage} confirmed segment containers hidden</div>
      {/if}
      {#if browser.containerGroups.suspected.length > 0}
        <div>{browser.containerGroups.suspected.length} possible backstage containers</div>
      {/if}
    </footer>
  {/if}
</section>

{#if menu}
  <ContextMenu
    x={menu.x}
    y={menu.y}
    label="Actions for container {menu.name}"
    items={menuItems(menu.name)}
    onClose={() => {
      menu = null;
      menuFrom?.focus();
    }}
  />
{/if}

<style>
  .container-filter {
    width: 100%;
    margin-top: var(--space-2);
    padding: 2px var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  .pane {
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--color-bg-pane);
    border-right: 1px solid var(--color-border);
  }

  .pane-head {
    padding: var(--space-3) var(--space-3) var(--space-2);
  }

  .title-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: var(--space-2);
  }

  .pane-title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: var(--weight-semibold);
  }

  .tools {
    display: flex;
    gap: var(--space-1);
  }

  .tool {
    display: grid;
    place-items: center;
    width: 1.6rem;
    height: 1.6rem;
    padding: 0;
    font: inherit;
    font-size: var(--text-lg);
    line-height: 1;
    color: var(--color-accent-strong);
    background: var(--color-bg-card);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .modes {
    display: inline-flex;
    padding: 2px;
    background: var(--color-bg-inset);
    border-radius: var(--radius-control);
  }

  .mode {
    padding: 0.15rem var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    background: none;
    border: none;
    border-radius: 5px;
    cursor: pointer;
    transition: all var(--motion-fast) var(--ease-out);
  }

  .mode[aria-selected="true"] {
    color: var(--color-text);
    background: var(--color-bg-card);
    box-shadow: var(--shadow-card);
  }

  /* Activation follows focus, so the focused tab is always the selected
     one, whose card shadow would otherwise replace the global focus ring. */
  .mode[aria-selected="true"]:focus-visible {
    box-shadow: var(--shadow-card), var(--focus-ring);
  }

  .rows {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0 var(--space-2) var(--space-2);
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .disconnected,
  .error {
    margin: var(--space-3) var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-text-faint);
  }

  .error {
    color: var(--color-danger);
    overflow-wrap: anywhere;
  }

  .container-row {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 1px;
    padding: var(--space-2) var(--space-2);
    font: inherit;
    text-align: left;
    color: var(--color-text);
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    cursor: pointer;
    transition:
      background-color var(--motion-fast) var(--ease-out),
      border-color var(--motion-fast) var(--ease-out);
  }

  .container-row:hover {
    background: var(--color-bg-card);
  }

  .container-row.selected {
    background: var(--color-selection-bg);
    border-color: var(--color-selection-border);
  }

  .container-row.subdued .container-name {
    color: var(--color-text-secondary);
  }

  .container-name {
    font-weight: var(--weight-medium);
    overflow-wrap: anywhere;
  }

  .container-detail {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .class-chip {
    margin-left: var(--space-1);
    padding: 0 var(--space-1);
    font-size: var(--text-xs);
    font-weight: var(--weight-semibold);
    color: var(--color-accent-strong);
    background: var(--color-accent-soft);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-pill);
  }

  .setting-badge {
    margin-left: var(--space-1);
    padding: 0 var(--space-1);
    font-size: var(--text-xs);
    font-weight: var(--weight-semibold);
    border-radius: var(--radius-pill);
    border: 1px solid var(--color-border-strong);
    color: var(--color-text-faint);
    background: none;
    white-space: nowrap;
  }

  .setting-badge.danger {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-color: var(--color-danger);
  }

  .setting-badge.warning {
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-color: var(--color-warning);
  }

  .setting-badge.muted {
    font-weight: normal;
    border-style: dashed;
  }

  .tool:disabled {
    cursor: default;
    opacity: 0.5;
  }

  .check-status {
    margin: 0 0 var(--space-1);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .group-label {
    margin: var(--space-3) 0 var(--space-1);
    padding: 0 var(--space-2);
    font-size: var(--text-xs);
    font-weight: var(--weight-semibold);
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--color-text-faint);
  }

  .group-toggle {
    padding: 0;
    font: inherit;
    letter-spacing: inherit;
    text-transform: inherit;
    color: inherit;
    background: none;
    border: 0;
    cursor: pointer;
  }

  .load-more {
    margin-top: var(--space-2);
    padding: var(--space-1);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-accent-strong);
    background: none;
    border: 1px dashed var(--color-border-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .load-more:disabled {
    cursor: wait;
    opacity: 0.6;
  }

  .retention-notice {
    margin: var(--space-2) 0 0;
    padding: var(--space-1);
    font-size: var(--text-sm);
    color: var(--color-text-faint);
    border: 1px dashed var(--color-border-strong);
    border-radius: var(--radius-control);
  }

  .pane-foot {
    padding: var(--space-2) var(--space-3);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    border-top: 1px solid var(--color-border);
  }
</style>
