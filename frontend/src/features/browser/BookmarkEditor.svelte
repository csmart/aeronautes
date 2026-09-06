<!-- Bookmark create/edit modal (§11). The API key field is write-only:
  it crosses the bridge once at save and is never readable back. -->
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
  let tlsMode = $state(existing?.tlsMode ?? "system");
  let caPem = $state("");
  let action = $state<"save" | "connect" | "delete" | null>(null);
  const saving = $derived(action !== null);
  let error: string | null = $state(null);
  let confirmingDelete = $state(false);

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
      const pending = browser.saveBookmark(
        {
          id: bookmark?.id ?? "",
          displayName,
          authUrl,
          username,
          storageUrlOverride: bookmark?.storageUrlOverride ?? "",
          tlsMode,
          caPem,
          hasCa: bookmark?.hasCa ?? false,
          hasCredential: bookmark?.hasCredential ?? false,
        },
        apiKey,
      );
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
  <form
    class="editor"
    use:trapFocus
    aria-label={bookmark ? "Edit bookmark" : "Add bookmark"}
    onsubmit={(e) => {
      e.preventDefault();
      save(true);
    }}
  >
    <h2 class="title">{bookmark ? "Edit bookmark" : "Add bookmark"}</h2>

    <fieldset class="fields" disabled={saving || !isCurrent()} aria-label="Bookmark settings">
      <label class="field">
        <span>Display name</span>
        <!-- svelte-ignore a11y_autofocus -->
        <input bind:value={displayName} autofocus required placeholder="Prod AU" />
      </label>
      <label class="field">
        <span>Auth URL <span class="hint">(TempAuth v1)</span></span>
        <input
          class="type-mono"
          bind:value={authUrl}
          required
          placeholder="https://swift.example/auth/v1.0"
        />
      </label>
      <label class="field">
        <span>Username</span>
        <input class="type-mono" bind:value={username} required placeholder="account:user" />
      </label>
      <label class="field">
        <span>API key</span>
        <input
          class="type-mono"
          type="password"
          bind:value={apiKey}
          autocomplete="off"
          required={!bookmark?.hasCredential}
        />
        {#if bookmark?.hasCredential}
          <span class="hint">
            Leave blank to keep the stored key when the auth URL, username and TLS trust
            stay the same. Re-enter the key when changing them or the storage override.
            Old keys may remain in credential storage after replacement or deletion.
          </span>
        {/if}
      </label>
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
    </fieldset>

    {#if browser.secretBackend === "session"}
      <p class="warning">
        No OS keychain is reachable: the API key is kept in memory only and
        must be re-entered after a restart.
      </p>
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
      <button type="button" class="secondary" onclick={() => save(false)} disabled={saving || !isCurrent()}>
        Save
      </button>
      <button type="submit" class="primary" disabled={saving || !isCurrent()}>
        {action === "save" ? "Saving…" : action === "delete" ? "Deleting…" : action === "connect" ? "Connecting…" : "Save & connect"}
      </button>
    </div>
  </form>
</div>

<style>
  .fields {
    border: 0;
    margin: 0;
    padding: 0;
    min-width: 0;
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
