<!-- Reusable metadata working-copy editor (§17): the parent owns the rows
  (seeded from the base set with rowsFromMeta) and binds them in, so it can
  read and rewrite the working copy itself; this editor shows a live
  semantic diff so removals are seen before they happen, refuses duplicate
  keys, and never talks to services. -->
<script lang="ts">
  import { diffMeta, isDiffEmpty, metaFromRows, type MetaRow } from "../../lib/metadataDiff";

  let {
    base,
    rows = $bindable(),
  }: { base: { [key: string]: string | undefined }; rows: MetaRow[] } = $props();

  const current = $derived(metaFromRows(rows));
  const diff = $derived(diffMeta(base, current.meta));

  /** The complete intended metadata set, or a duplicate-key error. */
  export function value(): ReturnType<typeof metaFromRows> {
    return metaFromRows(rows);
  }

  export function isDirty(): boolean {
    return !isDiffEmpty(diff);
  }

  function addRow() {
    rows = [...rows, { key: "", value: "" }];
  }

  function removeRow(index: number) {
    rows = rows.filter((_, i) => i !== index);
  }
</script>

<div class="editor">
  {#each rows as row, i (i)}
    <div class="row">
      <input class="key type-mono" bind:value={row.key} placeholder="key" aria-label="metadata key" />
      <input class="val" bind:value={row.value} placeholder="value" aria-label="metadata value" />
      <button class="remove" title="Remove key" onclick={() => removeRow(i)}>×</button>
    </div>
  {/each}
  <button class="add" onclick={addRow}>+ add key</button>

  {#if current.duplicate !== null}
    <p class="dup">Duplicate key “{current.duplicate}” — metadata names are case-insensitive.</p>
  {:else if !isDiffEmpty(diff)}
    <p class="diff num">
      {#each diff.added as key (key)}<span class="chip added">+ {key}</span>{/each}
      {#each diff.changed as key (key)}<span class="chip changed">~ {key}</span>{/each}
      {#each diff.removed as key (key)}<span class="chip removed">− {key}</span>{/each}
    </p>
    {#if diff.removed.length > 0}
      <p class="warn">
        Removed keys are erased on save — Swift replaces the whole metadata set.
      </p>
    {/if}
  {/if}
</div>

<style>
  .editor {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
  }

  .row {
    display: grid;
    grid-template-columns: 1fr 1.4fr auto;
    gap: var(--space-1);
  }

  .row input {
    padding: 2px var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    min-width: 0;
  }

  .key {
    font-family: var(--font-mono);
  }

  .remove {
    padding: 0 var(--space-2);
    font: inherit;
    color: var(--color-text-faint);
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .remove:hover {
    color: var(--color-danger);
    border-color: var(--color-border);
  }

  .add {
    align-self: flex-start;
    padding: 1px var(--space-2);
    font: inherit;
    font-size: var(--text-xs);
    color: var(--color-accent-strong);
    background: none;
    border: 1px dashed var(--color-border-strong);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .diff {
    margin: var(--space-1) 0 0;
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1);
  }

  .chip {
    padding: 0 var(--space-1);
    font-size: var(--text-xs);
    border-radius: var(--radius-pill);
    border: 1px solid;
  }

  .chip.added {
    color: var(--color-success);
    background: var(--color-success-soft);
    border-color: var(--color-success);
  }

  .chip.changed {
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-color: var(--color-warning);
  }

  .chip.removed {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-color: var(--color-danger);
  }

  .dup,
  .warn {
    margin: var(--space-1) 0 0;
    font-size: var(--text-xs);
    color: var(--color-danger);
  }

  .warn {
    color: var(--color-warning);
  }
</style>
