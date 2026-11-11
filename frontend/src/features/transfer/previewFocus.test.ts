import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { CopyPlanDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import TransferDialog from "./TransferDialog.svelte";
import { v1Offers } from "../../lib/v1.svelte";

// These cases move between the Copy and Mirror tabs, which the release does
// not offer (v1Offers.mirrorPreview), so they turn the tabs on for themselves.
beforeEach(() => { v1Offers.mirrorPreview = true; });
afterEach(() => { v1Offers.mirrorPreview = false; });

const plan: CopyPlanDTO = {
  summary: { new: 1, same: 0, changed: 0, unknown: 0, destOnly: 0, transferObjects: 1,
    transferBytes: 5, overwrites: 0, nativeRouted: 0, destOnlyBytes: 0 },
  dataPath: "current comparison", overwriteBytes: 0,
  versionedDestination: false, largeObjectsAsDlo: false,
};
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function press(target: EventTarget, key: string) {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
  target.dispatchEvent(event); flushSync();
  return event;
}

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  // JSDOM has no layout; exercise the real focus action with visible controls.
  vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockImplementation(function (this: HTMLElement) {
    return this === document.body || this.closest("[hidden]") ? null : this.parentElement;
  });
});
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear(); document.body.innerHTML = ""; vi.restoreAllMocks();
});

function show(mode: "copy" | "mirror" = "copy", loseFocus = true) {
  const api = newFakeApi();
  const replies: ReturnType<typeof deferred<CopyPlanDTO>>[] = [];
  api.planCopy = vi.fn(() => { const reply = deferred<CopyPlanDTO>(); replies.push(reply); return reply.promise; });
  api.startCopy = vi.fn().mockResolvedValue("started-job");
  api.startMirror = vi.fn().mockRejectedValue(new Error("Mirror is unavailable"));
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source"; browser.containerName = "media"; browser.prefix = "selected/";
  const root = document.createElement("div"); document.body.append(root);
  const onClose = vi.fn();
  const component = mount(TransferDialog, { target: root, props: { browser, onClose } });
  mounted.add(component); flushSync();
  const dialog = root.querySelector<HTMLElement>('[role="dialog"]')!;
  const button = (text: string) => [...root.querySelectorAll("button")].find((node) => node.textContent?.trim() === text)!;
  const destination = root.querySelector<HTMLInputElement>('[aria-label="Destination container"]')!;
  destination.value = "backup"; destination.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
  if (mode === "mirror") { button("Mirror").click(); flushSync(); }
  const preview = button("Preview");
  let onEnable: (() => void) | undefined;
  const disabled = Object.getOwnPropertyDescriptor(HTMLButtonElement.prototype, "disabled")!;
  Object.defineProperty(preview, "disabled", {
    configurable: true,
    get: () => disabled.get!.call(preview) as boolean,
    set(value: boolean) {
      // Native WebKit can discard focus when disabling this button. JSDOM
      // retains it and refuses blur once disabled, so model that transition
      // while leaving activeElement and the production focus action real.
      if (value && !preview.disabled && loseFocus && document.activeElement === preview) preview.blur();
      const wasDisabled = preview.disabled;
      disabled.set!.call(preview, value);
      if (wasDisabled && !value) {
        const callback = onEnable; onEnable = undefined; callback?.();
      }
    },
  });
  function discardFocus() {
    // JSDOM cannot blur a disabled control. Temporarily lift that limitation
    // to model WebKit's deferred focus loss with real focus events.
    const wasDisabled = preview.disabled;
    disabled.set!.call(preview, false);
    preview.blur();
    disabled.set!.call(preview, wasDisabled);
  }
  function begin(focus = true) {
    if (focus) preview.focus();
    preview.click(); flushSync();
  }
  async function finish(outcome: "success" | "failure" = "success", index = 0) {
    if (outcome === "success") replies[index].resolve(plan);
    else replies[index].reject(new Error("preview unavailable"));
    await settle();
  }
  return { api, browser, root, dialog, component, onClose, button, destination, preview,
    replies, begin, finish, discardFocus, onEnable: (callback: () => void) => { onEnable = callback; } };
}

