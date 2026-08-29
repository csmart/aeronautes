<!-- SwiftStack-style ACL editor (§19): the ACL string as structured
  entry chips with add/remove, and a raw mode showing the exact string —
  both edit the same value; nothing is reinterpreted. -->
<script lang="ts">
  let {
    label,
    value = $bindable(""),
  }: { label: string; value: string } = $props();

  let raw = $state(false);
  let newEntry = $state("");

  const entries = $derived(
    value
      .split(",")
      .map((e) => e.trim())
      .filter((e) => e !== ""),
  );

  function removeEntry(index: number) {
    value = entries.filter((_, i) => i !== index).join(",");
  }

  function addEntry() {
    const entry = newEntry.trim();
    if (entry === "") {
      return;
    }
    value = [...entries, entry].join(",");
    newEntry = "";
  }
</script>

<div class="acl">
  <div class="head">
    <span class="label">{label}</span>
    <button class="mode" onclick={() => (raw = !raw)}>{raw ? "structured" : "raw"}</button>
  </div>
  {#if raw}
    <input class="rawline type-mono" bind:value placeholder="e.g. .r:*,.rlistings,account:user" />
  {:else}
    <div class="chips">
      {#each entries as entry, i (i)}
        <span class="chip type-mono">
          {entry}
          <button class="x" title="Remove entry" onclick={() => removeEntry(i)}>×</button>
        </span>
      {/each}
      <input
        class="entry type-mono"
        bind:value={newEntry}
        placeholder="add entry…"
        onkeydown={(e) => e.key === "Enter" && addEntry()}
        onblur={addEntry}
      />
    </div>
  {/if}
</div>

<style>
  .acl {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
  }

  .label {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
  }

  .mode {
    font: inherit;
    font-size: var(--text-xs);
    color: var(--color-accent-strong);
    background: none;
    border: none;
    cursor: pointer;
  }

  .rawline,
  .entry {
    padding: 2px var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    font-family: var(--font-mono);
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    min-width: 0;
  }

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
    align-items: center;
  }

  .chip {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    padding: 0 var(--space-1);
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
    background: var(--color-bg-inset);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-pill);
  }

  .x {
    font: inherit;
    color: var(--color-text-faint);
    background: none;
    border: none;
    cursor: pointer;
    padding: 0;
  }

  .x:hover {
    color: var(--color-danger);
  }

  .entry {
    flex: 1;
    min-width: 8rem;
  }
</style>
