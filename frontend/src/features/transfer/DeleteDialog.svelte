<!-- Destructive-jobs dialog (§31–§33): preview (counts, bytes, the
  manifest disclosure, and the strategy the engine would pick) before a
  typed confirmation unlocks Start. Deletion is Aeronautes-native and
  manifest-only; the Go side re-validates everything. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { DeletePreviewDTO, DeleteRequestDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // svelte-ignore state_referenced_locally
  const hasPrefix = browser.prefix !== "";
  let mode: "prefix" | "empty" | "delete_container" = $state(hasPrefix ? "prefix" : "empty");
  let preview = $state<DeletePreviewDTO | null>(null);
  let confirmText = $state("");
  let busy = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);

  function request() {
    return {
      bookmarkId: browser.connectedBookmarkId ?? "",
      container: browser.containerName ?? "",
      prefix: mode === "prefix" ? browser.prefix : "",
      mode: mode as DeleteRequestDTO["mode"],
      confirm: confirmText,
    };
  }

  async function runPreview() {
    busy = true;
    error = null;
    try {
      preview = await browser.previewDelete(request());
    } catch (e) {
      preview = null;
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function start() {
    busy = true;
    error = null;
    try {
      startedJobId = await browser.startDelete(request());
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="dialog" aria-label="Delete" tabindex="-1" use:trapFocus>
    <header>
      <h2>Destructive operation</h2>
      <button class="close" onclick={onClose} aria-label="Close">✕</button>
    </header>

    <div class="modes" role="radiogroup" aria-label="Scope">
      {#if hasPrefix}
        <label class="mode">
          <input type="radio" value="prefix" bind:group={mode} onchange={() => (preview = null)} />
          <span>
            Delete prefix <span class="type-mono">{browser.containerName}/{browser.prefix}</span>
          </span>
        </label>
      {/if}
      <label class="mode">
        <input type="radio" value="empty" bind:group={mode} onchange={() => (preview = null)} />
        <span>
          Empty container <span class="type-mono">{browser.containerName}</span> — contents go,
          the container and all its configuration stay
        </span>
      </label>
      <label class="mode">
        <input
          type="radio"
          value="delete_container"
          bind:group={mode}
          onchange={() => (preview = null)}
        />
        <span>
          Delete container <span class="type-mono">{browser.containerName}</span> — empty it,
          then delete the container itself
        </span>
      </label>
    </div>

    {#if error}
      <p class="error">{error}</p>
    {/if}

    {#if preview}
      <div class="preview num">
        <p class="summary">
          <strong>{preview.objects}</strong> objects · {formatBytes(preview.bytes)} ·
          strategy <span class="type-mono">{preview.strategy}</span>
        </p>
        {#if preview.possibleManifests > 0}
          <p class="warn">
            {preview.possibleManifests} possible manifest{preview.possibleManifests === 1
              ? ""
              : "s"} in scope. Deletion is manifest-only: their segments (if any) stay behind as
            recoverable orphans rather than risking another object's data.
          </p>
        {/if}
        <label class="confirm">
          <span>
            Type the container name <span class="type-mono">{browser.containerName}</span> to
            arm:
          </span>
          <input class="type-mono" bind:value={confirmText} placeholder={browser.containerName} />
        </label>
      </div>
    {/if}

    <footer>
      {#if startedJobId}
        <span class="started">Job {startedJobId} started — follow it in the drawer.</span>
        <button onclick={onClose}>Close</button>
      {:else}
        <button onclick={runPreview} disabled={busy}>
          {busy ? "Counting…" : "Preview"}
        </button>
        <button
          class="danger"
          onclick={start}
          disabled={busy || !preview || confirmText !== (browser.containerName ?? "")}
        >
          {mode === "prefix"
            ? "Delete prefix"
            : mode === "empty"
              ? "Empty container"
              : "Delete container"}
        </button>
      {/if}
    </footer>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--color-bg) 60%, transparent);
    display: grid;
    place-items: center;
    z-index: 30;
  }

  .dialog {
    width: min(40rem, calc(100vw - 4rem));
    background: var(--color-bg-pane);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-card);
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  h2 {
    margin: 0;
    font-size: var(--text-base);
    color: var(--color-danger);
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .modes {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    font-size: var(--text-sm);
  }

  .mode {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
  }

  .error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .preview {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    padding: var(--space-3);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .summary {
    margin: 0;
    font-size: var(--text-sm);
  }

  .warn {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-warning);
  }

  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    font-size: var(--text-sm);
  }

  .confirm input {
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  footer {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: var(--space-2);
  }

  .started {
    margin-right: auto;
    font-size: var(--text-sm);
    color: var(--color-success);
  }

  footer button {
    font: inherit;
    font-size: var(--text-sm);
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: var(--color-bg-card);
    color: var(--color-text);
    cursor: pointer;
  }

  footer button:disabled {
    opacity: 0.5;
    cursor: default;
  }

  footer .danger {
    background: var(--color-danger);
    color: var(--color-bg);
    border-color: var(--color-danger);
  }
</style>