describe.each(["copy", "mirror"] as const)("%s Preview focus", (mode) => {
  it.each(["success", "failure"] as const)("restores only its initiating Preview after current %s", async (outcome) => {
    const f = show(mode); f.begin();
    expect(f.preview.disabled).toBe(true);
    expect(document.activeElement).toBe(document.body);
    await f.finish(outcome);
    expect(f.preview.disabled).toBe(false);
    expect(document.activeElement).toBe(f.preview);
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startMirror).not.toHaveBeenCalled();
    if (outcome === "failure") expect(f.root.textContent).toContain("preview unavailable");
  });

  it.each(["success", "failure"] as const)("restores focus discarded after the initial render before %s", async (outcome) => {
    const f = show(mode, false); f.begin(); await settle();
    expect(f.preview.disabled).toBe(true);
    expect(document.activeElement).toBe(f.preview);
    f.discardFocus();
    expect(f.preview.disabled).toBe(true);
    expect(document.activeElement).toBe(document.body);
    await f.finish(outcome);
    expect(document.activeElement).toBe(f.preview);
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startMirror).not.toHaveBeenCalled();
  });

  it("keeps deliberate Tab navigation while planning", async () => {
    const f = show(mode); f.begin();
    press(document.body, "Tab");
    const chosen = f.button("Copy");
    expect(document.activeElement).toBe(chosen);
    await f.finish();
    expect(document.activeElement).toBe(chosen);
    expect(f.api.startCopy).not.toHaveBeenCalled();
  });

  it.each([false, true])("does not claim an initially unfocused Preview, later empty=%s", async (empty) => {
    const f = show(mode); f.destination.focus(); f.begin(false);
    if (empty) f.destination.blur();
    const chosen = document.activeElement;
    await f.finish();
    expect(document.activeElement).toBe(chosen);
    expect(f.api.startCopy).not.toHaveBeenCalled();
  });

  it("leaves focus that the native control retained", async () => {
    const f = show(mode, false); f.begin(); await settle();
    expect(f.preview.disabled).toBe(true);
    expect(document.activeElement).toBe(f.preview);
    const focus = vi.spyOn(f.preview, "focus");
    await f.finish();
    expect(document.activeElement).toBe(f.preview);
    expect(focus).not.toHaveBeenCalled();
  });

  it("does not claim focus lost after re-enabling", async () => {
    const f = show(mode, false); f.begin(); await settle();
    expect(document.activeElement).toBe(f.preview);
    f.onEnable(() => f.preview.blur());
    await f.finish();
    expect(document.activeElement).toBe(document.body);
  });

  it.each(["Tab", "focus", "pointer", "click", "window"])("permanently retires %s navigation even with an empty final focus", async (route) => {
    const f = show(mode); f.begin(); await settle();
    const chosen = f.button(mode === "copy" ? "Copy" : "Mirror");
    if (route === "Tab") {
      press(document.body, "Tab"); f.button("Copy").blur();
    } else if (route === "focus") {
      chosen.focus(); chosen.blur();
    } else if (route === "pointer") {
      f.dialog.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    } else if (route === "click") {
      chosen.click();
    } else {
      window.dispatchEvent(new Event("blur")); window.dispatchEvent(new Event("focus"));
    }
    expect(document.activeElement).toBe(document.body);
    await f.finish();
    expect(document.activeElement).toBe(document.body);
    expect(f.root.querySelector(".preview")).not.toBeNull();
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startMirror).not.toHaveBeenCalled();
  });
});

it.each(["button", "scrim", "Escape"].flatMap((route) =>
  (["success", "failure"] as const).map((outcome) => ({ route, outcome })),
))("retires focus immediately on $route close before late $outcome", async ({ route, outcome }) => {
  const f = show(); f.begin(); await settle();
  if (route === "button") f.root.querySelector<HTMLButtonElement>('[aria-label="Close"]')!.click();
  else if (route === "scrim") f.root.querySelector<HTMLElement>(".scrim")!.click();
  else expect(press(document.body, "Escape").defaultPrevented).toBe(true);
  flushSync();
  expect(f.onClose).toHaveBeenCalledOnce();
  const focus = vi.spyOn(f.preview, "focus");
  await f.finish(outcome);
  expect(focus).not.toHaveBeenCalled();
  expect(f.root.querySelector(".preview")).toBeNull();
  expect(f.root.textContent).not.toContain("preview unavailable");
  expect(f.api.startCopy).not.toHaveBeenCalled();
});

