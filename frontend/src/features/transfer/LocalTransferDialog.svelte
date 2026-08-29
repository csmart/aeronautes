<!-- Upload / download dialog (§27.5): the COMPLETE collision preflight
  renders before any write, and the v1 policy is reject-on-collision —
  Start only exists for a clean report. Warnings (portability hazards)
  show but do not block on this target. -->
<script lang="ts">
  import type { BrowserState } from "../browser/browserState.svelte";
  import { formatBytes } from "../../lib/format";

  // The common §27.5 report shape (upload and download preflights share
  // problems/items/totalBytes; the union of the generated classes trips
  // the checker, so the dialog types the surface it reads).
  interface PreflightView {
    items: unknown[] | null;
    problems: { name: string; kind: string; detail: string; reject: boolean }[] | null;
    totalBytes: number;
  }

  let {
    browser,
    mode,
    onClose,
  }: { browser: BrowserState; mode: "upload" | "download"; onClose: () => void } = $props();

  let picked: string[] = $state([]);
  let destDir = $state("");
  let report = $state<PreflightView | null>(null);
  let busy = $state(false);
  let error: string | null = $state(null);
  let startedJobId: string | null = $state(null);

  const rejected = $derived(report?.problems?.some((p) => p.reject) ?? false);
  const warnings = $derived(report?.problems?.filter((p) => !p.reject) ?? []);
  const rejections = $derived(report?.problems?.filter((p) => p.reject) ?? []);

  async function pickAndPreflight() {
    busy = true;
    error = null;
    report = null;
    try {
      if (mode === "upload") {
        picked = await browser.pickUploadFiles();
        if (picked.length === 0) {
          busy = false;
          return;
        }
        report = await browser.preflightUpload(picked);
      } else {
        destDir = await browser.pickDownloadDir();
        if (!destDir) {
          busy = false;
          return;
        }
        report = await browser.preflightDownload(destDir);
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function start() {
    busy = true;
    error = null;
    try {
      startedJobId =
        mode === "upload"
          ? await browser.startUpload(picked)
          : await browser.startDownload(destDir);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const itemCount = $derived(report?.items?.length ?? 0);
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="dialog" aria-label={mode === "upload" ? "Upload" : "Download"} tabindex="-1">
    <header>
      <h2>
        {mode === "upload"
          ? `Upload to ${browser.containerName}/${browser.prefix}`
          : `Download ${browser.containerName}/${browser.prefix}`}
      </h2>
      <button class="close" onclick={onClose} aria-label="Close">✕</button>
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
    {:else}
      <div class="report num">
        {#if rejections.length > 0}
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
        <button class="primary" onclick={onClose}>Close</button>
      {:else}
        <button onclick={pickAndPreflight} disabled={busy}>
          {busy
            ? "Working…"
            : mode === "upload"
              ? "Choose files…"
              : "Choose directory…"}
        </button>
        {#if report && !rejected}
          <button class="primary" onclick={start} disabled={busy}>
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
