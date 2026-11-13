<!-- The four-pane primary shell (§4.3, §5.1), live: bookmark → auth →
  containers → objects → selection-triggered inspector HEAD — the v0.1
  vertical proof. -->
<script lang="ts">
  import { onMount, tick } from "svelte";
  import { liveApi, type BookmarkDTO } from "../../lib/api";
  import BookmarkRail from "./BookmarkRail.svelte";
  import BookmarkEditor from "./BookmarkEditor.svelte";
  import WelcomeDialog from "./WelcomeDialog.svelte";
  import ContainerColumn from "./ContainerColumn.svelte";
  import CreateContainerDialog from "./CreateContainerDialog.svelte";
  import WorkingList from "./WorkingList.svelte";
  import Inspector from "../inspector/Inspector.svelte";
  import JobsDrawer from "../jobs/JobsDrawer.svelte";
  import TransferDialog from "../transfer/TransferDialog.svelte";
  import LocalTransferDialog from "../transfer/LocalTransferDialog.svelte";
  import AboutDialog from "./AboutDialog.svelte";
  import SettingsDialog from "../settings/SettingsDialog.svelte";
  import ImportBookmarksDialog from "../settings/ImportBookmarksDialog.svelte";
  import CredentialStatusDialog from "./CredentialStatusDialog.svelte";
  import DeleteDialog from "../transfer/DeleteDialog.svelte";
  import ObjectDeleteDialog from "../inspector/ObjectDeleteDialog.svelte";
  import ManifestDeleteDialog from "../inspector/ManifestDeleteDialog.svelte";
  import SelectedDeleteDialog from "../inspector/SelectedDeleteDialog.svelte";
  import { BrowserState } from "./browserState.svelte";
  import { openSelectionDelete } from "./selectionDelete";
  import { activeModal } from "../../lib/trapFocus";
  import { applyEnabledCapabilities, v1Offers } from "../../lib/v1.svelte";

  const browser = new BrowserState(liveApi);

  let workingList: WorkingList | undefined = $state();
  let editorOpen = $state(false);
  // First run: the bookmark list is known and empty. A failed read is not
  // an empty list (audit UI-10), so the welcome waits for a successful
  // one; declining it lasts the window.
  let welcomeDismissed = $state(false);
  const firstRun = $derived(browser.bookmarksLoaded && !browser.bookmarksLoading && !browser.bookmarksStale
    && browser.bookmarksError === null && browser.bookmarks.length === 0);
  const welcomeOpen = $derived(firstRun && !welcomeDismissed && !editorOpen);
  let editorExisting = $state<BookmarkDTO | null>(null);
  let editorCopyOf = $state<BookmarkDTO | null>(null);

  // The native picker opens first; the dialog shows what it read, or why
  // it could not, and nothing when the pick was cancelled.
  async function openImport() {
    if ((await browser.imports.pick()) !== "cancelled") importOpen = true;
  }

  // The editor is mounted afresh for each bookmark it edits, so a copy
  // closes the editor it was asked from before opening its own.
  async function duplicateBookmark(bookmark: BookmarkDTO) {
    editorOpen = false;
    await tick();
    editorExisting = null;
    editorCopyOf = bookmark;
    editorOpen = true;
  }
  let createOpen = $state(false);
  let shellInfo = $state("connecting…");
  let enabledCapabilities: string[] = $state([]);
  let unrecognisedCapabilities: string[] = $state([]);
  let aboutOpen = $state(false);
  let settingsOpen = $state(false);
  let importOpen = $state(false);
  let credentialStatusOpen = $state(false);

  // ADR 0088: the interrupted-job notice. The banner announces it and offers a
  // one-click dismissal of the entries read (decision 3); the jobs panel shows
  // each one's detail and its own dismiss.
  const interruptions = $derived(browser.jobs.interrupted);
  const interruptionsShown = $derived(interruptions?.entries?.length ?? 0);
  const interruptionsBeyond = $derived(interruptions ? interruptions.total - interruptionsShown : 0);
  function dismissShownInterruptions() {
    const ids = (interruptions?.entries ?? []).map((e) => e.jobId);
    if (ids.length > 0) void browser.jobs.dismissInterrupted(ids);
  }

  const connectedName = $derived(
    browser.bookmarks.find((b) => b.id === browser.connectedBookmarkId)?.displayName ?? browser.account?.name ?? "",
  );

  let cleanupLabel = $derived.by(() => {
    if (browser.credentialStatusLoading) return "key cleanup: checking…";
    if (browser.credentialStatusError || !browser.credentialStatus) return "key cleanup: unknown";
    const pending = browser.credentialStatus.retired + browser.credentialStatus.unfinished;
    if (pending > 0) return `key cleanup: ${pending} pending`;
    if (browser.credentialStatus.saving > 0) return "key save in progress";
    return "key cleanup";
  });

  // The rail's width (§4.3: panes should be resizable). The operator drags
  // the rail's edge or uses the arrow keys on the handle; the width
  // persists with the application settings so the webview's own storage,
  // which need not survive a restart, is not relied on. Until the
  // settings are read, or when they cannot be saved, the width still
  // applies to this window. The bounds mirror internal/app/settingsservice.go.
  const railMinPx = 64;
  const railMaxPx = 480;
  const railStepPx = 16;
  let screenEl: HTMLDivElement | undefined = $state();
  let railDrag: number | null = $state(null);
  let railSession: number | null = $state(null);
  const railWidthPx = $derived(railDrag ?? railSession ?? browser.settings.document?.railWidthPx ?? 0);
  // The width the rail actually renders, from layout, so the splitter can
  // report its position at the layout's default too; zero until measured
  // (and in environments without layout), when the chosen or minimum width
  // stands in.
  let railRenderedPx = $state(0);
  const railValueNow = $derived(railDrag ?? (railRenderedPx || railWidthPx || railMinPx));
  $effect(() => {
    const rail = screenEl?.firstElementChild;
    if (!(rail instanceof HTMLElement) || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => {
      railRenderedPx = Math.round(rail.getBoundingClientRect().width);
    });
    observer.observe(rail);
    return () => observer.disconnect();
  });

  function clampRail(px: number): number {
    return Math.min(railMaxPx, Math.max(railMinPx, Math.round(px)));
  }

  function currentRailPx(): number {
    return railRenderedPx || railWidthPx || railMinPx;
  }

  function commitRail(px: number) {
    const width = px === 0 ? 0 : clampRail(px);
    railSession = width;
    void browser.settings.saveRailWidth(width);
  }

  function startRailResize(event: PointerEvent) {
    if (event.button !== 0) return;
    const handle = event.currentTarget as HTMLElement;
    const origin = event.clientX;
    const from = currentRailPx();
    handle.setPointerCapture?.(event.pointerId);
    const move = (moved: PointerEvent) => {
      railDrag = clampRail(from + moved.clientX - origin);
    };
    const stop = () => {
      handle.removeEventListener("pointermove", move);
      handle.removeEventListener("pointerup", stop);
      handle.removeEventListener("pointercancel", stop);
      if (railDrag !== null) commitRail(railDrag);
      railDrag = null;
    };
    handle.addEventListener("pointermove", move);
    handle.addEventListener("pointerup", stop);
    handle.addEventListener("pointercancel", stop);
    event.preventDefault();
  }

  function railKeydown(event: KeyboardEvent) {
    const from = railWidthPx || currentRailPx();
    if (event.key === "ArrowLeft") commitRail(from - railStepPx);
    else if (event.key === "ArrowRight") commitRail(from + railStepPx);
    else if (event.key === "Home") commitRail(0);
    else return;
    event.preventDefault();
  }

  onMount(async () => {
    void browser.loadCredentialStatus();
    void browser.settings.load();
    void browser.jobs.loadInterrupted();
    try {
      const info = await liveApi.info();
      // The Wails build and the secret store are in About, one click away.
      shellInfo = `Aeronautes ${info.version}`;
      browser.secretBackend = info.secretBackend;
      // A capability the operator switched on out of band is offered here
      // and named in the banner: it is enabled, not proven (ADR 0082).
      enabledCapabilities = info.enabledCapabilities ?? [];
      unrecognisedCapabilities = info.unrecognisedCapabilities ?? [];
      applyEnabledCapabilities(enabledCapabilities);
    } catch {
      shellInfo = "service bridge unavailable";
    }
    await browser.loadBookmarks();
  });

  function isEditing(target: EventTarget | null): boolean {
    return (
      target instanceof HTMLInputElement ||
      target instanceof HTMLTextAreaElement ||
      target instanceof HTMLSelectElement ||
      (target instanceof HTMLElement && target.isContentEditable)
    );
  }

  // §4.4: Backspace or Alt+↑ goes to the parent prefix; "/" focuses the
  // server prefix field. Never while typing in a field.
  function handleKeydown(event: KeyboardEvent) {
    if (event.defaultPrevented || activeModal() || isEditing(event.target)) {
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
    } else if ((event.ctrlKey || event.metaKey) && !event.altKey && (event.key === "a" || event.key === "A")) {
      // ADR 0093 decision 1: every loaded row, and never the page's text.
      if (browser.visibleEntries.length > 0) {
        event.preventDefault();
        browser.selectAll();
      }
    } else if (event.key === "Escape") {
      if (browser.chosenRows.length > 0) {
        event.preventDefault();
        browser.clearSelection();
      }
    } else if (event.key === "Delete") {
      // §4.4: Delete opens the safe delete flow that owns the selection and
      // never deletes; selectionDelete.ts says which.
      if (openSelectionDelete(browser)) event.preventDefault();
    }
  }
