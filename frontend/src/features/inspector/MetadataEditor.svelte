<!-- Reusable metadata working-copy editor (§17): the parent owns the rows
  (seeded from the base set with rowsFromMeta) and binds them in, so it can
  read and rewrite the working copy itself; this editor shows a live
  semantic diff so removals are seen before they happen, refuses duplicate
  keys, and never talks to services. -->
<script lang="ts">
  import { diffMeta, isDiffEmpty, metaFromRows, type MetaRow } from "../../lib/metadataDiff";
  import { matchingKeys, splitMatch, type KeySuggestion } from "../../lib/metadataKeys";

  let {
    base,
    rows = $bindable(),
    suggestions = [],
    onRawEdit = undefined,
  }: {
    base: { [key: string]: string | undefined };
    rows: MetaRow[];
    /** Keys offered under a key field (ADR 0125 decision 2). */
    suggestions?: KeySuggestion[];
    /** Called with the row's index when the operator edits or removes that
     * row here: after the binding has updated an edited row, and before a
     * removed row goes. A parent with a structured surface over a row thus
     * learns of a raw edit itself rather than inferring it from the value
     * (ADR 0101 decision 7). */
    onRawEdit?: (index: number) => void;
  } = $props();

  const current = $derived(metaFromRows(rows));
  const diff = $derived(diffMeta(base, current.meta));

  function addRow() {
    rows = [...rows, { key: "", value: "" }];
  }

  function removeRow(index: number) {
    onRawEdit?.(index);
    rows = rows.filter((_, i) => i !== index);
  }

  // The key field whose suggestions are listed, and the one marked in it.
  let listing: number | null = $state(null);
  let marked = $state(0);
  const id = $props.id();

  function offered(index: number): KeySuggestion[] {
    const others = rows.filter((_, i) => i !== index).map((row) => row.key);
    return matchingKeys(suggestions, rows[index]?.key ?? "", others);
  }

  const shown = $derived(listing === null ? [] : offered(listing));

  function take(index: number, suggestion: KeySuggestion) {
    rows[index].key = suggestion.key;
    onRawEdit?.(index);
    listing = null;
  }

  // The list owns the arrow keys, Enter and Escape while it is open: the
  // window moves through the working list on the arrows, and Escape would
  // otherwise close the editor.
  function onKeyKeydown(event: KeyboardEvent, index: number) {
    if (listing !== index || shown.length === 0) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      marked = (marked + (event.key === "ArrowDown" ? 1 : shown.length - 1)) % shown.length;
    } else if (event.key === "Enter") {
      take(index, shown[marked]);
    } else if (event.key === "Escape") {
      listing = null;
    } else {
      return;
    }
    event.preventDefault();
    event.stopPropagation();
  }
</script>

<div class="editor">
  {#each rows as row, i (i)}
    <div class="row">
      <span class="key-field">
        <input
          class="key type-mono"
          bind:value={row.key}
          placeholder="key"
          aria-label="metadata key"
          role="combobox"
          aria-autocomplete="list"
          aria-expanded={listing === i && shown.length > 0}
          aria-controls={`${id}-keys`}
          aria-activedescendant={listing === i && shown.length > 0 ? `${id}-key-${marked}` : undefined}
          autocomplete="off"
          oninput={() => { onRawEdit?.(i); listing = i; marked = 0; }}
          onfocus={() => { listing = i; marked = 0; }}
          onblur={() => { if (listing === i) listing = null; }}
          onkeydown={(event) => onKeyKeydown(event, i)}
        />
        {#if listing === i && shown.length > 0}
          <ul class="suggestions" role="listbox" id={`${id}-keys`} aria-label="Suggested keys">
            {#each shown as suggestion, n (suggestion.key)}
              {@const [before, match, after] = splitMatch(suggestion.key, row.key)}
              <!-- The pointer goes down before the field would lose focus,
                so the choice is taken without the list closing first. -->
              <li
                id={`${id}-key-${n}`}
                role="option"
                aria-selected={n === marked}
                class:marked={n === marked}
                onpointerdown={(event) => { event.preventDefault(); take(i, suggestion); }}
              >
                <span class="type-mono">{before}<mark>{match}</mark>{after}</span>
                <span class="note">{suggestion.note}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </span>
      <input class="val" bind:value={row.value} placeholder="value" aria-label="metadata value" oninput={() => onRawEdit?.(i)} />
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

  /* The suggestion list spans the whole row, key to remove button, so it
    stays inside the editor however narrow the key field is. */
  .row {
    position: relative;
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

  .key-field {
    display: flex;
    min-width: 0;
  }

  .key-field .key {
    flex: 1;
  }

  .suggestions {
    position: absolute;
    top: calc(100% + 2px);
    left: 0;
    right: 0;
    z-index: 20;
    max-height: 14rem;
    overflow-y: auto;
    margin: 0;
    padding: var(--space-1) 0;
    list-style: none;
    background: var(--color-bg-card);
    border: 1px solid var(--color-border-strong);
    border-radius: var(--radius-control);
    box-shadow: var(--shadow-raised);
  }

  .suggestions li {
    display: flex;
    flex-direction: column;
    padding: 2px var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-text);
    cursor: pointer;
  }

  .suggestions li.marked,
  .suggestions li:hover {
    background: var(--color-bg-inset);
  }

  .suggestions mark {
    color: var(--color-text);
    background: var(--color-warning-soft);
    border-radius: 2px;
  }

  .suggestions .note {
    font-size: var(--text-xs);
    color: var(--color-text-faint);
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
