import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import TransferDialog from "./TransferDialog.svelte";

// ADR 0115 in the copy dialog: the destination's names are suggested, a
// missing destination is offered for creation instead of an error, the
// create is sent with the scope the preview returned and is followed by a
// fresh preview, and Start stays disabled until a plan exists.
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

// The dialog's sentences break across template lines.
function text(): string {
  return (document.body.textContent ?? "").replace(/\s+/g, " ");
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error("Missing button: " + label);
  return found;
}

describe("copy destination", () => {
  it("suggests names, offers a missing destination, creates it with the preview's scope and plans again", async () => {
    const api = newFakeApi();
    const create = vi.spyOn(api, "createDestinationContainer");
    const browser = new BrowserState(api);
    const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
      username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
    await browser.connect(bookmark.id);
    await browser.openContainer("fixtures");
    await settle();
    mounted.add(mount(TransferDialog, { target: document.body, props: { browser, onClose: vi.fn() } }));
    await settle();

    const suggestions = [...document.querySelectorAll("datalist option")].map((o) => o.getAttribute("value"));
    expect(suggestions).toContain("fixtures");

    const field = document.querySelector<HTMLInputElement>('input[aria-label="Destination container"]')!;
    field.value = "missing-archive";
    field.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
    button("Preview").click();
    await settle();
    expect(text()).toContain("does not exist on Mock");
    expect(button("Start copy").disabled).toBe(true);

    button("Create container missing-archive on Mock").click();
    await settle();
    expect(create).toHaveBeenCalledWith({ bookmarkId: bookmark.id, generation: 1, container: "missing-archive" });
    expect(text()).not.toContain("does not exist on Mock");
    expect(button("Start copy").disabled).toBe(false);
  });
});
