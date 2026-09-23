<!-- Upload / download dialog (§27.5): the collision preflight renders
  before any write under one cross-platform namespace policy, and the
  v1 rule is reject-on-collision — Start only exists for a clean
  report. -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BrowserState } from "../browser/browserState.svelte";
  import type { DownloadRequestDTO, UploadRequestDTO } from "../../lib/api";
  import { formatBytes, formatCount } from "../../lib/format";

  // Upload still ships the complete item list; download is a bounded
  // view with first-class counts (ADR 0056).
  interface ProblemRow { name: string; kind: string; detail: string; reject: boolean }
  interface PreflightView {
    items?: unknown[] | null;
    problems?: ProblemRow[] | null;
    problemSamples?: ProblemRow[] | null;
    totalBytes: number;
    objects?: number;
    problemCount?: number;
    rejected?: boolean;
  }

  let {
    browser,
    mode,
    onClose,
  }: { browser: BrowserState; mode: "upload" | "download"; onClose: () => void } = $props();
  // The ceiling sentence needs the settings document; a dialog opened
  // before the drawer ever loaded it asks once (ADR 0072 decision 4).
  untrack(() => {
    if (mode === "download" && !browser.appSettings) void browser.loadAppSettings();
  });

  let checking = $state(false);
  let starting = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);
  let closed = $state(false);
  let generation = 0;
  type Scope = Omit<UploadRequestDTO, "selectionToken">;
  interface Intent {
    generation: number;
    browser: BrowserState;
    scopeRevision: number;
    localRevision: number;
    mode: "upload" | "download";
    scope: Scope;
  }
  type Prepared = {
    intent: Intent;
    report: PreflightView;
    destination: string;
  } & ({ mode: "upload"; request: UploadRequestDTO } | { mode: "download"; request: DownloadRequestDTO });
  let prepared = $state.raw<Prepared | null>(null);
  const report = $derived(prepared && isCurrent(prepared.intent) ? prepared.report : null);
  const destDir = $derived(report ? prepared?.destination ?? "" : "");
  const canChoose = $derived(!closed && !checking && !starting && !browser.connecting
    && !!browser.connectedBookmarkId && !!browser.containerName);

  const problemRows = $derived(mode === "download" ? report?.problemSamples ?? [] : report?.problems ?? []);
  const rejected = $derived(mode === "download" ? report?.rejected ?? false : report?.problems?.some((p) => p.reject) ?? false);
  const warnings = $derived(problemRows.filter((p) => !p.reject));
  const rejections = $derived(problemRows.filter((p) => p.reject));
  const problemCount = $derived(mode === "download" ? report?.problemCount ?? 0 : problemRows.length);
  const truncated = $derived(mode === "download" && problemCount > problemRows.length);

  function scope(): Scope {
    return {
      bookmarkId: browser.connectedBookmarkId ?? "",
      container: browser.containerName ?? "",
      prefix: browser.prefix,
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" },
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

  async function pickAndPreflight() {
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
        const picked = await browser.pickUploadFiles(
          { bookmarkId: capturedScope.bookmarkId, container: capturedScope.container, prefix: capturedScope.prefix });
        if (!isCurrent(intent) || !picked?.token || !picked.files?.length) return;
        const req = Object.freeze({ ...capturedScope, selectionToken: picked.token });
        const result = await browser.preflightUpload(req);
        if (isCurrent(intent)) prepared = { intent, mode: "upload", request: req, report: result, destination: "" };
      } else {
        const dir = await browser.pickDownloadDir(
          { bookmarkId: capturedScope.bookmarkId, container: capturedScope.container, prefix: capturedScope.prefix });
        if (!isCurrent(intent) || !dir?.token) return;
        const req = Object.freeze({ ...capturedScope, destDirToken: dir.token });
        const result = await browser.preflightDownload(req);
        if (isCurrent(intent)) prepared = { intent, mode: "download", request: req, report: result, destination: dir.dir };
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
    prepared = null;
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

  const itemCount = $derived(mode === "download" ? report?.objects ?? 0 : report?.items?.length ?? 0);
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div class="dialog" role="dialog" aria-modal="true" aria-label={mode === "upload" ? "Upload" : "Download"} tabindex="-1" use:trapFocus={close}>
    <header>
      <h2>
        {mode === "upload"
          ? `Upload to ${browser.containerName}/${browser.prefix}`
          : `Download ${browser.containerName}/${browser.prefix}`}
      </h2>
      <button class="close" onclick={close} aria-label="Close">✕</button>
    </header>

    {#if error}
      <p class="error">{error}</p>
    {/if}

    {#if !report}
      <p class="hint">
        {mode === "upload"
          ? "Choose files; every collision is reported before anything is written."
          : "Choose a destination directory; the whole scope is checked for collisions first."}
      </p>
      {#if mode === "download" && browser.appSettings}
        <p class="hint ceiling">
          Selections above {browser.appSettings.downloadObjectCeiling.toLocaleString("en-US")} objects are refused rather than cut short; the ceiling is set in the jobs drawer.
          {#if browser.prefixCount?.done && !browser.prefixCount.error}
            This prefix counted {formatCount(browser.prefixCount.scanned)} objects.
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

    <footer>
      {#if startedJobId}
        <span class="started">Job {startedJobId} started — follow it in the drawer.</span>
        <button class="primary" onclick={close}>Close</button>
      {:else}
        <button onclick={pickAndPreflight} disabled={!canChoose}>
          {checking || starting
            ? "Working…"
            : mode === "upload"
              ? "Choose files…"
              : "Choose directory…"}
        </button>
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

  .error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
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
