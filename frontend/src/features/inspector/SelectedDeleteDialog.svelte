<!-- The selected-set delete confirm (ADR 0093 decision 5): one dialog around
  the snapshot the browser state captured when it opened, listing the members
  with the full count, the prefix rows left out, the ceiling and what the
  delete does under the container's versioning mode. The confirmation is the
  number of members, typed, and the request is built from the snapshot alone.
  While the confirmation is still possible the snapshot is checked against the
  view, and a view that no longer matches ends it and says why. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import { onMount } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerSettingsDTO } from "../../lib/api";
  import { formatBytes, formatCount } from "../../lib/format";
  import { versioningSentence } from "./versioningSentence";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // The operation is owned by the browser state, as the single object's is;
  // opening without one is a programming error the screen's mount condition
  // prevents.
  // svelte-ignore state_referenced_locally
  const op = browser.selectedDelete!;
  const count = op.members.length;
  const listedBytes = op.members.reduce((sum, row) => sum + row.bytes, 0);
  /** How many members are named; the rest are counted. */
  const named = 50;
  const noun = count === 1 ? "object" : "objects";

  // undefined while the container's facts are being read, null when they
  // cannot be read. Confirmation waits for either answer.
  let facts: ContainerSettingsDTO | null | undefined = $state(undefined);
  let typed = $state("");

  const pending = $derived(op.status === "pending");
  // Checked only while the confirmation is still possible: once the job has
  // started the snapshot has done its work and the listing moves on.
  const problem = $derived(pending ? browser.selectedDeleteProblem() : null);
  // The first problem ends the confirmation for good, so a selection restored
  // to the same rows cannot re-arm a snapshot the operator has not re-read.
  let abandoned: string | null = $state(null);
  $effect(() => {
    if (pending && problem !== null && abandoned === null) abandoned = problem;
  });

  onMount(async () => {
    const result = await browser.containerFacts(op.container);
    if (browser.selectedDelete === op) facts = result;
  });

  const canConfirm = $derived(pending && abandoned === null && facts !== undefined && typed === String(count));

  async function confirm() {
    if (!canConfirm) return;
    await browser.runSelectedDelete(typed);
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="alertdialog" aria-label="Delete selected objects" aria-modal="true" tabindex="-1"
    use:trapFocus={onClose}>
    <h2 class="title">
      {op.status === "done" ? "Deletion started" : abandoned ? "Nothing deleted" : `Delete ${count} selected ${noun}`}
    </h2>
    <div class="body">
      {#if op.status === "done"}
        <p class="what">
          The deletion of {count} {noun} is job <span class="type-mono">{op.jobId}</span>. Its progress and each
          object's outcome are in the jobs drawer, and the listing refreshes when it ends.
        </p>
      {:else if abandoned}
        <p class="keeps warn" role="alert">
          <strong>Nothing was deleted:</strong> {abandoned}. Select the objects again and open the delete afresh.
        </p>
      {:else}
        <p class="what">
          {count} {noun} in <span class="type-mono">{op.container}</span>, {formatBytes(listedBytes)} as listed, will be
          deleted as one job.
        </p>
        <ul class="members type-mono" aria-label="Selected objects">
          {#each op.members.slice(0, named) as member (member.name)}
            <li>{member.name}</li>
          {/each}
        </ul>
        {#if count > named}
          <p class="keeps">and {formatCount(count - named)} more; {count} in all.</p>
        {/if}
        {#if op.excludedPrefixes.length > 0}
          <p class="keeps">
            Not included: {op.excludedPrefixes.length} prefix {op.excludedPrefixes.length === 1 ? "row" : "rows"}
            (<span class="type-mono">{op.excludedPrefixes.slice(0, 5).join(", ")}</span>{op.excludedPrefixes.length > 5 ? ", …" : ""}).
            A prefix is not deleted here.
          </p>
        {/if}
        <p class="keeps">{versioningSentence(facts)}</p>
        <p class="keeps">
          An object listed with a size is deleted only if it still matches its listing; one that changed since is left
          as it is and recorded as changed. A row listed as zero bytes or as a static large object cannot say what it
          holds, so it is read again first and what that read finds is what is deleted, a marker or a manifest included
          and a link refused. The delete cannot see a change that lands after its last look. A selection holds at most
          {op.ceiling.toLocaleString("en-US")} objects; the ceiling is set in Settings.
        </p>
        <label class="confirm">
          <span>Type the number of objects, {count}, to confirm</span>
          <input class="type-mono" bind:value={typed} placeholder={String(count)} inputmode="numeric" />
        </label>
      {/if}
      {#if op.error}
        <p class="error" role="alert">{op.error}</p>
      {/if}
    </div>
    <div class="actions">
      {#if op.status === "done" || abandoned}
        <button class="secondary" onclick={onClose}>Close</button>
      {:else}
        <button class="secondary" onclick={onClose} disabled={op.status === "running"}>Cancel</button>
        <button class="danger" onclick={confirm} disabled={!canConfirm}>
          {op.status === "running" ? "Starting…" : `Delete ${count} ${noun}`}
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgb(20 16 12 / 0.45);
    z-index: 10;
  }

  .dialog {
    width: min(30rem, calc(100vw - 2 * var(--space-5)));
    max-height: calc(100vh - 2 * var(--space-5));
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  .title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: var(--weight-semibold);
    color: var(--color-danger);
  }

  .what,
  .keeps {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }

  .keeps.warn {
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-radius: var(--radius-control);
    padding: var(--space-2);
  }

  .members {
    margin: 0;
    padding-left: var(--space-4);
    max-height: 10rem;
    overflow-y: auto;
    font-size: var(--text-sm);
    overflow-wrap: anywhere;
  }

  .confirm {
    display: flex;
    flex-direction: column;
    gap: var(--space-1);
    font-size: var(--text-sm);
  }

  .confirm input {
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .error {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-danger);
    background: var(--color-danger-soft);
    border-radius: var(--radius-control);
    overflow-wrap: anywhere;
  }

  .body {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    min-height: 0;
    overflow-y: auto;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }

  .actions button {
    padding: var(--space-1) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .secondary {
    color: var(--color-text-secondary);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
  }

  .danger {
    color: var(--color-text-invert);
    background: var(--color-danger);
    border: 1px solid var(--color-danger);
    font-weight: var(--weight-medium);
  }

  .actions button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
