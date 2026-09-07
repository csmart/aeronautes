<!-- Container configuration edit mode (§65.2 item 7): metadata working
  copy, ACL editors, quotas, and versioning per the pin — the mode
  select is capability-gated: modes the cluster is KNOWN to reject are
  disabled; an unknown /info leaves everything enabled with a note. -->
<script lang="ts">
  import { onDestroy } from "svelte";
  import MetadataEditor from "./MetadataEditor.svelte";
  import AclEditor from "./AclEditor.svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerDetailDTO } from "../../lib/api";

  let {
    browser,
    detail,
    onDone,
  }: { browser: BrowserState; detail: ContainerDetailDTO; onDone: () => void } = $props();

  // Keep the displayed working copy and its fingerprint from the same portrait.
  // svelte-ignore state_referenced_locally
  const target = { browser, scope: browser.scopeRevision, selection: browser.detailIdentity, detail };
  let closed = $state(false);
  function isCurrent() {
    return !closed && browser === target.browser && !browser.connecting
      && browser.scopeRevision === target.scope && browser.detailIdentity === target.selection;
  }
  const currentBaseline = $derived(isCurrent() && browser.containerDetail === target.detail && detail === target.detail);
  function close() {
    if (closed) return;
    closed = true;
    onDone();
  }
  onDestroy(() => { closed = true; });

  let editor: MetadataEditor | undefined = $state();
  // The panel mounts fresh per edit session; initial capture is intended.
  /* svelte-ignore state_referenced_locally */
  let readAcl = $state(detail.readAcl);
  /* svelte-ignore state_referenced_locally */
  let writeAcl = $state(detail.writeAcl);
  /* svelte-ignore state_referenced_locally */
  let versioningMode = $state(detail.versioningMode || "off");
  // svelte-ignore state_referenced_locally
  const initialVersioningMode = detail.versioningMode || "off";
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

  // A read ACL that grants the world (.r:* / .r:*,.rlistings) turns
  // private data public in one Save; require an explicit acknowledgement
  // first (DESIGN §19.1).
  const grantsPublic = $derived(/(^|,)\s*\.r:\*/.test(readAcl));
  let publicAck = $state(false);

  // Modern object_versioning has no safe editable representation in v1
  // (the header set differs from the legacy location modes); a
  // container using it is read-only here rather than silently rewritten
  // into a location shape (§65.10 tracks the edit path).
  const modernVersioning = $derived(initialVersioningMode === "enabled");

  async function save(force: boolean) {
    if (saving || !currentBaseline) return;
    if (modernVersioning) {
      error = "This container uses modern object versioning, which v1 cannot edit safely — manage it with swift/CLI for now.";
      return;
    }
    const { meta, duplicate } = editor!.value();
    if (duplicate !== null) {
      error = `duplicate key "${duplicate}"`;
      return;
    }
    if (grantsPublic && !publicAck) {
      error = "This read ACL makes the container publicly readable. Tick the box below to confirm.";
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
          ackPublic: grantsPublic && publicAck,
        },
        force,
      );
      if (!isCurrent()) return;
      if (result.outcome === "applied") {
        close();
      } else if (result.outcome === "conflict") {
        conflict = true;
      } else {
        mismatch = result.mismatch;
      }
    } catch (err) {
      if (isCurrent()) error = err instanceof Error ? err.message : String(err);
    } finally {
      if (isCurrent()) saving = false;
    }
  }
</script>

<section class="card">
  <h3 class="card-title">Configure container</h3>

  <fieldset class="fields" aria-label="Container configuration" disabled={saving || !currentBaseline}>
    <h4 class="sub">User metadata</h4>
    <MetadataEditor bind:this={editor} base={target.detail.meta ?? {}} />

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
          {#if versioningMode === "enabled"}
            <option value="enabled" disabled selected>in-container versions (read-only in v1)</option>
          {/if}
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
  </fieldset>
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
            if (saving || !isCurrent()) return;
            browser.refreshContainerDetail();
            close();
          }}>Reload theirs</button
        >
        <button class="danger" onclick={() => save(true)} disabled={saving || !currentBaseline}>Apply mine anyway</button>
      </div>
    </div>
  {/if}
  {#if mismatch}
    <p class="banner mismatch">Written, but verify found: {mismatch}</p>
  {/if}
  {#if error}
    <p class="banner mismatch">{error}</p>
  {/if}
  {#if !currentBaseline && !saving}
    <p class="banner mismatch">Details changed. Close and reopen the editor before saving.</p>
  {/if}

  <div class="actions">
    <button onclick={close} disabled={saving}>Cancel</button>
    {#if grantsPublic}
      <label class="public-ack">
        <input type="checkbox" bind:checked={publicAck} disabled={saving || !currentBaseline} />
        I understand this makes <span class="type-mono">{target.detail.name}</span> publicly readable.
      </label>
    {/if}
    <button class="primary" onclick={() => save(false)} disabled={saving || !currentBaseline}>
      {saving ? "Saving…" : "Save"}
    </button>
  </div>
</section>

<style>
  .fields {
    border: 0;
    margin: 0;
    padding: 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

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
