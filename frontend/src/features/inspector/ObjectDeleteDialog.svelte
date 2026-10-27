<!-- The single-object delete confirm (ADR 0092 decision 5): it binds the
  exact identity it opened for, says what the delete does to the content at
  this name under the container's versioning mode, and when the verifying
  read observed something at the name afterwards it shows that instead of a
  plain success. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import { onDestroy, onMount, untrack } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerSettingsDTO, ObjectDetailDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";

  let {
    browser,
    detail,
    container,
    onClose,
  }: {
    browser: BrowserState;
    detail: ObjectDetailDTO;
    container: string;
    onClose: () => void;
  } = $props();

  let deleting = $state(false);
  let settling = $state(false);
  let error: string | null = $state(null);
  let observed: ObjectDetailDTO | null = $state(null);
  // undefined while the container's facts are being read, null when they
  // cannot be read.
  let facts: ContainerSettingsDTO | null | undefined = $state(undefined);
  let closed = $state(false);

  // The dialog binds the EXACT identity it was opened for: whatever the
  // list, filter or selection does afterwards, confirm acts on this and
  // nothing else (C-04, as the manifest and bulk dialogs do).
  // svelte-ignore state_referenced_locally
  const target = {
    browser, scope: browser.scopeRevision, selection: browser.detailIdentity, detail, container, name: detail.name,
  };

  function isCurrent() {
    return !closed && browser === target.browser && !browser.connecting
      && !!target.container && !!target.name
      && target.container === target.selection?.container && target.name === target.selection.name
      && browser.scopeRevision === target.scope && browser.detailIdentity === target.selection
      && browser.objectDetail === target.detail && detail === target.detail;
  }
  function close() {
    if (closed) return;
    closed = true;
    onClose();
  }
  onDestroy(() => { closed = true; });
  // A view that moved closes the dialog, except while a delete is settling
  // or once it has settled with a successor to show: the listing refresh
  // that follows a success moves the selection on purpose.
  $effect(() => { if (!isCurrent() && !settling && !observed) untrack(close); });

  onMount(async () => {
    const result = await browser.containerFacts(target.container);
    if (!closed) facts = result;
  });

  // A directory marker reached as an object row is deleted as the literal
  // object it is; its children stay (ADR 0092 decision 2).
  const marker = $derived(target.detail.contentLength === 0 && target.detail.contentType === "application/directory");

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

  async function confirm() {
    if (deleting || observed || !isCurrent()) return;
    deleting = true;
    settling = true;
    error = null;
    try {
      const result = await browser.deleteObject(target.container, target.name, target.detail, () => !closed);
      if (closed) return;
      if (result.objectAfter) {
        observed = result.objectAfter;
      } else {
        close();
      }
    } catch (err) {
      if (!closed) error = err instanceof Error ? err.message : String(err);
    } finally {
      deleting = false;
      settling = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="alertdialog" aria-label="Delete object" aria-modal="true" tabindex="-1"
    use:trapFocus={close}>
    <h2 class="title">{observed ? "Deleted" : "Delete object"}</h2>
    <div class="body">
      {#if observed}
        <p class="what">
          <span class="type-mono">{target.name}</span> no longer holds what you were shown.
        </p>
        <p class="keeps warn">
          <strong>Something stands at the name now:</strong> a {kindOf(observed)} of
          {formatBytes(observed.contentLength)}{observed.etag ? ` with entity tag ${observed.etag}` : ""}{observed.symlinkTarget
            ? `, pointing at ${observed.symlinkTarget}`
            : ""}. It may be the archived predecessor or a concurrent write; this delete did not create it and did
          not touch it.
        </p>
      {:else}
        <p class="what">
          <span class="type-mono">{target.name}</span> will be deleted: {formatBytes(target.detail.contentLength)},
          entity tag <span class="type-mono">{target.detail.etag}</span>.
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
      {#if error}
        <p class="error" role="alert">{error}</p>
      {/if}
    </div>
    <div class="actions">
      {#if observed}
        <button class="secondary" onclick={close}>Close</button>
      {:else}
        <button class="secondary" onclick={close} disabled={deleting}>Cancel</button>
        <button class="danger" onclick={confirm} disabled={deleting || !isCurrent()}>
          {deleting ? "Deleting…" : "Delete object"}
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
