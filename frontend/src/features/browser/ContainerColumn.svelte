<script lang="ts">
  import type { BrowserState, ContainerMode } from "./browserState.svelte";
  import type { ContainerRowDTO, ContainerSettingsDTO } from "../../lib/api";
  import { formatBytes, formatCount } from "../../lib/format";

  let { browser, onCreate }: { browser: BrowserState; onCreate: () => void } = $props();

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
</script>

{#snippet badge(label: string, tone: string, detail: string)}
  <span class="setting-badge {tone}" title={detail}>{label}</span>
{/snippet}

{#snippet settingsBadges(settings: ContainerSettingsDTO)}
  <!-- Every badge is a positive fact one HEAD reported (ADR 0071); the
       tooltip carries the exact header value and when it was observed. -->
  {@const observed = `observed ${settings.observedAt}`}
  {#if settings.publicRead}
    {@render badge(settings.publicListing ? "public + listing" : "public", "danger", `read ACL ${settings.readAcl} · ${observed}`)}
  {/if}
  {#if (settings.referrerGrants?.length ?? 0) > 0}
    {@render badge("referrer", "warning", `read ACL ${settings.readAcl} · ${observed}`)}
  {/if}
  {#if settings.sharedWrite}
    {@render badge("shared write", "warning", `write ACL ${settings.writeAcl} · ${observed}`)}
  {/if}
  {#if settings.staticWeb}
    {@render badge("web", "neutral", `static web configured · ${observed}`)}
  {/if}
  {#if settings.versioningMode !== "off" && settings.versioningMode !== ""}
    {@render badge("versioned", "neutral", `versioning ${settings.versioningMode}${settings.versioningArchive ? ` → ${settings.versioningArchive}` : ""} · ${observed}`)}
  {/if}
  {#if settings.quota}
    {@render badge("quota", "neutral", `quota set · ${observed}`)}
  {/if}
  {#if settings.cors}
    {@render badge("cors", "neutral", `CORS configured · ${observed}`)}
  {/if}
{/snippet}

{#snippet containerRow(container: ContainerRowDTO)}
  <button
    class="container-row"
    class:selected={browser.containerName === container.name}
    onclick={() => browser.openContainer(container.name)}
  >
    <span class="container-name">
      {container.name}
      {#if browser.mode === "backstage" && container.class !== "normal"}
        <span class="class-chip">{container.class}</span>
      {/if}
      {#if container.settings}
        {@render settingsBadges(container.settings)}
      {:else if browser.settingsErrors[container.name]}
        <span class="setting-badge muted" title={`settings could not be checked: ${browser.settingsErrors[container.name]}`}>unchecked</span>
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
        <button class="create" title="Create container" onclick={onCreate}>+</button>
        {#if browser.settingsCheck?.running}
          <button
            class="check"
            title="Stop checking settings"
            onclick={() => browser.stopSettingsCheck()}
          >
            Stop
          </button>
        {:else}
          <button
            class="check"
            title="HEAD every loaded container once to show its access and feature settings"
            disabled={browser.account === null || browser.containers.length === 0}
            onclick={() => browser.checkSettings()}
          >
            Check settings
          </button>
        {/if}
      {/if}
    </div>
    {#if browser.settingsCheck}
      <p class="check-status" role="status">
        checked {browser.settingsCheck.checked} of {browser.settingsCheck.total}{browser.settingsCheck.running ? "…" : browser.settingsCheck.stopped ? " (stopped)" : ""}
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
  </header>

  <div class="rows" role="tabpanel" id={panelId} aria-labelledby={tabId(browser.mode)}>
    {#if !browser.account}
      <p class="disconnected">Not connected.</p>
    {:else if browser.containersLoading}
      <p class="disconnected" role="status">Listing…</p>
    {:else if browser.containersError}
      <p class="error">{browser.containersError}</p>
    {:else}
      {#each browser.containerGroups.main as container (container.name)}
        {@render containerRow(container)}
      {/each}

      {#if browser.containerGroups.suspected.length > 0}
        <h3 class="group-label">
          Possible backstage · {browser.containerGroups.suspected.length}
        </h3>
        {#each browser.containerGroups.suspected as container (container.name)}
          {@render containerRow(container)}
        {/each}
      {/if}

      {#if browser.containersAtCeiling}
        <p class="retention-notice" role="status">
          {browser.containers.length.toLocaleString("en-US")} containers loaded, the retention ceiling; further containers are not loaded. The ceiling is set in the jobs drawer.
        </p>
      {:else if browser.containersMarker !== ""}
        {#if browser.containersMoreError}
          <p class="error" role="alert">Could not load more containers: {browser.containersMoreError}</p>
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

<style>
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

  .create {
    width: 1.6rem;
    height: 1.6rem;
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

  .check {
    margin-left: var(--space-1);
    padding: 0 var(--space-2);
    font: inherit;
    font-size: var(--text-xs);
    color: var(--color-accent-strong);
    background: none;
    border: 1px solid var(--color-border-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .check:disabled {
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
