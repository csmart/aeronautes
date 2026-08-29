<script lang="ts">
  import type { BrowserState, ContainerMode } from "./browserState.svelte";
  import type { ContainerRowDTO } from "../../lib/api";
  import { formatBytes, formatCount } from "../../lib/format";

  let { browser, onCreate }: { browser: BrowserState; onCreate: () => void } = $props();

  const modes: { id: ContainerMode; label: string }[] = [
    { id: "logical", label: "Logical" },
    { id: "all", label: "All" },
    { id: "backstage", label: "Backstage" },
  ];
</script>

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
      {/if}
    </div>
    <div class="modes" role="tablist" aria-label="Container mode">
      {#each modes as mode (mode.id)}
        <button
          role="tab"
          class="mode"
          aria-selected={browser.mode === mode.id}
          onclick={() => (browser.mode = mode.id)}
        >
          {mode.label}
        </button>
      {/each}
    </div>
  </header>

  <div class="rows">
    {#if !browser.account}
      <p class="disconnected">Not connected.</p>
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

      {#if browser.containersMarker !== ""}
        <button class="load-more" onclick={() => browser.loadMoreContainers()}>
          Load more…
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

  .pane-foot {
    padding: var(--space-2) var(--space-3);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    border-top: 1px solid var(--color-border);
  }
</style>
