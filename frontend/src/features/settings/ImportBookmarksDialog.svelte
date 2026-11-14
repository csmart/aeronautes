<!-- Import bookmarks from rclone.conf or clouds.yaml (ADR 0114): the
  preview Go read, with no secret; the operator ticks rows; the save and
  the verification after it, which goes on if the dialog is closed. -->
<script lang="ts">
  import { onDestroy } from "svelte";
  import type { ImportOutcomeDTO } from "../../lib/api";
  import type { ImportState } from "../browser/importState.svelte";
  import { trapFocus } from "../../lib/trapFocus";

  let {
    imports,
    onClose,
    onEditBookmark,
  }: { imports: ImportState; onClose: () => void; onEditBookmark: (bookmarkId: string) => void } = $props();
  const id = $props.id();

  onDestroy(() => imports.stop());

  function close() {
    imports.stop();
    onClose();
  }

  // A row saved from a source with no password has no key to connect with
  // until one is entered in its editor (ADR 0118 decision 4).
  function hadSecret(index: number): boolean {
    return imports.preview?.rows.find((row) => row.index === index)?.hasSecret ?? true;
  }

  function outcomeText(outcome: ImportOutcomeDTO): string {
    switch (outcome.verify) {
      case "verified":
        return "Imported and verified.";
      case "pending":
        return "Imported; verifying…";
      case "failed":
        return `Imported, but did not verify: ${outcome.reason}.`;
      case "no_secret":
        return "Imported without a password; it asks for one when first used.";
      case "not_saved":
        return `Not imported: ${outcome.reason}.`;
      default:
        return "Imported.";
    }
  }

  const count = $derived(imports.chosen.length);
  const pending = $derived(imports.result?.outcomes.filter((o) => o.verify === "pending").length ?? 0);
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="dialog" aria-modal="true" aria-labelledby={`${id}-title`} tabindex="-1" use:trapFocus={close}>
    <header>
      <h2 id={`${id}-title`}>Import bookmarks</h2>
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>
    <div class="body">
      {#if imports.preview}
        <p class="source">From <span class="type-mono">{imports.preview.fileName}</span>. Passwords stay in the credential store and are never shown.</p>
      {/if}
      {#if imports.error}
        <p class="callout danger" role="alert">{imports.error}</p>
      {/if}

      {#if imports.result}
        <ul class="outcomes" aria-label="Import results">
          {#each imports.result.outcomes as outcome (outcome.index)}
            <li class:failed={outcome.verify === "failed" || outcome.verify === "not_saved"}>
              <span class="name">{outcome.name}</span>
              <span class="what">{outcomeText(outcome)}</span>
              {#if outcome.verify === "failed" && outcome.bookmarkId}
                <button onclick={() => onEditBookmark(outcome.bookmarkId)} aria-label="Edit {outcome.name}">Edit</button>
              {:else if outcome.bookmarkId && !hadSecret(outcome.index)}
                <button onclick={() => onEditBookmark(outcome.bookmarkId)} aria-label="Enter key for {outcome.name}">Enter key</button>
              {/if}
            </li>
          {/each}
        </ul>
        {#if pending > 0}
          <p class="note" role="status">Verifying {pending} more. Verification goes on if you close this dialog.</p>
        {/if}
      {:else if imports.preview}
        {#if imports.preview.rows.length === 0}
          <p class="note">This file names no Swift accounts.</p>
        {:else}
          <ul class="rows" aria-label="Accounts in the file">
            {#each imports.preview.rows as row (row.index)}
              <li class:skipped={row.state === "skipped"}>
                <label>
                  <input
                    type="checkbox"
                    checked={imports.chosen.includes(row.index)}
                    disabled={row.state === "skipped"}
                    onchange={(e) => imports.toggle(row.index, e.currentTarget.checked)}
                    aria-label="Import {row.name}"
                  />
                  {#if row.state === "skipped"}
                    <span class="name">{row.name}</span>
                  {:else}
                    <input class="name" bind:value={imports.names[row.index]} aria-label="Name for {row.name}" />
                  {/if}
                  <span class="kind">{row.kind}</span>
                </label>
                {#if row.state !== "skipped"}
                  <span class="detail type-mono">{row.user}{row.project ? ` · ${row.project}` : ""}{row.region ? ` · ${row.region}` : ""} at {row.authUrl}</span>
                {/if}
                {#if row.state === "skipped"}
                  <span class="reason">Not imported: it {row.reason}.</span>
                {:else if row.state === "duplicate"}
                  <span class="reason">A bookmark already has this auth URL, user and project; tick it to import a second.</span>
                {:else if !row.hasSecret}
                  <span class="reason">No password in the file; it is asked for when first used.</span>
                {/if}
              </li>
            {/each}
          </ul>
          {#if imports.unnamed}
            <p class="note" role="status">Give every ticked row a name.</p>
          {/if}
          <label class="verify">
            <input type="checkbox" bind:checked={imports.verify} />
            <span>Verify after import (signs in and reads each account; nothing is changed)</span>
          </label>
        {/if}
      {/if}
    </div>
    <footer>
      {#if imports.result}
        <button class="primary" onclick={close}>Close</button>
      {:else}
        <button onclick={close}>Cancel</button>
        {#if imports.preview && imports.preview.rows.length > 0}
          <button class="primary" onclick={() => imports.save()} disabled={count === 0 || imports.unnamed || imports.busy}>
            {imports.busy ? "Importing…" : `Import ${count} ${count === 1 ? "bookmark" : "bookmarks"}`}
          </button>
        {/if}
      {/if}
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
    width: min(44rem, calc(100vw - 4rem));
    max-height: calc(100vh - 4rem);
    display: flex;
    flex-direction: column;
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  header,
  footer {
    display: flex;
    align-items: center;
    padding: var(--space-3) var(--space-4);
  }

  header {
    justify-content: space-between;
    border-bottom: 1px solid var(--color-border);
  }

  footer {
    justify-content: flex-end;
    gap: var(--space-2);
    border-top: 1px solid var(--color-border);
  }

  h2 {
    margin: 0;
    font-size: var(--text-base);
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: var(--space-3) var(--space-4);
  }

  .rows,
  .outcomes {
    list-style: none;
    margin: 0 0 var(--space-3);
    padding: 0;
  }

  .rows li,
  .outcomes li {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    padding: var(--space-2) 0;
    border-bottom: 1px solid var(--color-border);
  }

  .outcomes li {
    flex-direction: row;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--space-2);
  }

  .name {
    font-weight: 600;
  }

  .rows label {
    display: flex;
    align-items: center;
    gap: var(--space-2);
  }

  /* The name takes the row's width, so a long one can be read and edited. */
  input.name {
    flex: 1;
    min-width: 0;
    padding: var(--space-1) var(--space-2);
    font: inherit;
    font-weight: 600;
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  footer button,
  .outcomes button {
    padding: var(--space-1) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  footer button.primary {
    color: var(--color-text-invert);
    background: var(--color-accent);
    border-color: var(--color-accent-strong);
    font-weight: var(--weight-medium);
  }

  footer button:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .kind,
  .detail,
  .reason,
  .source,
  .note {
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }

  .detail {
    overflow-wrap: anywhere;
  }

  .skipped .name {
    color: var(--color-text-faint);
  }

  .failed .what {
    color: var(--color-warning);
  }

  .verify {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
  }
</style>
