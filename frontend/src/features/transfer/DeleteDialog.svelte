<!-- Destructive-jobs dialog (§31–§33): preview (counts, bytes, the
  manifest disclosure, and the strategy the engine would pick) before a
  typed confirmation unlocks Start. Deletion is Aeronautes-native and
  manifest-only; the Go side re-validates everything. -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerSettingsDTO, DeletePreviewDTO, DeleteRequestDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";
  import { archiveOwnersSentence, versioningSentence } from "../inspector/versioningSentence";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  const hasPrefix = $derived(browser.prefix !== "");
  // The values are the generated enum's own, so a mode the bridge renames
  // fails to compile here instead of being sent as a name it no longer knows.
  type Mode = Exclude<`${DeleteRequestDTO["mode"]}`, "">;
  // svelte-ignore state_referenced_locally
  let mode: Mode = $state(hasPrefix ? "prefix" : "empty");
  let confirmText = $state("");
  let previewing = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);
  let closed = $state(false);
  // `facts` is undefined while the container's versioning facts are still
  // loading, then null or a value once settled. Start waits for settled
  // (facts !== undefined) so the versioning consequence is disclosed before a
  // destructive request is authorised (ADR 0095 decision 9).
  let facts: ContainerSettingsDTO | null | undefined = $state(undefined);
  let generation = 0;
  interface Intent {
    generation: number;
    browser: BrowserState;
    scopeRevision: number;
    request: DeleteRequestDTO;
  }
  let prepared = $state.raw<{ intent: Intent; plan: DeletePreviewDTO } | null>(null);
  const preview = $derived(prepared && isCurrent(prepared.intent) ? prepared.plan : null);
  const canPreview = $derived(!closed && !previewing && !starting && !browser.connecting
    && !!browser.connectedBookmarkId && !!browser.containerName && (mode !== "prefix" || hasPrefix));
  const canStart = $derived(!!prepared && !!preview && !previewing && !starting
    && facts !== undefined
    && confirmText === prepared.intent.request.container);

  function request(): DeleteRequestDTO {
    return {
      bookmarkId: browser.connectedBookmarkId ?? "",
      container: browser.containerName ?? "",
      prefix: mode === "prefix" ? browser.prefix : "",
      mode: mode as DeleteRequestDTO["mode"],
      confirm: "",
    };
  }

  function isCurrent(intent: Intent): boolean {
    return !closed && !browser.connecting && intent.generation === generation
      && intent.browser === browser && intent.scopeRevision === browser.scopeRevision
      && JSON.stringify(intent.request) === JSON.stringify(request());
  }

  function invalidate() {
    generation++;
    prepared = null;
    confirmText = "";
    previewing = false;
    startedJobId = null;
    error = null;
  }

  $effect(() => {
    void browser.scopeRevision;
    untrack(invalidate);
  });

  // The container's versioning facts drive the consent sentence that says what
  // a delete leaves at the name, the same pattern the manifest dialog uses.
  $effect(() => {
    const container = browser.containerName;
    facts = undefined;
    if (!container) return;
    untrack(() => {
      void (async () => {
        const loaded = await browser.containerFacts(container);
        if (browser.containerName === container) facts = loaded;
      })();
    });
  });

  function close() {
    closed = true;
    invalidate();
    onClose();
  }
  onDestroy(() => { closed = true; invalidate(); });

  async function runPreview() {
    if (!canPreview) return;
    invalidate();
    const req = Object.freeze(request());
    const intent: Intent = { generation, browser, scopeRevision: browser.scopeRevision, request: req };
    previewing = true;
    try {
      const plan = await browser.previewDelete(req);
      if (isCurrent(intent)) prepared = { intent, plan };
    } catch (e) {
      if (isCurrent(intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      if (isCurrent(intent)) previewing = false;
    }
  }

  async function start() {
    const current = prepared;
    if (starting || previewing || !current || !isCurrent(current.intent)
      || facts === undefined
      || confirmText !== current.intent.request.container) return;
    const req = { ...current.intent.request, confirm: confirmText };
    prepared = null;
    confirmText = "";
    starting = true;
    error = null;
    try {
      const jobId = await browser.startDelete(req);
      if (isCurrent(current.intent)) startedJobId = jobId;
    } catch (e) {
      if (isCurrent(current.intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      // A scope edit cannot open a second Start while this one is pending.
      starting = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="Delete" tabindex="-1" use:trapFocus={close}>
    <header>
      <h2>Destructive operation</h2>
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>

    <div class="modes" role="radiogroup" aria-label="Scope">
      {#if hasPrefix}
        <label class="mode">
          <input type="radio" name="delete-scope" value="prefix" bind:group={mode} onchange={invalidate} />
          <span>
            Delete prefix <span class="type-mono">{browser.containerName}/{browser.prefix}</span>
          </span>
        </label>
      {/if}
      <label class="mode">
        <input type="radio" name="delete-scope" value="empty" bind:group={mode} onchange={invalidate} />
        <span>
          Empty container <span class="type-mono">{browser.containerName}</span> — contents go,
          the container and all its configuration stay
        </span>
      </label>
      <label class="mode">
        <input
          type="radio"
          name="delete-scope"
          value="delete_container"
          bind:group={mode}
          onchange={invalidate}
        />
        <span>
          Delete container <span class="type-mono">{browser.containerName}</span> — empty it,
          then delete the container itself
        </span>
      </label>
    </div>

    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}

    {#if preview}
      {@const archiveSentence = archiveOwnersSentence(preview)}
      <div class="preview num">
        <p class="summary">
          <strong>{preview.objects}</strong> objects · {formatBytes(preview.bytes)} ·
          strategy <span class="type-mono">{preview.strategy}</span>
        </p>
        <p class="cost">
          Each object is compared then deleted, and a row that cannot say what it holds is read once
          more first: <strong>{preview.requestCost}</strong> requests{preview.laterBaselineRows > 0
            ? ` (${preview.laterBaselineRows} of these rows take that extra read)`
            : ""}, not counting this listing, any retries, the final check or each member's own
          verifying read.
        </p>
        <p class="keeps">
          An object listed with a size is deleted only if it still matches that listing; one that
          changed since is left and recorded. A row listed as zero bytes or as a static large object
          is read once more first, and what that read then holds is what is deleted, a directory
          marker or a manifest included. A link is refused and left, never followed. Each name is
          requested once by this job; whatever is still listed after that is left for a fresh run.
        </p>
        <p class="keeps">{versioningSentence(facts)}</p>
        {#if archiveSentence}
          <p class="warn">{archiveSentence}</p>
        {/if}
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
        <button onclick={close}>Close</button>
      {:else}
        <button onclick={runPreview} disabled={!canPreview}>
          {previewing ? "Counting…" : "Preview"}
        </button>
        <button
          class="danger"
          onclick={start}
          disabled={!canStart}
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

  .cost,
  .keeps {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
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
