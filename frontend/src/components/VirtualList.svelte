<!-- The virtualised list primitive (§60 Phase 0: present from the start so
  million-object listings never render a row per object). Fixed row height,
  windowed rendering with overscan, list-level keyboard selection (
  arrows move, Enter activates). Selection state lives with the caller;
  this component only reports intents, including how a pointer chose a row
  (plain, toggled with Ctrl or Cmd, or a range with Shift). -->
<script lang="ts" generics="T">
  import { onMount, type Snippet } from "svelte";

  type SelectMode = "single" | "toggle" | "range";

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
    /** Whether a row is in a multi-selection beyond the focused one; the
     * caller owns that state too, and offering it marks the listbox as
     * multi-selectable. */
    rowSelected?: (item: T, index: number) => boolean;
    /** Keyboard moves are always plain. */
    onSelect?: (index: number, mode: SelectMode) => void;
    onActivate?: (item: T, index: number) => void;
    /** A row's context menu was asked for, by a right-click or by the
     * ContextMenu key or Shift+F10 on the focused row, to open at (x, y). */
    onContextMenu?: (item: T, index: number, x: number, y: number) => void;
  }

  let {
    items,
    rowHeight,
    overscan = 6,
    selected = -1,
    viewport,
    ariaLabel,
    row,
    rowSelected,
    onSelect,
    onActivate,
    onContextMenu,
  }: Props = $props();

  // A caller that owns membership says which rows are selected; the focused
  // row is then only the keyboard position, which may have been toggled
  // off. Without that owner the focused row is the selection.
  function isSelected(item: T, index: number): boolean {
    return rowSelected ? rowSelected(item, index) : index === selected;
  }

  function modeOf(event: PointerEvent): SelectMode {
    if (event.shiftKey) return "range";
    if (event.ctrlKey || event.metaKey) return "toggle";
    return "single";
  }

  function handlePointerdown(event: PointerEvent, index: number) {
    // A right-click on a row already selected keeps a multi-selection, so
    // its menu acts on everything selected, as in a file manager; on any
    // other row it is a plain select, whatever modifier is held.
    if (event.button === 2) {
      if (!isSelected(items[index], index)) onSelect?.(index, "single");
      return;
    }
    const mode = modeOf(event);
    if (mode === "range") {
      // A Shift click would otherwise select the rows' text; the focus the
      // default would have moved comes here explicitly.
      event.preventDefault();
      el?.focus();
    }
    onSelect?.(index, mode);
  }

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

  // Loading another page must not undo a user's scroll away from the selection.
  let lastEnsured = -1;
  $effect(() => {
    if (selected < 0 || selected >= items.length) {
      lastEnsured = -1;
      return;
    }
    if (selected === lastEnsured || !el) return;
    lastEnsured = selected;
    ensureVisible(selected);
  });

  function handleKeydown(event: KeyboardEvent) {
    // The screen owns Alt+Up; consuming it here would hide parent navigation
    // from the screen's defaultPrevented guard.
    if (event.defaultPrevented || (event.altKey && event.key === "ArrowUp")) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (items.length === 0) {
        return;
      }
      const next =
        event.key === "ArrowDown"
          ? Math.min(items.length - 1, selected + 1)
          : Math.max(0, selected - 1);
      onSelect?.(next, "single");
      ensureVisible(next);
    } else if (event.key === "Enter" && selected >= 0 && selected < items.length) {
      event.preventDefault();
      onActivate?.(items[selected], selected);
    } else if ((event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) && selected >= 0 && selected < items.length) {
      event.preventDefault();
      const box = document.getElementById(`${uid}-row-${selected}`)?.getBoundingClientRect();
      onContextMenu?.(items[selected], selected, box ? box.left + 24 : 0, box ? box.bottom : 0);
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
  aria-multiselectable={rowSelected ? true : undefined}
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
        aria-selected={isSelected(item, index)}
        style:transform="translateY({index * rowHeight}px)"
        style:height="{rowHeight}px"
        onpointerdown={(event) => handlePointerdown(event, index)}
        ondblclick={() => onActivate?.(item, index)}
        oncontextmenu={(event) => {
          if (!onContextMenu) return;
          event.preventDefault();
          onContextMenu(item, index, event.clientX, event.clientY);
        }}
      >
        {@render row(item, index, isSelected(item, index))}
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
