<script lang="ts">
  import { onMount } from "svelte";
  import { AppService } from "../bindings/github.com/csmart/aeronautes/internal/app";
  import type { Info } from "../bindings/github.com/csmart/aeronautes/internal/app";

  // Fetching Info over the bindings bridge is the hello-window proof that
  // the typed Go service transport works (§55, §65 commit 9).
  let info: Info | null = $state(null);
  let bridgeError: string | null = $state(null);

  onMount(async () => {
    try {
      info = await AppService.Info();
    } catch (err) {
      bridgeError = String(err);
    }
  });
</script>

<main>
  <h1 class="wordmark">Aeronautes</h1>
  <p class="tagline">Desktop operator workbench for OpenStack Swift</p>
  {#if info}
    <p class="status">
      shell {info.version} · Wails {info.wails} · service bridge OK
    </p>
  {:else if bridgeError}
    <p class="status error">service bridge failed: {bridgeError}</p>
  {:else}
    <p class="status">connecting to Go…</p>
  {/if}
</main>

<style>
  main {
    text-align: center;
  }

  .wordmark {
    margin: 0;
    font-size: 2.6rem;
    font-weight: 650;
    letter-spacing: 0.01em;
  }

  .tagline {
    margin: 0.5rem 0 2rem;
    color: #b8ac9e;
  }

  .status {
    font-size: 0.85rem;
    color: #8d8375;
  }

  .status.error {
    color: #d98a7e;
  }
</style>
