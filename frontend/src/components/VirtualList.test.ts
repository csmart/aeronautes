import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { SvelteMap } from "svelte/reactivity";
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

function renderChanging(initialItems = items, selected = 5) {
  const rows = new SvelteMap([["items", initialItems]]);
  const values = new SvelteMap([["selected", selected], ["viewport", 360]]);
  const target = document.createElement("div");
  document.body.appendChild(target);
  app = mount(Harness, {
    target,
    props: {
      get items() { return rows.get("items")!; },
      get selected() { return values.get("selected")!; },
      get viewport() { return values.get("viewport")!; },
    },
  });
  flushSync();
  const viewport = target.querySelector<HTMLElement>(".viewport")!;
  function scroll(top: number) {
    viewport.scrollTop = top;
    viewport.dispatchEvent(new Event("scroll"));
    flushSync();
  }
  return { target, viewport, rows, values, scroll };
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

  it.each(["page append", "same-order refresh", "viewport resize"])(
    "preserves manual scroll after a %s with unchanged selection",
    (update) => {
      const f = renderChanging();
      expect(f.target.querySelector('[data-index="5"]')?.getAttribute("data-selected")).toBe("true");
      f.scroll(90 * 36);
      expect(labels(f.target)).not.toContain("item-5");

      if (update === "page append") f.rows.set("items", [...items, "next-page"]);
      else if (update === "same-order refresh") f.rows.set("items", [...items]);
      else f.values.set("viewport", 396);
      flushSync();

      expect(f.viewport.scrollTop).toBe(90 * 36);
      expect(labels(f.target)).toContain("item-90");
      expect(labels(f.target)).not.toContain("item-5");
      expect(f.viewport.getAttribute("aria-activedescendant")).toMatch(/-row-5$/);
    },
  );

  it("reveals externally selected rows above and below the viewport", () => {
    const f = renderChanging();
    f.values.set("selected", 900);
    flushSync();
    expect(f.viewport.scrollTop).toBe(901 * 36 - 360);
    expect(f.target.querySelector('[data-index="900"]')?.getAttribute("data-selected")).toBe("true");

    f.values.set("selected", 5);
    flushSync();
    expect(f.viewport.scrollTop).toBe(5 * 36);
    expect(f.target.querySelector('[data-index="5"]')?.getAttribute("data-selected")).toBe("true");
  });

  it("reveals the same row when selection was cleared in between", () => {
    const f = renderChanging();
    f.scroll(90 * 36);
    f.values.set("selected", -1);
    flushSync();
    expect(f.viewport.scrollTop).toBe(90 * 36);
    expect(f.viewport.hasAttribute("aria-activedescendant")).toBe(false);

    f.values.set("selected", 5);
    flushSync();
    expect(f.viewport.scrollTop).toBe(5 * 36);
    expect(f.target.querySelector('[data-index="5"]')?.getAttribute("data-selected")).toBe("true");
  });

  it("reveals a previously unavailable selection when its rows arrive", () => {
    const f = renderChanging(items.slice(0, 50), 90);
    expect(f.viewport.scrollTop).toBe(0);
    expect(labels(f.target)).not.toContain("item-90");

    f.rows.set("items", items);
    flushSync();
    expect(f.viewport.scrollTop).toBe(91 * 36 - 360);
    expect(f.target.querySelector('[data-index="90"]')?.getAttribute("data-selected")).toBe("true");
  });
});
