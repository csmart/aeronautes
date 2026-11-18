<!-- A value row's copy button (ADR 0125 decision 4): always drawn, faint
  until the pointer is on it, so nothing appears or moves on hover. What
  happened is said beside it for a moment, drawn over the row rather than
  in it, so the row keeps its width. -->
<script lang="ts">
  let { label, value, copy }: { label: string; value: string; copy: (text: string) => Promise<void> } = $props();

  let said: string | null = $state(null);
  let clear: ReturnType<typeof setTimeout> | undefined;

  async function run() {
    try {
      await copy(value);
      said = "Copied";
    } catch (error) {
      said = `Not copied: ${error instanceof Error ? error.message : String(error)}`;
    }
    clearTimeout(clear);
    clear = setTimeout(() => (said = null), 1500);
  }

  $effect(() => () => clearTimeout(clear));
</script>

<span class="copy-value">
  <button class="copy" type="button" title={`Copy ${label}`} aria-label={`Copy ${label}`} onclick={run}>
    <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
      <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.3" />
      <path d="M3.5 10.5h-0.5a1 1 0 0 1-1-1v-6.5a1 1 0 0 1 1-1h6.5a1 1 0 0 1 1 1v0.5" fill="none" stroke="currentColor" stroke-width="1.3" />
    </svg>
  </button>
  {#if said}
    <span class="said" role="status">{said}</span>
  {/if}
</span>

<style>
  .copy-value {
    position: relative;
    flex: none;
    display: inline-flex;
  }

  .copy {
    display: grid;
    place-items: center;
    width: 1.2rem;
    height: 1.2rem;
    padding: 0;
    color: var(--color-text-faint);
    background: none;
    border: none;
    border-radius: var(--radius-control);
    cursor: pointer;
    opacity: 0.55;
  }

  .copy:hover,
  .copy:focus-visible {
    opacity: 1;
    color: var(--color-accent-strong);
    background: var(--color-bg-inset);
  }

  .said {
    position: absolute;
    right: 100%;
    top: 50%;
    transform: translateY(-50%);
    margin-right: var(--space-1);
    padding: 0 var(--space-1);
    font-size: var(--text-xs);
    white-space: nowrap;
    color: var(--color-text-invert);
    background: var(--color-text);
    border-radius: var(--radius-control);
  }
</style>
