<!-- The selected-set delete confirm (ADR 0093 decisions 5, 8 and 9): one
  dialog around the snapshot the browser state captured when it opened,
  listing the selected objects and folders, with what each folder holds now
  from the folder deletion's own preview, the ceiling, what happens to large
  objects' segments and what the delete does under the container's
  versioning mode. The confirmation is the word delete and the number of
  selected rows, typed, and the request is built from the snapshot alone.
  While the confirmation is still possible the snapshot is checked against
  the view, and a view that no longer matches ends it and says why. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";
  import { onMount } from "svelte";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerSettingsDTO, DeletePreviewDTO } from "../../lib/api";
  import { formatBytes, formatCount } from "../../lib/format";
  import { versioningSentence, whySegmentsStay } from "./versioningSentence";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // The operation is owned by the browser state, as the single object's is;
  // opening without one is a programming error the screen's mount condition
  // prevents.
  // svelte-ignore state_referenced_locally
  const op = browser.selectedDelete!;
  const count = op.members.length;
  const objects = op.members.filter((row) => row.kind === "object");
  const folders = op.members.filter((row) => row.kind !== "object");
  const listedBytes = objects.reduce((sum, row) => sum + row.bytes, 0);
  /** How many members are named; the rest are counted. */
  const named = 50;
  const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;
  const what = [
    objects.length > 0 ? plural(objects.length, "object", "objects") : "",
    folders.length > 0 ? plural(folders.length, "folder", "folders") : "",
  ].filter(Boolean).join(" and ");
  // A row the listing marks as a static large object, or lists as zero
  // bytes, may be a manifest, as the folder deletion's preview counts them.
  const objectManifests = objects.filter((row) => row.sloListed || row.bytes === 0).length;

  // undefined while the container's facts are being read, null when they
  // cannot be read. Confirmation waits for either answer.
  let facts: ContainerSettingsDTO | null | undefined = $state(undefined);
  // What each selected folder holds now, from the folder deletion's preview,
  // which also says whether the container is or may be another's archive.
  // undefined while being read.
  type Count = { preview: DeletePreviewDTO | null; error: string | null };
  let counts = $state<Map<string, Count> | undefined>(undefined);
  let typed = $state("");
  let segmentsChosen: boolean | null = $state(null);

  const pending = $derived(op.status === "pending");
  // Checked only while the confirmation is still possible: once the job has
  // started the snapshot has done its work and the listing moves on.
  const problem = $derived(pending ? browser.selectedDeleteProblem() : null);
  // The first problem ends the confirmation for good, so a selection restored
  // to the same rows cannot re-arm a snapshot the operator has not re-read.
  let abandoned: string | null = $state(null);
  const previewRequest = new AbortController();
  $effect(() => {
    if (pending && problem !== null && abandoned === null) abandoned = problem;
    if (!pending || abandoned !== null || browser.selectedDelete !== op) previewRequest.abort();
  });

  async function readPreview(): Promise<Map<string, Count>> {
    if (folders.length === 0) return new Map();
    try {
      const rows = await browser.previewSelectedDelete({
        scope: { ...op.listingScope }, container: op.container, members: op.members.map((m) => ({ ...m })),
        confirm: "", segments: false,
      }, previewRequest.signal);
      return new Map(rows.map((row) => [row.prefix, { preview: row.preview, error: row.error }]));
    } catch (err) {
      const error = err instanceof Error ? err.message : String(err);
      return new Map(folders.map((row) => [row.name, { preview: null, error }]));
    }
  }

  onMount(() => {
    void Promise.all([browser.containerFacts(op.container), readPreview()]).then(([result, read]) => {
      if (previewRequest.signal.aborted || browser.selectedDelete !== op) return;
      facts = result;
      counts = read;
    });
    return () => previewRequest.abort();
  });

  const previews = $derived([...(counts?.values() ?? [])]);
  const possibleManifests = $derived(objectManifests
    + [...(counts?.values() ?? [])].reduce((sum, c) => sum + (c.preview?.possibleManifests ?? 0), 0));
  const settled = $derived(facts !== undefined && counts !== undefined);
  // Offered as the folder deletion offers it: something may be a manifest,
  // and the container keeps no versions, where the service refuses it.
  const offerSegments = $derived(settled && possibleManifests > 0 && whySegmentsStay(facts) === null);
  // In a container that is, or may be, another's archive, the option starts
  // unticked (ADR 0120 decision 2). Only a folder's preview reads that scan,
  // so with no folder selected, or a preview that failed, it was not read
  // and the option starts unticked too.
  const archiveNamed = $derived(previews.some((c) => c.preview !== null
    && ((c.preview.archiveOf ?? []).length > 0 || !!c.preview.archiveUnestablished)));
  const archiveUnread = $derived(previews.length === 0 || previews.some((c) => c.preview === null));
  const mayBeArchive = $derived(archiveNamed || archiveUnread);
  const withSegments = $derived(offerSegments && (segmentsChosen ?? !mayBeArchive));
  const words = $derived(`delete ${count}${withSegments ? " and segments" : ""}`);
  const canConfirm = $derived(pending && abandoned === null && settled && typed === words);

  async function confirm() {
    if (!canConfirm) return;
    await browser.runSelectedDelete(typed, withSegments);
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="alertdialog" aria-label="Delete selected" aria-modal="true" tabindex="-1"
    use:trapFocus={onClose}>
    <h2 class="title">
      {op.status === "done" ? "Deletion started" : abandoned ? "Nothing deleted" : `Delete ${what}`}
    </h2>
    <div class="body">
      {#if op.status === "done"}
        <p class="what">
          The deletion of {what} is job <span class="type-mono">{op.jobId}</span>. Its progress and each
          object's outcome are in the jobs drawer, and the listing refreshes when it ends.
        </p>
      {:else if abandoned}
        <p class="keeps warn" role="alert">
          <strong>Nothing was deleted:</strong> {abandoned}. Select the rows again and open the delete afresh.
        </p>
      {:else}
        <p class="what">
          {#if objects.length > 0}
            {plural(objects.length, "object", "objects")}, {formatBytes(listedBytes)} as listed,
          {/if}
          {#if folders.length > 0}
            {objects.length > 0 ? "and " : ""}{plural(folders.length, "folder", "folders")} with everything in
            {folders.length === 1 ? "it" : "them"}
          {/if}
          in <span class="type-mono">{op.container}</span> will be deleted as one job.
        </p>
        {#if objects.length > 0}
          <ul class="members type-mono" aria-label="Selected objects">
            {#each objects.slice(0, named) as member (member.name)}
              <li>{member.name}</li>
            {/each}
          </ul>
          {#if objects.length > named}
            <p class="keeps">and {formatCount(objects.length - named)} more; {objects.length} in all.</p>
          {/if}
        {/if}
        {#if folders.length > 0}
          <ul class="members" aria-label="Selected folders">
            {#each folders.slice(0, named) as folder (folder.name)}
              {@const held = counts?.get(folder.name)}
              <li>
                <span class="type-mono">{folder.name}</span>:
                {#if !held}
                  counting…
                {:else if held.preview}
                  {plural(held.preview.objects, "object", "objects")}, {formatBytes(held.preview.bytes)}{held.preview
                    .possibleManifests > 0
                    ? `, ${plural(held.preview.possibleManifests, "possible manifest", "possible manifests")}`
                    : ""} now
                {:else}
                  what it holds could not be counted: {held.error}
                {/if}
              </li>
            {/each}
          </ul>
          {#if folders.length > named}
            <p class="keeps">and {formatCount(folders.length - named)} more folders.</p>
          {/if}
          <p class="keeps warn">
            Everything under each selected folder goes, including anything written there before the job lists it.
          </p>
        {/if}
        {#if browser.segmentStoreCaution}
          <p class="keeps warn">{browser.segmentStoreCaution}</p>
        {/if}
        <p class="keeps">{versioningSentence(facts)}</p>
        {#if possibleManifests > 0}
          {#if offerSegments}
            <label class="option">
              <input
                type="checkbox"
                checked={withSegments}
                onchange={(e) => {
                  segmentsChosen = e.currentTarget.checked;
                  typed = "";
                }}
              />
              <span>Also delete the segments of large objects</span>
            </label>
            {#if archiveNamed}
              <p class="keeps">
                It starts unticked here: an archived version and the object it came from can use the same segments,
                and deleting them would break that object.
              </p>
            {:else if archiveUnread}
              <p class="keeps">
                It starts unticked here: whether this container keeps another container's versions was not read,
                because only a selected folder's preview reads it, and an archived version can use the same segments
                as the object it came from.
              </p>
            {/if}
          {:else if settled && whySegmentsStay(facts)}
            <p class="keeps">Their segments cannot be deleted with them here: {whySegmentsStay(facts)}</p>
          {/if}
          <p class={withSegments ? "keeps warn" : "keeps"}>
            {withSegments
              ? `The ${plural(possibleManifests, "possible manifest", "possible manifests")} lose their segments after them: the ones a static manifest lists, or everything under the prefix a dynamic manifest names. Nothing here can show that another object does not use the same segments; one that does will lose its data.`
              : `The ${plural(possibleManifests, "possible manifest", "possible manifests")} are deleted without their segments, which stay behind.`}
          </p>
        {/if}
        <p class="keeps">
          Each name is deleted by one request, whatever it holds when that request arrives: if something else has
          written to a name since it was listed, that is what goes. A link is deleted as the link it is. The store's
          answer to each request is its result, and nothing is read again to check it. A selection holds at most
          {op.ceiling.toLocaleString("en-US")} objects, counting what its folders hold; the ceiling is set in Settings.
        </p>
        <label class="confirm">
          <span>Type <span class="type-mono">{words}</span> to confirm</span>
          <input class="type-mono" bind:value={typed} placeholder={words} autocomplete="off" spellcheck="false" />
        </label>
      {/if}
      {#if op.error}
        <p class="error callout danger" role="alert">{op.error}</p>
      {/if}
    </div>
    <div class="actions">
      {#if op.status === "done" || abandoned}
        {#if op.status === "done" && op.jobId}
          {@const jobId = op.jobId}
          <button
            class="secondary"
            onclick={() => {
              onClose();
              void browser.jobs.reveal(jobId);
            }}>Show in jobs</button
          >
        {/if}
        <button class="primary" onclick={onClose}>Close</button>
      {:else}
        <button class="secondary" onclick={onClose} disabled={op.status === "running"}>Cancel</button>
        <button class="danger" onclick={confirm} disabled={!canConfirm}>
          {op.status === "running" ? "Starting…" : `Delete ${what}`}
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

  .option {
    display: flex;
    gap: var(--space-2);
    align-items: center;
    font-size: var(--text-sm);
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

  /* A finished dialog's Close is its next step, highlighted as in the copy
    and transfer dialogs; anything beside it stays plain. */
  .primary {
    color: var(--color-bg);
    background: var(--color-accent);
    border: 1px solid var(--color-accent);
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
