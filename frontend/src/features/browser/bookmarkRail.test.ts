import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, AppSettingsDTO, BookmarkDTO } from "../../lib/api";
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
  return { id, displayName, authUrl: `https://${host}/auth/v1.0`, username, storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: true };
}

function showRail(bookmarks: BookmarkDTO[]) {
  const state = new BrowserState(api);
  state.bookmarks = bookmarks;
  state.bookmarksLoaded = true;
  mounted.add(mount(BookmarkRail, { target: document.body,
    props: { browser: state, onAddBookmark: vi.fn(), onEditBookmark: vi.fn(), onOpenSettings: vi.fn() } }));
  flushSync();
  return state;
}

function typeFilter(value: string) {
  const filter = document.querySelector<HTMLInputElement>(".filter")!;
  filter.value = value;
  filter.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function railNames(): string[] {
  return [...document.querySelectorAll(".bookmark-row .label")].map((node) => node.textContent ?? "");
}


describe("an imported bookmark that did not verify", () => {
  // ADR 0114: the reason stays on the tile until the bookmark signs in.
  it("shows its reason on the tile until a connect clears it", async () => {
    const saved = await api.saveBookmark(bookmark("", "Imported", "swift.example"), "key");
    const state = showRail([{ ...saved, verifyFailure: "the identity service refused these credentials" }]);
    const flag = () => document.querySelector('[aria-label="Did not verify: the identity service refused these credentials"]');
    expect(flag()).not.toBeNull();

    await state.connect(saved.id);
    await settle();
    expect(flag()).toBeNull();
  });
});

describe("bookmark rail arrangement", () => {
  // The operator found the host headings noise in the 0.5.0 test run: the
  // rail is one list of letter tiles, named beneath, with the host on hover.
  it("lists every bookmark by name with no host headings and describes each on hover", () => {
    showRail([
      bookmark("1", "zeta", "swift.b.example"),
      bookmark("2", "Alpha", "swift.b.example", "tenant:alpha"),
      bookmark("3", "Mid", "swift.a.example"),
    ]);
    expect(document.querySelectorAll(".rail h3")).toHaveLength(0);
    expect(railNames()).toEqual(["Alpha", "Mid", "zeta"]);
    const alpha = document.querySelector<HTMLButtonElement>('[aria-label="Alpha"]')!;
    expect(alpha.title).toBe("Alpha\ntenant:alpha at swift.b.example");
    expect(document.querySelector(".filter")).toBeNull();
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

  it("keeps the connected bookmark and its TLS warning in view through a filter", () => {
    const many = Array.from({ length: 8 }, (_, i) => bookmark(`bm-${i}`, `Site ${i}`, "one.example"));
    many[3] = { ...many[3], tlsMode: "insecure" };
    const state = showRail(many);
    state.connectedBookmarkId = "bm-3";
    flushSync();
    typeFilter("Site 5");
    // The account in use is listed beside the match, warning and all.
    expect(railNames()).toEqual(["Site 3", "Site 5"]);
    expect(document.querySelector(".bookmark.active .insecure-flag")).not.toBeNull();
    expect(document.querySelector<HTMLButtonElement>(".bookmark.active")!.title).toContain("TLS verification DISABLED");
  });

  it("keeps the filter box while a filter is typed, however short the list becomes", () => {
    const many = Array.from({ length: 8 }, (_, i) => bookmark(`bm-${i}`, `Site ${i}`, "one.example"));
    const state = showRail(many);
    typeFilter("Site 7");
    expect(railNames()).toEqual(["Site 7"]);
    state.bookmarks = many.filter((b) => b.displayName !== "Site 7");
    flushSync();
    expect(document.querySelector(".filter")).not.toBeNull();
    expect(railNames()).toEqual([]);
    expect(document.body.textContent).toContain("No bookmark matches.");
    typeFilter("");
    expect(railNames()).toHaveLength(7);
    expect(document.querySelector(".filter")).toBeNull();
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
    // The fake starts with no bookmark, so the first-run welcome opens and
    // its focus trap keeps keys away from the rest of the screen; these
    // tests are about the rail, so decline it first.
    const notNow = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === "Not now");
    if (notNow) {
      notNow.click();
      flushSync();
      await settle();
    }
    return {
      handle: document.querySelector<HTMLElement>('[aria-label="Resize bookmark rail"]')!,
      screen: document.querySelector<HTMLElement>(".screen")!,
    };
  }

  it("drags to a width, applies it to the layout and persists it with the settings", async () => {
    const save = vi.spyOn(api, "saveAppSettings");
    const { handle, screen: el } = await screen();
    expect(el.style.getPropertyValue("--rail-width")).toBe("");
    // The splitter reports a position at the layout's default too; JSDOM
    // measures nothing, so the minimum stands in for the rendered width.
    expect(handle.getAttribute("aria-valuenow")).toBe("64");
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
    expect(handle.getAttribute("aria-valuenow")).toBe("64");
  });

  it("stores the last width chosen while an earlier write was still in flight", async () => {
    let release!: (settings: AppSettingsDTO) => void;
    const held = new Promise<AppSettingsDTO>((resolve) => { release = resolve; });
    const saves: number[] = [];
    api.saveAppSettings = vi.fn(async (settings: AppSettingsDTO) => {
      saves.push(settings.railWidthPx);
      return saves.length === 1 ? held : settings;
    });
    const { handle, screen: el } = await screen();
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }));
    await settle();
    expect(el.style.getPropertyValue("--rail-width")).toBe("96px");
    expect(saves).toEqual([80]);
    release({ ...(await newFakeApi().appSettings()), railWidthPx: 80 });
    await settle();
    expect(saves).toEqual([80, 96]);
    expect(el.style.getPropertyValue("--rail-width")).toBe("96px");
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

// Every press of the theme button shows the other theme: from following a
// light system it goes dark, then light, then dark (0.5.0 test run: one
// press in three changed nothing).
it("switches between light and dark on every press", () => {
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: false, media: query, addEventListener: () => {}, removeEventListener: () => {} }));
  try {
    showRail([]);
    const press = () => {
      document.querySelector<HTMLButtonElement>('button[aria-label="Switch between light and dark"]')!.click();
      flushSync();
      return document.documentElement.getAttribute("data-theme");
    };
    expect(press()).toBe("dark");
    expect(press()).toBe("light");
    expect(press()).toBe("dark");
  } finally {
    document.documentElement.removeAttribute("data-theme");
    vi.unstubAllGlobals();
  }
});
