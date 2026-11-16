<script lang="ts">
  import { onDestroy } from "svelte";
  import { formatBytes, formatCount } from "../../lib/format";
  import type { PrefixDetailDTO } from "../../lib/api";
  import { v1Offers } from "../../lib/v1.svelte";
  import { isLiteralPlainObject } from "../../lib/objectKind";
  import type { BrowserState } from "../browser/browserState.svelte";
import { isLink } from "../browser/browserState.svelte";
  import ObjectEditPanel from "./ObjectEditPanel.svelte";
  import ContainerEditPanel from "./ContainerEditPanel.svelte";
  import AccountEditPanel from "./AccountEditPanel.svelte";
  import TempUrlKeys from "./TempUrlKeys.svelte";

  let { browser }: { browser: BrowserState } = $props();

  // The inspector follows selection (§53): object/prefix row if selected,
  // else the open container, else the account.
  const entry = $derived(browser.selectedEntry);
  const detail = $derived(browser.objectDetail);

  let editingObject = $state(false);
  let editingContainer = $state(false);
  let editingAccount = $state(false);
  let markerBusy = $state(false);
  let markerError: string | null = $state(null);
  let alive = true;
  onDestroy(() => { alive = false; });

  // Identity survives filter reordering, while a fresh selection or scope ends editing.
  $effect(() => {
    void browser.detailIdentity;
    void browser.scopeRevision;
    editingObject = false;
    editingContainer = false;
    editingAccount = false;
    markerError = null;
    markerBusy = false;
  });

  // Where a manifest's segments live, for the backstage jump and the
  // delete dialog's honesty line.
  const segmentsAt = $derived.by(() => {
    if (detail?.class === "dlo" && detail.manifest !== "") {
      return detail.manifest;
    }
    const first = browser.sloManifest?.entries?.find((e) => e.kind === "object");
    if (first) {
      const dir = first.object?.includes("/")
        ? first.object.slice(0, first.object.lastIndexOf("/") + 1)
        : "";
      return `${first.container}/${dir}`;
    }
    return "";
  });

  function openBackstage() {
    const at = segmentsAt;
    if (at === "" || !detail) {
      return;
    }
    const slash = at.indexOf("/");
    browser.openBackstage(at.slice(0, slash), at.slice(slash + 1), detail.name);
  }

  async function markerAction(expected: PrefixDetailDTO, action: (ownsView: () => boolean) => Promise<unknown>) {
    if (!alive || markerBusy || browser.connecting || !browser.connectedBookmarkId
      || browser.prefixDetail !== expected || browser.detailIdentity?.name !== expected.prefix) return;
    const owner = browser;
    const scope = browser.scopeRevision;
    const selection = browser.detailIdentity;
    const current = () => alive && owner === browser && scope === browser.scopeRevision && selection === browser.detailIdentity;
    markerBusy = true;
    markerError = null;
    try {
      await action(current);
    } catch (err) {
      if (current()) markerError = err instanceof Error ? err.message : String(err);
    } finally {
      if (current()) markerBusy = false;
    }
  }

  function sortedEntries(record: { [key: string]: string | undefined } | null): [string, string][] {
    return Object.entries(record ?? {})
      .filter((kv): kv is [string, string] => kv[1] !== undefined)
      .sort(([a], [b]) => a.localeCompare(b));
  }

  function plural(count: number, one: string, many: string): string {
    return `${formatCount(count)} ${count === 1 ? one : many}`;
  }
</script>

