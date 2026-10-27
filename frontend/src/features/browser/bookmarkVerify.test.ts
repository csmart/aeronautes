import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { BookmarkDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BookmarkEditor from "./BookmarkEditor.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
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

function field(placeholder: string, value: string) {
  const input = document.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function key(value: string) {
  const input = document.querySelector<HTMLInputElement>('input[type="password"]')!;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error("Missing button: " + label);
  return found;
}

function show(existing: BookmarkDTO | null = null) {
  const api = newFakeApi();
  const state = new BrowserState(api);
  const verify = vi.spyOn(api, "verifyBookmark");
  const save = vi.spyOn(api, "saveBookmark");
  mounted.add(mount(BookmarkEditor, { target: document.body, props: { browser: state, existing, onClose: vi.fn() } }));
  flushSync();
  return { api, state, verify, save };
}

const status = () => document.querySelector('[role="status"].verified')?.textContent ?? null;
const alert = () => document.querySelector('[role="alert"]')?.textContent ?? null;

describe("verify before save", () => {
  it("reports the account the endpoint answered for and saves nothing", async () => {
    const f = show();
    field("Prod AU", "Fixture");
    field("https://swift.example/auth/v1.0", "https://mock.example/auth/v1.0");
    field("account:user", "test:tester");
    key("testing");
    button("Verify").click();
    await settle();
    expect(f.verify).toHaveBeenCalledWith(expect.objectContaining({ id: "", displayName: "Fixture", username: "test:tester" }), "testing");
    expect(status()).toContain("Verified: account AUTH_demo");
    expect(alert()).toBeNull();
    expect(f.save).not.toHaveBeenCalled();
    expect(f.state.bookmarks).toEqual([]);
  });

  it("shows the endpoint's refusal in place and leaves Save available", async () => {
    const f = show();
    field("Prod AU", "Fixture");
    field("https://swift.example/auth/v1.0", "https://mock.example/auth/v1.0");
    field("account:user", "test:tester");
    key("not-the-key");
    button("Verify").click();
    await settle();
    expect(alert()).toContain("rejected the user or key");
    expect(status()).toBeNull();
    expect(button("Save").disabled).toBe(false);
    expect(f.save).not.toHaveBeenCalled();
  });

  it("drops a stale verdict when a detail changes afterwards", async () => {
    show();
    field("Prod AU", "Fixture");
    field("https://swift.example/auth/v1.0", "https://mock.example/auth/v1.0");
    field("account:user", "test:tester");
    key("testing");
    button("Verify").click();
    await settle();
    expect(status()).not.toBeNull();
    field("account:user", "someone:else");
    expect(status()).toBeNull();
  });

  it("verifies an existing bookmark with its stored key when the key field is left empty", async () => {
    const f = show({ id: "bm-1", displayName: "Saved", authUrl: "https://mock.example/auth/v1.0", username: "test:tester",
      storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: true });
    button("Verify").click();
    await settle();
    expect(f.verify).toHaveBeenCalledWith(expect.objectContaining({ id: "bm-1" }), "");
    expect(status()).toContain("Verified");
  });
});
