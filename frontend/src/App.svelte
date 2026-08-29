<script lang="ts">
  import { onMount } from "svelte";
  import { AppService } from "../bindings/github.com/csmart/aeronautes/internal/app";
  import type { Info } from "../bindings/github.com/csmart/aeronautes/internal/app";
  import { getTheme, setTheme, type Theme } from "./design/tokens";

  // Fetching Info over the bindings bridge is the hello-window proof that
  // the typed Go service transport works (§55, §65 commit 9).
  let info: Info | null = $state(null);
  let bridgeError: string | null = $state(null);
  let theme: Theme = $state(getTheme());

  onMount(async () => {
    try {
      info = await AppService.Info();
    } catch (err) {
      bridgeError = String(err);
    }
  });

  const cycle: Record<Theme, Theme> = {
    system: "light",
    light: "dark",
    dark: "system",
  };

  function cycleTheme() {
    theme = cycle[theme];
    setTheme(theme);
  }
</script>

<main>
  <div class="identity">
    <h1 class="type-display wordmark">Aeronautes</h1>
    <p class="tagline">Desktop operator workbench for OpenStack Swift</p>
    {#if info}
      <p class="status num">
        shell {info.version} · Wails {info.wails} · service bridge OK
      </p>
    {:else if bridgeError}
      <p class="status error">service bridge failed: {bridgeError}</p>
    {:else}
      <p class="status">connecting to Go…</p>
    {/if}
  </div>

  <button class="theme-toggle" onclick={cycleTheme}>
    theme: <span class="type-mono">{theme}</span>
  </button>
</main>

<style>
  main {
    height: 100%;
    display: grid;
    place-items: center;
  }

  .identity {
    text-align: center;
  }

  .wordmark {
    margin: 0;
    font-size: var(--text-2xl);
    color: var(--color-text);
  }

  .tagline {
    margin: var(--space-2) 0 var(--space-5);
    color: var(--color-text-secondary);
  }

  .status {
    font-size: var(--text-sm);
    color: var(--color-text-faint);
  }

  .status.error {
    color: var(--color-danger);
  }

  .theme-toggle {
    position: fixed;
    right: var(--space-4);
    bottom: var(--space-4);
    padding: var(--space-1) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-pill);
    box-shadow: var(--shadow-card);
    cursor: pointer;
    transition:
      border-color var(--motion-fast) var(--ease-out),
      color var(--motion-fast) var(--ease-out);
  }

  .theme-toggle:hover {
    color: var(--color-accent-strong);
    border-color: var(--color-accent-border);
  }
</style>
