<!-- Account metadata edit mode: the account's user-metadata working copy
  and its quota, over the same working-copy editor the container and object
  editors use (§17, §18). The secret TempURL key slots are never shown or
  edited here; the adapter refuses them, so this editor cannot reach them
  (K-SEC-006). -->
<script lang="ts">
  import { onDestroy } from "svelte";
  import MetadataEditor from "./MetadataEditor.svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { AccountDTO } from "../../lib/api";
  import { metaFromRows, rowsFromMeta, type MetaRow } from "../../lib/metadataDiff";

  let {
    browser,
    account,
    onDone,
  }: { browser: BrowserState; account: AccountDTO; onDone: () => void } = $props();

  // Keep the displayed working copy and its fingerprint from the same portrait.
  // svelte-ignore state_referenced_locally
  const target = { browser, scope: browser.scopeRevision, account };
  let closed = $state(false);
  function isCurrent() {
    return !closed && browser === target.browser && !browser.connecting
      && browser.scopeRevision === target.scope;
  }
  const currentBaseline = $derived(isCurrent() && browser.account === target.account && account === target.account);
  function close() {
    if (closed) return;
    closed = true;
    onDone();
  }
  onDestroy(() => { closed = true; });

  // The panel mounts fresh per edit session; initial capture is intended.
  /* svelte-ignore state_referenced_locally */
  let rows: MetaRow[] = $state(rowsFromMeta(account.meta ?? {}));
  /* svelte-ignore state_referenced_locally */
  let quotaBytes = $state(account.quotaBytes);
  let saving = $state(false);
  let error: string | null = $state(null);
  let conflict = $state(false);
  let mismatch: string | null = $state(null);

  async function save(force: boolean) {
    if (saving || !currentBaseline) return;
    const { meta, duplicate } = metaFromRows(rows);
    if (duplicate !== null) {
      error = `duplicate key "${duplicate}"`;
      return;
    }
    saving = true;
    error = null;
    conflict = false;
    mismatch = null;
    try {
      const result = await browser.saveAccountMetadata({ meta, quotaBytes: Number(quotaBytes) || 0 }, force);
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
  <h3 class="card-title">Edit account metadata</h3>

  <fieldset class="fields" aria-label="Account metadata" disabled={saving || !currentBaseline}>
    <h4 class="sub">User metadata</h4>
    <MetadataEditor suggestions={browser.keySuggestions("account")} base={target.account.meta ?? {}} bind:rows />

    <label class="field">
      <span>Quota bytes (0 = none)</span>
      <input type="number" min="0" bind:value={quotaBytes} />
    </label>
  </fieldset>

  {#if conflict}
    <div class="banner conflict">
      <p>This account changed since you loaded it. Your edit was <strong>not</strong> written.</p>
      <div class="banner-actions">
        <button
          onclick={async () => {
            if (saving || !isCurrent()) return;
            saving = true;
            error = null;
            const reloaded = await browser.refreshAccount();
            if (!isCurrent()) return;
            saving = false;
            if (reloaded) {
              close();
            } else {
              error = browser.accountError ?? "Reloading the account failed. Try again.";
            }
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
    <p class="banner mismatch">The account changed. Close and reopen the editor before saving.</p>
  {/if}

  <div class="actions">
    <button onclick={close} disabled={saving}>Cancel</button>
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

  .field {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    min-width: 0;
  }

  .field input {
    padding: 2px var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    min-width: 0;
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
