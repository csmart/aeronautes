<!-- The four-pane primary shell (§4.3, §5.1), live: bookmark → auth →
  containers → objects → selection-triggered inspector HEAD — the v0.1
  vertical proof. -->
<script lang="ts">
  import { onMount } from "svelte";
  import { liveApi, type BookmarkDTO } from "../../lib/api";
  import BookmarkRail from "./BookmarkRail.svelte";
  import BookmarkEditor from "./BookmarkEditor.svelte";
  import ContainerColumn from "./ContainerColumn.svelte";
  import CreateContainerDialog from "./CreateContainerDialog.svelte";
  import WorkingList from "./WorkingList.svelte";
  import Inspector from "../inspector/Inspector.svelte";
  import JobsDrawer from "../jobs/JobsDrawer.svelte";
  import TransferDialog from "../transfer/TransferDialog.svelte";
  import LocalTransferDialog from "../transfer/LocalTransferDialog.svelte";
  import AboutDialog from "./AboutDialog.svelte";
  import CredentialStatusDialog from "./CredentialStatusDialog.svelte";
  import DeleteDialog from "../transfer/DeleteDialog.svelte";
  import { BrowserState } from "./browserState.svelte";

  const browser = new BrowserState(liveApi);

  let workingList: WorkingList | undefined = $state();
  let editorOpen = $state(false);
  let editorExisting = $state<BookmarkDTO | null>(null);
  let createOpen = $state(false);
  let shellInfo = $state("connecting…");
  let aboutOpen = $state(false);
  let credentialStatusOpen = $state(false);

  let cleanupLabel = $derived.by(() => {
    if (browser.credentialStatusLoading) return "key cleanup: checking…";
    if (browser.credentialStatusError || !browser.credentialStatus) return "key cleanup: unknown";
    const pending = browser.credentialStatus.retired + browser.credentialStatus.unfinished;
    if (pending > 0) return `key cleanup: ${pending} pending`;
    if (browser.credentialStatus.saving > 0) return "key save in progress";
    return "key cleanup";
  });

  onMount(async () => {
    void browser.loadCredentialStatus();
    try {
      const info = await liveApi.info();
      shellInfo = `shell ${info.version} · Wails ${info.wails} · secrets: ${info.secretBackend}`;
      browser.secretBackend = info.secretBackend;
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
    if (
      editorOpen ||
      createOpen ||
      aboutOpen ||
      credentialStatusOpen ||
      browser.transferOpen ||
      browser.localTransfer !== null ||
      browser.deleteOpen
    ) {
      if (event.key === "Escape") {
        editorOpen = false;
        createOpen = false;
        aboutOpen = false;
        credentialStatusOpen = false;
        browser.transferOpen = false;
        browser.localTransfer = null;
        browser.deleteOpen = false;
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
    } else if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      // Arrows anywhere outside a field drive the working list (§4.4).
      workingList?.focusList();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="screen">
  <BookmarkRail
    {browser}
    onAddBookmark={() => {
      editorExisting = null;
      editorOpen = true;
    }}
    onEditBookmark={(bookmark) => {
      editorExisting = bookmark;
      editorOpen = true;
    }}
  />
  <ContainerColumn {browser} onCreate={() => (createOpen = true)} />
  <WorkingList bind:this={workingList} {browser} />
  <Inspector {browser} />

  <div class="drawer-slot">
    <JobsDrawer {browser} />
  </div>

  <footer class="status num">
    <span>
      {#if browser.connectError}
        <span class="connect-error">connect failed: {browser.connectError}</span>
      {:else}
        {browser.account ? `connected · ${browser.account.name}` : "not connected"}
      {/if}
    </span>
    <button class="about-link" onclick={() => (credentialStatusOpen = true)}>{cleanupLabel}</button>
    <button class="about-link" onclick={() => (aboutOpen = true)}>{shellInfo}</button>
  </footer>
</div>

{#if editorOpen}
  <BookmarkEditor {browser} existing={editorExisting} onClose={() => (editorOpen = false)} />
{/if}
{#if createOpen}
  <CreateContainerDialog {browser} onClose={() => (createOpen = false)} />
{/if}

{#if browser.transferOpen}
  <TransferDialog {browser} onClose={() => (browser.transferOpen = false)} />
{/if}

{#if browser.localTransfer !== null}
  <LocalTransferDialog
    {browser}
    mode={browser.localTransfer}
    onClose={() => (browser.localTransfer = null)}
  />
{/if}

{#if browser.deleteOpen}
  <DeleteDialog {browser} onClose={() => (browser.deleteOpen = false)} />
{/if}

{#if aboutOpen}
  <AboutDialog onClose={() => (aboutOpen = false)} />
{/if}

{#if credentialStatusOpen}
  <CredentialStatusDialog {browser} onClose={() => (credentialStatusOpen = false)} />
{/if}

<style>
  .screen {
    height: 100%;
    display: grid;
    grid-template-columns: 5.4rem 17rem minmax(24rem, 1fr) minmax(19rem, 22rem);
    grid-template-rows: minmax(0, 1fr) auto auto;
  }

  /* §65.8 item 7: the four panes track narrower windows instead of
     jamming — tighter columns first, then the rail and side panes give
     up more ground. Below that the working list stays usable and the
     page body itself should not scroll horizontally; wide content
     scrolls inside its own pane. */
  @media (max-width: 1100px) {
    .screen {
      grid-template-columns: 4.6rem 13rem minmax(19rem, 1fr) minmax(15rem, 18rem);
    }
  }

  @media (max-width: 920px) {
    .screen {
      grid-template-columns: 4.2rem 10.5rem minmax(16rem, 1fr) minmax(13rem, 15rem);
    }
  }

  .screen > :global(*) {
    grid-row: 1;
  }

  /* Declared after the catch-all so the tie-break lands here. */
  .about-link {
    font: inherit;
    background: none;
    border: none;
    padding: 0;
    color: inherit;
    cursor: pointer;
  }

  .about-link:hover {
    color: var(--color-text);
  }

  .drawer-slot {
    grid-row: 2;
    grid-column: 1 / -1;
  }

  .connect-error {
    color: var(--color-danger);
  }

  .status {
    grid-row: 3;
    grid-column: 1 / -1;
    display: flex;
    justify-content: space-between;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-3);
    padding: var(--space-1) var(--space-4);
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    background: var(--color-bg);
    border-top: 1px solid var(--color-border);
  }
</style>
