<!-- Hover text as a bubble. The desktop window's webview draws no tooltip
  for a title attribute, so every control named by its hover text (the
  marks on the path's row, the small tool buttons) showed nothing there.
  One listener on the document draws the title of what the pointer is over,
  or what has keyboard focus, in a bubble beside it. The title stays on the
  element, so its accessible name and anything that finds it by title are
  unchanged. -->
<script lang="ts">
  type Shown = { text: string; left: number; top: number; above: boolean };

  let shown: Shown | null = $state(null);
  let owner: Element | null = null;

  // Half the bubble's widest, so a bubble near an edge stays on screen.
  const halfWidth = 176;

  function titled(target: EventTarget | null): Element | null {
    return target instanceof Element ? target.closest("[title]") : null;
  }

  // While a bubble is shown its element is read again, a few times a
  // second: a title can change under a resting pointer (a count's total)
  // and an element can be removed without any pointer or focus event (a
  // container opened from the keyboard drops the "Public" mark).
  let recheck: ReturnType<typeof setInterval> | undefined;

  function show(element: Element) {
    owner = element;
    clearInterval(recheck);
    recheck = setInterval(place, 200);
    place();
  }

  function place() {
    const element = owner;
    const text = element?.getAttribute("title")?.trim();
    if (!element || !element.isConnected || !text) {
      hide();
      return;
    }
    const box = element.getBoundingClientRect();
    // Below the element unless that would run off the bottom of the window.
    const above = box.bottom + 80 > window.innerHeight;
    const centre = box.left + box.width / 2;
    shown = {
      text,
      left: Math.min(Math.max(centre, halfWidth + 8), Math.max(halfWidth + 8, window.innerWidth - halfWidth - 8)),
      top: above ? box.top - 6 : box.bottom + 6,
      above,
    };
  }

  function hide() {
    clearInterval(recheck);
    recheck = undefined;
    owner = null;
    shown = null;
  }

  $effect(() => () => clearInterval(recheck));

  function enter(event: Event) {
    const element = titled(event.target);
    if (element && element !== owner) show(element);
  }

  function leave(event: Event & { relatedTarget: EventTarget | null }) {
    if (owner === null) return;
    const next = event.relatedTarget;
    if (next instanceof Node && owner.contains(next)) return;
    hide();
  }
</script>

<svelte:document
  onpointerover={enter}
  onpointerout={leave}
  onfocusin={enter}
  onfocusout={leave}
  onpointerdown={hide}
  onkeydown={(event) => event.key === "Escape" && hide()}
/>
<svelte:window onscroll={hide} onresize={hide} />

{#if shown}
  <div
    class="bubble"
    class:above={shown.above}
    role="tooltip"
    style:left="{shown.left}px"
    style:top="{shown.top}px"
  >
    {shown.text}
  </div>
{/if}

<style>
  .bubble {
    position: fixed;
    z-index: 1000;
    max-width: 22rem;
    padding: var(--space-1) var(--space-2);
    transform: translateX(-50%);
    font-size: var(--text-xs);
    line-height: 1.4;
    color: var(--color-text-invert);
    background: var(--color-text);
    border-radius: var(--radius-control);
    box-shadow: var(--shadow-raised);
    pointer-events: none;
    white-space: pre-line;
  }

  .bubble.above {
    transform: translate(-50%, -100%);
  }
</style>
