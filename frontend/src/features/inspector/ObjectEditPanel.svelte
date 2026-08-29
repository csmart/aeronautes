<!-- Object metadata edit mode (§65.2 item 6): working copy → diff →
  save with preflight; conflict offers reload-theirs or apply-mine
  (a deliberate overwrite that skips the fingerprint check); a verify
  mismatch is shown red, never swallowed. -->
<script lang="ts">
  import MetadataEditor from "./MetadataEditor.svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ObjectDetailDTO } from "../../lib/api";

  let {
    browser,
    detail,
    onDone,
  }: { browser: BrowserState; detail: ObjectDetailDTO; onDone: () => void } = $props();

  let editor: MetadataEditor | undefined = $state();
  // The panel mounts fresh per edit session; initial capture is intended.
  /* svelte-ignore state_referenced_locally */
  let contentType = $state(detail.contentType);
  /* svelte-ignore state_referenced_locally */
  let deleteAt = $state(detail.deleteAt);
  let saving = $state(false);
  let error: string | null = $state(null);
  let conflict = $state(false);
  let mismatch: string | null = $state(null);

  async function save(force: boolean) {
    const { meta, duplicate } = editor!.value();
    if (duplicate !== "") {
      error = `duplicate key "${duplicate}"`;
      return;
    }
    saving = true;
    error = null;
    conflict = false;
    mismatch = null;
    try {
      const result = await browser.saveObjectMetadata(
        { meta, contentType, deleteAt },
        force,
      );
      if (result.outcome === "applied") {
        onDone();
      } else if (result.outcome === "conflict") {
        conflict = true;
      } else {
        mismatch = result.mismatch;
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      saving = false;
    }
  }
</script>

<section class="card">
  <h3 class="card-title">Edit metadata</h3>

  <MetadataEditor bind:this={editor} base={detail.meta ?? {}} />

  <label class="field">
    <span>Content type</span>
    <input class="type-mono" bind:value={contentType} />
  </label>
  <label class="field">
    <span>Delete at <span class="hint">(RFC3339, empty = never)</span></span>
    <input class="type-mono" bind:value={deleteAt} placeholder="2027-01-01T00:00:00Z" />
  </label>

  {#if conflict}
    <div class="banner conflict">
      <p>
        This object changed since you loaded it. Your edit was <strong>not</strong> written.
      </p>
      <div class="banner-actions">
        <button
          onclick={() => {
            browser.refreshDetail();
            onDone();
          }}>Reload theirs</button
        >
        <button class="danger" onclick={() => save(true)}>Apply mine anyway</button>
      </div>
    </div>
  {/if}
  {#if mismatch}
    <p class="banner mismatch">Written, but verify found: {mismatch}</p>
  {/if}
  {#if error}
    <p class="banner mismatch">{error}</p>
  {/if}

  <div class="actions">
    <button onclick={onDone} disabled={saving}>Cancel</button>
    <button class="primary" onclick={() => save(false)} disabled={saving}>
      {saving ? "Saving…" : "Save"}
    </button>
  </div>
</section>

<style>
  .card {
    padding: var(--space-3);
    background: var(--color-bg-card);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-card);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .card-title {
    margin: 0;
    font-size: var(--text-sm);
    font-weight: var(--weight-semibold);
    color: var(--color-accent-strong);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .field input {
    padding: 2px var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  .hint {
    color: var(--color-text-faint);
  }

  .banner {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-sm);
    border-radius: var(--radius-control);
  }

  .banner.conflict {
    color: var(--color-warning);
    background: var(--color-warning-soft);
  }

  .banner.conflict p {
    margin: 0 0 var(--space-2);
  }

  .banner.mismatch {
    color: var(--color-danger);
    background: var(--color-danger-soft);
  }

  .banner-actions {
    display: flex;
    gap: var(--space-2);
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }

  button {
    padding: 2px var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  button.primary {
    color: var(--color-text-invert);
    background: var(--color-accent);
    border-color: var(--color-accent-strong);
  }

  button.danger {
    color: var(--color-danger);
    border-color: var(--color-danger);
    background: none;
  }

  button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
