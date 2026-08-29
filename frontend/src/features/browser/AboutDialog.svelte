<!-- About (§65.6): identity, the pinned engine, and dependency
  licences — fetched live so it never drifts from the binary. -->
<script lang="ts">
  import { onMount } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import { liveApi, type About } from "../../lib/api";

  let { onClose }: { onClose: () => void } = $props();
  let about = $state<About | null>(null);
  let error: string | null = $state(null);

  onMount(async () => {
    try {
      about = await liveApi.about();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  });
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="dialog" aria-label="About Aeronautes" tabindex="-1" use:trapFocus>
    <header>
      <h2>About Aeronautes</h2>
      <button class="close" onclick={onClose} aria-label="Close">✕</button>
    </header>
    {#if error}
      <p class="error">{error}</p>
    {:else if about}
      <p class="line num">
        Version <span class="type-mono">{about.version}</span> · Wails
        <span class="type-mono">{about.wails}</span> · pinned rclone
        <span class="type-mono">{about.rclonePin}</span> · secrets: {about.secretBackend}
      </p>
      <p class="line">{about.license}</p>
      <h3>Dependency licences</h3>
      <div class="licenses num">
        {#each about.licenses ?? [] as entry (entry.name)}
          <span class="type-mono lname">{entry.name}</span>
          <span class="ltype">{entry.license}</span>
          <span class="lurl type-mono">{entry.url}</span>
        {/each}
      </div>
    {:else}
      <p class="line">Loading…</p>
    {/if}
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
    width: min(38rem, calc(100vw - 4rem));
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  h2 {
    margin: 0;
    font-size: var(--text-base);
  }

  h3 {
    margin: var(--space-2) 0 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .line {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .licenses {
    display: grid;
    grid-template-columns: minmax(12rem, 1.4fr) 7rem minmax(10rem, 1.6fr);
    gap: 2px var(--space-3);
    font-size: var(--text-xs);
    max-height: 14rem;
    overflow-y: auto;
  }

  .lname,
  .lurl {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .ltype,
  .lurl {
    color: var(--color-text-faint);
  }
</style>
