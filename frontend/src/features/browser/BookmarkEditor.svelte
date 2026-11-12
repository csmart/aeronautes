<!-- Bookmark create/edit modal (§11). The API key field is write-only:
  it crosses the bridge at save, or at a verify, and is never readable
  back. Verify checks the typed details against the endpoint and saves
  nothing, so a wrong URL, user or key is found before anything is stored. -->
<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { BookmarkDTO } from "../../lib/api";
  import type { BrowserState } from "./browserState.svelte";

  let {
    browser,
    existing = null,
    onClose,
  }: {
    browser: BrowserState;
    existing?: BookmarkDTO | null;
    onClose: () => void;
  } = $props();

  // The form must not borrow a replacement prop's bookmark identity.
  // svelte-ignore state_referenced_locally
  const target = { browser, existing };
  let closed = $state(false);
  let savedBookmark = $state<BookmarkDTO | null>(null);
  const bookmark = $derived(savedBookmark ?? target.existing);
  function isCurrent() { return !closed && browser === target.browser && existing === target.existing; }
  function close() {
    if (closed) return;
    closed = true;
    apiKey = "";
    onClose();
  }
  onDestroy(() => { closed = true; apiKey = ""; });
  $effect(() => { if (!isCurrent()) untrack(close); });

  // The modal is mounted fresh per open ({#if} in BrowserScreen), so
  // capturing the prop's initial values is exactly right.
  /* svelte-ignore state_referenced_locally */
  let displayName = $state(existing?.displayName ?? "");
  /* svelte-ignore state_referenced_locally */
  let authUrl = $state(existing?.authUrl ?? "");
  /* svelte-ignore state_referenced_locally */
  let username = $state(existing?.username ?? "");
  let apiKey = $state("");
  /* svelte-ignore state_referenced_locally */
  let authVersion = $state(existing?.authVersion ?? 1);
  /* svelte-ignore state_referenced_locally */
  let userDomain = $state(existing?.userDomain ?? "");
  /* svelte-ignore state_referenced_locally */
  let projectName = $state(existing?.projectName ?? "");
  /* svelte-ignore state_referenced_locally */
  let projectDomain = $state(existing?.projectDomain ?? "");
  // An ID names the user or the project instead of the name and domain
  // (ADR 0113), so the name it replaces is no longer required.
  /* svelte-ignore state_referenced_locally */
  let userId = $state(existing?.userId ?? "");
  /* svelte-ignore state_referenced_locally */
  let projectId = $state(existing?.projectId ?? "");
  /* svelte-ignore state_referenced_locally */
  let region = $state(existing?.region ?? "");
  /* svelte-ignore state_referenced_locally */
  let tlsMode = $state(existing?.tlsMode ?? "system");
  // What a segmented upload publishes (ADR 0111); a stored "slo" is the
  // same as the default, so it shows as the default.
  /* svelte-ignore state_referenced_locally */
  let largeObjects = $state(existing?.largeObjects === "dlo" ? "dlo" : "");
  let caPem = $state("");
  // Keystone v3 and TempAuth v1 are the two auth types; the v3 identity
  // fields show only for v3 (ADR 0100).
  const keystone = $derived(authVersion === 3);
  // Read from the shared list, which the backend's answer refreshes, since
  // only a verify of the saved bookmark clears the flag (ADR 0114).
  const verifyFailure = $derived(browser.bookmarks.find((b) => b.id === bookmark?.id)?.verifyFailure ?? "");
  let action = $state<"save" | "connect" | "delete" | "verify" | null>(null);
  const saving = $derived(action !== null);
  let error: string | null = $state(null);
  let verified: string | null = $state(null);
  let confirmingDelete = $state(false);

  function draft(): BookmarkDTO {
    return {
      id: bookmark?.id ?? "",
      displayName,
      authUrl,
      username,
      storageUrlOverride: keystone ? "" : (bookmark?.storageUrlOverride ?? ""),
      authVersion,
      userDomain: keystone ? userDomain : "",
      projectName: keystone ? projectName : "",
      projectDomain: keystone ? projectDomain : "",
      userId: keystone ? userId : "",
      projectId: keystone ? projectId : "",
      region: keystone ? region : "",
      tlsMode,
      caPem,
      hasCa: bookmark?.hasCa ?? false,
      hasCredential: bookmark?.hasCredential ?? false,
      largeObjects,
    };
  }

  async function verify() {
    if (saving || !isCurrent()) return;
    action = "verify";
    error = null;
    verified = null;
    try {
      const result = await browser.verifyBookmark(draft(), apiKey);
      if (!isCurrent()) return;
      if (verifyFailure) void browser.loadBookmarks();
      verified = `Verified: account ${result.account}, ${result.containers.toLocaleString("en-US")} containers, ${result.objects.toLocaleString("en-US")} objects.`;
    } catch (err) {
      if (isCurrent()) error = err instanceof Error ? err.message : String(err);
    } finally {
      if (isCurrent()) action = null;
    }
  }

  async function remove() {
    if (saving || !isCurrent() || !bookmark) {
      return;
    }
    action = "delete";
    error = null;
    try {
      await browser.deleteBookmark(bookmark.id);
      if (isCurrent()) close();
    } catch (err) {
      if (isCurrent()) {
        error = err instanceof Error ? err.message : String(err);
        confirmingDelete = false;
      }
    } finally {
      if (isCurrent()) action = null;
    }
  }

  async function save(connect: boolean) {
    if (saving || !isCurrent()) return;
    action = "save";
    error = null;
    try {
      const pending = browser.saveBookmark(draft(), apiKey);
      // An active-bookmark save resets the connection synchronously itself.
      const scope = browser.scopeRevision;
      const saved = await pending;
      if (!isCurrent()) return;
      savedBookmark = saved;
      if (connect) {
        if (scope !== browser.scopeRevision) {
          error = "Saved. The connection changed; select the bookmark to connect.";
          return;
        }
        action = "connect";
        const connected = await browser.connect(saved.id, isCurrent);
        if (!isCurrent()) return;
        if (!connected) {
          error = "Saved. The connection attempt was interrupted; connect again.";
          return;
        }
      }
      close();
    } catch (err) {
      if (isCurrent()) error = err instanceof Error ? err.message : String(err);
    } finally {
      if (isCurrent()) action = null;
    }
  }
