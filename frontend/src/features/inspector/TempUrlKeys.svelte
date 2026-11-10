<!-- TempURL key slots (§22, ADR 0101): the two slots Swift reads, each set
  or not, with Set (Replace when set) and Clear. The secret is typed or
  generated here, shown only on request, sent once and dropped on success;
  no value ever comes back from the store. The request carries the presence
  this card showed, so a slot that changed under it is refused as a conflict
  rather than overwritten. -->
<script lang="ts">
  import type { BrowserState } from "../browser/browserState.svelte";

  let { browser, container = "", slots }: { browser: BrowserState; container?: string; slots: string[] } = $props();

  const slotNames = ["Temp-URL-Key", "Temp-URL-Key-2"] as const;
  function isSet(slot: string): boolean {
    return slots.some((shown) => shown.toLowerCase() === slot.toLowerCase());
  }

  let setting: string | null = $state(null);
  let clearing: string | null = $state(null);
  let secret = $state("");
  let reveal = $state(false);
  let busy = $state(false);
  let error: string | null = $state(null);
  let conflict = $state(false);
  let mismatch: string | null = $state(null);

  function open(slot: string, clear: boolean) {
    setting = clear ? null : slot;
    clearing = clear ? slot : null;
    secret = "";
    reveal = false;
    error = null;
    conflict = false;
    mismatch = null;
  }

  function cancel() {
    setting = null;
    clearing = null;
    secret = "";
    reveal = false;
  }

  // 32 random bytes as hex: long enough for an HMAC key, header-safe, and
  // revealed at once because this is the only time it can be copied.
  function generate() {
    const bytes = new Uint8Array(32);
    crypto.getRandomValues(bytes);
    secret = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
    reveal = true;
  }

  async function submit(slot: string, clear: boolean) {
    if (busy || (!clear && secret === "")) return;
    busy = true;
    error = null;
    conflict = false;
    mismatch = null;
    try {
      const result = await browser.updateTempUrlKey({
        container, slot, baseSlots: [...slots], secret: clear ? "" : secret, clear,
      });
      if (result.outcome === "applied") {
        cancel();
      } else if (result.outcome === "conflict") {
        conflict = true;
      } else {
        mismatch = result.mismatch;
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    } finally {
      busy = false;
    }
  }

  // A reload that fails keeps the conflict and its Reload on screen with the
  // store's error, as the account editor does; only a fresh presence closes
  // the form. A failed container reload clears the detail, which unmounts
  // this card with the rest of the container view.
  async function reload() {
    if (busy) return;
    busy = true;
    error = null;
    let refreshed: boolean;
    if (container === "") {
      refreshed = await browser.refreshAccount();
      if (!refreshed) error = browser.accountError ?? "Reloading the account failed. Try again.";
    } else {
      refreshed = await browser.refreshContainerDetail();
      if (!refreshed) error = browser.containerDetailError ?? "Reloading the container failed. Try again.";
    }
    busy = false;
    if (refreshed) {
      cancel();
      conflict = false;
    }
  }
</script>

<section class="card" aria-label="TempURL keys">
  <h3 class="card-title">TempURL keys</h3>
  <p class="teach">
    Two slots sign temporary URLs. To rotate, set the other slot, move your signers to it, then clear this one.
    Values are never shown: whoever sets a key is its only holder.
  </p>
  {#each slotNames as slot (slot)}
    <div class="slot">
      <span class="type-mono name">{slot}</span>
      <span class="state">{isSet(slot) ? "set" : "not set"}</span>
      {#if setting !== slot && clearing !== slot}
        <!-- Both slots show the same words, so each button's name carries its slot. -->
        <button class="inline" aria-label={`${isSet(slot) ? "Replace" : "Set"} ${slot}…`} onclick={() => open(slot, false)} disabled={busy}>{isSet(slot) ? "Replace…" : "Set…"}</button>
        {#if isSet(slot)}
          <button class="inline danger" aria-label={`Clear ${slot}…`} onclick={() => open(slot, true)} disabled={busy}>Clear…</button>
        {/if}
      {/if}
    </div>
    {#if setting === slot}
      <form
        class="form"
        aria-label={`Set ${slot}`}
        onsubmit={(e) => {
          e.preventDefault();
          submit(slot, false);
        }}
      >
        <label class="field">
          <span>New key for <span class="type-mono">{slot}</span></span>
          <div class="secret">
            <!-- Frozen while a save is pending, so the value on screen is the
              value that was sent. -->
            {#if reveal}
              <input class="type-mono" type="text" bind:value={secret} autocomplete="off" spellcheck="false" aria-label={`New key for ${slot}`} disabled={busy} />
            {:else}
              <input class="type-mono" type="password" bind:value={secret} autocomplete="off" aria-label={`New key for ${slot}`} disabled={busy} />
            {/if}
            <button type="button" class="inline" onclick={() => (reveal = !reveal)} disabled={busy}>{reveal ? "Hide" : "Show"}</button>
            <button type="button" class="inline" onclick={generate} disabled={busy}>Generate</button>
          </div>
        </label>
        <p class="note">Copy the key now if you need it elsewhere: it is sent once and never shown again.</p>
        <div class="actions">
          <button type="button" onclick={cancel} disabled={busy}>Cancel</button>
          <button type="submit" class="primary" disabled={busy || secret === ""}>
            {busy ? "Saving…" : isSet(slot) ? "Replace key" : "Set key"}
          </button>
        </div>
      </form>
    {:else if clearing === slot}
      <div class="form" role="group" aria-label={`Clear ${slot}`}>
        <p class="note">Clear <span class="type-mono">{slot}</span>? Temporary URLs signed with it stop working.</p>
        <div class="actions">
          <button onclick={cancel} disabled={busy}>Keep it</button>
          <button class="danger" onclick={() => submit(slot, true)} disabled={busy}>{busy ? "Clearing…" : "Clear key"}</button>
        </div>
      </div>
    {/if}
  {/each}

  {#if conflict}
    <div class="banner conflict" role="status">
      <p>The key slots changed since you loaded them. Nothing was written.</p>
      <div class="banner-actions">
        <button onclick={reload} disabled={busy}>Reload</button>
      </div>
    </div>
  {/if}
  {#if mismatch}
    <p class="banner mismatch" role="alert">Written, but verify found: {mismatch}</p>
  {/if}
  {#if error}
    <p class="banner mismatch" role="alert">{error}</p>
  {/if}
</section>

<style>
  .card {
    padding: var(--space-3);
    background: var(--color-bg-card);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-card);
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .card-title {
    margin: 0;
    font-size: var(--text-sm);
    font-weight: var(--weight-semibold);
    color: var(--color-accent-strong);
  }

  .teach,
  .note {
    margin: 0;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    line-height: 1.4;
  }

  .slot {
    display: flex;
    align-items: baseline;
    gap: var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-text);
  }

  .name {
    min-width: 9em;
  }

  .state {
    color: var(--color-text-secondary);
    flex: 1;
  }

  .form {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
    padding-left: var(--space-2);
    border-left: 2px solid var(--color-accent-border);
  }

  .field {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: var(--text-xs);
    color: var(--color-text-faint);
    min-width: 0;
  }

  .secret {
    display: flex;
    gap: var(--space-1);
    align-items: center;
  }

  .secret input {
    flex: 1;
    min-width: 0;
    padding: 2px var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
  }

  .banner {
    margin: 0;
    padding: var(--space-2);
    font-size: var(--text-sm);
    border-radius: var(--radius-control);
  }

  .banner.conflict {
    color: var(--color-warning);
    background: var(--color-warning-soft);
  }

  .banner.conflict p {
    margin: 0 0 var(--space-2);
  }

  .banner.mismatch {
    color: var(--color-danger);
    background: var(--color-danger-soft);
  }

  .banner-actions,
  .actions {
    display: flex;
    gap: var(--space-2);
  }

  .actions {
    justify-content: flex-end;
  }

  button {
    padding: 2px var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  button.inline {
    padding: 0 var(--space-2);
    font-size: var(--text-xs);
    color: var(--color-accent-strong);
    background: var(--color-bg-card);
    border-color: var(--color-accent-border);
  }

  button.primary {
    color: var(--color-text-invert);
    background: var(--color-accent);
    border-color: var(--color-accent-strong);
  }

  button.danger {
    color: var(--color-danger);
    border-color: var(--color-danger);
    background: none;
  }

  button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
