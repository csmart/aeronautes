import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, MutationResultDTO, SegmentOwnershipDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";
import AboutDialog from "./AboutDialog.svelte";
import ManifestDeleteDialog from "../inspector/ManifestDeleteDialog.svelte";
const applied: MutationResultDTO = { outcome: "applied", mismatch: "", limitation: "", transIds: ["txfake", "txfake"], objectAfter: null, containerAfter: null };

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  Object.assign(api, newFakeApi());
  // JSDOM has no layout. Keep visibility deterministic while exercising the
  // production focus action, including its own closed-details filtering.
  vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockImplementation(function (this: HTMLElement) {
    return this.closest("[hidden]") ? null : this.parentElement;
  });
});
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function get<T extends HTMLElement = HTMLElement>(selector: string, root: ParentNode = document): T {
  const found = root.querySelector<T>(selector);
  if (!found) throw new Error(`Missing element: ${selector}`);
  return found;
}
function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const found = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}
function click(element: HTMLElement) { element.click(); flushSync(); }
function press(target: EventTarget, key: string, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(event); flushSync();
  return event;
}
function selected() { return get('[role="listbox"]').getAttribute("aria-activedescendant"); }

async function screen() {
  await api.saveBookmark({ id: "", displayName: "Keyboard fixture", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  const root = document.createElement("div"); document.body.append(root);
  const component = mount(BrowserScreen, { target: root }); mounted.add(component);
  flushSync(); await settle();
  click(get('[aria-label="Keyboard fixture"]')); await settle();
  click([...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((b) => b.querySelector(".container-name")?.textContent?.trim() === "fixtures")!);
  await settle();
  expect(selected()).toBeNull();
  return { root, component };
}
async function manifest() {
  const f = await screen();
  const row = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((el) => el.querySelector(".name")?.textContent?.trim() === "external-dlo")!;
  row.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
  const opener = button("Download…"); opener.focus();
  const ownership = deferred<SegmentOwnershipDTO>(); api.checkSegmentOwnership = vi.fn(() => ownership.promise);
  const bookmarks = await api.listBookmarks();
  const browser = new BrowserState(api);
  await browser.connect(bookmarks[0]!.id);
  await browser.openContainer("fixtures");
  browser.select(browser.entries.findIndex((entry) => entry.name === "external-dlo")); await settle();
  let dialogComponent: ReturnType<typeof mount>;
  const close = () => { const done = unmount(dialogComponent); mounted.delete(dialogComponent); return done; };
  dialogComponent = mount(ManifestDeleteDialog, { target: document.body, props: {
    browser, detail: browser.objectDetail!, segmentsAt: "backing/old/", onClose: close,
  } });
  mounted.add(dialogComponent); flushSync(); await settle();
  return { ...f, opener, ownership, browser, close, dialog: get('[role="alertdialog"]'),
    reopen() {
      dialogComponent = mount(ManifestDeleteDialog, { target: document.body, props: {
        browser, detail: browser.objectDetail!, segmentsAt: "backing/old/", onClose: close,
      } });
      mounted.add(dialogComponent); flushSync();
    },
  };
}

describe("browser keyboard ownership", () => {
  it.each(["budget", "job filter", "versioning"])("leaves arrows with the native %s select", async (control) => {
    await screen();
    let select: HTMLSelectElement;
    if (control === "versioning") {
      click(button("Configure container"));
      select = get("select");
    } else {
      click(get(".drawer .bar"));
      const selects = [...document.querySelectorAll<HTMLSelectElement>(".drawer select")];
      select = selects[control === "budget" ? 0 : 1];
    }
    expect(select).toBeDefined(); select.focus();
    for (const key of ["ArrowDown", "ArrowUp"]) {
      expect(press(select, key).defaultPrevented).toBe(false);
      expect(document.activeElement).toBe(select);
      expect(selected()).toBeNull();
    }
  });

  it.each(["input", "textarea", "editable descendant"])("leaves native editing keys with %s", async (kind) => {
    const f = await screen();
    let target: HTMLElement;
    if (kind === "input") target = get('[placeholder="filter loaded rows…"]');
    else if (kind === "textarea") { target = document.createElement("textarea"); f.root.append(target); }
    else {
      const editor = document.createElement("div"); editor.setAttribute("contenteditable", "true");
      target = document.createElement("span"); target.tabIndex = 0; editor.append(target); f.root.append(editor);
      // JSDOM omits the native inherited isContentEditable accessor.
      Object.defineProperty(target, "isContentEditable", { value: true });
    }
    target.focus();
    for (const key of ["ArrowDown", "ArrowUp", "Backspace", "/"]) {
      expect(press(target, key).defaultPrevented).toBe(false);
      expect(document.activeElement).toBe(target);
      expect(selected()).toBeNull();
    }
  });

  it.each(["ArrowDown", "ArrowUp", "Backspace", "/"])("respects a control that consumed %s", async (key) => {
    await screen();
    const target = get('[aria-label="Up one level"]'); target.focus();
    target.addEventListener("keydown", (event) => event.preventDefault(), { once: true });
    press(target, key); await settle();
    expect(document.activeElement).toBe(target);
    expect(document.querySelector('[role="listbox"]')).not.toBeNull();
    expect(selected()).toBeNull();
  });

  it("keeps normal list selection, prefix focus and parent navigation usable", async () => {
    await screen();
    const list = get('[role="listbox"]');
    press(button("Find in container"), "ArrowDown"); await settle();
    expect(document.activeElement).toBe(list);
    expect(selected()).toMatch(/-row-0$/);
    press(list, "ArrowDown"); await settle();
    expect(selected()).toMatch(/-row-1$/);
    press(list, "/");
    expect(document.activeElement).toBe(get('[placeholder="server-side prefix…"]'));
    const prefix = get<HTMLInputElement>('[placeholder="server-side prefix…"]');
    prefix.value = "path/"; prefix.dispatchEvent(new Event("change", { bubbles: true })); await settle();
    press(button("Find in container"), "Backspace"); await settle();
    expect(prefix.value).toBe("");
  });

  it("lets Alt+Up reach parent navigation from the working list", async () => {
    await screen();
    const prefix = get<HTMLInputElement>('[placeholder="server-side prefix…"]');
    prefix.value = "normal/"; prefix.dispatchEvent(new Event("change", { bubbles: true })); await settle();
    const list = get('[role="listbox"]'); list.focus();
    press(list, "ArrowUp", { altKey: true }); await settle();
    expect(prefix.value).toBe("");
    expect(selected()).toBeNull();
  });

  it("does not let the working list reuse a consumed arrow", async () => {
    await screen();
    const list = get('[role="listbox"]'); list.focus();
    list.addEventListener("keydown", (event) => event.preventDefault(), { once: true });
    press(list, "ArrowDown"); await settle();
    expect(selected()).toBeNull(); expect(document.activeElement).toBe(list);
  });
});

describe("production modal ownership", () => {
  it.each([
    ["Add", "Add bookmark"], ["Create container", "Create container"],
    ["Download…", "Download"], ["key cleanup", "Credential cleanup"],
  ])("isolates and dismisses the %s dialog through its own callback", async (label, name) => {
    await screen();
    const opener = label === "Create container" ? get('[title="Create container"]')
      : label === "Add" ? get('[title="Add bookmark"]') : button(label);
    opener.focus(); click(opener); await settle();
    const dialog = get('[role="dialog"]');
    const labelId = dialog.getAttribute("aria-labelledby");
    expect(labelId ? document.getElementById(labelId)?.textContent : dialog.getAttribute("aria-label")).toBe(name);
    expect(dialog.getAttribute("aria-modal")).toBe("true");
    expect(dialog.contains(document.activeElement)).toBe(true);
    const target = document.activeElement!; press(target, "ArrowDown"); await settle();
    expect(document.activeElement).toBe(target); expect(selected()).toBeNull();
    press(target, "Escape"); await settle();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(opener);
    expect(document.querySelector("[inert]")).toBeNull();
  });

  it("keeps manifest intent and focus inside through repeated arrows and background focus", async () => {
    const f = await manifest(); const before = selected();
    const cancel = button("Cancel", f.dialog); cancel.focus();
    for (const key of ["ArrowDown", "ArrowDown", "ArrowUp", "/", "Backspace"]) {
      press(document.activeElement!, key); await settle();
      expect(document.activeElement).toBe(cancel);
      expect(selected()).toBe(before);
      expect(document.contains(f.dialog)).toBe(true);
    }
    const list = get('[role="listbox"]'); list.focus();
    expect(f.dialog.contains(document.activeElement)).toBe(true);
    press(list, "ArrowDown"); await settle();
    expect(selected()).toBe(before);
    expect(get(".drawer").closest("[inert]")).not.toBeNull();
    expect(f.dialog.closest("[inert]")).toBeNull();
    expect(f.dialog.getAttribute("aria-modal")).toBe("true");
    press(document.activeElement!, "Escape"); await settle();
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    expect(document.activeElement).toBe(f.opener);
    expect(document.querySelector("[inert]")).toBeNull();
    press(f.opener, "/"); expect(document.activeElement).toBe(get('[placeholder="server-side prefix…"]'));
    press(list, "ArrowDown"); await settle(); expect(selected()).not.toBe(before);
  });

  it("lets a dialog control consume Escape before modal dismissal", async () => {
    const f = await manifest(); const cancel = button("Cancel", f.dialog); cancel.focus();
    cancel.addEventListener("keydown", (event) => event.preventDefault(), { once: true });
    press(cancel, "Escape"); await settle();
    expect(document.contains(f.dialog)).toBe(true);
    press(cancel, "Escape"); await settle(); expect(document.contains(f.dialog)).toBe(false);
  });

  it("routes body Escape to the active busy dialog", async () => {
    const f = await manifest();
    const reply = deferred<Awaited<ReturnType<Api["deleteManifest"]>>>(); api.deleteManifest = vi.fn(() => reply.promise);
    click(button("Delete manifest only", f.dialog)); await settle();
    press(document.body, "Escape"); await settle();
    expect(document.contains(f.dialog)).toBe(false);
    expect(document.activeElement).toBe(f.opener);
    reply.reject(new Error("retired body-Escape deletion")); await settle();
    expect(document.body.textContent).not.toContain("retired body-Escape deletion");
  });

  it("does not reuse a consumed body Escape to dismiss the modal", async () => {
    const f = await manifest();
    const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    event.preventDefault(); document.body.dispatchEvent(event); flushSync(); await settle();
    expect(document.contains(f.dialog)).toBe(true);
    expect(f.dialog.contains(document.activeElement)).toBe(true);
  });

  it.each(["success", "failure"])("Escape while deletion is busy retires the old %s and preserves a reopened dialog", async (outcome) => {
    const f = await manifest(); const before = selected();
    const reply = deferred<Awaited<ReturnType<Api["deleteManifest"]>>>(); api.deleteManifest = vi.fn(() => reply.promise);
    click(button("Delete manifest only", f.dialog)); await settle();
    // The third argument is what the dialog was SHOWN, so it is compared with
    // the detail the fake served for this object rather than with a literal: a
    // dialog passing any constant would satisfy a literal and defeat ADR 0085.
    const shownDetail = await api.objectDetail("fixtures", "external-dlo", "bm-1");
    expect(shownDetail.contentFingerprint).toBeTruthy();
    expect(api.deleteManifest).toHaveBeenCalledExactlyOnceWith(
      shownDetail.scope, "fixtures", "external-dlo", shownDetail.contentFingerprint);
    press(window, "Tab"); expect(document.activeElement).toBe(f.dialog);
    press(window, "Escape"); await settle();
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    f.reopen(); await settle(); const replacement = get('[role="alertdialog"]');
    if (outcome === "success") reply.resolve(applied);
    else reply.reject(new Error("retired deletion failure"));
    await settle();
    expect(document.contains(replacement)).toBe(true);
    expect(replacement.textContent).not.toContain("retired deletion failure");
    expect(button("Delete manifest only", replacement).disabled).toBe(false);
    expect(selected()).toBe(before);
    expect(replacement.contains(document.activeElement)).toBe(true);
  });

  it("only closes the top dialog and restores its parent before the browser", async () => {
    const f = await manifest(); const cancel = button("Cancel", f.dialog); cancel.focus();
    const layer = document.createElement("div"); document.body.append(layer);
    let top: ReturnType<typeof mount>;
    const close = vi.fn(() => { void unmount(top); mounted.delete(top); });
    top = mount(AboutDialog, { target: layer, props: { onClose: close } }); mounted.add(top); flushSync(); await settle();
    expect(f.dialog.closest("[inert]")).not.toBeNull();
    press(document.activeElement!, "Escape"); await settle();
    expect(close).toHaveBeenCalledOnce();
    expect(document.contains(f.dialog)).toBe(true);
    expect(document.activeElement).toBe(cancel);
    press(cancel, "Escape"); await settle(); expect(document.activeElement).toBe(f.opener);
  });

  it("does not restore a removed lower opener over the current dialog", async () => {
    const f = await manifest();
    const layer = document.createElement("div"); document.body.append(layer);
    const top = mount(AboutDialog, { target: layer, props: { onClose: () => {} } }); mounted.add(top); flushSync(); await settle();
    const active = document.activeElement;
    expect(layer.contains(active)).toBe(true);
    await f.close();
    await unmount(f.component); mounted.delete(f.component); await settle();
    expect(document.activeElement).toBe(active);
    await unmount(top); mounted.delete(top); await settle();
    expect(document.querySelector("[inert]")).toBeNull();
    const fresh = await screen();
    press(button("Find in container"), "ArrowDown"); await settle();
    expect(fresh.root.contains(document.activeElement)).toBe(true);
    expect(selected()).toMatch(/-row-0$/);
  });

  it("falls back to the remaining dialog when a nested opener was removed", async () => {
    const f = await manifest(); const cancel = button("Cancel", f.dialog); cancel.focus();
    const layer = document.createElement("div"); document.body.append(layer);
    const top = mount(AboutDialog, { target: layer, props: { onClose: () => {} } }); mounted.add(top); flushSync(); await settle();
    cancel.remove();
    await unmount(top); mounted.delete(top); await settle();
    expect(document.activeElement).toBe(f.dialog);
    press(f.dialog, "Tab"); expect(document.activeElement).toBe(button("Delete manifest only", f.dialog));
  });

  it("isolates later background nodes and restores existing inert values", async () => {
    await screen();
    const existing = document.createElement("div"); existing.setAttribute("inert", "existing"); document.body.append(existing);
    click(button("Download…")); await settle(); const dialog = get('[role="dialog"]');
    const late = document.createElement("button"); late.textContent = "late background"; document.body.append(late); await settle();
    expect(late.hasAttribute("inert")).toBe(true);
    late.focus(); expect(dialog.contains(document.activeElement)).toBe(true);
    press(document.activeElement!, "Escape"); await settle();
    expect(late.hasAttribute("inert")).toBe(false);
    expect(existing.getAttribute("inert")).toBe("existing");
  });

  it("does not cycle into fields disabled by a pending bookmark save", async () => {
    await screen(); click(get('[aria-label="Edit Keyboard fixture"]')); await settle();
    const dialog = get('[role="dialog"]');
    const reply = deferred<Awaited<ReturnType<Api["saveBookmark"]>>>(); api.saveBookmark = vi.fn(() => reply.promise);
    click(button("Save", dialog)); await settle();
    const field = get<HTMLInputElement>("input", dialog);
    expect(field.disabled).toBe(false); expect(field.matches(":disabled")).toBe(true);
    press(window, "Tab"); expect(document.activeElement).toBe(dialog);
    press(window, "Escape"); await settle();
    reply.reject(new Error("retired save")); await settle();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.body.textContent).not.toContain("retired save");
  });

  it("admits one form submission and retires its navigation when Escape closes creation", async () => {
    await screen(); click(get('[title="Create container"]')); await settle();
    const dialog = get('[role="dialog"]'); const name = get<HTMLInputElement>("input", dialog);
    name.value = "keyboard-created"; name.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
    const reply = deferred<Awaited<ReturnType<Api["createContainer"]>>>(); api.createContainer = vi.fn(() => reply.promise);
    const form = get<HTMLFormElement>("form", dialog); form.requestSubmit(); form.requestSubmit(); await settle();
    expect(api.createContainer).toHaveBeenCalledExactlyOnceWith(expect.anything(), { name: "keyboard-created", policy: "", meta: {} });
    press(window, "Tab"); expect(document.activeElement).toBe(dialog);
    press(window, "Escape"); await settle();
    reply.resolve(await api.containerDetail("fixtures", "bm-1")); await settle();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(get<HTMLInputElement>('[placeholder="server-side prefix…"]').value).toBe("");
    expect(get(".crumb.root").textContent?.trim()).toBe("fixtures");
  });

  it("cycles Tab over the download dialog's enabled controls", async () => {
    await screen(); click(button("Download…")); await settle();
    const dialog = get('[role="dialog"]');
    expect(dialog.getAttribute("aria-label")).toBe("Download");
    const close = get('[aria-label="Close"]', dialog);
    const choose = button("Choose directory…", dialog);
    expect(choose.disabled).toBe(false);
    close.focus(); press(close, "Tab", { shiftKey: true });
    expect(document.activeElement).toBe(choose);
    press(choose, "Tab");
    expect(document.activeElement).toBe(close);
  });
});
