<!-- A row's context menu: the actions the toolbar already offers for what
  is selected, at the pointer or the row. It follows the WAI-ARIA menu
  pattern: focus starts on the first enabled item, arrows move, Enter or
  a click runs one, and Escape, Tab or a click elsewhere closes it. -->
<script lang="ts" module>
  export type MenuItem = { label: string; disabled?: boolean; danger?: boolean; run: () => void };
</script>

<script lang="ts">
  import { onMount } from "svelte";

  let {
    x,
    y,
    label,
    items,
    onClose,
  }: { x: number; y: number; label: string; items: MenuItem[]; onClose: () => void } = $props();

  let menu: HTMLDivElement | undefined = $state();
  let left = $state(0);
  let top = $state(0);

  function buttons(): HTMLButtonElement[] {
    return [...(menu?.querySelectorAll<HTMLButtonElement>("button:not(:disabled)") ?? [])];
  }

  onMount(() => {
    // Keep the menu inside the window when opened near an edge.
    const box = menu!.getBoundingClientRect();
    left = Math.max(0, Math.min(x, window.innerWidth - box.width));
    top = Math.max(0, Math.min(y, window.innerHeight - box.height));
    buttons()[0]?.focus();
  });

  function choose(item: MenuItem) {
    onClose();
    item.run();
  }

  function keydown(event: KeyboardEvent) {
    const enabled = buttons();
    const at = enabled.indexOf(document.activeElement as HTMLButtonElement);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const step = event.key === "ArrowDown" ? 1 : -1;
      enabled[(at + step + enabled.length) % enabled.length]?.focus();
    } else if (event.key === "Escape" || event.key === "Tab") {
      event.preventDefault();
      onClose();
    }
  }
</script>

<!-- A transparent layer takes the click or right-click that closes the menu,
  so it cannot also reach what is underneath. -->
<div
  class="layer"
  role="presentation"
  onpointerdown={(e) => e.target === e.currentTarget && onClose()}
  oncontextmenu={(e) => {
    e.preventDefault();
    onClose();
  }}
>
  <div class="menu" role="menu" aria-label={label} tabindex="-1" bind:this={menu} style:left="{left}px" style:top="{top}px" onkeydown={keydown}>
    {#each items as item (item.label)}
      <button role="menuitem" class:danger={item.danger} disabled={item.disabled} onclick={() => choose(item)}>{item.label}</button>
    {/each}
  </div>
</div>

<style>
  .layer {
    position: fixed;
    inset: 0;
    z-index: 40;
  }

  .menu {
    position: fixed;
    min-width: 12rem;
    display: flex;
    flex-direction: column;
    padding: var(--space-1) 0;
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  button {
    font: inherit;
    text-align: left;
    padding: var(--space-1) var(--space-3);
    background: none;
    border: none;
    color: var(--color-text);
    cursor: pointer;
  }

  button:hover:not(:disabled),
  button:focus-visible {
    background: var(--color-selection-bg);
    outline: none;
  }

  button:disabled {
    color: var(--color-text-faint);
    cursor: default;
  }

  button.danger {
    color: var(--color-danger);
  }
</style>
