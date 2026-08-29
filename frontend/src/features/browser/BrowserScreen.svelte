<!-- The four-pane primary shell (§4.3, §5.1): bookmark rail, container
  column, working list, always-visible inspector; transfer drawer and
  status footer beneath. Static against the §58 fixtures until the live
  wiring commit. -->
<script lang="ts">
  import { onMount } from "svelte";
  import { AppService } from "../../../bindings/github.com/csmart/aeronautes/internal/app";
  import BookmarkRail from "./BookmarkRail.svelte";
  import ContainerColumn from "./ContainerColumn.svelte";
  import WorkingList from "./WorkingList.svelte";
  import Inspector from "../inspector/Inspector.svelte";
  import { BrowserState } from "./browserState.svelte";

  const browser = new BrowserState();

  let workingList: WorkingList | undefined = $state();
  let shellInfo = $state("connecting…");

  onMount(async () => {
    try {
      const info = await AppService.Info();
      shellInfo = `shell ${info.version} · Wails ${info.wails}`;
    } catch {
      shellInfo = "service bridge unavailable";
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
  <BookmarkRail />
  <ContainerColumn {browser} />
  <WorkingList bind:this={workingList} {browser} />
  <Inspector target={browser.inspectorTarget} account={browser.account} />

  <div class="drawer num">
    <span>No active transfers</span>
    <span class="drawer-hint">transfer &amp; job drawer · Phase 5</span>
  </div>

  <footer class="status num">
    <span>static §58 fixture namespace</span>
    <span>{shellInfo}</span>
  </footer>
</div>

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
