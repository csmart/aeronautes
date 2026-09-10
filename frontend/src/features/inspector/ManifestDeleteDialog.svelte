<!-- The manifest-only delete confirm (§60 Phase 4, §30 honesty): says
  exactly what dies and exactly what survives, and where. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import { onDestroy, onMount, untrack } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ObjectDetailDTO, SegmentOwnershipDTO } from "../../lib/api";

  let {
    browser,
    detail,
    segmentsAt,
    onClose,
  }: {
    browser: BrowserState;
    detail: ObjectDetailDTO;
    segmentsAt: string;
    onClose: () => void;
  } = $props();
  const confirmId = $props.id();

  let deleting = $state(false);
  let error: string | null = $state(null);
  // §34/§35 segments option: proof-gated; Backstage force needs the
  // typed name.
  let withSegments = $state(false);
  let ownership = $state<SegmentOwnershipDTO | null>(null);
  let confirmText = $state("");
  let closed = $state(false);

  // The dialog binds the EXACT identity it was opened for: whatever
  // the list, filter, or selection does afterwards, confirm acts on
  // this and nothing else (C-04).
  // svelte-ignore state_referenced_locally
  const target = {
    browser, scope: browser.scopeRevision, selection: browser.detailIdentity, detail, segmentsAt,
    request: Object.freeze({ bookmarkId: browser.connectedBookmarkId ?? "", container: browser.containerName ?? "",
      name: detail.name, force: false, confirm: "" }),
  };

  function isCurrent() {
    return !closed && browser === target.browser && !browser.connecting
      && !!target.request.bookmarkId && !!target.request.container && !!target.request.name
      && target.request.container === target.selection?.container && target.request.name === target.selection.name
      && browser.scopeRevision === target.scope && browser.detailIdentity === target.selection
      && browser.objectDetail === target.detail && detail === target.detail;
  }
  function close() {
    if (closed) return;
    closed = true;
    ownership = null;
    confirmText = "";
    onClose();
  }
  onDestroy(() => { closed = true; });
  $effect(() => { if (!isCurrent()) untrack(close); });

  onMount(async () => {
    try {
      if (!isCurrent()) return;
      const result = await browser.checkSegmentOwnership(target.request);
      if (isCurrent()) ownership = result;
    } catch {
      if (isCurrent()) ownership = null;
    }
  });

  // Segment deletion is ALWAYS force-gated now (R-02): ownership
  // evidence is advisory until a live reference scan exists, so even a
  // "proven" prefix requires the typed confirmation before its backing
  // segments can be deleted.
  const forceNeeded = $derived(withSegments);
  const armed = $derived(isCurrent() && (!withSegments || (ownership !== null && confirmText === target.request.name)));

  async function confirm() {
    if (deleting || !armed) return;
    deleting = true;
    error = null;
    try {
      if (withSegments) {
        // Force is always true for segment deletion; the confirmation is
        // always what the operator typed.
        await browser.deleteManifestWithSegments({ ...target.request, force: true, confirm: confirmText }, isCurrent);
      } else {
        await browser.deleteManifest(target.request.container, target.request.name, isCurrent);
      }
      if (isCurrent()) close();
    } catch (err) {
      if (isCurrent()) error = err instanceof Error ? err.message : String(err);
    } finally {
      if (isCurrent()) deleting = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="alertdialog" aria-label="Delete manifest" aria-modal="true" tabindex="-1"
    use:trapFocus={close}>
    <h2 class="title">Delete {target.detail.class.toUpperCase()} manifest</h2>
    <!-- The body scrolls inside the viewport bound (UI-13): the title and the
      actions stay visible while a long backing target or error stays reachable. -->
    <div class="body">
      <p class="what">
        <span class="type-mono">{target.request.name}</span> will be deleted. The logical object stops
        existing: reads of this name will fail.
      </p>
      {#if !withSegments}
        <p class="keeps">
          <strong>{(ownership?.inlineEntries ?? 0) > 0
            ? "Referenced segment objects survive; inline data does not."
            : "Every referenced segment object survives."}</strong> The data remains at
          <span class="type-mono">{target.segmentsAt}</span> and becomes orphaned unless another manifest
          references it.
        </p>
        {#if (ownership?.inlineEntries ?? 0) > 0}
          <p class="keeps warn">
            <strong>{ownership?.inlineEntries} inline data entr{(ownership?.inlineEntries ?? 0) === 1 ? "y" : "ies"}
            will be destroyed:</strong> inline data lives only inside the manifest itself and
            cannot survive its deletion.
          </p>
        {/if}
      {/if}

      {#if ownership}
        <label class="segments-opt">
          <input type="checkbox" bind:checked={withSegments} disabled={deleting || !isCurrent()} />
          <span>
            Also delete the segments ({ownership.target})
            {#if ownership.proven}
              — evidence suggests <strong class="proven">likely this app's</strong> (ownership record)
            {/if}
          </span>
        </label>
        {#if forceNeeded}
          <p class="force-warn">
            Deleting backing segments is a <strong>Backstage override</strong>. Ownership evidence
            can identify likely-owned segments but cannot yet prove that no other object references
            them, so this always requires explicit confirmation.
            <label for={confirmId}>Type the object name to arm it:</label>
          </p>
          <input
            id={confirmId}
            class="force-confirm type-mono"
            bind:value={confirmText}
            placeholder={target.request.name}
            disabled={deleting || !isCurrent()}
          />
        {/if}
      {/if}
      {#if error}
        <p class="error" role="alert">{error}</p>
      {/if}
    </div>
    <div class="actions">
      <button class="secondary" onclick={close} disabled={deleting}>Cancel</button>
      <button class="danger" onclick={confirm} disabled={deleting || !armed}>
        {deleting
          ? "Deleting…"
          : withSegments
            ? "Delete manifest + segments"
            : "Delete manifest only"}
      </button>
    </div>
  </div>
</div>

<style>
  .segments-opt {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
    font-size: var(--text-sm);
  }

  /* The literal backing target has no break opportunities, so the label needs
     both a shrinkable flex item and permission to break inside the word. */
  .segments-opt span {
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .proven {
    color: var(--color-success);
  }

  .keeps.warn {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-radius: var(--radius-control);
    padding: var(--space-2);
  }

  .force-warn {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-warning);
  }

  .force-confirm {
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgb(20 16 12 / 0.45);
    z-index: 10;
  }

  .dialog {
    width: min(26rem, calc(100vw - 2 * var(--space-5)));
    max-height: calc(100vh - 2 * var(--space-5));
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  .title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: var(--weight-semibold);
    color: var(--color-danger);
  }

  .what,
  .keeps {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }

  .error {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-radius: var(--radius-control);
    overflow-wrap: anywhere;
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    min-height: 0;
    overflow-y: auto;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }

  .actions button {
    padding: var(--space-1) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .secondary {
    color: var(--color-text-secondary);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
  }

  .danger {
    color: var(--color-text-invert);
    background: var(--color-danger);
    border: 1px solid var(--color-danger);
    font-weight: var(--weight-medium);
  }

  .actions button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
