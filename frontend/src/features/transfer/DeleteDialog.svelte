<!-- Destructive-jobs dialog (§31–§33): preview (counts, bytes, the
  manifest disclosure, and the strategy the engine would pick) before a
  typed confirmation unlocks Start. It is opened for one job, a prefix, a
  container's contents or a container, and offers no choice between them
  (ADR 0121). Deletion is Aeronautes-native and
  manifest-only unless the option that takes segments too is ticked, as it
  is to start with (ADR 0119, ADR 0120); the Go side re-validates
  everything. -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { ContainerSettingsDTO, DeletePreviewDTO, DeleteRequestDTO } from "../../lib/api";
  import { formatBytes, formatCount } from "../../lib/format";
  import { archiveOwnersSentence, versioningSentence, whySegmentsStay } from "../inspector/versioningSentence";

  let { browser, onClose }: { browser: BrowserState; onClose: () => void } = $props();

  // The selected folder when one is selected alone, otherwise the folder
  // being browsed (ADR 0095 decision 7).
  const targetPrefix = $derived(browser.selectedPrefix || browser.prefix);
  const hasPrefix = $derived(targetPrefix !== "");
  // The values are the generated enum's own, so a mode the bridge renames
  // fails to compile here instead of being sent as a name it no longer knows.
  type Mode = Exclude<`${DeleteRequestDTO["mode"]}`, "">;
  // The job the action that opened this dialog named. It never changes
  // while the dialog is open: another job is another action.
  const mode: Mode = untrack(() => browser.deleteOpen ?? "empty");
  const jobs: Record<Mode, { heading: string; words: string }> = {
    prefix: { heading: "Delete prefix", words: "the container and prefix" },
    empty: { heading: "Empty container", words: "“empty” and the container name" },
    delete_container: { heading: "Delete container", words: "“delete” and the container name" },
  };
  let confirmText = $state("");
  let previewing = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);
  // What the started job deletes, in words, captured at start: the folder a
  // prefix deletion removed is gone from the listing by the time it ends.
  let startedWhat = $state("");
  // What the operator chose for the option that each large object in the
  // scope also loses its segments (ADR 0119), null until they choose: the
  // option then stands at its starting value. Every new preview forgets the
  // choice, and the option is not part of the previewed request, whose
  // counts are the same either way. startedSegments is what the started job
  // was asked.
  let segmentsChosen: boolean | null = $state(null);
  let startedSegments = $state(false);
  // The started job as the drawer's poll last saw it, so the dialog says when
  // it is done instead of leaving the operator to guess.
  const startedJob = $derived(startedJobId ? browser.jobs.list.find((job) => job.id === startedJobId) ?? null : null);
  const startedEnded = $derived(!!startedJob && ["complete", "partial", "failed", "cancelled"].includes(startedJob.state));
  let closed = $state(false);
  // `facts` is undefined while the container's versioning facts are still
  // loading, then null or a value once settled. Start waits for settled
  // (facts !== undefined) so the versioning consequence is disclosed before a
  // destructive request is authorised (ADR 0095 decision 9).
  let facts: ContainerSettingsDTO | null | undefined = $state(undefined);
  let generation = 0;
  interface Intent {
    generation: number;
    browser: BrowserState;
    scopeRevision: number;
    request: DeleteRequestDTO;
  }
  let prepared = $state.raw<{ intent: Intent; plan: DeletePreviewDTO } | null>(null);
  const preview = $derived(prepared && isCurrent(prepared.intent) ? prepared.plan : null);
  const canPreview = $derived(!closed && !previewing && !starting && !browser.connecting
    && !!browser.connectedBookmarkId && !!browser.containerName && (mode !== "prefix" || hasPrefix));
  // Segments are offered, beside a preview that counted a row that may be a
  // manifest, when the container keeps no versions: a kept version still
  // uses its segments, and the Go side refuses the option there.
  const offerSegments = $derived(!!preview && preview.possibleManifests > 0 && whySegmentsStay(facts) === null);
  // An archived version and the object it came from can name the same
  // segments, so in a container that is, or may be, another's archive the
  // option starts unticked (ADR 0120 decision 2).
  const mayBeArchive = $derived(!!preview && ((preview.archiveOf ?? []).length > 0 || !!preview.archiveUnestablished));
  // Ticked to start with, by the operator's decision (ADR 0120 decision 1).
  const withSegments = $derived(offerSegments && (segmentsChosen ?? !mayBeArchive));
  const armWord = $derived(prepared ? armingText(prepared.intent.request, withSegments) : "");
  const canStart = $derived(!!prepared && !!preview && !previewing && !starting
    && facts !== undefined
    && confirmText === armWord);

  // What the operator types to arm the deletion, the same text the Go side
  // requires (ADR 0095 decision 7): a prefix is armed by its scope, so the
  // typing shows which prefix was read. Each container job is armed by its
  // own verb and the name, so the words for emptying never destroy the
  // container and its settings, and the bare name starts neither (ADR 0121
  // decision 3). A deletion that takes segments is armed by other words
  // again, so the words for one never start the other (ADR 0119 decision 2).
  function armingText(req: DeleteRequestDTO, segments: boolean): string {
    const words = req.mode === "prefix" ? `${req.container}/${req.prefix}`
      : req.mode === "delete_container" ? `delete ${req.container}` : `empty ${req.container}`;
    return segments ? `${words} and segments` : words;
  }

  function request(): DeleteRequestDTO {
    return {
      bookmarkId: browser.connectedBookmarkId ?? "",
      container: browser.containerName ?? "",
      prefix: mode === "prefix" ? targetPrefix : "",
      mode: mode as DeleteRequestDTO["mode"],
      confirm: "",
      segments: false,
    };
  }

  function isCurrent(intent: Intent): boolean {
    return !closed && !browser.connecting && intent.generation === generation
      && intent.browser === browser && intent.scopeRevision === browser.scopeRevision
      && JSON.stringify(intent.request) === JSON.stringify(request());
  }

  function invalidate() {
    generation++;
    prepared = null;
    confirmText = "";
    segmentsChosen = null;
    previewing = false;
    startedJobId = null;
    error = null;
  }

  $effect(() => {
    void browser.scopeRevision;
    // Once this dialog has started a job there is no preview left to
    // protect, and a later scope change, such as the browser leaving the
    // container that job deleted, must not turn it back into a new deletion.
    untrack(() => {
      if (startedJobId === null) invalidate();
    });
  });

  // The dialog acts on the container open when it appeared. When that
  // container is no longer open, for example because this dialog's own job
  // deleted it, there is nothing left for the dialog to act on.
  const openedOn = untrack(() => browser.containerName);

  // This application and rclone keep the segments of the large objects
  // uploaded to a container in a second container named after it. A
  // deletion here removes manifests only and leaves segments, where they
  // still count against quota, so when a container of that name exists the
  // dialog says so and, once its job is done, offers the way to it (0.5.0
  // test run). The name is all that is known: what is in that container,
  // and whether another large object still uses it, is not, so the dialog
  // does not say those are this container's segments or advise deleting it.
  const segmentsContainer = $derived(browser.containers.find((row) => row.name === `${openedOn}_segments`) ?? null);

  $effect(() => {
    if (browser.containerName === openedOn) return;
    // A job that deleted the container has left it, and the dialog stays
    // only to say what that job did not delete.
    untrack(() => {
      if (!(startedJobId !== null && segmentsContainer)) close();
    });
  });

  function openSegmentsContainer() {
    const name = segmentsContainer?.name;
    close();
    if (name) void browser.openContainer(name);
  }

  // The container's versioning facts drive the consent sentence that says what
  // a delete leaves at the name, the same pattern the manifest dialog uses.
  $effect(() => {
    const container = browser.containerName;
    facts = undefined;
    if (!container) return;
    untrack(() => {
      void (async () => {
        const loaded = await browser.containerFacts(container);
        if (browser.containerName === container) facts = loaded;
      })();
    });
  });

  function close() {
    closed = true;
    invalidate();
    onClose();
  }
  onDestroy(() => { closed = true; invalidate(); });

  async function runPreview() {
    if (!canPreview) return;
    invalidate();
    const req = Object.freeze(request());
    const intent: Intent = { generation, browser, scopeRevision: browser.scopeRevision, request: req };
    previewing = true;
    try {
      const plan = await browser.previewDelete(req);
      if (isCurrent(intent)) prepared = { intent, plan };
    } catch (e) {
      if (isCurrent(intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      if (isCurrent(intent)) previewing = false;
    }
  }

  async function start() {
    const current = prepared;
    if (starting || previewing || !current || !isCurrent(current.intent)
      || facts === undefined
      || confirmText !== armingText(current.intent.request, withSegments)) return;
    const req = { ...current.intent.request, confirm: confirmText, segments: withSegments };
    prepared = null;
    confirmText = "";
    starting = true;
    error = null;
    try {
      const jobId = await browser.startDelete(req);
      if (isCurrent(current.intent)) {
        startedJobId = jobId;
        startedSegments = req.segments;
        startedWhat = req.mode === "prefix" ? `prefix ${req.container}/${req.prefix}`
          : req.mode === "empty" ? `the contents of container ${req.container}` : `container ${req.container}`;
      }
    } catch (e) {
      if (isCurrent(current.intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      // A scope edit cannot open a second Start while this one is pending.
      starting = false;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label="Delete" tabindex="-1" use:trapFocus={close}>
    <header>
      <h2>
        {!startedJobId ? jobs[mode].heading : !startedEnded ? "Deletion started"
          : startedJob?.state === "complete" ? "Deletion complete" : "Deletion ended"}
      </h2>
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>

    {#if startedJobId}
      <!-- Once a job has started the dialog only reports it: the choices
        would read as an offer to delete more (0.5.0 test run). -->
      <p class="next-step">
        {#if !startedEnded}
          Deleting <span class="type-mono">{startedWhat}</span>. Its progress and each object's outcome are in the jobs
          drawer.
        {:else if startedJob?.state === "complete"}
          Deleted <span class="type-mono">{startedWhat}</span>: {startedJob.counters.objectsDeleted}
          {startedJob.counters.objectsDeleted === 1 ? "object" : "objects"}{startedSegments
            ? ", the segments of large objects among them"
            : ""}.
        {:else}
          The deletion of <span class="type-mono">{startedWhat}</span> ended {startedJob?.state}; the jobs drawer says
          what was left and why.
        {/if}
      </p>
      {#if startedEnded && segmentsContainer}
        <p class="warn" role="status">
          {#if startedSegments}
            <span class="type-mono">{segmentsContainer.name}</span> still exists. The job's records list each
            segment deleted from it; whatever is left there still counts against quota.
          {:else}
            <span class="type-mono">{segmentsContainer.name}</span> still exists, with
            {formatCount(segmentsContainer.count)} objects and {formatBytes(segmentsContainer.bytes)} as listed; this
            deletion removed nothing from it. If it held segments of large objects deleted here, they are still there
            and still count against quota.
          {/if}
        </p>
      {/if}
    {:else}
    <p class="scope">
      {#if mode === "prefix"}
        Everything under <span class="type-mono">{browser.containerName}/{targetPrefix}</span> goes; the rest of the
        container stays.
      {:else if mode === "empty"}
        The contents of <span class="type-mono">{browser.containerName}</span> go; the container and all its
        configuration stay.
      {:else}
        <span class="type-mono">{browser.containerName}</span> is emptied, then the container itself is deleted, with
        all its configuration.
      {/if}
    </p>
    {#if browser.segmentStoreCaution}
      <p class="warn">{browser.segmentStoreCaution}</p>
    {/if}
    {#if segmentsContainer}
      <p class="warn">
        A container named <span class="type-mono">{segmentsContainer.name}</span> exists, with
        {formatCount(segmentsContainer.count)} objects and {formatBytes(segmentsContainer.bytes)} as listed. It is
        named like the place where this application and rclone keep the segments of large objects uploaded to
        <span class="type-mono">{openedOn}</span>.
        {#if preview}
          {withSegments
            ? "With the option below ticked, this deletion also deletes the segments of its large objects, wherever they are kept."
            : "A deletion here removes nothing from it."}
        {/if}
      </p>
    {/if}
    {/if}

    {#if error}
      <p class="error callout danger" role="alert">{error}</p>
    {/if}

    {#if !preview && !startedJobId}
      <!-- The start button stays disabled until both steps are done, and
        nothing else on the page says why. -->
      <p class="next-step">
        Preview first: it counts what will go, then asks you to type {jobs[mode].words} to arm the deletion.
      </p>
    {/if}

    {#if preview}
      {@const archiveSentence = archiveOwnersSentence(preview)}
      <div class="preview num">
        <p class="summary">
          <strong>{preview.objects}</strong> objects · {formatBytes(preview.bytes)} ·
          strategy <span class="type-mono">{preview.strategy}</span>
        </p>
        <p class="cost">
          Each object is compared then deleted, and a row that cannot say what it holds is read once
          more first: <strong>{preview.requestCost}</strong> requests{preview.laterBaselineRows > 0
            ? ` (${preview.laterBaselineRows} of these rows take that extra read)`
            : ""}, not counting this listing, any retries, the final check or each member's own
          verifying read.
        </p>
        <p class="keeps">
          An object listed with a size is deleted only if it still matches that listing; one that
          changed since is left and recorded. A row listed as zero bytes or as a static large object
          is read once more first, and what that read then holds is what is deleted, a directory
          marker or a manifest included. A link is refused and left, never followed. Each name is
          requested once by this job; whatever is still listed after that is left for a fresh run.
        </p>
        <p class="keeps">{versioningSentence(facts)}</p>
        {#if archiveSentence}
          <p class="warn">{archiveSentence}</p>
        {/if}
        {#if preview.possibleManifests > 0}
          {#if !withSegments}
            <p class="warn">
              {preview.possibleManifests} possible manifest{preview.possibleManifests === 1
                ? ""
                : "s"} in scope. Deletion is manifest-only: their segments (if any) stay behind as
              recoverable orphans rather than risking another object's data.
            </p>
          {/if}
          {#if offerSegments}
            <label class="option">
              <input
                type="checkbox"
                checked={withSegments}
                onchange={(e) => {
                  segmentsChosen = e.currentTarget.checked;
                  confirmText = "";
                }}
              />
              <span>Also delete the segments of large objects</span>
            </label>
            {#if mayBeArchive}
              <p class="keeps">
                It starts unticked here: an archived version and the object it came from can use the same segments,
                and deleting them would break that object.
              </p>
            {/if}
            {#if withSegments}
              <p class="warn" role="status">
                Each large object's segments are deleted after it: the ones a static manifest lists, or everything
                under the prefix a dynamic manifest names. Nothing here can show that another object does not use
                the same segments; one that does will lose its data. How many segments there are is not known until
                each large object is read. Every segment is compared then deleted like any other object and listed
                in the job. Segments that cannot be bounded safely are kept, as when a dynamic manifest's prefix is
                wider than what is being deleted here, and the job then ends partial and says why.
              </p>
            {/if}
          {:else if whySegmentsStay(facts)}
            <p class="keeps">Their segments cannot be deleted with them here: {whySegmentsStay(facts)}</p>
          {/if}
        {/if}
        <label class="confirm">
          <span>
            Type {jobs[mode].words}{withSegments ? ", then “and segments”," : ""}
            <span class="type-mono">{armWord}</span> to arm:
          </span>
          <input class="type-mono" bind:value={confirmText} placeholder={armWord} />
        </label>
      </div>
    {/if}

    <footer>
      {#if startedJobId}
        <span class="started">Job {startedJobId} started.</span>
        <button
          onclick={() => {
            const jobId = startedJobId;
            close();
            if (jobId) void browser.jobs.reveal(jobId);
          }}>Show in jobs</button
        >
        {#if startedEnded && segmentsContainer}
          <button onclick={openSegmentsContainer} title={`Open ${segmentsContainer.name}`}>Open that container</button>
        {/if}
        <button class="primary" onclick={close}>Close</button>
      {:else}
        <!-- The highlight marks the next step: Preview until a preview is
          shown, then the deletion, which stays plain before it. -->
        <button class:primary={!preview} onclick={runPreview} disabled={!canPreview}>
          {previewing ? "Counting…" : "Preview"}
        </button>
        <button
          class:danger={!!preview}
          onclick={start}
          disabled={!canStart}
        >
          {jobs[mode].heading}
        </button>
      {/if}
    </footer>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--color-bg) 60%, transparent);
    display: grid;
    place-items: center;
    z-index: 30;
  }

  .dialog {
    width: min(40rem, calc(100vw - 4rem));
    background: var(--color-bg-pane);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-card);
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  h2 {
    margin: 0;
    font-size: var(--text-base);
    color: var(--color-danger);
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .scope {
    margin: 0;
    font-size: var(--text-sm);
    overflow-wrap: anywhere;
  }

  .option {
    display: flex;
    gap: var(--space-2);
    align-items: baseline;
  }

  .option {
    font-size: var(--text-sm);
  }

  .error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .preview {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    padding: var(--space-3);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .next-step {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .summary {
    margin: 0;
    font-size: var(--text-sm);
  }

  .cost,
  .keeps {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
  }

  .warn {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-warning);
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

  footer {
    display: flex;
    justify-content: flex-end;
    align-items: center;
    gap: var(--space-2);
  }

  .started {
    margin-right: auto;
    font-size: var(--text-sm);
    color: var(--color-success);
  }

  footer button {
    font: inherit;
    font-size: var(--text-sm);
    padding: var(--space-1) var(--space-3);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    background: var(--color-bg-card);
    color: var(--color-text);
    cursor: pointer;
  }

  footer button:disabled {
    opacity: 0.5;
    cursor: default;
  }

  footer .primary {
    background: var(--color-accent);
    color: var(--color-bg);
    border-color: var(--color-accent);
  }

  footer .danger {
    background: var(--color-danger);
    color: var(--color-bg);
    border-color: var(--color-danger);
  }
</style>
