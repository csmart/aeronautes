<!-- The per-job settings every transfer dialog offers (§38.3): the §40
  profile and the typed overrides. A download segments nothing, so it
  leaves the chunk size out. -->
<script lang="ts">
  import { untrack } from "svelte";
  import type { JobSettingsInput } from "./jobSettings";

  let {
    settings = $bindable(),
    defaultProfile,
    chunk = true,
    onchange,
  }: { settings: JobSettingsInput; defaultProfile: string | undefined; chunk?: boolean; onchange: () => void } = $props();

  // A dialog can open before the application settings have been read, so
  // the saved default may arrive after the profile was seeded with Auto.
  // It replaces the seed until the operator picks a profile themselves.
  let profileChosen = false;
  $effect(() => {
    const saved = defaultProfile;
    untrack(() => {
      if (saved && !profileChosen && settings.profile !== saved) {
        settings.profile = saved;
        onchange();
      }
    });
  });

  function chooseProfile() {
    profileChosen = true;
    onchange();
  }
</script>

<details class="advanced">
  <summary>Per-job settings</summary>
  <div class="advanced-fields num">
    <label>
      <span>Profile</span>
      <select bind:value={settings.profile} onchange={chooseProfile}>
        <option value="auto">Auto</option>
        <option value="balanced">Balanced</option>
        <option value="aggressive">Aggressive</option>
        <option value="custom">Custom</option>
      </select>
    </label>
    {#if chunk}
      <label>
        <span>Chunk size</span>
        <input class="type-mono" placeholder="5Gi (default)" bind:value={settings.chunkSize} oninput={onchange} />
      </label>
    {/if}
    <label>
      <span>Transfers</span>
      <input class="type-mono" placeholder="4 (default)" bind:value={settings.transfers} oninput={onchange} />
    </label>
    <label>
      <span>Bandwidth</span>
      <input class="type-mono" placeholder="unlimited" bind:value={settings.bwLimit} oninput={onchange} />
    </label>
    <label>
      <span>Buffer size</span>
      <input class="type-mono" placeholder="16Mi (default)" bind:value={settings.bufferSize} oninput={onchange} />
    </label>
  </div>
</details>

<style>
  .advanced summary {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .advanced-fields {
    display: flex;
    gap: var(--space-3);
    padding-top: var(--space-2);
  }

  .advanced-fields label {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .advanced-fields select {
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .advanced-fields input {
    width: 8rem;
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }
</style>
