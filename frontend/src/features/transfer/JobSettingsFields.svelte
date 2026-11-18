<!-- The per-job settings every transfer dialog offers (§38.3): the §40
  profile and the typed overrides. A download segments nothing, so it
  leaves the chunk size out; only an upload chooses what a segmented file
  is published as (ADR 0111). -->
<script lang="ts">
  import { untrack } from "svelte";
  import type { JobSettingsInput } from "./jobSettings";

  let {
    settings = $bindable(),
    defaultProfile,
    chunk = true,
    largeObjects = false,
    transfer,
    onchange,
  }: {
    settings: JobSettingsInput;
    defaultProfile: string | undefined;
    chunk?: boolean;
    largeObjects?: boolean;
    transfer: "upload" | "download" | "copy";
    onchange: () => void;
  } = $props();

  // How a confirmed transfer decides what to replace. Nothing sets it yet;
  // it is written here, beside the settings, so the rule can be known
  // (operator, 18 November 2026; ADR 0122 decisions 4 and 12).
  const replacing: Record<typeof transfer, string> = {
    upload:
      "Once you confirm replacing, a name that exists is replaced when its size or modification time differs from the file's, as rclone copy decides; " +
      "one whose time alone differs keeps its bytes if its MD5 matches. " +
      "If any file in the upload is large enough to go in segments, sizes are ignored for the whole upload: " +
      "a file whose time matches is left even when its size differs, one whose time differs is still kept if its MD5 matches, " +
      "and a large object, which has no MD5, is replaced whenever its time differs.",
    download:
      "Once you confirm replacing, a file that exists is replaced when its size or modification time differs from the object's, as rclone copy decides; " +
      "one whose time alone differs keeps its bytes if its MD5 matches. A large object has no MD5, so its file is replaced whenever the time differs.",
    copy:
      "Once you confirm replacing, an object that exists is replaced unless both listings show the same size and ETag. " +
      "A large or empty object is read once on each side and left alone only when the store reports the same size and ETag for both.",
  };

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
    {#if largeObjects}
      <label>
        <span>Large objects</span>
        <select bind:value={settings.largeObjects} onchange={onchange}>
          <option value="">Bookmark's choice</option>
          <option value="slo">Static</option>
          <option value="dlo">Dynamic</option>
        </select>
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
    <p class="replacing"><span class="replacing-title">Replacing</span> {replacing[transfer]}</p>
  </div>
</details>

<style>
  .advanced summary {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    cursor: pointer;
  }

  /* Five fields do not fit one row at every text size or dialog width;
     they wrap rather than run past the dialog's edge. */
  .advanced-fields {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-3);
    padding-top: var(--space-2);
  }

  .replacing {
    flex-basis: 100%;
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .replacing-title {
    color: var(--color-text-secondary);
    font-weight: var(--weight-semibold);
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
