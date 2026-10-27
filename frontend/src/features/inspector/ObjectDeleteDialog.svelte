<!-- The single-object delete confirm (ADR 0092 decision 5): it binds the
  exact identity it opened for, says what the delete does to the content at
  this name under the container's versioning mode, and when the verifying
  read observed something at the name afterwards it shows that instead of a
  plain success. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import { onMount, untrack } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerSettingsDTO, ObjectDetailDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // The operation is owned by the browser state (ADR 0092 decision 5): it was
  // captured when the dialog opened and it outlives the listing refresh that
  // follows a success, so the successor the verifying read observed is still
  // here to show. Opening without an operation is a programming error the
  // screen's mount condition prevents.
  // svelte-ignore state_referenced_locally
  const op = browser.objectDelete!;

  // undefined while the container's facts are being read, null when they
  // cannot be read. Confirmation waits for either answer.
  let facts: ContainerSettingsDTO | null | undefined = $state(undefined);

  const pending = $derived(op.status === "pending");
  const observed = $derived(op.status === "done" ? op.result?.objectAfter ?? null : null);
  const current = $derived(browser.objectDeleteCurrent());

  // A view the operator moved before confirming abandons the operation; the
  // refresh after a success is the state's own and does not reach here as
  // pending.
  $effect(() => {
    if (pending && !op.error && !current) untrack(onClose);
  });

  onMount(async () => {
    const result = await browser.containerFacts(op.container);
    if (browser.objectDelete === op) facts = result;
  });

  // A directory marker reached as an object row is deleted as the literal
  // object it is; its children stay (ADR 0092 decision 2).
  const marker = $derived(op.detail.contentLength === 0 && op.detail.contentType === "application/directory");

  // Measured against the owned Swift on 27 October 2026
  // (docs/experiments/2026-10-27-delete-under-versioning.md): a versions
  // location archives on overwrite and never on delete.
  function versioningSentence(f: ContainerSettingsDTO | null | undefined): string {
    if (f === undefined) return "Checking how this container keeps versions…";
    if (f === null) return "How this container keeps versions could not be read; assume the content is destroyed.";
    switch (f.versioningMode) {
      case "versions_location":
        return `This container keeps versions in ${f.versioningArchive}, but a delete archives nothing: the current content is destroyed, and if an earlier version is archived the name reverts to it.`;
      case "history_location":
        return `This container keeps history in ${f.versioningArchive}: the current content moves there with a deletion marker, and the name stops resolving.`;
      case "enabled":
        return "This container uses in-container versioning, which this release does not manage; the delete follows the cluster's own rules.";
      default:
        return "This container keeps no versions: the content is destroyed.";
    }
  }

  function kindOf(d: ObjectDetailDTO): string {
    if (d.symlinkTarget) return "symlink";
    if (d.class !== "plain") return `${d.class.toUpperCase()} manifest`;
    return "plain object";
  }

  // Confirmation waits for the versioning consequence to be known or
  // explicitly unknown, so the warning a versions location needs is on
  // screen before the request can leave.
  const canConfirm = $derived(pending && facts !== undefined && current);

  async function confirm() {
    if (!canConfirm) return;
    await browser.runObjectDelete();
    const settled = browser.objectDelete;
    if (settled === op && op.status === "done" && !op.result?.objectAfter) onClose();
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="alertdialog" aria-label="Delete object" aria-modal="true" tabindex="-1"
    use:trapFocus={onClose}>
    <h2 class="title">{observed ? "Deleted" : "Delete object"}</h2>
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
          <span class="type-mono">{op.name}</span> will be deleted: {formatBytes(op.detail.contentLength)},
          entity tag <span class="type-mono">{op.detail.etag}</span>.
        </p>
        <p class="keeps">{versioningSentence(facts)}</p>
        {#if marker}
          <p class="keeps">This is a directory marker: the objects under it stay, and the folder remains implied by them.</p>
        {/if}
        <p class="keeps">
          The delete is refused if the object changes before it runs; it cannot see a change that lands after its
          last look.
        </p>
      {/if}
      {#if op.error}
        <p class="error" role="alert">{op.error}</p>
      {/if}
    </div>
    <div class="actions">
      {#if observed}
        <button class="secondary" onclick={onClose}>Close</button>
      {:else}
        <button class="secondary" onclick={onClose} disabled={op.status === "running"}>Cancel</button>
        <button class="danger" onclick={confirm} disabled={!canConfirm}>
          {op.status === "running" ? "Deleting…" : "Delete object"}
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
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

  .keeps.warn {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-radius: var(--radius-control);
    padding: var(--space-2);
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
