<script lang="ts">
  import { formatBytes, formatCount } from "../../lib/format";
  import { suspectedSegmentStore, type BrowserState } from "../browser/browserState.svelte";

  let { browser }: { browser: BrowserState } = $props();

  // The inspector follows selection (§53): object/prefix row if selected,
  // else the open container, else the account.
  const entry = $derived(browser.selectedEntry);
  const detail = $derived(browser.objectDetail);

  function sortedEntries(record: { [key: string]: string | undefined } | null): [string, string][] {
    return Object.entries(record ?? {})
      .filter((kv): kv is [string, string] => kv[1] !== undefined)
      .sort(([a], [b]) => a.localeCompare(b));
  }

  const rawHeaderText = $derived(
    sortedEntries(detail?.rawHeaders ?? null)
      .map(([k, v]) => `${k}: ${v}`)
      .join("\n"),
  );
</script>

{#snippet kv(label: string, value: string, mono?: boolean)}
  <div class="kv">
    <span class="kv-label">{label}</span>
    <span class="kv-value {mono ? 'type-mono' : ''} num">{value}</span>
  </div>
{/snippet}

<aside class="pane" aria-label="Inspector">
  {#if entry && entry.kind === "object"}
    <header class="head">
      <h2 class="title type-mono object-title">{entry.name}</h2>
      <p class="subtitle">
        {#if browser.detailLoading}Object · HEAD…
        {:else if detail?.class === "slo"}Static Large Object (SLO)
        {:else if detail?.class === "dlo"}Dynamic Large Object (DLO)
        {:else}Object{/if}
      </p>
    </header>

    <section class="card">
      {#if detail && detail.class !== "plain"}
        {@render kv("Logical size", formatBytes(detail.contentLength))}
        {@render kv("Listed size", formatBytes(entry.bytes))}
      {:else}
        {@render kv("Size", formatBytes(detail?.contentLength ?? entry.bytes))}
      {/if}
      {@render kv("Content type", detail?.contentType ?? entry.contentType)}
      {@render kv("ETag", detail?.etag ?? entry.etag, true)}
      {@render kv("Last modified", detail?.lastModified ?? entry.lastModified, true)}
      {#if detail?.deleteAt}
        {@render kv("Delete at", detail.deleteAt, true)}
      {/if}
      {#if detail?.transId}
        {@render kv("Trans id", detail.transId, true)}
      {/if}
    </section>

    {#if browser.detailError}
      <section class="card">
        <p class="error">HEAD failed: {browser.detailError}</p>
      </section>
    {/if}

    {#if detail && detail.class === "dlo"}
      <section class="card">
        <h3 class="card-title">Large object</h3>
        {@render kv("Manifest", detail.manifest, true)}
        <p class="teach">
          Segments live under this prefix; the manifest assembles whatever is
          there at read time. The segment table arrives in Phase 4.
        </p>
      </section>
    {:else if detail && detail.class === "slo"}
      <section class="card">
        <h3 class="card-title">Large object</h3>
        <p class="teach">
          Static manifest with a fixed segment list. The segment table arrives
          in Phase 4 (multipart-manifest=get).
        </p>
      </section>
    {/if}

    {#if detail && sortedEntries(detail.meta).length > 0}
      <section class="card">
        <h3 class="card-title">User metadata</h3>
        {#each sortedEntries(detail.meta) as [key, value] (key)}
          {@render kv(key, value)}
        {/each}
      </section>
    {/if}

    {#if detail}
      <section class="card">
        <h3 class="card-title">Raw headers</h3>
        <pre class="raw type-mono">{rawHeaderText}</pre>
      </section>
    {/if}
  {:else if entry && entry.kind === "prefix"}
    <header class="head">
      <h2 class="title type-mono prefix-title">{entry.name}</h2>
      <p class="subtitle">Prefix · {entry.markerBacked ? "marker-backed" : "implied"}</p>
    </header>
    <section class="card">
      {#if entry.markerBacked}
        <p class="teach">
          An explicit zero-byte <span class="type-mono">application/directory</span>
          marker object exists at this name. The prefix survives even with no
          children.
        </p>
      {:else}
        <p class="teach">
          This prefix exists because objects live beneath it; no explicit marker
          object is required.
        </p>
      {/if}
    </section>
  {:else if browser.container}
    <header class="head">
      <h2 class="title type-display">{browser.container.name}</h2>
      <p class="subtitle">Container</p>
    </header>
    <section class="card">
      {@render kv("Objects", formatCount(browser.container.count))}
      {@render kv("Bytes", formatBytes(browser.container.bytes))}
    </section>
    <section class="card">
      <h3 class="card-title">Segment classification</h3>
      {#if suspectedSegmentStore(browser.container.name)}
        <p class="teach">
          Named like a segment store, but nothing confirms it yet. Treated as
          possible backstage — shown, never hidden. Manifest evidence arrives
          with Phase 2.
        </p>
      {:else}
        <p class="teach">Ordinary logical container.</p>
      {/if}
    </section>
  {:else if browser.account}
    <header class="head">
      <h2 class="title type-display">{browser.account.name}</h2>
      <p class="subtitle">Account</p>
    </header>
    <section class="card">
      {@render kv("Containers", formatCount(browser.account.containers))}
      {@render kv("Objects", formatCount(browser.account.objects))}
      {@render kv("Bytes used", formatBytes(browser.account.bytesUsed))}
      {#if browser.account.quotaBytes > 0}
        {@render kv("Quota", formatBytes(browser.account.quotaBytes))}
      {/if}
      {@render kv(
        "TempURL keys",
        (browser.account.tempUrlKeySlots?.length ?? 0) > 0
          ? `${browser.account.tempUrlKeySlots?.length} set`
          : "none",
      )}
    </section>
    {#if sortedEntries(browser.account.meta).length > 0}
      <section class="card">
        <h3 class="card-title">Account metadata</h3>
        {#each sortedEntries(browser.account.meta) as [key, value] (key)}
          {@render kv(key, value)}
        {/each}
      </section>
    {/if}
  {:else}
    <header class="head">
      <h2 class="title type-display">Aeronautes</h2>
      <p class="subtitle">Not connected</p>
    </header>
    <section class="card">
      <p class="teach">
        Connect a bookmark to inspect an account. The inspector — not the file
        list — is the centre of this application.
      </p>
    </section>
  {/if}
</aside>

<style>
  .pane {
    min-height: 0;
    overflow-y: auto;
    padding: var(--space-3);
    background: var(--color-bg);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .head {
    padding: 0 var(--space-1);
  }

  .title {
    margin: 0;
    font-size: var(--text-xl);
    overflow-wrap: anywhere;
  }

  .object-title,
  .prefix-title {
    font-size: var(--text-lg);
    white-space: pre-wrap;
  }

  .subtitle {
    margin: var(--space-1) 0 0;
    font-size: var(--text-sm);
    color: var(--color-accent-strong);
  }

  .card {
    padding: var(--space-3);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-card);
  }

  .card-title {
    margin: 0 0 var(--space-2);
    font-size: var(--text-sm);
    font-weight: var(--weight-semibold);
    color: var(--color-text-secondary);
  }

  .kv {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    padding: var(--space-1) 0;
    font-size: var(--text-sm);
  }

  .kv-label {
    color: var(--color-text-faint);
    white-space: nowrap;
  }

  .kv-value {
    text-align: right;
    overflow-wrap: anywhere;
  }

  .teach {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }

  .raw {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-xs);
    line-height: 1.6;
    color: var(--color-text-secondary);
    background: var(--color-bg-inset);
    border-radius: var(--radius-control);
    overflow-x: auto;
  }
</style>
