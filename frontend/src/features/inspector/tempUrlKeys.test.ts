import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import type { BookmarkDTO } from "../../lib/api";
import Inspector from "./Inspector.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

async function settle() {
  for (let i = 0; i < 12; i++) {
    await Promise.resolve();
    flushSync();
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

function bookmark(): BookmarkDTO {
  return {
    id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: false,
  };
}

// Connect and show the inspector on the account, or on an open container
// with nothing selected (§53); the TempURL card sits in both views.
async function show(container?: string) {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  const saved = await browser.saveBookmark(bookmark(), "testing");
  await browser.connect(saved.id);
  if (container) await browser.openContainer(container);
  await settle();
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(Inspector, { target: root, props: { browser } }));
  flushSync();
  const card = () => root.querySelector<HTMLElement>('[aria-label="TempURL keys"]')!;
  const slotRows = () => [...card().querySelectorAll<HTMLElement>(".slot")].map((row) => row.textContent?.replace(/\s+/g, " ").trim());
  const click = (label: string) => {
    const button = [...card().querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
    if (!button) throw new Error(`button "${label}" not found`);
    button.click();
    flushSync();
  };
  const keyInput = () => card().querySelector<HTMLInputElement>('input[aria-label="new key"]')!;
  const type = (value: string) => {
    keyInput().value = value;
    keyInput().dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
  };
  return { api, browser, root, card, slotRows, click, keyInput, type };
}

describe("TempURL key slots (ADR 0101)", () => {
  it("shows each slot's presence with set or replace and clear actions", async () => {
    const f = await show();
    expect(f.slotRows()).toEqual(["Temp-URL-Key set Replace… Clear…", "Temp-URL-Key-2 not set Set…"]);
  });

  it("sets a slot with a generated key shown once and sent once, carrying the presence it showed", async () => {
    const f = await show();
    const call = vi.spyOn(f.api, "updateTempUrlKey");
    f.click("Set…");
    const submit = [...f.card().querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === "Set key")!;
    expect(submit.disabled).toBe(true);
    expect(f.keyInput().type).toBe("password");
    f.click("Generate");
    expect(f.keyInput().type).toBe("text");
    expect(f.keyInput().value).toMatch(/^[0-9a-f]{64}$/);
    const generated = f.keyInput().value;
    f.click("Set key");
    await settle();
    expect(call).toHaveBeenCalledTimes(1);
    expect(call.mock.calls[0][1]).toEqual({ container: "", slot: "Temp-URL-Key-2", baseSlots: ["Temp-Url-Key"], secret: generated, clear: false });
    expect(f.card().querySelector('input[aria-label="new key"]')).toBeNull();
    expect(f.slotRows()[1]).toBe("Temp-URL-Key-2 set Replace… Clear…");
    expect(f.browser.account?.tempUrlKeySlots).toEqual(["Temp-Url-Key", "Temp-Url-Key-2"]);
  });

  it("clears a slot after a confirmation and never sends a secret with it", async () => {
    const f = await show();
    const call = vi.spyOn(f.api, "updateTempUrlKey");
    f.click("Clear…");
    expect(f.card().textContent).toContain("Temporary URLs signed with it stop working");
    f.click("Clear key");
    await settle();
    expect(call.mock.calls[0][1]).toEqual({ container: "", slot: "Temp-URL-Key", baseSlots: ["Temp-Url-Key"], secret: "", clear: true });
    expect(f.slotRows()[0]).toBe("Temp-URL-Key not set Set…");
  });

  it("reports a presence that changed underneath as a conflict with nothing written", async () => {
    const f = await show();
    // Another client fills the second slot after the card was drawn.
    await f.api.updateTempUrlKey(f.browser.account!.scope, { container: "", slot: "Temp-URL-Key-2", baseSlots: ["Temp-Url-Key"], secret: "elsewhere", clear: false });
    f.click("Clear…");
    f.click("Clear key");
    await settle();
    expect(f.card().textContent).toContain("The key slots changed since you loaded them");
    // The confirmation stays open under the banner; the slot is still set.
    expect(f.slotRows()[0]).toBe("Temp-URL-Key set");
    f.click("Reload");
    await settle();
    expect(f.slotRows()).toEqual(["Temp-URL-Key set Replace… Clear…", "Temp-URL-Key-2 set Replace… Clear…"]);
  });

  it("freezes the key while a save is pending, so the value shown is the value sent", async () => {
    const f = await show();
    const pending = deferred<Awaited<ReturnType<typeof f.api.updateTempUrlKey>>>();
    const real = f.api.updateTempUrlKey.bind(f.api);
    const call = vi.spyOn(f.api, "updateTempUrlKey").mockImplementation(async (scope, edit) => {
      const result = await real(scope, edit);
      await pending.promise;
      return result;
    });
    f.click("Set…");
    f.click("Generate");
    const sent = f.keyInput().value;
    f.click("Set key");
    await settle();
    expect(call).toHaveBeenCalledTimes(1);
    expect(f.keyInput().disabled).toBe(true);
    expect([...f.card().querySelectorAll<HTMLButtonElement>("button")].filter((b) => ["Generate", "Hide", "Show"].includes(b.textContent?.trim() ?? "")).every((b) => b.disabled)).toBe(true);
    f.click("Generate");
    expect(f.keyInput().value).toBe(sent);
    pending.resolve(undefined as never);
    await settle();
    expect(f.card().querySelector('input[aria-label="new key"]')).toBeNull();
    expect(f.slotRows()[1]).toBe("Temp-URL-Key-2 set Replace… Clear…");
  });

  it("keeps a failed account reload visible and retryable", async () => {
    const f = await show();
    await f.api.updateTempUrlKey(f.browser.account!.scope, { container: "", slot: "Temp-URL-Key-2", baseSlots: ["Temp-Url-Key"], secret: "elsewhere", clear: false });
    f.click("Clear…");
    f.click("Clear key");
    await settle();
    expect(f.card().textContent).toContain("The key slots changed since you loaded them");
    const read = vi.spyOn(f.api, "account").mockRejectedValueOnce(new Error("503 from the fixture"));
    f.click("Reload");
    await settle();
    expect(read).toHaveBeenCalledTimes(1);
    expect(f.card().textContent).toContain("503 from the fixture");
    expect(f.card().textContent).toContain("The key slots changed since you loaded them");
    f.click("Reload");
    await settle();
    expect(f.card().textContent).not.toContain("The key slots changed since you loaded them");
    expect(f.slotRows()).toEqual(["Temp-URL-Key set Replace… Clear…", "Temp-URL-Key-2 set Replace… Clear…"]);
  });

  it("targets the open container and adopts its verified after-state", async () => {
    const f = await show("fixtures");
    const call = vi.spyOn(f.api, "updateTempUrlKey");
    expect(f.slotRows()).toEqual(["Temp-URL-Key not set Set…", "Temp-URL-Key-2 not set Set…"]);
    f.click("Set…");
    f.type("typed-key-for-fixtures");
    f.click("Set key");
    await settle();
    expect(call.mock.calls[0][1]).toMatchObject({ container: "fixtures", slot: "Temp-URL-Key", baseSlots: [], clear: false });
    expect(f.browser.containerDetail?.tempUrlKeySlots).toEqual(["Temp-Url-Key"]);
    expect(f.slotRows()[0]).toBe("Temp-URL-Key set Replace… Clear…");
  });
});
