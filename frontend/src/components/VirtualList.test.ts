import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import Harness from "./VirtualListTestHarness.svelte";

const items = Array.from({ length: 1000 }, (_, i) => `item-${i}`);

let app: ReturnType<typeof mount> | null = null;

function render(props: Record<string, unknown> = {}) {
  const target = document.createElement("div");
  document.body.appendChild(target);
  app = mount(Harness, { target, props: { items, ...props } });
  flushSync();
  return target;
}

afterEach(() => {
  if (app) {
    unmount(app);
    app = null;
  }
  document.body.innerHTML = "";
});

function labels(target: HTMLElement): string[] {
  return [...target.querySelectorAll(".row-label")].map((el) => el.textContent ?? "");
}

describe("VirtualList", () => {
  it("renders only the visible window, not the whole list", () => {
    const target = render();
    const rendered = labels(target);
    // viewport 360 / rowHeight 36 = 10 rows + 2 overscan below (none above
    // at the top of the list).
    expect(rendered[0]).toBe("item-0");
    expect(rendered.length).toBeLessThan(20);
    expect(rendered).not.toContain("item-500");
  });

  it("sizes the scroll spacer for the full list", () => {
    const target = render();
    const spacer = target.querySelector(".spacer") as HTMLElement;
    expect(spacer.style.height).toBe(`${1000 * 36}px`);
  });

  it("re-windows on scroll", () => {
    const target = render();
    const viewport = target.querySelector(".viewport") as HTMLElement;
    viewport.scrollTop = 100 * 36;
    viewport.dispatchEvent(new Event("scroll"));
    flushSync();
    const rendered = labels(target);
    expect(rendered).toContain("item-100");
    expect(rendered).not.toContain("item-0");
  });

  it("moves selection with arrow keys and activates with Enter", () => {
    const onActivate = vi.fn();
    const target = render({ onActivate });
    const viewport = target.querySelector(".viewport") as HTMLElement;

    const press = (key: string) => {
      viewport.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
      flushSync();
    };

    press("ArrowDown"); // -1 -> 0
    press("ArrowDown"); // 0 -> 1
    expect(target.querySelector('[data-index="1"]')?.getAttribute("data-selected")).toBe("true");

    press("ArrowUp"); // 1 -> 0
    expect(target.querySelector('[data-index="0"]')?.getAttribute("data-selected")).toBe("true");

    press("Enter");
    expect(onActivate).toHaveBeenCalledWith("item-0", 0);
  });

  it("selects on pointerdown and activates on double click", () => {
    const onActivate = vi.fn();
    const target = render({ onActivate });
    const row = target.querySelector('[data-index="3"]')?.closest(".vrow") as HTMLElement;

    row.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    flushSync();
    expect(target.querySelector('[data-index="3"]')?.getAttribute("data-selected")).toBe("true");

    row.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    flushSync();
    expect(onActivate).toHaveBeenCalledWith("item-3", 3);
  });
});
