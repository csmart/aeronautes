<!-- The four-pane primary shell (§4.3, §5.1), live: bookmark → auth →
  containers → objects → selection-triggered inspector HEAD — the v0.1
  vertical proof (§65 commit 13). -->
<script lang="ts">
  import { onMount } from "svelte";
  import { liveApi } from "../../lib/api";
  import BookmarkRail from "./BookmarkRail.svelte";
  import BookmarkEditor from "./BookmarkEditor.svelte";
  import ContainerColumn from "./ContainerColumn.svelte";
  import WorkingList from "./WorkingList.svelte";
  import Inspector from "../inspector/Inspector.svelte";
  import { BrowserState } from "./browserState.svelte";

  const browser = new BrowserState(liveApi);

  let workingList: WorkingList | undefined = $state();
  let editorOpen = $state(false);
  let shellInfo = $state("connecting…");

  onMount(async () => {
    try {
      const info = await liveApi.info();
      shellInfo = `shell ${info.version} · Wails ${info.wails}`;
    } catch {
      shellInfo = "service bridge unavailable";
    }
    try {
      await browser.loadBookmarks();
    } catch {
      // Surfaced through the empty states; nothing to do here.
    }
  });

  function isEditing(target: EventTarget | null): boolean {
    return (
      target instanceof HTMLInputElement ||
      target instanceof HTMLTextAreaElement ||
      (target instanceof HTMLElement && target.isContentEditable)
    );
  }

  // §4.4: Backspace or Alt+↑ goes to the parent prefix; "/" focuses the
  // server prefix field. Never while typing in a field.
  function handleKeydown(event: KeyboardEvent) {
    if (editorOpen) {
      if (event.key === "Escape") {
        editorOpen = false;
      }
      return;
    }
    if (isEditing(event.target)) {
      return;
    }
    if (event.key === "Backspace" || (event.altKey && event.key === "ArrowUp")) {
      event.preventDefault();
      browser.up();
    } else if (event.key === "/") {
      event.preventDefault();
      workingList?.focusPrefixField();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="screen">
  <BookmarkRail {browser} onAddBookmark={() => (editorOpen = true)} />
  <ContainerColumn {browser} />
  <WorkingList bind:this={workingList} {browser} />
  <Inspector {browser} />

  <div class="drawer num">
    <span>
      {#if browser.connectError}
        <span class="connect-error">connect failed: {browser.connectError}</span>
      {:else}
        No active transfers
      {/if}
    </span>
    <span class="drawer-hint">transfer &amp; job drawer · Phase 5</span>
  </div>

  <footer class="status num">
    <span>
      {browser.account ? `connected · ${browser.account.name}` : "not connected"}
    </span>
    <span>{shellInfo}</span>
  </footer>
</div>

{#if editorOpen}
  <BookmarkEditor {browser} onClose={() => (editorOpen = false)} />
{/if}

<style>
  .screen {
    height: 100%;
    display: grid;
    grid-template-columns: 5.4rem 17rem minmax(24rem, 1fr) minmax(19rem, 22rem);
    grid-template-rows: minmax(0, 1fr) auto auto;
  }

  .screen > :global(*) {
    grid-row: 1;
  }

  .drawer {
    grid-row: 2;
    grid-column: 1 / -1;
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--space-2) var(--space-4);
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    background: var(--color-bg-pane);
    border-top: 1px solid var(--color-border);
  }

  .connect-error {
    color: var(--color-danger);
  }

  .drawer-hint {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .status {
    grid-row: 3;
    grid-column: 1 / -1;
    display: flex;
    justify-content: space-between;
    padding: var(--space-1) var(--space-4);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    background: var(--color-bg);
    border-top: 1px solid var(--color-border);
  }
</style>