{#snippet kv(label: string, value: string, mono?: boolean)}
  <div class="kv">
    <span class="kv-label">{label}</span>
    <span class="kv-value {mono ? 'type-mono' : ''} num">{value}</span>
  </div>
{/snippet}

<!-- Every header of the reply, secrets already redacted by the backend. It
  is for diagnosing what the store said, so it starts closed. -->
{#snippet rawHeaders(record: { [key: string]: string | undefined } | null)}
  <details class="card raw-card">
    <summary class="card-title">Raw headers</summary>
    <pre class="raw type-mono">{sortedEntries(record)
        .map(([k, v]) => `${k}: ${v}`)
        .join("\n")}</pre>
  </details>
{/snippet}

<aside class="pane" aria-label="Inspector">
  {#if browser.chosenRows.length >= 2}
    <!-- ADR 0093 decision 1: several rows selected show a summary from the
      listing's own facts, and no row is inspected, so nothing is read. -->
    {@const chosen = browser.chosenRows}
    {@const objects = chosen.filter((row) => row.kind === "object")}
    <header class="head">
      <h2 class="title">{chosen.length} rows selected</h2>
      <p class="subtitle">Selection · no row is inspected</p>
    </header>
    <section class="card">
      {@render kv("Objects", formatCount(objects.length))}
      {@render kv("Listed bytes", formatBytes(objects.reduce((sum, row) => sum + row.bytes, 0)))}
      {@render kv("Prefix rows", formatCount(chosen.length - objects.length))}
      <p class="teach">
        Listed facts only; nothing is read for a summary. Ctrl or Cmd with a click toggles a row, Shift with a click
        extends the selection, Ctrl or Cmd with A selects every loaded row, and Escape clears it.
      </p>
      {#if v1Offers.objectDelete && objects.length > 0}
        <p class="teach">
          Delete selected… in the working list deletes the objects as one job, one request each, whatever each name
          holds when its request arrives; prefix rows are left out.
        </p>
      {/if}
    </section>
  {:else if entry && entry.kind === "object"}
    <header class="head">
      <h2 class="title type-mono object-title">{entry.name}</h2>
      <p class="subtitle">
        {#if browser.detailLoading}Object · HEAD…
        {:else if detail?.followedSymlink}Symlink → {detail.symlinkTarget}
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
        <p class="error callout danger">HEAD failed: {browser.detailError}</p>
      </section>
    {/if}

    {#if browser.mutationNotice}
      <p class="mutation-notice" role="status">{browser.mutationNotice}</p>
    {/if}

    {#if detail && !editingObject}
      <button class="edit-btn" onclick={() => (editingObject = true)}>Edit metadata</button>
      {#if v1Offers.objectDelete && isLiteralPlainObject(detail)}
        <button class="edit-btn danger" onclick={() => browser.openObjectDelete()}>Delete object…</button>
      {/if}
    {:else if detail && editingObject}
      <ObjectEditPanel {browser} {detail} onDone={() => (editingObject = false)} />
    {/if}


    {#if detail && detail.class === "dlo"}
      <section class="card">
        <h3 class="card-title">Large object · dynamic</h3>
        {@render kv("Manifest", detail.manifest, true)}
        <p class="teach">
          A read assembles every object under this prefix, in name order, as
          it exists at that moment.
        </p>
        {#if browser.dloSegments}
          {@const preview = browser.dloSegments}
          <table class="segments num">
            <thead>
              <tr><th>#</th><th>Segment</th><th class="right">Size</th></tr>
            </thead>
            <tbody>
              {#each preview.segments ?? [] as segment, i (segment.name)}
                <tr>
                  <td>{i + 1}</td>
                  <td class="type-mono seg-name">{segment.name}</td>
                  <td class="right">{formatBytes(segment.bytes)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
          <p class="teach num">
            {#if preview.hasMore}
              first {preview.segments?.length} segments ({formatBytes(preview.previewBytes)} shown);
              more exist — logical size {formatBytes(detail.contentLength)}
            {:else}
              {preview.segments?.length} segments · {formatBytes(preview.previewBytes)}
            {/if}
          </p>
        {:else if browser.detailLoading}
          <p class="teach">fetching segment preview…</p>
        {:else}
          <p class="teach">Segment details are unavailable.</p>
          <button class="edit-btn inline" onclick={() => browser.refreshDetail()}>Reload segment details</button>
        {/if}
        <div class="lo-actions">
          <button class="edit-btn inline" onclick={openBackstage}>Open segments backstage</button>
          {#if v1Offers.manifestDelete && isLink(detail)}
            <p class="teach">This name is a symbolic link to the manifest: deleting a link is not offered here, and the manifest is deleted at its own name.</p>
          {:else if v1Offers.manifestDelete}
            <button class="edit-btn inline danger" onclick={() => browser.openManifestDelete()}>
              Delete manifest…
            </button>
          {/if}
        </div>
      </section>
    {:else if detail && detail.class === "slo"}
      <section class="card">
        <h3 class="card-title">Large object · exact manifest</h3>
        {#if browser.sloManifest}
          <table class="segments num">
            <thead>
              <tr><th>#</th><th>Segment</th><th class="right">Size</th></tr>
            </thead>
            <tbody>
              {#each browser.sloManifest.entries ?? [] as segment, i (i)}
                <tr>
                  <td>{i + 1}</td>
                  {#if segment.kind === "object"}
                    <td class="type-mono seg-name">
                      {segment.container}/{segment.object}{segment.range
                        ? ` [${segment.range}]`
                        : ""}{segment.nestedSlo ? " (sub-SLO)" : ""}
                    </td>
                    <td class="right">{formatBytes(segment.size)}</td>
                  {:else if segment.kind === "data"}
                    <td class="type-mono seg-name">(inline data)</td>
                    <td class="right">{formatBytes(segment.dataSize)}</td>
                  {:else}
                    <td class="type-mono seg-name">(unrecognised entry — preserved)</td>
                    <td class="right">—</td>
                  {/if}
                </tr>
              {/each}
            </tbody>
          </table>
          {@const declared = (browser.sloManifest.entries ?? []).reduce(
            (sum, e) => sum + (e.kind === "object" ? e.size : e.dataSize),
            0,
          )}
          <p class="teach num">
            {browser.sloManifest.entries?.length} entries · declared
            {formatBytes(declared)} · HEAD reports {formatBytes(detail.contentLength)}
          </p>
        {:else if browser.detailLoading}
          <p class="teach">fetching exact manifest…</p>
        {:else}
          <p class="teach">Exact manifest details are unavailable.</p>
          <button class="edit-btn inline" onclick={() => browser.refreshDetail()}>Reload segment details</button>
        {/if}
        <div class="lo-actions">
          {#if segmentsAt !== ""}
            <button class="edit-btn inline" onclick={openBackstage}>Open segments backstage</button>
          {/if}
          {#if v1Offers.manifestDelete && isLink(detail)}
            <p class="teach">This name is a symbolic link to the manifest: deleting a link is not offered here, and the manifest is deleted at its own name.</p>
          {:else if v1Offers.manifestDelete}
            <button class="edit-btn inline danger" onclick={() => browser.openManifestDelete()}>
              Delete manifest…
            </button>
          {/if}
        </div>
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
      {@render rawHeaders(detail.rawHeaders)}
    {/if}
  {:else if entry && entry.kind === "prefix"}
    {@const pd = browser.prefixDetail}
    <header class="head">
      <h2 class="title type-mono prefix-title">{entry.name}</h2>
      <p class="subtitle">
        {#if browser.detailLoading}Prefix · classifying…
        {:else if pd?.markerBacked}Prefix · marker-backed
        {:else if pd?.selfObject}Prefix · object at this name
        {:else if pd}Prefix · implied
        {:else}Prefix{/if}
      </p>
    </header>
    <section class="card">
      {#if browser.detailError}
        <p class="error callout danger">classification failed: {browser.detailError}</p>
      {:else if pd?.markerBacked}
        <p class="teach">
          An explicit zero-byte <span class="type-mono">application/directory</span>
          marker object exists at this name. The prefix survives even with no
          children.
        </p>
      {:else if pd?.selfObject}
        <p class="teach">
          A <strong>real object</strong> ({pd.selfObject.contentType},
          {pd.selfObject.contentLength} bytes) is stored at this exact name —
          data wearing a folder suit. The listing folds it into this row;
          it is not a directory marker.
        </p>
      {:else if pd}
        <p class="teach">
          This prefix exists because objects live beneath it; no explicit marker
          object is required.
        </p>
      {/if}
    </section>
    {#if (v1Offers.markerCreate || v1Offers.markerDelete) && pd && !browser.detailLoading}
      <section class="card">
        <h3 class="card-title">Marker</h3>
        {#if !pd.selfObject}
          {#if v1Offers.markerCreate}
            <button
              class="edit-btn inline"
              disabled={markerBusy}
              onclick={() => markerAction(pd, () => browser.createMarker(pd.prefix))}
            >
              Create marker object
            </button>
            <p class="teach">
              An explicit marker keeps this prefix visible even with no children.
            </p>
          {:else}
            <p class="teach">Marker creation is not offered in this build.</p>
          {/if}
        {:else if pd.markerBacked && pd.selfObject && isLink(pd.selfObject)}
          <p class="teach">This name is a symbolic link to a marker: deleting a link is not offered here, and the marker is deleted at its own name.</p>
        {:else if pd.markerBacked}
          {#if v1Offers.markerDelete}
            <button class="edit-btn inline danger" onclick={() => browser.openMarkerDelete()}>
              Delete marker…
            </button>
            <p class="teach">Opens the delete for the marker object only; children are untouched.</p>
          {:else}
            <p class="teach">Marker deletion is not offered in this build; the marker and its children are untouched.</p>
          {/if}
        {:else}
          <p class="teach">No marker action: a real object occupies this name.</p>
        {/if}
        {#if markerError}
          <p class="error callout danger">{markerError}</p>
        {/if}
      </section>
    {/if}
  {:else if browser.container}
    {@const cd = browser.containerDetail}
    {@const containerClass = cd?.class ?? browser.container.class}
    <header class="head">
      <h2 class="title type-display">{browser.container.name}</h2>
      <p class="subtitle">
        Container{cd?.storagePolicy ? ` · policy ${cd.storagePolicy}` : ""}
      </p>
    </header>
    <section class="card">
      {@render kv("Objects", formatCount(cd?.objectCount ?? browser.container.count))}
      {@render kv("Bytes", formatBytes(cd?.bytesUsed ?? browser.container.bytes))}
      {#if cd}
        {@render kv("Storage policy", cd.storagePolicy || "—")}
        {@render kv("Read ACL", cd.readAcl || "—", cd.readAcl !== "")}
        {#if browser.openContainerSettings?.publicRead}
          <p class="teach public-teach">
            Anyone with the URL can read objects in this container (read ACL <span class="type-mono">.r:*</span>){browser.openContainerSettings.publicListing ? ", and can list its object names" : ""}.
          </p>
        {/if}
        {@render kv("Write ACL", cd.writeAcl || "—", cd.writeAcl !== "")}
        {#if cd.versioningMode === "off"}
          {@render kv("Versioning", "off")}
        {:else}
          {@render kv(
            "Versioning",
            cd.versioningMode === "enabled"
              ? "in-container versions (object_versioning)"
              : `${cd.versioningMode === "history_location" ? "history" : "versions"} → ${cd.versioningArchive}`,
            true,
          )}
        {/if}
        {@render kv("Quota", cd.quotaBytes > 0 ? formatBytes(cd.quotaBytes) : "none")}
        {#if cd.quotaCount > 0}
          {@render kv("Quota (objects)", formatCount(cd.quotaCount))}
        {/if}
        {@render kv("CORS", cd.cors ? "configured" : "—")}
        {@render kv("Static web", cd.staticWeb ? "configured" : "—")}
      {/if}
    </section>
    {#if cd}
      <TempUrlKeys {browser} container={cd.name} slots={cd.tempUrlKeySlots ?? []} />
    {/if}
    <!-- An ordinary container has nothing to classify; the card appears only
      when the container is, or may be, a segment store. -->
    {#if containerClass === "confirmed" || containerClass === "declared" || containerClass === "suspected"}
    <section class="card">
      <h3 class="card-title">Segment classification</h3>
      {#if containerClass === "confirmed"}
        <p class="teach">
          Confirmed segment store: manifests observed during inspection
          reference objects here. Deleting from it breaks the logical objects
          they assemble — and confirmed proves <em>referenced</em>, never
          <em>unshared</em>.
        </p>
      {:else if containerClass === "declared"}
        <p class="teach">Pinned as a segment store by an operator.</p>
      {:else if containerClass === "suspected"}
        <p class="teach">
          Named like a segment store, but nothing confirms it. Treated as
          possible backstage — listed under that heading rather than hidden,
          and never destructively classified from the name alone.
        </p>
      {/if}
    </section>
    {/if}
    {#if cd && sortedEntries(cd.meta).length > 0}
      <section class="card">
        <h3 class="card-title">User metadata</h3>
        {#each sortedEntries(cd.meta) as [key, value] (key)}
          {@render kv(key, value)}
        {/each}
      </section>
    {/if}
    {#if cd && !editingContainer}
      <button class="edit-btn" onclick={() => (editingContainer = true)}>Configure container</button>
    {:else if cd && editingContainer}
      <ContainerEditPanel {browser} detail={cd} onDone={() => (editingContainer = false)} />
    {/if}
    {#if cd}
      {@render rawHeaders(cd.rawHeaders)}
    {/if}
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
    </section>
    {#if (browser.account.policyUsage ?? []).length > 0}
      <section class="card">
        <h3 class="card-title">Storage policies</h3>
        {#each browser.account.policyUsage ?? [] as usage (usage.policy)}
          {@render kv(usage.policy, `${formatBytes(usage.bytesUsed)} · ${plural(usage.objectCount, "object", "objects")} · ${plural(usage.containerCount, "container", "containers")}`)}
        {/each}
      </section>
    {/if}
    <TempUrlKeys {browser} slots={browser.account.tempUrlKeySlots ?? []} />
    {#if editingAccount}
      <AccountEditPanel {browser} account={browser.account} onDone={() => (editingAccount = false)} />
    {:else}
      {#if sortedEntries(browser.account.meta).length > 0}
        <section class="card">
          <h3 class="card-title">Account metadata</h3>
          {#each sortedEntries(browser.account.meta) as [key, value] (key)}
            {@render kv(key, value)}
          {/each}
        </section>
      {/if}
      <button class="edit-btn" onclick={() => (editingAccount = true)}>Edit metadata</button>
    {/if}
    {@render rawHeaders(browser.account.rawHeaders)}
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
  .mutation-notice {
    margin: 0.5rem 0;
    padding: 0.5rem 0.6rem;
    border-left: 3px solid var(--warn, #b8860b);
    font-size: 0.85rem;
    line-height: 1.35;
  }

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

  .raw-card > summary {
    margin: 0;
    cursor: pointer;
  }

  .raw-card[open] > summary {
    margin-bottom: var(--space-2);
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

  .edit-btn {
    align-self: flex-start;
    padding: 2px var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-accent-strong);
    background: var(--color-bg-card);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-control);
    cursor: pointer;
    transition: border-color var(--motion-fast) var(--ease-out);
  }

  .edit-btn:hover {
    border-color: var(--color-accent);
  }

  .edit-btn.inline {
    margin-bottom: var(--space-1);
  }

  .edit-btn.danger {
    color: var(--color-danger);
    border-color: var(--color-danger);
  }

  .edit-btn:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .lo-actions {
    margin-top: var(--space-2);
    display: flex;
    gap: var(--space-2);
    flex-wrap: wrap;
  }

  .segments {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--text-xs);
  }

  .segments th {
    text-align: left;
    font-weight: var(--weight-medium);
    color: var(--color-text-faint);
    padding: var(--space-1);
    border-bottom: 1px solid var(--color-border);
  }

  .segments td {
    padding: var(--space-1);
    border-bottom: 1px solid var(--color-border);
    color: var(--color-text-secondary);
  }

  .seg-name {
    overflow-wrap: anywhere;
  }

  .right {
    text-align: right;
  }
</style>
