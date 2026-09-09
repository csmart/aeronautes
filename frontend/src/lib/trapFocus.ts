const modals: HTMLElement[] = [];
const background = new Map<HTMLElement, string | null>();

export function activeModal(): HTMLElement | undefined {
  return modals.at(-1);
}

// The inspector can own a modal inside the browser's layout. Only siblings
// along that modal's ancestry become inert; never one of its ancestors.
function isolateModal() {
  for (const [element, inert] of background) {
    if (inert === null) element.removeAttribute("inert");
    else element.setAttribute("inert", inert);
  }
  background.clear();
  for (let child = activeModal(); child?.parentElement; child = child.parentElement) {
    for (const sibling of child.parentElement.children) {
      if (sibling === child || !(sibling instanceof HTMLElement)) continue;
      background.set(sibling, sibling.getAttribute("inert"));
      sibling.setAttribute("inert", "");
    }
    if (child.parentElement === document.body) break;
  }
}

function available(element: HTMLElement): boolean {
  if (!element.isConnected || element.matches(":disabled") || element.closest("[hidden], [inert]")) return false;
  for (let parent: HTMLElement | null = element; parent; parent = parent.parentElement) {
    if (getComputedStyle(parent).visibility === "hidden") return false;
    if (parent instanceof HTMLDetailsElement && !parent.open) {
      const summary = parent.querySelector(":scope > summary");
      if (!summary?.contains(element)) return false;
    }
  }
  return element.offsetParent !== null;
}

// Mount order owns focus and Escape, including inspector-owned dialogs.
// Removing an older dialog must not focus behind its replacement.
export function trapFocus(node: HTMLElement, onClose: () => void) {
  const opener = document.activeElement as HTMLElement | null;
  modals.push(node);
  isolateModal();

  const observer = new MutationObserver(() => {
    if (activeModal() === node) isolateModal();
  });
  observer.observe(document.body, { childList: true, subtree: true });

  function focusables(): HTMLElement[] {
    return Array.from(
      node.querySelectorAll<HTMLElement>(
        'a[href], button, input, select, textarea, summary, [tabindex]',
      ),
    ).filter((el) => el.tabIndex >= 0 && available(el));
  }

  function focusInside() { (focusables()[0] ?? node).focus(); }
  focusInside();

  function onFocus(event: FocusEvent) {
    if (activeModal() === node && event.target instanceof Node && !node.contains(event.target)) focusInside();
  }

  // Capture refuses events aimed at the background before its list handlers
  // can change selection, even if a stale caller retained a DOM reference.
  function guardBackground(event: KeyboardEvent) {
    if (activeModal() !== node || !(event.target instanceof Node) || node.contains(event.target)) return;
    // Disabling the focused control can leave the body as the key target.
    if (event.key === "Escape" && !event.defaultPrevented) {
      onKeydown(event);
      return;
    }
    event.preventDefault();
    event.stopImmediatePropagation();
    focusInside();
  }

  function onKeydown(event: KeyboardEvent) {
    if (activeModal() !== node || event.defaultPrevented) return;
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopImmediatePropagation();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const items = focusables();
    if (items.length === 0) {
      event.preventDefault();
      node.focus();
      return;
    }
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (event.shiftKey && (active === first || !items.includes(active as HTMLElement))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && (active === last || !items.includes(active as HTMLElement))) {
      event.preventDefault();
      first.focus();
    }
  }

  document.addEventListener("focusin", onFocus, true);
  document.addEventListener("keydown", guardBackground, true);
  window.addEventListener("keydown", onKeydown);
  return {
    update(close: () => void) { onClose = close; },
    destroy() {
      observer.disconnect();
      document.removeEventListener("focusin", onFocus, true);
      document.removeEventListener("keydown", guardBackground, true);
      window.removeEventListener("keydown", onKeydown);
      const wasActive = activeModal() === node;
      modals.splice(modals.indexOf(node), 1);
      isolateModal();
      if (wasActive) {
        if (opener && available(opener)) opener.focus();
        else activeModal()?.focus();
      }
    },
  };
}
