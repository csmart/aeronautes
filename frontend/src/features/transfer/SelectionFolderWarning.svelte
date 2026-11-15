<!-- What a selection holding folder rows is told before Copy or Download
  (ADR 0108 decision 1). A selection's folders are not copied or downloaded,
  and one line inside the dialog was too quiet a way to say so: a copy that
  left a folder behind read as a loss. So the count and the names come
  first, with the way to take everything. It sends nothing; going on opens
  the action's own dialog for the selected objects. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import { formatCount } from "../../lib/format";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // The action that was pressed; the screen mounts this only while one is held.
  // svelte-ignore state_referenced_locally
  const action = browser.transferWarning!;
  const verb = action === "copy" ? "copy" : "download";
  const does = action === "copy" ? "copies" : "downloads";
  const done = action === "copy" ? "copied" : "downloaded";
  const button = action === "copy" ? "Copy" : "Download";

  /** How many folders are named; the rest are counted, and said to be. */
  const named = 5;
  const folders = $derived(browser.chosenRows.filter((row) => row.kind === "prefix").map((row) => row.name));
  const objects = $derived(browser.transferSource.members.length);
  const folderWord = $derived(folders.length === 1 ? "folder" : "folders");
  const objectWord = $derived(objects === 1 ? "object" : "objects");
  // What the same button reads once the selection is cleared.
  const scopeWord = $derived(browser.prefix === "" ? "container" : "folder");
  // Rows are named as the list shows them, without the folder being browsed.
  const directoryLength = $derived(browser.prefix.lastIndexOf("/") + 1);
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="alertdialog" aria-label="Selected folders are left out" aria-modal="true" tabindex="-1"
    use:trapFocus={onClose}>
    <h2 class="title">
      {#if objects > 0}
        {formatCount(folders.length)} selected {folderWord} will not be {done}
      {:else}
        Several folders cannot be {done} in one go yet
      {/if}
    </h2>
    <div class="body">
      <p class="what">
        This selection holds {formatCount(folders.length)} {folderWord}{objects > 0 ? ` beside ${formatCount(objects)} ${objectWord}` : " and no objects"}:
        <span class="type-mono">{folders.slice(0, named).map((name) => name.slice(directoryLength)).join(", ")}</span>{#if folders.length > named}, and {formatCount(folders.length - named)} more not named here{/if}.
      </p>
      {#if objects > 0}
        <p class="what warn" role="alert">
          {folders.length === 1 ? "That folder and everything in it" : "Those folders and everything in them"} will not be {done}.
          Going on {does} the {formatCount(objects)} selected {objectWord} only.
        </p>
      {:else}
        <p class="what warn" role="alert">Nothing is {done} from this selection.</p>
      {/if}
      <p class="what">
        To {verb} one folder with everything in it, select that folder alone and use “{button} folder…”.
        To {verb} everything here, close this, press Escape to clear the selection and use “{button} {scopeWord}…”.
      </p>
    </div>
    <div class="actions">
      {#if objects > 0}
        <button class="secondary" onclick={onClose}>Cancel</button>
        <button class="primary" onclick={() => browser.openTransfer(action)}>
          {button} the {formatCount(objects)} {objectWord} only…
        </button>
      {:else}
        <button class="primary" onclick={onClose}>Close</button>
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
    width: min(30rem, calc(100vw - 2 * var(--space-5)));
    max-height: calc(100vh - 2 * var(--space-5));
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  .title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: var(--weight-semibold);
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    min-height: 0;
    overflow-y: auto;
  }

  .what {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }

  .what.warn {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-radius: var(--radius-control);
    padding: var(--space-2);
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
</style>
