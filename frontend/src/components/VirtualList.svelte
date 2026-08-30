<!-- The virtualised list primitive (§60 Phase 0: present from the start so
  million-object listings never render a row per object). Fixed row height,
  windowed rendering with overscan, list-level keyboard selection (
  arrows move, Enter activates). Selection state lives with the caller;
  this component only reports intents. -->
<script lang="ts" generics="T">
  import { onMount, type Snippet } from "svelte";

  interface Props {
    items: T[];
    /** Fixed row height in px — virtualisation depends on it. */
    rowHeight: number;
    /** Rows rendered beyond each edge of the viewport. */
    overscan?: number;
    /** Selected index, -1 for none. Owned by the caller. */
    selected?: number;
    /** Viewport height override in px (tests, fixed panes); otherwise measured. */
    viewport?: number;
    /** Accessible name for the listbox. */
    ariaLabel?: string;
    row: Snippet<[T, number, boolean]>;
    onSelect?: (index: number) => void;
    onActivate?: (item: T, index: number) => void;
  }

  let {
    items,
    rowHeight,
    overscan = 6,
    selected = -1,
    viewport,
    ariaLabel,
    row,
    onSelect,
    onActivate,
  }: Props = $props();

  // focus() moves keyboard focus into the listbox (screen shortcuts).
  export function focus() {
    el?.focus();
  }

  // Focus stays on the listbox; the selected option is announced through
  // aria-activedescendant, so rows themselves are never tab stops.
  const uid = $props.id();

  let el: HTMLDivElement | undefined = $state();
  let scrollTop = $state(0);
  let measured = $state(0);

  const height = $derived(viewport ?? (measured || 600));
  const totalHeight = $derived(items.length * rowHeight);
  const first = $derived(Math.max(0, Math.floor(scrollTop / rowHeight) - overscan));
  const last = $derived(
    Math.min(items.length, Math.ceil((scrollTop + height) / rowHeight) + overscan),
  );
  const visible = $derived(items.slice(first, last));

  onMount(() => {
    measured = el?.clientHeight ?? 0;
    if (typeof ResizeObserver !== "undefined" && el) {
      const ro = new ResizeObserver(() => {
        measured = el?.clientHeight ?? 0;
      });
      ro.observe(el);
      return () => ro.disconnect();
    }
  });

  function handleScroll() {
    scrollTop = el?.scrollTop ?? 0;
  }

  function ensureVisible(index: number) {
    if (!el) {
      return;
    }
    const top = index * rowHeight;
    const bottom = top + rowHeight;
    if (top < el.scrollTop) {
      el.scrollTop = top;
    } else if (bottom > el.scrollTop + height) {
      el.scrollTop = bottom - height;
    }
    scrollTop = el.scrollTop;
  }

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (items.length === 0) {
        return;
      }
      const next =
        event.key === "ArrowDown"
          ? Math.min(items.length - 1, selected + 1)
          : Math.max(0, selected - 1);
      onSelect?.(next);
      ensureVisible(next);
    } else if (event.key === "Enter" && selected >= 0 && selected < items.length) {
      event.preventDefault();
      onActivate?.(items[selected], selected);
    }
  }
</script>

<div
  class="viewport"
  bind:this={el}
  onscroll={handleScroll}
  onkeydown={handleKeydown}
  tabindex="0"
  role="listbox"
  aria-label={ariaLabel}
  aria-activedescendant={selected >= 0 ? `${uid}-row-${selected}` : undefined}
>
  <div class="spacer" style:height="{totalHeight}px">
    {#each visible as item, i (first + i)}
      {@const index = first + i}
      <div
        class="vrow"
        id="{uid}-row-{index}"
        role="option"
        tabindex={-1}
        aria-selected={index === selected}
        style:transform="translateY({index * rowHeight}px)"
        style:height="{rowHeight}px"
        onpointerdown={() => onSelect?.(index)}
        ondblclick={() => onActivate?.(item, index)}
      >
        {@render row(item, index, index === selected)}
      </div>
    {/each}
  </div>
</div>

<style>
  .viewport {
    overflow-y: auto;
    height: 100%;
    position: relative;
    outline: none;
  }

  .spacer {
    position: relative;
  }

  .vrow {
    position: absolute;
    inset-inline: 0;
    top: 0;
  }
</style>
