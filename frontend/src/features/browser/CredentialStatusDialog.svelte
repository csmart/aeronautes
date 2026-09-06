<script lang="ts">
  import { onMount } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "./browserState.svelte";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  onMount(() => {
    void browser.loadCredentialStatus();
  });
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div
    class="dialog"
    role="dialog"
    aria-modal="true"
    aria-labelledby="credential-status-title"
    tabindex="-1"
    use:trapFocus
  >
    <header>
      <h2 id="credential-status-title">Credential cleanup</h2>
      <button onclick={onClose} aria-label="Close credential cleanup">✕</button>
    </header>
    <div aria-live="polite" aria-busy={browser.credentialStatusLoading}>
      {#if browser.credentialStatusLoading}
        <p>Checking recorded cleanup work…</p>
      {:else if browser.credentialStatusError || !browser.credentialStatus}
        <p class="error">Cleanup status is unavailable. Refresh to try again.</p>
      {:else}
        {@const status = browser.credentialStatus}
        <dl class="num">
          <dt>Old key records</dt><dd>{status.retired}</dd>
          <dt>Unfinished saves</dt><dd>{status.unfinished}</dd>
          <dt>Saves in progress</dt><dd>{status.saving}</dd>
        </dl>
        {#if status.retired + status.unfinished > 0}
          <p>Recorded key changes await cleanup.</p>
        {:else}
          <p>No pending credential cleanup is recorded.</p>
        {/if}
      {/if}
    </div>
    <p>
      Keys may remain in credential storage after replacement, an unsuccessful save
      or bookmark deletion. Cleanup is not available in this version.
    </p>
    <p>
      These counts describe recorded work and cannot confirm which keys still exist
      in the OS keychain or a previous session.
    </p>
    <footer>
      <button onclick={() => browser.loadCredentialStatus()} disabled={browser.credentialStatusLoading}>
        Refresh
      </button>
      <button onclick={onClose}>Close</button>
    </footer>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: color-mix(in srgb, var(--color-bg) 60%, transparent);
    z-index: 30;
  }

  .dialog {
    width: min(30rem, calc(100vw - 4rem));
    max-height: calc(100vh - 4rem);
    overflow-y: auto;
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  header,
  footer {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  header {
    justify-content: space-between;
  }

  footer {
    justify-content: flex-end;
  }

  h2 {
    margin: 0;
    font-size: var(--text-base);
  }

  p,
  dl {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  dl {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: var(--space-1) var(--space-3);
    margin-bottom: var(--space-2);
  }

  dd {
    margin: 0;
  }

  .error {
    color: var(--color-danger);
  }

  button {
    padding: var(--space-1) var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
