// trapFocus is a Svelte action for modal dialogs: focus moves in on
// mount, Tab cycles inside, and focus returns to the opener on destroy
// (§65.6 accessibility pass).
export function trapFocus(node: HTMLElement) {
  const opener = document.activeElement as HTMLElement | null;

  function focusables(): HTMLElement[] {
    return Array.from(
      node.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((el) => el.offsetParent !== null);
  }

  const initial = focusables();
  (initial[0] ?? node).focus();

  function onKeydown(event: KeyboardEvent) {
    if (event.key !== "Tab") {
      return;
    }
    const items = focusables();
    if (items.length === 0) {
      event.preventDefault();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && (active === first || active === node)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  node.addEventListener("keydown", onKeydown);
  return {
    destroy() {
      node.removeEventListener("keydown", onKeydown);
      opener?.focus?.();
    },
  };
}
