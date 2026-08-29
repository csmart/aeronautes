<script lang="ts">
  import { getTheme, setTheme, type Theme } from "../../design/tokens";
  import type { BrowserState } from "./browserState.svelte";

  let { browser, onAddBookmark }: { browser: BrowserState; onAddBookmark: () => void } =
    $props();

  let theme: Theme = $state(getTheme());

  const cycle: Record<Theme, Theme> = { system: "light", light: "dark", dark: "system" };

  function cycleTheme() {
    theme = cycle[theme];
    setTheme(theme);
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
    {#each browser.bookmarks as bookmark (bookmark.id)}
      <button
        class="bookmark"
        class:active={browser.connectedBookmarkId === bookmark.id}
        title={bookmark.displayName}
        aria-pressed={browser.connectedBookmarkId === bookmark.id}
        disabled={browser.connecting}
        onclick={() => browser.connect(bookmark.id).catch(() => {})}
      >
        <span class="badge">{initials(bookmark.displayName)}</span>
        <span class="label">{bookmark.displayName}</span>
      </button>
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
