import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import HoverText from "./HoverText.svelte";

let shown: ReturnType<typeof mount> | null = null;
afterEach(async () => {
  vi.useRealTimers();
  if (shown) await unmount(shown);
  shown = null;
  document.body.innerHTML = "";
});

function setUp() {
  document.body.innerHTML = `<span id="mark" title="Public: anyone with the URL can read objects">Public</span><span id="plain">plain</span>`;
  shown = mount(HoverText, { target: document.body });
  flushSync();
  return { mark: document.getElementById("mark")!, plain: document.getElementById("plain")! };
}

const bubble = () => document.querySelector('[role="tooltip"]')?.textContent?.trim() ?? null;

// The window draws no tooltip for a title, so the title is drawn as a bubble
// while the pointer is over its element or the element has focus.
it("shows an element's title in a bubble while the pointer is over it", () => {
  const { mark, plain } = setUp();
  mark.dispatchEvent(new PointerEvent("pointerover", { bubbles: true }));
  flushSync();
  expect(bubble()).toBe("Public: anyone with the URL can read objects");
  expect(mark.getAttribute("title")).toBe("Public: anyone with the URL can read objects");
  mark.dispatchEvent(new PointerEvent("pointerout", { bubbles: true, relatedTarget: plain }));
  flushSync();
  expect(bubble()).toBeNull();
});

it("shows it for keyboard focus too, and Escape closes it", () => {
  const { mark } = setUp();
  mark.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
  flushSync();
  expect(bubble()).toBe("Public: anyone with the URL can read objects");
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  flushSync();
  expect(bubble()).toBeNull();
});

// A shown bubble follows its element: a title rewritten under a resting
// pointer is shown as it now reads, and an element removed without any
// pointer or focus event takes its bubble with it.
it("follows a title that changes while it is shown", () => {
  vi.useFakeTimers();
  const { mark } = setUp();
  mark.dispatchEvent(new PointerEvent("pointerover", { bubbles: true }));
  flushSync();
  mark.setAttribute("title", "12 objects in fixtures · counted 2026-11-18 05:00Z");
  vi.advanceTimersByTime(250);
  flushSync();
  expect(bubble()).toBe("12 objects in fixtures · counted 2026-11-18 05:00Z");
});

it("goes when its element is removed while it is shown", () => {
  vi.useFakeTimers();
  const { mark } = setUp();
  mark.dispatchEvent(new PointerEvent("pointerover", { bubbles: true }));
  flushSync();
  expect(bubble()).not.toBeNull();
  mark.remove();
  vi.advanceTimersByTime(250);
  flushSync();
  expect(bubble()).toBeNull();
});