it.each(["success", "failure"] as const)("does not refocus an unmounted dialog after %s or touch its replacement", async (outcome) => {
  const old = show(); old.begin(); await settle();
  await unmount(old.component); mounted.delete(old.component);
  const current = show();
  const chosen = document.activeElement;
  const focus = vi.spyOn(old.preview, "focus");
  await old.finish(outcome);
  expect(focus).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(chosen);
  current.begin(); await current.finish();
  expect(document.activeElement).toBe(current.preview);
});

it.each(["scope", "destination", "mode"].flatMap((change) =>
  (["success", "failure"] as const).map((outcome) => ({ change, outcome })),
))("does not revive focus after a $change change and return with late $outcome", async ({ change, outcome }) => {
  const f = show(); f.begin(); await settle();
  if (change === "scope") {
    f.browser.prefix = "elsewhere/"; f.browser.prefix = "selected/";
  } else if (change === "destination") {
    for (const value of ["elsewhere", "backup"]) {
      f.destination.value = value; f.destination.dispatchEvent(new Event("input", { bubbles: true }));
    }
  } else {
    f.button("Mirror").click(); f.button("Copy").click();
  }
  flushSync();
  await f.finish(outcome);
  expect(document.activeElement).toBe(document.body);
  expect(f.root.querySelector(".preview")).toBeNull();
  expect(f.api.startCopy).not.toHaveBeenCalled();
});

it.each(["before", "after"].flatMap((order) =>
  (["success", "failure"] as const).map((outcome) => ({ order, outcome })),
))("keeps a newer request's focus claim when old $outcome arrives $order", async ({ order, outcome }) => {
  const f = show(); f.begin(); await settle();
  f.browser.prefix = "new/"; flushSync(); f.begin(); await settle();
  if (order === "before") {
    await f.finish(outcome, 0);
    expect(f.preview.disabled).toBe(true);
    expect(document.activeElement).toBe(document.body);
    await f.finish("success", 1);
  } else {
    await f.finish("success", 1);
    await f.finish(outcome, 0);
  }
  expect(document.activeElement).toBe(f.preview);
  expect(f.root.textContent).not.toContain("preview unavailable");
  expect(f.api.planCopy).toHaveBeenCalledTimes(2);
  expect(f.api.startCopy).not.toHaveBeenCalled();
});

it("does not take focus from a newer modal or restore later when it closes", async () => {
  const old = show(); old.begin(); await settle();
  const top = show(); const chosen = document.activeElement;
  await old.finish();
  expect(document.activeElement).toBe(chosen);
  await unmount(top.component); mounted.delete(top.component); await settle();
  expect(document.activeElement).not.toBe(old.preview);
  expect(old.api.startCopy).not.toHaveBeenCalled();
});

it.each(["scope", "close", "modal"])("rechecks %s retirement at the re-enable render boundary", async (change) => {
  const f = show(); f.begin(); await settle();
  let chosen: Element | null = document.body;
  f.onEnable(() => {
    if (change === "scope") f.browser.prefix = "new/";
    else if (change === "close") f.root.querySelector<HTMLButtonElement>('[aria-label="Close"]')!.click();
    else { show(); chosen = document.activeElement; }
  });
  await f.finish();
  expect(document.activeElement).toBe(chosen);
  expect(f.api.startCopy).not.toHaveBeenCalled();
});

it("still starts the captured request once after explicit activation", async () => {
  const f = show(); f.begin(); await f.finish();
  expect(document.activeElement).toBe(f.preview);
  expect(f.api.startCopy).not.toHaveBeenCalled();
  const start = f.button("Start copy");
  start.click(); start.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await settle();
  expect(f.api.startCopy).toHaveBeenCalledExactlyOnceWith(vi.mocked(f.api.planCopy).mock.calls[0][0]);
  expect(f.root.textContent).toContain("Job started-job started");
});
