<!-- The manifest-only delete confirm (§60 Phase 4, §30 honesty): says
  exactly what dies and exactly what survives, and where. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import { onMount } from "svelte";
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

  let deleting = $state(false);
  let error: string | null = $state(null);
  // §34/§35 segments option: proof-gated; Backstage force needs the
  // typed name.
  let withSegments = $state(false);
  let ownership = $state<SegmentOwnershipDTO | null>(null);
  let confirmText = $state("");

  onMount(async () => {
    try {
      // svelte-ignore state_referenced_locally
      ownership = await browser.checkSegmentOwnership(
        browser.containerName ?? "",
        detail.name,
      );
    } catch {
      ownership = null; // segments option simply unavailable
    }
  });

  const forceNeeded = $derived(withSegments && ownership !== null && !ownership.proven);
  const armed = $derived(
    !withSegments || (ownership !== null && (ownership.proven || confirmText === detail.name)),
  );

  async function confirm() {
    deleting = true;
    error = null;
    try {
      if (withSegments) {
        // The proven path's confirmation is the armed dialog itself; the
        // Go side still requires the exact name, so send it. The force
        // path sends what the operator typed.
        const confirm = ownership?.proven ? detail.name : confirmText;
        await browser.deleteManifestWithSegments(
          browser.containerName ?? "",
          detail.name,
          forceNeeded,
          confirm,
        );
      } else {
        await browser.deleteManifest();
      }
      onClose();
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      deleting = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="alertdialog" aria-label="Delete manifest"
    use:trapFocus>
    <h2 class="title">Delete {detail.class.toUpperCase()} manifest</h2>
    <p class="what">
      <span class="type-mono">{detail.name}</span> will be deleted. The logical object stops
      existing: reads of this name will fail.
    </p>
    {#if !withSegments}
      <p class="keeps">
        <strong>Every segment survives.</strong> The data remains at
        <span class="type-mono">{segmentsAt}</span> and becomes orphaned unless another manifest
        references it.
      </p>
    {/if}

    {#if ownership}
      <label class="segments-opt">
        <input type="checkbox" bind:checked={withSegments} />
        <span>
          Also delete the segments ({ownership.target})
          {#if ownership.proven}
            — <strong class="proven">proven exclusively this app's</strong> (§28.6 ownership
            record)
          {/if}
        </span>
      </label>
      {#if forceNeeded}
        <p class="force-warn">
          These segments are <strong>not proven exclusively ours</strong>: sharing cannot be
          disproved from one manifest, and another manifest may still reference them. This is a
          Backstage override. Type the object name to arm it:
        </p>
        <input
          class="force-confirm type-mono"
          bind:value={confirmText}
          placeholder={detail.name}
        />
      {/if}
    {/if}
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}
    <div class="actions">
      <button class="secondary" onclick={onClose} disabled={deleting}>Cancel</button>
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

  .proven {
    color: var(--color-success);
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
