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

function bookmark(): BookmarkDTO {
  return {
    id: "",
    displayName: "Mock",
    authUrl: "http://mock/auth/v1.0",
    username: "test:tester",
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "",
    tlsMode: "system",
    caPem: "",
    hasCa: false,
    hasCredential: false,
  };
}

// Connect and mount the inspector with nothing selected, so it shows the
// account (§53): object detail if selected, else the open container, else
// the account.
async function showAccount() {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  const saved = await browser.saveBookmark(bookmark(), "testing");
  await browser.connect(saved.id);
  await settle();
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(Inspector, { target: root, props: { browser } }));
  flushSync();
  return { api, browser, root };
}

function clickButton(root: HTMLElement, text: string) {
  const button = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim().includes(text));
  if (!button) throw new Error(`button "${text}" not found`);
  button.click();
  flushSync();
}

describe("account metadata editor (Inspector wiring)", () => {
  it("opens the editor, saves, and returns to the account view", async () => {
    const f = await showAccount();
    expect(f.root.textContent).toContain("Account metadata");

    clickButton(f.root, "Edit metadata");
    expect(f.root.textContent).toContain("Edit account metadata");

    clickButton(f.root, "Save");
    await settle();
    // Applied: the editor closed and the account view offers Edit again.
    expect(f.root.textContent).not.toContain("Edit account metadata");
    expect([...f.root.querySelectorAll("button")].some((b) => b.textContent?.trim().includes("Edit metadata"))).toBe(true);
  });

  it("shows a conflict banner on a stale fingerprint and applies with force", async () => {
    const f = await showAccount();
    // A working copy whose fingerprint no longer matches the account.
    f.browser.account = { ...f.browser.account!, editableFingerprint: "stale" };
    flushSync();

    clickButton(f.root, "Edit metadata");
    clickButton(f.root, "Save");
    await settle();
    expect(f.root.textContent).toContain("changed since you loaded it");

    clickButton(f.root, "Apply mine anyway");
    await settle();
    // Force applied: the editor closed.
    expect(f.root.textContent).not.toContain("Edit account metadata");
  });

  it("shows an error and stays open when reloading the account fails", async () => {
    const f = await showAccount();
    f.browser.account = { ...f.browser.account!, editableFingerprint: "stale" };
    flushSync();
    clickButton(f.root, "Edit metadata");
    clickButton(f.root, "Save");
    await settle();
    expect(f.root.textContent).toContain("changed since you loaded it");

    f.api.account = vi.fn().mockRejectedValue(new Error("account HEAD failed: 503"));
    clickButton(f.root, "Reload theirs");
    await settle();
    // The failure is shown and the editor stays open for a retry.
    expect(f.root.textContent).toContain("503");
    expect(f.root.textContent).toContain("Edit account metadata");
  });
});
