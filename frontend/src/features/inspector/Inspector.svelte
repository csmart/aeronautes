<script lang="ts">
  import { largeObjectDetail, objectMetaFixture } from "../../lib/fixtures";
  import { formatBytes, formatCount } from "../../lib/format";
  import type { InspectorTarget } from "../../lib/types";

  let { target, account }: {
    target: InspectorTarget;
    account: { name: string; containers: number; objects: number; bytes: number; meta: Record<string, string>; tempURLKeys: number };
  } = $props();

  const objectDetail = $derived(
    target.kind === "object" ? (largeObjectDetail[target.object.name] ?? null) : null,
  );
  const objectMeta = $derived(
    target.kind === "object" ? (objectMetaFixture[target.object.name] ?? {}) : {},
  );

  function rawHeaders(): string {
    if (target.kind !== "object") {
      return "";
    }
    const o = target.object;
    const lines = [
      `Content-Length: ${objectDetail?.logicalBytes ?? o.bytes}`,
      `Content-Type: ${o.contentType}`,
      `Etag: ${objectDetail ? '"' + o.etag + '"' : o.etag}`,
      `Last-Modified: ${o.lastModified}`,
    ];
    if (objectDetail?.kind === "dlo") {
      lines.push(`X-Object-Manifest: ${objectDetail.manifest}`);
    }
    if (objectDetail?.kind === "slo") {
      lines.push("X-Static-Large-Object: True");
    }
    for (const [k, v] of Object.entries(objectMeta)) {
      lines.push(`${k}: ${v}`);
    }
    lines.push("X-Trans-Id: txfixture-0000000000static");
    return lines.join("\n");
  }
</script>

{#snippet kv(label: string, value: string, mono?: boolean)}
  <div class="kv">
    <span class="kv-label">{label}</span>
    <span class="kv-value {mono ? 'type-mono' : ''} num">{value}</span>
  </div>
{/snippet}

<aside class="pane" aria-label="Inspector">
  {#if target.kind === "account"}
    <header class="head">
      <h2 class="title type-display">{account.name}</h2>
      <p class="subtitle">Account</p>
    </header>
    <section class="card">
      {@render kv("Containers", formatCount(account.containers))}
      {@render kv("Objects", formatCount(account.objects))}
      {@render kv("Bytes used", formatBytes(account.bytes))}
      {@render kv("TempURL keys", account.tempURLKeys > 0 ? `${account.tempURLKeys} set` : "none")}
    </section>
    <section class="card">
      <h3 class="card-title">Account metadata</h3>
      {#each Object.entries(account.meta) as [key, value] (key)}
        {@render kv(key, value)}
      {/each}
    </section>
  {:else if target.kind === "container"}
    <header class="head">
      <h2 class="title type-display">{target.container.name}</h2>
      <p class="subtitle">Container · policy {target.container.policy}</p>
    </header>
    <section class="card">
      {@render kv("Objects", formatCount(target.container.objectCount))}
      {@render kv("Bytes", formatBytes(target.container.bytes))}
      {@render kv("Storage policy", target.container.policy)}
      {@render kv("Read ACL", "—")}
      {@render kv("Write ACL", "—")}
      {@render kv("Versioning", "off")}
      {@render kv("Quota", "none")}
    </section>
    <section class="card">
      <h3 class="card-title">Segment classification</h3>
      {#if target.container.segmentRole === "confirmed"}
        <p class="teach">
          Confirmed segment store: manifests elsewhere reference objects in this
          container. Deleting here breaks the logical objects they assemble.
        </p>
      {:else if target.container.segmentRole === "suspected"}
        <p class="teach">
          Named like a segment store, but nothing confirms it yet. Treated as
          possible backstage — shown, never hidden.
        </p>
      {:else}
        <p class="teach">Ordinary logical container.</p>
      {/if}
    </section>
  {:else if target.kind === "prefix"}
    {@const label = target.prefix.prefix}
    <header class="head">
      <h2 class="title type-mono prefix-title">{label}</h2>
      <p class="subtitle">Prefix · {target.prefix.markerBacked ? "marker-backed" : "implied"}</p>
    </header>
    <section class="card">
      {#if target.prefix.markerBacked}
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
  {:else}
    {@const o = target.object}
    <header class="head">
      <h2 class="title type-mono object-title">{o.name}</h2>
      <p class="subtitle">
        {#if objectDetail?.kind === "slo"}Static Large Object (SLO)
        {:else if objectDetail?.kind === "dlo"}Dynamic Large Object (DLO)
        {:else}Object{/if}
      </p>
    </header>

    <section class="card">
      {#if objectDetail}
        {@render kv("Logical size", formatBytes(objectDetail.logicalBytes))}
        {@render kv("Listed size", formatBytes(o.bytes))}
      {:else}
        {@render kv("Size", formatBytes(o.bytes))}
      {/if}
      {@render kv("Content type", o.contentType)}
      {@render kv("ETag", o.etag, true)}
      {@render kv("Last modified", o.lastModified, true)}
    </section>

    {#if objectDetail}
      <section class="card">
        <h3 class="card-title">Large object</h3>
        {@render kv("Manifest", objectDetail.manifest, true)}
        {@render kv("Segments", String(objectDetail.segments.length))}
        <table class="segments num">
          <thead>
            <tr><th>#</th><th>Segment</th><th class="right">Size</th></tr>
          </thead>
          <tbody>
            {#each objectDetail.segments as segment, i (segment.name)}
              <tr>
                <td>{i + 1}</td>
                <td class="type-mono seg-name">{segment.name}</td>
                <td class="right">{formatBytes(segment.bytes)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </section>
    {/if}

    {#if Object.keys(objectMeta).length > 0}
      <section class="card">
        <h3 class="card-title">User metadata</h3>
        {#each Object.entries(objectMeta) as [key, value] (key)}
          {@render kv(key.replace("X-Object-Meta-", ""), value)}
        {/each}
      </section>
    {/if}

    <section class="card">
      <h3 class="card-title">Raw headers</h3>
      <pre class="raw type-mono">{rawHeaders()}</pre>
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

  .segments {
    width: 100%;
    margin-top: var(--space-2);
    border-collapse: collapse;
    font-size: var(--text-xs);
  }

  .segments th {
    text-align: left;
    font-weight: var(--weight-medium);
    color: var(--color-text-faint);
    padding: var(--space-1) var(--space-1);
    border-bottom: 1px solid var(--color-border);
  }

  .segments td {
    padding: var(--space-1) var(--space-1);
    border-bottom: 1px solid var(--color-border);
    color: var(--color-text-secondary);
  }

  .seg-name {
    overflow-wrap: anywhere;
  }

  .right {
    text-align: right;
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
