<!-- Copy / Mirror-preview dialog (§27.1, §27.2). Both modes share the
  §15.2/§28.4 preview with its honest conservative counts and
  disclosures; Copy can execute, Mirror renders the full destructive
  plan — copy set AND would-delete set — with execution structurally
  absent until Phase 6 (§65.4: inert by construction, there is no
  button to omit a handler from). -->
<script lang="ts">
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { CopyPlanDTO, CopyRequestDTO } from "../../lib/api";
  import { formatBytes } from "../../lib/format";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  let mode: "copy" | "mirror" = $state("copy");
  // The dialog remounts per open, so the initial value is the intent.
  // svelte-ignore state_referenced_locally
  let destBookmarkId = $state(browser.connectedBookmarkId ?? "");
  let destContainer = $state("");
  let destPrefix = $state("");
  let preview: CopyPlanDTO | null = $state(null);
  let previewing = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);

  function request(): CopyRequestDTO {
    return {
      sourceBookmarkId: browser.connectedBookmarkId ?? "",
      sourceContainer: browser.containerName ?? "",
      sourcePrefix: browser.prefix,
      destBookmarkId,
      destContainer: destContainer.trim(),
      destPrefix: destPrefix.trim(),
    };
  }

  // Any input change invalidates the preview — Start is gated on a
  // preview of exactly what would run.
  function invalidate() {
    preview = null;
    startedJobId = null;
    error = null;
  }

  async function runPreview() {
    previewing = true;
    error = null;
    try {
      preview = await browser.planCopy(request());
    } catch (e) {
      preview = null;
      error = e instanceof Error ? e.message : String(e);
    } finally {
      previewing = false;
    }
  }

  async function start() {
    starting = true;
    error = null;
    try {
      startedJobId = await browser.startCopy(request());
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      starting = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="dialog" aria-label="Transfer" tabindex="-1">
    <header>
      <div class="modes" role="tablist">
        <button
          role="tab"
          class:active={mode === "copy"}
          aria-selected={mode === "copy"}
          onclick={() => (mode = "copy")}>Copy</button
        >
        <button
          role="tab"
          class:active={mode === "mirror"}
          aria-selected={mode === "mirror"}
          onclick={() => (mode = "mirror")}>Mirror preview</button
        >
      </div>
      <button class="close" onclick={onClose} aria-label="Close">✕</button>
    </header>

    <div class="scope num">
      <div class="side">
        <span class="side-label">Source</span>
        <span class="type-mono">{browser.containerName ?? "—"}/{browser.prefix}</span>
      </div>
      <span class="arrow">→</span>
      <div class="side fields">
        <span class="side-label">Destination</span>
        <select
          value={destBookmarkId}
          onchange={(e) => {
            destBookmarkId = e.currentTarget.value;
            invalidate();
          }}
        >
          {#each browser.bookmarks as bookmark (bookmark.id)}
            <option value={bookmark.id}>{bookmark.displayName}</option>
          {/each}
        </select>
        <input
          class="type-mono"
          placeholder="container"
          value={destContainer}
          oninput={(e) => {
            destContainer = e.currentTarget.value;
            invalidate();
          }}
        />
        <input
          class="type-mono"
          placeholder="prefix/ (optional)"
          value={destPrefix}
          oninput={(e) => {
            destPrefix = e.currentTarget.value;
            invalidate();
          }}
        />
      </div>
    </div>

    {#if error}
      <p class="error">{error}</p>
    {/if}

    {#if preview}
      {@const s = preview.summary}
      <div class="preview num">
        <div class="counts">
          <span class="count"><strong>{s.new}</strong> new</span>
          <span class="count"><strong>{s.changed}</strong> changed</span>
          <span class="count"><strong>{s.same}</strong> unchanged</span>
          <span class="count conservative" title="Equality unprovable from listings (§28.4): transferred conservatively, resolved per object">
            <strong>{s.unknown}</strong> unable to prove unchanged
          </span>
          <span class="count"><strong>{formatBytes(s.transferBytes)}</strong> to transfer</span>
        </div>
        <div class="notes">
          {#if s.overwrites > 0}
            <p>
              {s.overwrites} existing destination object{s.overwrites === 1 ? "" : "s"} will be
              overwritten via stage-and-commit.
            </p>
          {/if}
          {#if s.nativeRouted > 0}
            <p>
              {s.nativeRouted} name{s.nativeRouted === 1 ? "" : "s"} the engine cannot carry
              safely will go through the native path.
            </p>
          {/if}
          {#if preview.largeObjectsAsDlo}
            <p>
              Large objects commit as destination DLOs: their reads depend on container-listing
              consistency.
            </p>
          {/if}
          {#if preview.versionedDestination}
            <p class="warn">
              The destination container is versioned: every overwrite archives a version — this
              copy will accumulate versions by design.
            </p>
          {/if}
        </div>

        {#if mode === "mirror"}
          <div class="mirror num">
            <h3>Would delete at the destination</h3>
            <p class="would-delete">
              <strong>{s.destOnly}</strong> destination-only object{s.destOnly === 1 ? "" : "s"}
              · {formatBytes(s.destOnlyBytes)}
            </p>
            <p class="warn">
              A Mirror would remove these after the copy half verifies. Deletion is
              Aeronautes-native and manifest-only, and never acts on unprovable equality — but
              <strong>Mirror execution does not exist yet</strong>: this preview is the whole
              feature until the destructive-jobs phase.
            </p>
          </div>
        {/if}
      </div>
    {/if}

    <footer>
      {#if startedJobId}
        <span class="started">Job {startedJobId} started — follow it in the drawer.</span>
        <button class="primary" onclick={onClose}>Close</button>
      {:else}
        <button onclick={runPreview} disabled={previewing || !destContainer.trim()}>
          {previewing ? "Planning…" : "Preview"}
        </button>
        {#if mode === "copy"}
          <button class="primary" onclick={start} disabled={!preview || starting}>
            {starting ? "Starting…" : "Start copy"}
          </button>
        {/if}
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
    width: min(44rem, calc(100vw - 4rem));
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
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

  .modes {
    display: flex;
    gap: var(--space-2);
  }

  .modes [role="tab"] {
    font: inherit;
    font-size: var(--text-sm);
    padding: 2px var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-pill);
    background: none;
    color: var(--color-text-secondary);
    cursor: pointer;
  }

  .modes [role="tab"].active {
    color: var(--color-text);
    background: var(--color-bg-card);
    border-color: var(--color-text-faint);
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .scope {
    display: flex;
    align-items: flex-start;
    gap: var(--space-3);
    font-size: var(--text-sm);
  }

  .side {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .side-label {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .fields {
    flex: 1;
  }

  .fields select,
  .fields input {
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .arrow {
    margin-top: 1.1rem;
    color: var(--color-text-faint);
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

  .counts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    font-size: var(--text-sm);
  }

  .conservative {
    color: var(--color-warning);
  }

  .notes {
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
  }

  .notes p {
    margin: 0 0 2px;
  }

  .warn {
    color: var(--color-warning);
  }

  .mirror {
    border-top: 1px dashed var(--color-border);
    padding-top: var(--space-2);
  }

  .mirror h3 {
    margin: 0 0 var(--space-1);
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .would-delete {
    margin: 0 0 var(--space-1);
    font-size: var(--text-sm);
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

  footer .primary {
    background: var(--color-accent);
    color: var(--color-bg);
    border-color: var(--color-accent);
  }
</style>
