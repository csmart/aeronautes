<!-- The manifest-only delete confirm (§60 Phase 4, ADR 0094 decision 5):
  owned by the browser state so its outcome survives the refresh that follows,
  it binds the identity it opened for, says exactly what dies and exactly what
  survives and where, waits for the container's versioning facts before it can
  be confirmed, and shows what the verifying read observed afterwards instead
  of a plain success. The with-segments option is the force-gated Backstage
  override bulk deletion offers; its ownership check is asked only when that
  option is taken, never for the manifest-only path. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import { onMount, onDestroy, untrack } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerSettingsDTO, ObjectDetailDTO, SegmentOwnershipDTO } from "../../lib/api";
  import { v1Offers } from "../../lib/v1.svelte";
  import { formatBytes } from "../../lib/format";
  import { versioningSentence } from "./versioningSentence";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();
  const confirmId = $props.id();

  // The operation was captured when the dialog opened and outlives the
  // listing refresh that follows a success (ADR 0092 decision 5). Opening
  // without one is a programming error the screen's mount condition prevents.
  // svelte-ignore state_referenced_locally
  const op = browser.objectDelete!;
  // svelte-ignore state_referenced_locally
  const request = Object.freeze({
    bookmarkId: browser.connectedBookmarkId ?? "", container: op.container, name: op.name, force: false, confirm: "",
    reviewed: { class: "", fingerprint: "", length: 0, etag: "" },
  });

  // undefined while the container's facts are being read, null when they
  // cannot be read. Confirmation waits for either answer.
  let facts: ContainerSettingsDTO | null | undefined = $state(undefined);
  let withSegments = $state(false);
  // undefined until the with-segments option asks; null when the check could
  // not answer, which keeps that option disarmed.
  let ownership = $state<SegmentOwnershipDTO | null | undefined>(undefined);
  let confirmText = $state("");
  let segmentsBusy = $state(false);
  let segmentsError: string | null = $state(null);
  // The segments deletion starts a job and resolves after the dialog may
  // already have closed or unmounted; a late success must not call back then.
  let live = true;
  onDestroy(() => { live = false; });

  const pending = $derived(op.status === "pending");
  const observed = $derived(op.status === "done" ? op.result?.objectAfter ?? null : null);
  const current = $derived(browser.objectDeleteCurrent());

  // A view that moved while the operation is still pending abandons it, as
  // the object deletion's dialog does; the refresh after a success is the
  // state's own and does not reach here as pending.
  $effect(() => {
    if (pending && !current) untrack(onClose);
  });

  onMount(async () => {
    const result = await browser.containerFacts(op.container);
    if (browser.objectDelete === op) facts = result;
  });

  // Where the segments are: the dynamic manifest's own prefix, or the list a
  // static manifest names.
  const segmentsAt = $derived(op.detail.class === "dlo" && op.detail.manifest ? op.detail.manifest : "the segments this manifest lists");
  // The inline-data disclosure never depends on the ownership check: a DLO
  // holds no inline data, an SLO's inline entries are counted from the
  // manifest the inspector already read, and otherwise the count is unknown.
  const inlineWithoutTheCheck = $derived(
    op.detail.class !== "slo"
      ? 0
      : (browser.sloManifest?.entries ?? null)?.filter((entry) => entry.kind === "data").length ?? null,
  );
  const inlineEntries = $derived(ownership?.inlineEntries ?? inlineWithoutTheCheck);

  // The ownership check is classified with bulk deletion, so it is asked once
  // the operator takes the option bulk deletion offers, and never otherwise.
  $effect(() => {
    if (!withSegments || ownership !== undefined) return;
    ownership = null;
    untrack(() => {
      void (async () => {
        try {
          const result = await browser.checkSegmentOwnership(request);
          if (browser.objectDelete === op) ownership = result;
        } catch (err) {
          if (browser.objectDelete === op) segmentsError = err instanceof Error ? err.message : String(err);
        }
      })();
    });
  });

  // Confirmation waits for the versioning consequence to be known or
  // explicitly unknown, so the warning a versions location needs is on
  // screen before the request can leave; the segments override also needs
  // the ownership evidence and the typed name (R-02).
  const canConfirm = $derived(
    pending && facts !== undefined && current
      && (!withSegments || (!!ownership && confirmText === op.name && !segmentsBusy)),
  );

  function kindOf(d: ObjectDetailDTO): string {
    if (d.symlinkTarget) return "symlink";
    if (d.class !== "plain") return `${d.class.toUpperCase()} manifest`;
    return "plain object";
  }

  async function confirm() {
    if (!canConfirm) return;
    if (withSegments) {
      segmentsBusy = true;
      segmentsError = null;
      try {
        // Force is always true for segment deletion; the confirmation is what
        // the operator typed, and the reviewed graph the check returned is
        // carried back so the job acts on what was shown (ADR 0095 decision 8).
        await browser.deleteManifestWithSegments({ ...request, force: true, confirm: confirmText, reviewed: ownership!.reviewed });
        if (live && browser.objectDelete === op) onClose();
      } catch (err) {
        if (browser.objectDelete === op) segmentsError = err instanceof Error ? err.message : String(err);
      } finally {
        if (browser.objectDelete === op) segmentsBusy = false;
      }
      return;
    }
    await browser.runObjectDelete();
    const settled = browser.objectDelete;
    if (settled === op && op.status === "done" && !op.result?.objectAfter) onClose();
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="alertdialog" aria-label="Delete manifest" aria-modal="true" tabindex="-1"
    use:trapFocus={onClose}>
    <h2 class="title">{observed ? "Deleted" : `Delete ${op.detail.class.toUpperCase()} manifest`}</h2>
    <div class="body">
      {#if observed}
        <p class="what">
          <span class="type-mono">{op.name}</span> no longer holds what you were shown.
        </p>
        <p class="keeps warn">
          <strong>Something stands at the name now:</strong> a {kindOf(observed)} of
          {formatBytes(observed.contentLength)}{observed.etag ? ` with entity tag ${observed.etag}` : ""}{observed.symlinkTarget
            ? `, pointing at ${observed.symlinkTarget}`
            : ""}. It may be the archived predecessor or a concurrent write; what this delete did to it is not
          observed.
        </p>
      {:else}
        <p class="what">
          The {op.detail.class.toUpperCase()} manifest you were shown at <span class="type-mono">{op.name}</span> will be
          deleted; what then stands at the name, if anything, follows how this container keeps versions.
        </p>
        {#if browser.segmentStoreCaution}
          <p class="keeps warn">{browser.segmentStoreCaution}</p>
        {/if}
        {#if !withSegments}
          <p class="keeps">
            <strong>{inlineEntries === null
              ? "Referenced segment objects survive."
              : inlineEntries > 0
                ? "Referenced segment objects survive; inline data does not."
                : "Every referenced segment object survives."}</strong> The data remains at
            <span class="type-mono">{segmentsAt}</span> and becomes orphaned unless another manifest
            references it.
          </p>
          {#if inlineEntries === null}
            <p class="keeps warn">
              <strong>Whether this manifest holds inline data is unknown here:</strong> the checks
              that would answer it are not available in this build. Inline data lives only inside
              the manifest itself, so any of it this manifest holds is destroyed with it.
            </p>
          {:else if inlineEntries > 0}
            <p class="keeps warn">
              <strong>{inlineEntries} inline data entr{inlineEntries === 1 ? "y" : "ies"}
              will be destroyed:</strong> inline data lives only inside the manifest itself and
              cannot survive its deletion.
            </p>
          {/if}
        {/if}
        <p class="keeps">{versioningSentence(facts)}</p>
        <p class="keeps">
          The delete is refused if the manifest changes before it runs; it cannot see a change that lands after
          its last look, and a manifest of the same kind found afterwards leaves the outcome unverified.
        </p>

        {#if v1Offers.bulkDelete}
          <label class="segments-opt">
            <input type="checkbox" bind:checked={withSegments} disabled={segmentsBusy || !current} />
            <span>
              Also delete the segments
              {#if ownership}
                ({ownership.target})
                {#if ownership.proven}
                  — evidence suggests <strong class="proven">likely this app's</strong> (ownership record)
                {/if}
              {:else if withSegments && ownership === null && !segmentsError}
                — checking which segments this manifest owns…
              {/if}
            </span>
          </label>
          {#if withSegments}
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
              placeholder={op.name}
              disabled={segmentsBusy || !current}
            />
          {/if}
        {/if}
      {/if}
      {#if op.error}
        <p class="error callout danger" role="alert">{op.error}</p>
      {/if}
      {#if segmentsError}
        <p class="error callout danger" role="alert">{segmentsError}</p>
      {/if}
    </div>
    <div class="actions">
      {#if observed}
        <button class="primary" onclick={onClose}>Close</button>
      {:else}
        <button class="secondary" onclick={onClose} disabled={op.status === "running" || segmentsBusy}>Cancel</button>
        <button class="danger" onclick={confirm} disabled={!canConfirm}>
          {op.status === "running" || segmentsBusy
            ? "Deleting…"
            : withSegments
              ? "Delete manifest + segments"
              : "Delete manifest only"}
        </button>
      {/if}
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

  /* A finished dialog's Close is its next step, highlighted as in the copy
    and transfer dialogs; anything beside it stays plain. */
  .primary {
    color: var(--color-bg);
    background: var(--color-accent);
    border: 1px solid var(--color-accent);
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
