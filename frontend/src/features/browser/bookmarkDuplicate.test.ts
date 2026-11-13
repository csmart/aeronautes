import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, BookmarkDTO } from "../../lib/api";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

// Duplicate starts a new bookmark from a saved one's details, for another
// account on the same cluster. What is secret, or belongs to the first
// account alone, is not carried: its key, its CA bundle and its storage URL
// override.

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}
function input(label: string): HTMLInputElement | HTMLTextAreaElement {
  const field = [...document.querySelectorAll<HTMLLabelElement>("label.field")]
    .find((l) => l.querySelector("span")?.firstChild?.textContent?.trim() === label);
  const found = field?.querySelector<HTMLInputElement | HTMLTextAreaElement>("input, textarea");
  if (!found) throw new Error(`Missing field: ${label}`);
  return found;
}
function type(label: string, value: string) {
  const field = input(label);
  field.value = value;
  field.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

const blank: BookmarkDTO = { id: "", displayName: "", authUrl: "", username: "", storageUrlOverride: "", authVersion: 1,
  userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false };

async function duplicateOf(source: BookmarkDTO, key: string) {
  Object.assign(api, newFakeApi());
  const saved = await api.saveBookmark(source, key);
  const save = vi.spyOn(api, "saveBookmark");
  mounted.add(mount(BrowserScreen, { target: document.body }));
  flushSync(); await settle();
  document.querySelector<HTMLElement>(`[aria-label="Edit ${source.displayName}"]`)!.click();
  flushSync(); await settle();
  button("Duplicate…").click();
  flushSync(); await settle();
  return { saved, save };
}

describe("duplicating a bookmark", () => {
  it("opens a new bookmark filled from the saved one, asks for its own key, and saves a second", async () => {
    const { saved, save } = await duplicateOf({ ...blank, displayName: "Prod AU", authUrl: "https://keystone.example.test/v3",
      username: "alice", authVersion: 3, userDomain: "Staff", projectName: "media", projectDomain: "Default", region: "RegionOne" }, "alice-key");

    expect(document.querySelector("form.editor")?.getAttribute("aria-label")).toBe("Add bookmark");
    const note = document.querySelector('[role="status"]')?.textContent ?? "";
    expect(note).toContain("Copied from Prod AU");
    // Nothing is said about a CA bundle or an override this bookmark never had.
    expect(note).not.toContain("CA bundle");
    expect(note).not.toContain("override");
    expect(input("Display name").value).toBe("Prod AU copy");
    expect(input("Identity URL").value).toBe("https://keystone.example.test/v3");
    expect(input("Username").value).toBe("alice");
    expect(input("User domain").value).toBe("Staff");
    expect(input("Project").value).toBe("media");
    expect(input("Region").value).toBe("RegionOne");
    const key = input("Password") as HTMLInputElement;
    expect(key.value).toBe("");
    expect(key.required).toBe(true);

    type("Username", "bob");
    type("Password", "bob-key");
    button("Save").click();
    flushSync(); await settle();
    expect(save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: "", displayName: "Prod AU copy", username: "bob",
      authUrl: "https://keystone.example.test/v3", authVersion: 3, userDomain: "Staff", projectName: "media", region: "RegionOne" }), "bob-key");
    const list = await api.listBookmarks();
    expect(list.map((b) => b.displayName).sort()).toEqual(["Prod AU", "Prod AU copy"]);
    // The bookmark it was copied from is as it was.
    expect(list.find((b) => b.id === saved.id)).toEqual(saved);
  });

  it("does not carry the storage URL override or the CA bundle", async () => {
    const { save } = await duplicateOf({ ...blank, displayName: "Lab", authUrl: "https://swift.example.test/auth/v1.0",
      username: "lab:alice", storageUrlOverride: "https://swift.example.test/v1/AUTH_lab", tlsMode: "custom_ca",
      caPem: "-----BEGIN CERTIFICATE-----\nlab\n-----END CERTIFICATE-----" }, "alice-key");

    const note = document.querySelector('[role="status"]')?.textContent ?? "";
    expect(note).toContain("Paste the CA bundle again");
    expect(note).toContain("Its storage URL override is not copied");
    const bundle = document.querySelector<HTMLTextAreaElement>("textarea.ca")!;
    expect(bundle.value).toBe("");
    expect(bundle.required).toBe(true);

    bundle.value = "-----BEGIN CERTIFICATE-----\nagain\n-----END CERTIFICATE-----";
    bundle.dispatchEvent(new Event("input", { bubbles: true }));
    type("Username", "lab:bob");
    type("API key", "bob-key");
    button("Save").click();
    flushSync(); await settle();
    expect(save).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: "", username: "lab:bob", storageUrlOverride: "",
      tlsMode: "custom_ca", hasCa: false, caPem: "-----BEGIN CERTIFICATE-----\nagain\n-----END CERTIFICATE-----" }), "bob-key");
  });
});
