import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, BookmarkDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BookmarkRail from "./BookmarkRail.svelte";
import BrowserScreen from "./BrowserScreen.svelte";

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  Object.assign(api, newFakeApi());
});
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 16; i++) {
    await Promise.resolve();
    flushSync();
  }
}

function bookmark(id: string, displayName: string, host: string, username = "acct:user"): BookmarkDTO {
  return { id, displayName, authUrl: `https://${host}/auth/v1.0`, username, storageUrlOverride: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: true };
}

function showRail(bookmarks: BookmarkDTO[]) {
  const state = new BrowserState(api);
  state.bookmarks = bookmarks;
  state.bookmarksLoaded = true;
  mounted.add(mount(BookmarkRail, { target: document.body,
    props: { browser: state, onAddBookmark: vi.fn(), onEditBookmark: vi.fn() } }));
  flushSync();
}

function railNames(): string[] {
  return [...document.querySelectorAll(".bookmark-row .label")].map((node) => node.textContent ?? "");
}

function headings(): string[] {
  return [...document.querySelectorAll(".rail .group")].map((node) => node.textContent ?? "");
}

describe("bookmark rail arrangement", () => {
  it("groups by endpoint, sorts both levels and describes each account on hover", () => {
    showRail([
      bookmark("1", "zeta", "swift.b.example"),
      bookmark("2", "Alpha", "swift.b.example", "tenant:alpha"),
      bookmark("3", "Mid", "swift.a.example"),
    ]);
    expect(headings()).toEqual(["swift.a.example", "swift.b.example"]);
    expect(railNames()).toEqual(["Mid", "Alpha", "zeta"]);
    const alpha = document.querySelector<HTMLButtonElement>('[aria-label="Alpha"]')!;
    expect(alpha.title).toBe("Alpha\ntenant:alpha at swift.b.example");
    expect(document.querySelector(".filter")).toBeNull();
  });

  it("shows no heading when every bookmark points at one cloud", () => {
    showRail([bookmark("1", "b", "one.example"), bookmark("2", "a", "one.example")]);
    expect(headings()).toEqual([]);
    expect(railNames()).toEqual(["a", "b"]);
  });

  it("offers a filter once eight bookmarks are saved and narrows by name, account or endpoint", async () => {
    const many = Array.from({ length: 8 }, (_, i) =>
      bookmark(`bm-${i}`, `Site ${i}`, i < 4 ? "north.example" : "south.example", `tenant${i}:user`));
    showRail(many);
    const filter = document.querySelector<HTMLInputElement>(".filter")!;
    expect(filter).not.toBeNull();
    expect(railNames()).toHaveLength(8);

    filter.value = "south";
    filter.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    expect(railNames()).toEqual(["Site 4", "Site 5", "Site 6", "Site 7"]);
    expect(headings()).toEqual([]);

    filter.value = "tenant2";
    filter.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    expect(railNames()).toEqual(["Site 2"]);

    filter.value = "nowhere";
    filter.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    expect(railNames()).toEqual([]);
    expect(document.body.textContent).toContain("No bookmark matches.");
  });
});

describe("bookmark rail width", () => {
  function pointer(type: string, clientX: number): PointerEvent {
    // JSDOM lacks PointerEvent; a MouseEvent carries what the handler reads.
    const event = new MouseEvent(type, { bubbles: true, clientX, button: 0 }) as unknown as PointerEvent;
    Object.defineProperty(event, "pointerId", { value: 1 });
    return event;
  }

  async function screen() {
    mounted.add(mount(BrowserScreen, { target: document.body }));
    flushSync();
    await settle();
    return {
      handle: document.querySelector<HTMLElement>('[aria-label="Resize bookmark rail"]')!,
      screen: document.querySelector<HTMLElement>(".screen")!,
    };
  }

  it("drags to a width, applies it to the layout and persists it with the settings", async () => {
    const save = vi.spyOn(api, "saveAppSettings");
    const { handle, screen: el } = await screen();
    expect(el.style.getPropertyValue("--rail-width")).toBe("");
    handle.dispatchEvent(pointer("pointerdown", 10));
    handle.dispatchEvent(pointer("pointermove", 110));
    flushSync();
    // JSDOM measures nothing, so the drag starts from the minimum width.
    expect(el.style.getPropertyValue("--rail-width")).toBe("164px");
    handle.dispatchEvent(pointer("pointerup", 110));
    await settle();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0][0]).toMatchObject({ railWidthPx: 164, maxActiveJobs: 2 });
    expect(el.style.getPropertyValue("--rail-width")).toBe("164px");
    expect(handle.getAttribute("aria-valuenow")).toBe("164");
  });

  it("moves by keyboard within the bounds and resets with Home", async () => {
    const save = vi.spyOn(api, "saveAppSettings");
    const { handle, screen: el } = await screen();
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    await settle();
    expect(el.style.getPropertyValue("--rail-width")).toBe("80px");
    for (let i = 0; i < 40; i++) handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    await settle();
    expect(el.style.getPropertyValue("--rail-width")).toBe("480px");
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "Home", bubbles: true }));
    await settle();
    expect(el.style.getPropertyValue("--rail-width")).toBe("");
    expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ railWidthPx: 0 }));
  });

  it("opens at the persisted width and keeps a dragged width for the window when saving fails", async () => {
    const stored = { ...(await newFakeApi().appSettings()), railWidthPx: 240 };
    api.appSettings = async () => stored;
    api.saveAppSettings = vi.fn().mockRejectedValue(new Error("settings unavailable"));
    const { handle, screen: el } = await screen();
    expect(el.style.getPropertyValue("--rail-width")).toBe("240px");
    handle.dispatchEvent(pointer("pointerdown", 0));
    handle.dispatchEvent(pointer("pointermove", -100));
    handle.dispatchEvent(pointer("pointerup", -100));
    await settle();
    expect(api.saveAppSettings).toHaveBeenCalledWith(expect.objectContaining({ railWidthPx: 140 }));
    expect(el.style.getPropertyValue("--rail-width")).toBe("140px");
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });
});