</script>

<svelte:window onkeydown={handleKeydown} onfocus={() => void browser.refreshContainersOnReturn()} />

<div class="shell">
{#if enabledCapabilities.length > 0 || unrecognisedCapabilities.length > 0}
  <div class="unproven-banner" role="alert">
    {#if enabledCapabilities.length > 0}
      <strong>Unproven:</strong>
      this run offers {enabledCapabilities.join(", ")} because it was switched on for this
      process. The safety work for it is not finished, so treat anything it writes as at risk.
    {/if}
    {#if unrecognisedCapabilities.length > 0}
      <span class="unproven-ignored">
        Ignored, not a capability: {unrecognisedCapabilities.join(", ")}.
      </span>
    {/if}
  </div>
{/if}
{#if browser.jobs.interruptedError}
  <div class="interrupted-banner" role="alert">
    <span>
      <strong>Interruption status is unknown.</strong>
      The check for jobs an earlier run left unfinished did not complete, so whether any are
      still pending cannot be shown.
    </span>
  </div>
{:else if interruptions && interruptions.total > 0}
  <div class="interrupted-banner" role="alert">
    <span>
      <strong>A previous run was interrupted.</strong>
      {interruptions.total} unfinished {interruptions.total === 1 ? "job" : "jobs"} from an earlier session
      {interruptions.total === 1 ? "was" : "were"} not resumed. Their data and records are kept; nothing was
      cleaned up. Open the jobs panel below to see each one.
    </span>
    <span class="interrupted-actions">
      <button onclick={dismissShownInterruptions}>
        Dismiss {interruptionsShown === 1 ? "this interruption" : `these ${interruptionsShown}`}
      </button>
      {#if interruptionsBeyond > 0}
        <span class="interrupted-more">{interruptionsBeyond} more not shown</span>
      {/if}
    </span>
    {#if browser.jobs.dismissError}
      <span class="interrupted-error">The dismissal did not happen; the notice remains.</span>
    {/if}
  </div>
{/if}
<div class="screen" bind:this={screenEl} style:--rail-width={railWidthPx ? `${railWidthPx}px` : undefined}>
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
    onImportBookmarks={() => void openImport()}
    onOpenSettings={() => (settingsOpen = true)}
  />
  <!-- The WAI-ARIA window-splitter pattern: a focusable separator that
    reports its position and moves with the arrow keys; Svelte's rule
    knows separators only as static. -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
  <div
    class="rail-handle"
    role="separator"
    aria-orientation="vertical"
    aria-label="Resize bookmark rail"
    aria-valuemin={railMinPx}
    aria-valuemax={railMaxPx}
    aria-valuenow={railValueNow}
    tabindex="0"
    title="Drag to resize the bookmark rail; Home resets it"
    onpointerdown={startRailResize}
    onkeydown={railKeydown}
    ondblclick={() => commitRail(0)}
  ></div>
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
        {#if browser.account}
          <!-- The account's own name is often an opaque project id; the
            bookmark is the name the operator chose. -->
          <span title={`account ${browser.account.name}`}>connected to {connectedName}</span>
        {:else}
          not connected
        {/if}
      {/if}
    </span>
    <button class="about-link" onclick={() => (credentialStatusOpen = true)}>{cleanupLabel}</button>
    <button class="about-link" title="About Aeronautes" onclick={() => (aboutOpen = true)}>{shellInfo}</button>
  </footer>
</div>
</div>

{#if welcomeOpen}
  <WelcomeDialog
    onAdd={() => {
      welcomeDismissed = true;
      editorExisting = null;
      editorOpen = true;
    }}
    onImport={() => {
      welcomeDismissed = true;
      void openImport();
    }}
    onDismiss={() => (welcomeDismissed = true)}
  />
{/if}
{#if editorOpen}
  <BookmarkEditor
    {browser}
    existing={editorExisting}
    copyOf={editorCopyOf}
    onDuplicate={duplicateBookmark}
    onClose={() => {
      editorOpen = false;
      editorCopyOf = null;
    }}
  />
{/if}
{#if createOpen}
  <CreateContainerDialog {browser} onClose={() => (createOpen = false)} />
{/if}

{#if v1Offers.copy && browser.transferOpen}
  <TransferDialog {browser} onClose={() => (browser.transferOpen = false)} />
{/if}

{#if (v1Offers.download && browser.localTransfer === "download")
  || (v1Offers.upload && browser.localTransfer === "upload")}
  <LocalTransferDialog
    {browser}
    mode={browser.localTransfer === "upload" ? "upload" : "download"}
    onClose={() => (browser.localTransfer = null)}
  />
{/if}

{#if v1Offers.bulkDelete && browser.deleteOpen}
  <DeleteDialog {browser} onClose={() => (browser.deleteOpen = false)} />
{/if}
{#if browser.objectDelete && ((browser.objectDelete.kind === "object" && v1Offers.objectDelete) || (browser.objectDelete.kind === "marker" && v1Offers.markerDelete))}
  <ObjectDeleteDialog {browser} onClose={() => browser.closeObjectDelete()} />
{/if}
{#if browser.objectDelete?.kind === "manifest" && v1Offers.manifestDelete}
  <ManifestDeleteDialog {browser} onClose={() => browser.closeObjectDelete()} />
{/if}
{#if v1Offers.objectDelete && browser.selectedDelete}
  <SelectedDeleteDialog {browser} onClose={() => browser.closeSelectedDelete()} />
{/if}

{#if aboutOpen}
  <AboutDialog onClose={() => (aboutOpen = false)} />
{/if}

{#if settingsOpen}
  <SettingsDialog
    settings={browser.settings}
    onClose={() => (settingsOpen = false)}
    onImportBookmarks={() => {
      settingsOpen = false;
      void openImport();
    }}
  />
{/if}

{#if importOpen}
  <ImportBookmarksDialog
    imports={browser.imports}
    onClose={() => (importOpen = false)}
    onEditBookmark={(bookmarkId) => {
      importOpen = false;
      editorExisting = browser.bookmarks.find((b) => b.id === bookmarkId) ?? null;
      editorOpen = editorExisting !== null;
    }}
  />
{/if}

{#if credentialStatusOpen}
  <CredentialStatusDialog {browser} onClose={() => (credentialStatusOpen = false)} />
{/if}

<style>
  .shell {
    height: 100%;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }

  .unproven-banner {
    padding: var(--space-1) var(--space-3);
    font-size: var(--text-sm);
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-bottom: 1px solid var(--color-warning);
  }

  .unproven-ignored {
    color: var(--color-text-faint);
  }

  .interrupted-banner {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
    padding: var(--space-1) var(--space-3);
    font-size: var(--text-sm);
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-bottom: 1px solid var(--color-warning);
  }

  .interrupted-actions {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
    margin-left: auto;
  }

  .interrupted-more,
  .interrupted-error {
    color: var(--color-text-faint);
  }

  .screen {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: var(--rail-width, 5.4rem) 17rem minmax(24rem, 1fr) minmax(19rem, 22rem);
    grid-template-rows: minmax(0, 1fr) auto auto;
  }

  /* §65.8 item 7: the four panes track narrower windows instead of
     jamming — tighter columns first, then the rail and side panes give
     up more ground. Below that the working list stays usable and the
     page body itself should not scroll horizontally; wide content
     scrolls inside its own pane. */
  @media (max-width: 1100px) {
    .screen {
      grid-template-columns: var(--rail-width, 4.6rem) 13rem minmax(19rem, 1fr) minmax(15rem, 18rem);
    }
  }

  @media (max-width: 920px) {
    .screen {
      grid-template-columns: var(--rail-width, 4.2rem) 10.5rem minmax(16rem, 1fr) minmax(13rem, 15rem);
    }
  }

  .screen > :global(*) {
    grid-row: 1;
  }

  /* The rail and its handle share the first cell: both placed there
     explicitly, so the handle overlaps the rail's edge instead of taking
     a column of its own. */
  .screen > :global(.rail) {
    grid-column: 1;
  }

  .rail-handle {
    grid-column: 1;
    justify-self: end;
    width: 6px;
    margin-right: -3px;
    z-index: 1;
    cursor: col-resize;
    touch-action: none;
  }

  .rail-handle:hover,
  .rail-handle:focus-visible {
    background: var(--color-accent-border);
    outline: none;
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
