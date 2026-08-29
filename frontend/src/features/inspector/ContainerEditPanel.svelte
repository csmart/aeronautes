<!-- Container configuration edit mode (§65.2 item 7): metadata working
  copy, ACL editors, quotas, and versioning per the §21 pin — the mode
  select is capability-gated: modes the cluster is KNOWN to reject are
  disabled; an unknown /info leaves everything enabled with a note. -->
<script lang="ts">
  import MetadataEditor from "./MetadataEditor.svelte";
  import AclEditor from "./AclEditor.svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerDetailDTO } from "../../lib/api";

  let {
    browser,
    detail,
    onDone,
  }: { browser: BrowserState; detail: ContainerDetailDTO; onDone: () => void } = $props();

  let editor: MetadataEditor | undefined = $state();
  // The panel mounts fresh per edit session; initial capture is intended.
  /* svelte-ignore state_referenced_locally */
  let readAcl = $state(detail.readAcl);
  /* svelte-ignore state_referenced_locally */
  let writeAcl = $state(detail.writeAcl);
  /* svelte-ignore state_referenced_locally */
  let versioningMode = $state(detail.versioningMode || "off");
  /* svelte-ignore state_referenced_locally */
  let versioningArchive = $state(detail.versioningArchive);
  /* svelte-ignore state_referenced_locally */
  let quotaBytes = $state(detail.quotaBytes);
  /* svelte-ignore state_referenced_locally */
  let quotaCount = $state(detail.quotaCount);
  let saving = $state(false);
  let error: string | null = $state(null);
  let conflict = $state(false);
  let mismatch: string | null = $state(null);

  const caps = $derived(browser.capabilities);
  const capsKnown = $derived(caps?.source === "advertised");

  function modeAllowed(flag: string): boolean {
    return !capsKnown || (caps?.versioningFlags ?? []).includes(flag);
  }

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
      const result = await browser.saveContainer(
        {
          meta,
          readAcl,
          writeAcl,
          versioningMode,
          versioningArchive: versioningMode === "off" ? "" : versioningArchive,
          quotaBytes: Number(quotaBytes) || 0,
          quotaCount: Number(quotaCount) || 0,
        },
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
  <h3 class="card-title">Configure container</h3>

  <h4 class="sub">User metadata</h4>
  <MetadataEditor bind:this={editor} base={detail.meta ?? {}} />

  <AclEditor label="Read ACL" bind:value={readAcl} />
  <AclEditor label="Write ACL" bind:value={writeAcl} />

  <div class="grid">
    <label class="field">
      <span>Versioning</span>
      <select bind:value={versioningMode}>
        <option value="off">off</option>
        <option value="versions_location" disabled={!modeAllowed("x-versions-location")}>
          versions location
        </option>
        <option value="history_location" disabled={!modeAllowed("x-history-location")}>
          history location
        </option>
      </select>
    </label>
    {#if versioningMode !== "off"}
      <label class="field">
        <span>Archive container</span>
        <input class="type-mono" bind:value={versioningArchive} placeholder="container name" />
      </label>
    {/if}
    <label class="field">
      <span>Quota bytes (0 = none)</span>
      <input type="number" min="0" bind:value={quotaBytes} />
    </label>
    <label class="field">
      <span>Quota objects (0 = none)</span>
      <input type="number" min="0" bind:value={quotaCount} />
    </label>
  </div>
  {#if !capsKnown}
    <p class="note">
      /info could not be read: nothing is gated; the cluster will decide.
    </p>
  {/if}

  {#if conflict}
    <div class="banner conflict">
      <p>This container changed since you loaded it. Your edit was <strong>not</strong> written.</p>
      <div class="banner-actions">
        <button
          onclick={() => {
            browser.refreshContainerDetail();
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

  .sub {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    font-weight: var(--weight-medium);
  }

  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: var(--space-2);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    min-width: 0;
  }

  .field input,
  .field select {
    padding: 2px var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    min-width: 0;
  }

  .note {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-warning);
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
