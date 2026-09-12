<script lang="ts">
  import { getTheme, setTheme, type Theme } from "../../design/tokens";
  import type { BrowserState } from "./browserState.svelte";

  import type { BookmarkDTO } from "../../lib/api";

  let {
    browser,
    onAddBookmark,
    onEditBookmark,
  }: {
    browser: BrowserState;
    onAddBookmark: () => void;
    onEditBookmark: (bookmark: BookmarkDTO) => void;
  } = $props();

  let theme: Theme = $state(getTheme());

  const cycle: Record<Theme, Theme> = { system: "light", light: "dark", dark: "system" };

  function cycleTheme() {
    theme = cycle[theme];
    setTheme(theme);
  }

  function refreshBookmarks() {
    if (browser.bookmarksLoading) return;
    void browser.loadBookmarks();
  }

  function initials(label: string): string {
    const words = label.split(/\s+/).filter(Boolean);
    return words
      .slice(0, 2)
      .map((w) => w[0])
      .join("")
      .toUpperCase();
  }
</script>

<nav class="rail" aria-label="Bookmarks">
  <div class="bookmarks">
    <div class="list-status">
      {#if browser.bookmarksLoading}
        <p role="status">{browser.bookmarksLoaded ? "Refreshing bookmarks…" : "Loading bookmarks…"}</p>
      {:else if browser.bookmarksError}
        <p role="alert">{browser.bookmarksError}</p>
      {:else if !browser.bookmarksLoaded}
        <p role="status">Bookmarks not loaded.</p>
      {:else if !browser.bookmarksStale && browser.bookmarks.length === 0}
        <p role="status">No bookmarks saved.</p>
      {/if}
      {#if browser.bookmarksLoaded && browser.bookmarksStale}
        <p role="status">Previously loaded list may be out of date.</p>
      {/if}
      <button class="list-refresh" aria-label="Refresh bookmark list"
        disabled={browser.bookmarksLoading} onclick={refreshBookmarks}>Refresh list</button>
    </div>
    {#each browser.bookmarks as bookmark (bookmark.id)}
      <div class="bookmark-row">
        <button
          class="bookmark"
          class:active={browser.connectedBookmarkId === bookmark.id}
          class:insecure={bookmark.tlsMode === "insecure"}
          title={bookmark.tlsMode === "insecure"
            ? `${bookmark.displayName} — TLS verification DISABLED`
            : bookmark.displayName}
          aria-pressed={browser.connectedBookmarkId === bookmark.id}
          disabled={browser.connecting}
          onclick={() => browser.connect(bookmark.id).catch(() => {})}
        >
          <span class="badge">{initials(bookmark.displayName)}</span>
          <span class="label">{bookmark.displayName}</span>
          {#if bookmark.tlsMode === "insecure"}
            <!-- K-SEC-005: the warning is persistent chrome, not an
              editor-only detail. -->
            <span class="insecure-flag" role="img" aria-label="TLS verification disabled">⚠</span>
          {/if}
        </button>
        <button
          class="edit"
          title="Edit {bookmark.displayName}"
          aria-label="Edit {bookmark.displayName}"
          onclick={() => onEditBookmark(bookmark)}
        >✎</button>
      </div>
    {/each}

    <button class="bookmark add" title="Add bookmark" onclick={onAddBookmark}>
      <span class="badge plus">+</span>
      <span class="label">Add</span>
    </button>
  </div>

  <div class="rail-foot">
    <button class="foot-btn" title="Theme: {theme}" onclick={cycleTheme} aria-label="Cycle theme ({theme})">
      <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
        <circle cx="8" cy="8" r="6.25" fill="none" stroke="currentColor" stroke-width="1.5" />
        <path d="M8 1.75 A 6.25 6.25 0 0 1 8 14.25 Z" fill="currentColor" />
      </svg>
    </button>
  </div>
</nav>

<style>
  .list-status {
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
    text-align: center;
    overflow-wrap: anywhere;
  }

  .list-status p {
    margin: 0 0 var(--space-2);
  }

  .list-status [role="alert"] {
    color: var(--color-danger);
  }

  .list-refresh {
    max-width: 100%;
    padding: var(--space-1);
    font: inherit;
    color: inherit;
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .list-refresh:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .bookmark-row {
    position: relative;
  }

  .bookmark-row .edit {
    position: absolute;
    top: 2px;
    right: 2px;
    padding: 0 3px;
    font-size: var(--text-xs);
    line-height: 1.4;
    color: var(--color-text-secondary);
    background: transparent;
    border: none;
    border-radius: var(--radius-control);
    cursor: pointer;
    opacity: 0;
  }

  .bookmark-row:hover .edit,
  .bookmark-row .edit:focus-visible {
    opacity: 1;
  }

  .bookmark.insecure {
    box-shadow: inset 0 0 0 1px var(--color-danger);
  }

  .insecure-flag {
    font-size: var(--text-xs);
    color: var(--color-danger);
  }

  .rail {
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    padding: var(--space-3) var(--space-2);
    background: var(--color-bg);
    border-right: 1px solid var(--color-border);
    overflow-y: auto;
  }

  .bookmarks {
    display: flex;
    flex-direction: column;
    gap: var(--space-2);
  }

  .bookmark {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--space-1);
    padding: var(--space-2) var(--space-1);
    font: inherit;
    color: var(--color-text-secondary);
    background: none;
    border: 1px solid transparent;
    border-radius: var(--radius-card);
    cursor: pointer;
    transition:
      background-color var(--motion-fast) var(--ease-out),
      border-color var(--motion-fast) var(--ease-out);
  }

  .bookmark:hover {
    background: var(--color-bg-pane);
  }

  .bookmark.active {
    color: var(--color-text);
    background: var(--color-selection-bg);
    border-color: var(--color-selection-border);
  }

  .bookmark:disabled {
    opacity: 0.6;
    cursor: default;
  }

  .badge {
    display: grid;
    place-items: center;
    width: 2.1rem;
    height: 2.1rem;
    font-size: var(--text-sm);
    font-weight: var(--weight-semibold);
    color: var(--color-accent-strong);
    background: var(--color-accent-soft);
    border: 1px solid var(--color-accent-border);
    border-radius: var(--radius-control);
  }

  .badge.plus {
    color: var(--color-text-faint);
    background: none;
    border-style: dashed;
    border-color: var(--color-border-strong);
    font-size: var(--text-lg);
  }

  .label {
    font-size: var(--text-xs);
    max-width: 4.5rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .rail-foot {
    display: flex;
    justify-content: center;
    padding-top: var(--space-2);
  }

  .foot-btn {
    display: grid;
    place-items: center;
    width: 2rem;
    height: 2rem;
    color: var(--color-text-faint);
    background: none;
    border: none;
    border-radius: var(--radius-control);
    cursor: pointer;
    transition: color var(--motion-fast) var(--ease-out);
  }

  .foot-btn:hover {
    color: var(--color-accent-strong);
  }
</style>
