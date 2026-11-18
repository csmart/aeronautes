<!-- Upload / download dialog (§27.5): the collision preflight renders
  before any write under one cross-platform namespace policy. A report
  that rejects anything offers no Start; one whose only rejections are
  names already in use may be asked again as a replacing plan, which
  replaces only what differs (ADR 0081, ADR 0122 decision 12). -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { DownloadPreflightView, DownloadRequestDTO, PickedFilesDTO, UploadPreflight, UploadRequestDTO } from "../../lib/api";
  import { folderSummary, folderWarns } from "./folderSummary";
  import { formatBytes, formatCount } from "../../lib/format";
  import JobSettingsFields from "./JobSettingsFields.svelte";
  import { newJobSettings, requestSettings } from "./jobSettings";
  import { countOfSelected } from "./selectionSummary";

  let {
    browser,
    mode,
    onClose,
  }: { browser: BrowserState; mode: "upload" | "download"; onClose: () => void } = $props();
  // The ceiling sentence needs the settings document; a dialog opened
  // before the drawer ever loaded it asks once (ADR 0072 decision 4).
  untrack(() => {
    if (mode === "download" && !browser.settings.document) void browser.settings.load();
  });

  // The profile is seeded from the application default (§40). The settings
  // are part of the scope: the upload report's large-object note depends on
  // the chunk size, so changing any of them asks for a new check.
  // svelte-ignore state_referenced_locally
  let jobSettings = $state(newJobSettings(browser.settings.document?.defaultProfile ?? "auto"));
  let checking = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);
  let closed = $state(false);
  let generation = 0;
  // The scope is what navigation binds: the fields both requests share.
  // Overwrite acknowledgement is not one of them — it is the operator's
  // answer, set at the request and kept out of the identity a late
  // navigation is compared against.
  // Members name a download's selected objects (ADR 0108); an upload has none.
  type Scope = Omit<UploadRequestDTO, "selectionToken" | "acknowledgeOverwrite"> & { members?: string[] };
  interface Intent {
    generation: number;
    browser: BrowserState;
    scopeRevision: number;
    localRevision: number;
    mode: "upload" | "download";
    scope: Scope;
  }
  // Upload still ships the complete item list; download is a bounded view
  // with first-class counts (ADR 0056). Each arm carries its own generated
  // report type, so a field the bridge renames fails to compile here.
  type Prepared = {
    intent: Intent;
    destination: string;
  } & (
    | { mode: "upload"; request: UploadRequestDTO; report: UploadPreflight }
    | { mode: "download"; request: DownloadRequestDTO; report: DownloadPreflightView }
  );
  let prepared = $state.raw<Prepared | null>(null);
  // The folder pick's own summary (ADR 0103): shown with the report, or alone
  // when the folder holds nothing to upload. A file pick leaves it null.
  let folder = $state.raw<PickedFilesDTO | null>(null);
  const current = $derived(prepared && isCurrent(prepared.intent) ? prepared : null);
  const report = $derived(current?.report ?? null);
  const destDir = $derived(current?.destination ?? "");
  // A download follows the selection (ADR 0108); an upload goes into the
  // folder being browsed. The members are part of the scope, so a change of
  // selection disarms a preflight.
  const source = $derived(browser.transferSource);
  const downloadSource = $derived(mode === "download" ? source : null);
  // A count is offered as this download's size only when it was taken of
  // exactly what the download takes: a count of a selected folder must not
  // stand for a download of the container or of a file (ADR 0123 decision 3).
  const countedHere = $derived(
    downloadSource !== null && downloadSource.members.length === 0 && browser.prefixCount?.done === true
      && !browser.prefixCount.error && downloadSource.prefix === browser.countedPrefix,
  );

  const canChoose = $derived(!closed && !checking && !starting && !browser.connecting
    && !!browser.connectedBookmarkId && !!browser.containerName);

  const problemRows = $derived((current?.mode === "download" ? current.report.problemSamples : current?.report.problems) ?? []);
  const rejected = $derived(current?.mode === "download" ? current.report.rejected : problemRows.some((p) => p.reject));
  const warnings = $derived(problemRows.filter((p) => !p.reject));
  const rejections = $derived(problemRows.filter((p) => p.reject));
  // §63.8: name what a segmented file is published as before Start, the way
  // the copy preview names its placement (ADR 0111). The backend names the
  // kind only when the selection holds a file the engine segments; a refused
  // upload writes nothing, so the note is moot there.
  const largeObjects = $derived(current?.mode === "upload" && !rejected ? current.report.largeObjects : "");
  // Why a static choice uploads as dynamic, when it does.
  const largeObjectsBecause = $derived.by(() => {
    if (current?.mode !== "upload") return "";
    switch (current.report.largeObjectsOrigin) {
      case "capability":
        return ": this cluster does not offer static large objects that Aeronautes can read back";
      case "versioning":
        return ": this container keeps versions, where a static large object would leave an extra version of each file";
      default:
        return "";
    }
  });
  const problemCount = $derived(current?.mode === "download" ? current.report.problemCount : problemRows.length);
  const truncated = $derived(current?.mode === "download" && problemCount > problemRows.length);

  // A collision is the one rejection the operator can answer: ADR 0081's
  // acknowledged overwrite. Every OTHER rejecting problem is not a question,
  // so a report mixing a collision with anything else stays a plain refusal
  // and offers nothing. A download's report is a bounded sample, so its
  // complete count comes from the backend, which gives none when anything
  // else is refused.
  const collisionCount = $derived(current?.mode === "download"
    ? current.report.replaceable
    : rejections.filter((p) => p.kind === "exists").length);
  const collisionsOnly = $derived(rejected && collisionCount > 0
    && (current?.mode === "download" || collisionCount === rejections.length));
  // The acknowledged plan, once asked for: the collisions come back as
  // warnings rather than rejections, and the names are what may be replaced.
  const acknowledged = $derived(!!report && prepared?.request.acknowledgeOverwrite === true);
  const replacingCount = $derived(!acknowledged ? 0 : current?.mode === "download"
    ? current.report.replacing
    : warnings.filter((p) => p.kind === "exists").length);

  function scope(): Scope {
    return {
      bookmarkId: browser.connectedBookmarkId ?? "",
      container: browser.containerName ?? "",
      prefix: downloadSource ? downloadSource.prefix : browser.prefix,
      // Left out for an upload and for a whole scope, whose requests stay as before.
      members: downloadSource && downloadSource.members.length > 0 ? downloadSource.members : undefined,
      profile: jobSettings.profile,
      // A download segments nothing, so it never sends a chunk size.
      settings: requestSettings(mode === "upload" ? jobSettings : { ...jobSettings, chunkSize: "" }),
    };
  }

  function isCurrent(intent: Intent): boolean {
    return !closed && !browser.connecting && intent.generation === generation
      && intent.browser === browser && intent.scopeRevision === browser.scopeRevision
      && intent.localRevision === browser.localTransferRevision && intent.mode === mode
      && JSON.stringify(intent.scope) === JSON.stringify(scope());
  }

  function invalidate() {
    generation++;
    prepared = null;
    folder = null;
    checking = false;
    startedJobId = null;
    error = null;
  }

  $effect(() => {
    void browser.scopeRevision;
    void browser.localTransferRevision;
    void mode;
    untrack(invalidate);
  });

  function close() {
    closed = true;
    invalidate();
    onClose();
  }
  onDestroy(() => { closed = true; invalidate(); });

  async function pickAndPreflight(kind: "files" | "folder" = "files") {
    if (!canChoose) return;
    invalidate();
    const capturedScope = scope();
    Object.freeze(capturedScope.settings);
    Object.freeze(capturedScope);
    const intent: Intent = { generation, browser, scopeRevision: browser.scopeRevision,
      localRevision: browser.localTransferRevision, mode, scope: capturedScope };
    checking = true;
    try {
      if (intent.mode === "upload") {
        // The pick is bound to this scope for its whole life (ADR 0041).
        const pickScope = { bookmarkId: capturedScope.bookmarkId, container: capturedScope.container, prefix: capturedScope.prefix };
        const picked = kind === "folder"
          ? await browser.pickUploadDirectory(pickScope)
          : await browser.pickUploadFiles(pickScope);
        if (!isCurrent(intent) || !picked) return;
        // A folder's summary stands even when the walk found nothing to
        // upload; the pick then ends here with nothing to start.
        if (kind === "folder") folder = picked;
        if (!picked.token || !picked.files?.length) return;
        const req = Object.freeze({ ...capturedScope, selectionToken: picked.token, acknowledgeOverwrite: false });
        const result = await browser.preflightUpload(req);
        if (isCurrent(intent)) prepared = { intent, mode: "upload", request: req, report: result, destination: "" };
      } else {
        const dir = await browser.pickDownloadDir(
          { bookmarkId: capturedScope.bookmarkId, container: capturedScope.container, prefix: capturedScope.prefix });
        if (!isCurrent(intent) || !dir?.token) return;
        const req = Object.freeze({ ...capturedScope, destDirToken: dir.token, acknowledgeOverwrite: false });
        const result = await browser.preflightDownload(req);
        if (isCurrent(intent)) prepared = { intent, mode: "download", request: req, report: result, destination: dir.dir };
      }
    } catch (e) {
      if (isCurrent(intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      if (isCurrent(intent)) checking = false;
    }
  }

  // Ask for the same selection as a REPLACING plan. The pick is not repeated:
  // it is bound to this scope for its whole life (ADR 0041), and picking again
  // would be a second selection the operator never made. Setting the field
  // only says the operator asked — ADR 0081 decision 2 keeps the authorising
  // question on a native surface this page cannot draw, and the backend raises
  // it when the job starts, so this button cannot answer it.
  async function replace() {
    const current = prepared;
    if (checking || starting || !current || !isCurrent(current.intent)) return;
    const intent = current.intent;
    checking = true;
    error = null;
    try {
      if (current.mode === "upload") {
        const req = Object.freeze({ ...current.request, acknowledgeOverwrite: true });
        const result = await browser.preflightUpload(req);
        if (isCurrent(intent)) prepared = { intent, mode: "upload", request: req, report: result, destination: "" };
      } else {
        const req = Object.freeze({ ...current.request, acknowledgeOverwrite: true });
        const result = await browser.preflightDownload(req);
        if (isCurrent(intent)) prepared = { intent, mode: "download", request: req, report: result, destination: current.destination };
      }
    } catch (e) {
      if (isCurrent(intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      if (isCurrent(intent)) checking = false;
    }
  }

  async function start() {
    const current = prepared;
    if (starting || checking || !current || !isCurrent(current.intent)
      || (current.mode === "download" ? current.report.rejected : current.report.problems?.some((p) => p.reject))) return;
    // The selection is spent once it starts, so what described it goes too.
    prepared = null;
    folder = null;
    starting = true;
    error = null;
    try {
      const jobId = current.mode === "upload"
        ? await browser.startUpload(current.request)
        : await browser.startDownload(current.request);
      if (isCurrent(current.intent)) startedJobId = jobId;
    } catch (e) {
      if (isCurrent(current.intent)) error = e instanceof Error ? e.message : String(e);
    } finally {
      // A new picker cannot overtake this dialog's one pending Start.
      starting = false;
    }
  }

  const itemCount = $derived(current?.mode === "download" ? current.report.objects : current?.report.items?.length ?? 0);
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label={mode === "upload" ? "Upload" : "Download"} tabindex="-1" use:trapFocus={close}>
    <header>
      <h2>
        {mode === "upload"
          ? `Upload to ${browser.containerName}/${browser.prefix}`
          : downloadSource && downloadSource.members.length > 0
            ? `Download ${countOfSelected(downloadSource.members)} from ${browser.containerName}/${downloadSource.prefix}`
            : `Download ${browser.containerName}/${downloadSource?.prefix ?? browser.prefix}`}
      </h2>
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>

    {#if error}
      <p class="error callout danger" role="alert">{error}</p>
    {/if}

    {#if !report}
      <p class="hint">
        {mode === "upload"
          ? "Choose files to upload into this prefix, or a folder to upload as a folder of its own name inside it; every collision is reported before anything is written."
          : downloadSource && downloadSource.members.length > 0
            ? "Choose a destination directory; what is selected, with everything in each selected folder, is checked for collisions first."
            : "Choose a destination directory; the whole scope is checked for collisions first."}
      </p>
      {#if folder}
        <p class="folder" class:hint={!folderWarns(folder)} class:callout={folderWarns(folder)} class:warning={folderWarns(folder)} role="status">{folderSummary(folder, `${browser.containerName}/${browser.prefix}`)}</p>
      {/if}
      {#if mode === "download" && browser.settings.document}
        <p class="hint ceiling">
          Selections above {browser.settings.document.downloadObjectCeiling.toLocaleString("en-US")} objects are refused rather than cut short; the ceiling is set in Settings.
          {#if countedHere}
            {formatCount(browser.prefixCount!.scanned)} objects were counted in {browser.containerName}/{browser.countedPrefix}.
          {/if}
        </p>
      {/if}
    {:else}
      <div class="report num">
        {#if rejected}
          <h3 class="bad">Rejected — nothing was written</h3>
          <div class="problems">
            {#each rejections as p (p.kind + p.name)}
              <div class="problem">
                <span class="kind">{p.kind}</span>
                <span class="pname type-mono">{p.name}</span>
                <span class="detail">{p.detail}</span>
              </div>
            {/each}
          </div>
        {:else}
          <p class="summary">
            <strong>{itemCount}</strong>
            {mode === "upload" ? "files" : "objects"} · {formatBytes(report.totalBytes)}
            {mode === "download" ? ` → ${destDir}` : ""}
          </p>
        {/if}
        {#if folder}
          <p class="folder" class:note={!folderWarns(folder)} class:callout={folderWarns(folder)} class:warning={folderWarns(folder)}>{folderSummary(folder, `${browser.containerName}/${browser.prefix}`)}</p>
        {/if}
        {#if largeObjects === "slo"}
          <p class="note">
            Files larger than the chunk size upload in segments as static large objects.
          </p>
        {:else if largeObjects === "dlo"}
          <p class="note">
            Files larger than the chunk size upload in segments as dynamic large
            objects, whose reads depend on container-listing consistency{largeObjectsBecause}.
          </p>
        {/if}
        {#if collisionsOnly}
          <p class="hint">
            {#if mode === "upload"}
              {collisionCount === 1 ? "That name is" : "Those names are"} already in use at the destination.
            {:else}
              {collisionCount === 1 ? "That file is" : "Those files are"} already in the folder.
            {/if}
            Replacing what differs is allowed, and the application will ask you to confirm on its
            own dialog — not on this page — before anything is written.
          </p>
        {/if}
        {#if acknowledged && replacingCount > 0}
          <p class="hint">
            {#if mode === "upload"}
              This upload may REPLACE what is at {replacingCount}
              {replacingCount === 1 ? "name" : "names"}, each only where it differs from your file. The
              application will ask you to confirm, naming the account and what it observed about the
              container, before it writes anything.
            {:else}
              This download may REPLACE {replacingCount} {replacingCount === 1 ? "file" : "files"} in the
              folder, each only where it differs from the object. A replaced file is not kept. The
              application will ask you to confirm, naming the folder and where the files come from,
              before it writes anything.
            {/if}
          </p>
        {/if}
        {#if truncated}
          <p class="summary">{problemCount} problems ({problemRows.length} shown)</p>
        {/if}
        {#if warnings.length > 0}
          <div class="problems">
            {#each warnings as p (p.kind + p.name)}
              <div class="problem warn-row">
                <span class="kind warn-kind">{p.kind}</span>
                <span class="pname type-mono">{p.name}</span>
                <span class="detail">{p.detail}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    <JobSettingsFields bind:settings={jobSettings} defaultProfile={browser.settings.document?.defaultProfile} chunk={mode === "upload"} largeObjects={mode === "upload"} transfer={mode} onchange={invalidate} />

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
        <button class="primary" onclick={close}>Close</button>
      {:else}
        <button onclick={() => pickAndPreflight("files")} disabled={!canChoose}>
          {checking || starting
            ? "Working…"
            : mode === "upload"
              ? "Choose files…"
              : "Choose directory…"}
        </button>
        {#if mode === "upload"}
          <button onclick={() => pickAndPreflight("folder")} disabled={!canChoose}>Choose folder…</button>
        {/if}
        {#if collisionsOnly}
          <button class="primary" onclick={replace} disabled={checking || starting}>
            {mode === "upload"
              ? `Replace ${collisionCount} existing object${collisionCount === 1 ? "" : "s"}…`
              : "Replace existing files…"}
          </button>
        {/if}
        {#if report && !rejected}
          <button class="primary" onclick={start} disabled={checking || starting}>
            {mode === "upload" ? "Start upload" : "Start download"}
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
    background: color-mix(in srgb, var(--color-bg) 60%, transparent);
    display: grid;
    place-items: center;
    z-index: 30;
  }

  .dialog {
    width: min(42rem, calc(100vw - 4rem));
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
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
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .hint,
  .summary {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .note {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
  }

  .report {
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    padding: var(--space-3);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .bad {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .problems {
    max-height: 12rem;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-xs);
  }

  .problem {
    display: grid;
    grid-template-columns: 8rem minmax(8rem, 1fr) minmax(10rem, 1.6fr);
    gap: var(--space-2);
    align-items: baseline;
  }

  .kind {
    color: var(--color-danger);
    font-weight: var(--weight-semibold);
  }

  .warn-kind {
    color: var(--color-warning);
  }

  .pname,
  .detail {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .detail {
    color: var(--color-text-faint);
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
</style>