</script>

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
  <div role="dialog" aria-modal="true" aria-label={bookmark ? "Edit bookmark" : "Add bookmark"}
    tabindex="-1" use:trapFocus={close}>
  <form
    class="editor"
    aria-label={bookmark ? "Edit bookmark" : "Add bookmark"}
    onsubmit={(e) => {
      e.preventDefault();
      save(true);
    }}
  >
    <h2 class="title">{bookmark ? "Edit bookmark" : "Add bookmark"}</h2>
    {#if verifyFailure}
      <p class="callout warning" role="status">
        This bookmark did not verify after import: {verifyFailure}. Verifying it here with its saved
        key, or connecting to it, clears this note.
      </p>
    {/if}

    <fieldset class="fields" disabled={saving || !isCurrent()} aria-label="Bookmark settings" oninput={() => (verified = null)}>
      <label class="field">
        <span>Display name</span>
        <!-- svelte-ignore a11y_autofocus -->
        <input bind:value={displayName} autofocus required placeholder="Prod AU" />
      </label>
      <label class="field">
        <span>Auth type</span>
        <select bind:value={authVersion}>
          <option value={1}>TempAuth v1</option>
          <option value={3}>Keystone v3</option>
        </select>
      </label>
      <label class="field">
        <span>{keystone ? "Identity URL" : "Auth URL"} <span class="hint">({keystone ? "Keystone v3" : "TempAuth v1"})</span></span>
        <input
          class="type-mono"
          bind:value={authUrl}
          required
          placeholder={keystone ? "https://keystone.example/v3" : "https://swift.example/auth/v1.0"}
        />
      </label>
      <label class="field">
        <span>Username</span>
        <input class="type-mono" bind:value={username} required={!(keystone && userId !== "")} placeholder="account:user" />
      </label>
      <label class="field">
        <span>{keystone ? "Password" : "API key"}</span>
        <input
          class="type-mono"
          type="password"
          bind:value={apiKey}
          autocomplete="off"
          required={!bookmark?.hasCredential}
        />
        {#if bookmark?.hasCredential}
          <span class="hint">
            Leave blank to keep the stored {keystone ? "password" : "key"} when the connection
            identity and TLS trust stay the same. Re-enter it when changing the auth URL,
            username{keystone ? ", domain, project, IDs or region" : " or storage override"}.
            Old keys may remain in credential storage after replacement or deletion.
          </span>
        {/if}
      </label>
      {#if keystone}
        <label class="field">
          <span>Project</span>
          <input class="type-mono" bind:value={projectName} required={projectId === ""} placeholder="my-project" />
        </label>
        <label class="field">
          <span>Region</span>
          <input class="type-mono" bind:value={region} required placeholder="RegionOne" />
        </label>
        <label class="field">
          <span>User domain <span class="hint">(default: Default)</span></span>
          <input class="type-mono" bind:value={userDomain} placeholder="Default" />
        </label>
        <label class="field">
          <span>Project domain <span class="hint">(default: Default)</span></span>
          <input class="type-mono" bind:value={projectDomain} placeholder="Default" />
        </label>
        <label class="field">
          <span>User ID <span class="hint">(optional: used instead of the username and user domain)</span></span>
          <input class="type-mono" bind:value={userId} />
        </label>
        <label class="field">
          <span>Project ID <span class="hint">(optional: used instead of the project and project domain)</span></span>
          <input class="type-mono" bind:value={projectId} />
        </label>
      {/if}

      <label class="field">
        <span>TLS</span>
        <select bind:value={tlsMode}>
          <option value="system">System trust store</option>
          <option value="custom_ca">Custom CA (verify against exactly this bundle)</option>
          <option value="insecure">Insecure (skip verification — labs only)</option>
        </select>
      </label>

      {#if tlsMode === "custom_ca"}
        <label class="field">
          <span>
            CA bundle (PEM){bookmark?.hasCa ? " — leave empty to keep the stored bundle" : ""}
          </span>
          <textarea
            class="type-mono ca"
            bind:value={caPem}
            placeholder="-----BEGIN CERTIFICATE----- …"
            rows="5"
            required={!bookmark?.hasCa}
          ></textarea>
        </label>
      {/if}

      <label class="field">
        <span>Segmented uploads</span>
        <select bind:value={largeObjects}>
          <option value="">Static large objects (default)</option>
          <option value="dlo">Dynamic large objects (reads depend on listings)</option>
        </select>
      </label>
    </fieldset>

    {#if browser.secretBackend === "session"}
      <p class="warning">
        No OS keychain is reachable: the API key is kept in memory only and
        must be re-entered after a restart.
      </p>
    {/if}

    {#if verified}
      <p class="verified" role="status">{verified}</p>
    {/if}
    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}

    <div class="actions">
      {#if bookmark}
        {#if confirmingDelete}
          <button type="button" class="danger" onclick={remove} disabled={saving || !isCurrent()}>
            Really delete
          </button>
        {:else}
          <button type="button" class="danger-soft" onclick={() => { if (isCurrent()) confirmingDelete = true; }} disabled={saving || !isCurrent()}>
            Delete…
          </button>
        {/if}
      {/if}
      <span class="spacer"></span>
      <button type="button" class="secondary" onclick={close} disabled={saving}>
        Cancel
      </button>
      <button
        type="button"
        class="secondary"
        title="Check these details against the endpoint without saving them"
        onclick={verify}
        disabled={saving || !isCurrent()}
      >
        {action === "verify" ? "Verifying…" : "Verify"}
      </button>
      <button type="button" class="secondary" onclick={() => save(false)} disabled={saving || !isCurrent()}>
        Save
      </button>
      <button type="submit" class="primary" disabled={saving || !isCurrent()}>
        {action === "save" ? "Saving…" : action === "delete" ? "Deleting…" : action === "connect" ? "Connecting…" : "Save & connect"}
      </button>
    </div>
  </form>
  </div>
</div>

<style>
  .fields {
    border: 0;
    margin: 0;
    padding: 0;
    min-width: 0;
    /* The dialog is capped to the viewport; the fields are the part that
       scrolls so the title and the actions stay in view and reachable at
       the app's 600 px minimum window height, however many v3 fields show.
       min-height:0 lets this flex child shrink below its content. */
    flex: 1 1 auto;
    min-height: 0;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
  }

  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgb(20 16 12 / 0.45);
    z-index: 10;
  }

  .editor {
    width: min(26rem, calc(100vw - 2 * var(--space-5)));
    max-height: calc(100dvh - 2 * var(--space-4));
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  .title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: var(--weight-semibold);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .hint {
    color: var(--color-text-faint);
  }

  .field input,
  .field textarea.ca {
    font-size: var(--text-xs);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: var(--space-1) var(--space-2);
    resize: vertical;
  }

  .field select {
    padding: var(--space-1) var(--space-2);
    font: inherit;
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  .field input.type-mono {
    font-family: var(--font-mono);
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

  .verified {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-success);
    background: var(--color-success-soft);
    border-radius: var(--radius-control);
  }

  .warning {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-warning);
    background: var(--color-warning-soft);
    border-radius: var(--radius-control);
  }

  .actions .spacer {
    flex: 1;
  }

  .actions .danger {
    color: var(--color-text-invert);
    background: var(--color-danger);
    border: 1px solid var(--color-danger);
  }

  .actions .danger-soft {
    color: var(--color-danger);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
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

  .primary {
    color: var(--color-text-invert);
    background: var(--color-accent);
    border: 1px solid var(--color-accent-strong);
    font-weight: var(--weight-medium);
  }

  .primary:disabled,
  .secondary:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
