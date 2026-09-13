import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import CredentialStatusDialog from "./CredentialStatusDialog.svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

let dialog: ReturnType<typeof mount> | null = null;

afterEach(async () => {
  if (dialog) await unmount(dialog);
  dialog = null;
  document.body.innerHTML = "";
});

it("shows unavailable status, refreshes counts, and discloses retained keys", async () => {
  const api = newFakeApi();
  api.credentialStatus = vi.fn().mockRejectedValueOnce(new Error("private details"))
    .mockResolvedValueOnce({ retired: 2, unfinished: 1, saving: 1, unverified: 0 });
  const browser = new BrowserState(api);
  const onClose = vi.fn();
  dialog = mount(CredentialStatusDialog, { target: document.body, props: { browser, onClose } });
  flushSync();
  await vi.waitFor(() => expect(document.body.textContent).toContain("Cleanup status is unavailable"));
  expect(document.body.textContent).not.toContain("No pending credential cleanup");
  expect(document.body.textContent).not.toContain("private details");
  const refresh = [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Refresh")!;
  refresh.click();
  await vi.waitFor(() => expect([...document.querySelectorAll("dd")].map((n) => n.textContent)).toEqual(["2", "1", "1", "0"]));
  expect(document.body.textContent).toContain("await cleanup");
  expect(document.body.textContent).toContain("Cleanup is not available in this version");
  expect(document.body.textContent).toContain("cannot confirm which keys still exist");
  expect(document.querySelector('[role="dialog"]')?.getAttribute("aria-modal")).toBe("true");
  (document.querySelector('[aria-label="Close credential cleanup"]') as HTMLButtonElement).click();
  expect(onClose).toHaveBeenCalledOnce();
});

it("refreshes when reopened instead of displaying the previous counts", async () => {
  const api = newFakeApi();
  api.credentialStatus = vi.fn().mockResolvedValueOnce({ retired: 2, unfinished: 0, saving: 0, unverified: 0 })
    .mockResolvedValueOnce({ retired: 3, unfinished: 1, saving: 0, unverified: 0 });
  const browser = new BrowserState(api);
  const props = { browser, onClose: () => {} };
  dialog = mount(CredentialStatusDialog, { target: document.body, props });
  flushSync();
  await vi.waitFor(() => expect(document.querySelector("dd")?.textContent).toBe("2"));
  await unmount(dialog);
  dialog = mount(CredentialStatusDialog, { target: document.body, props });
  flushSync();
  await vi.waitFor(() => expect(document.querySelector("dd")?.textContent).toBe("3"));
  expect(api.credentialStatus).toHaveBeenCalledTimes(2);
});

it("separates work in an unconfirmed store from work this session recorded", async () => {
  // The rendered copy wraps, so compare against collapsed whitespace.
  const text = () => document.body.textContent!.replace(/\s+/g, " ");
  const api = newFakeApi();
  api.credentialStatus = vi.fn()
    .mockResolvedValueOnce({ retired: 3, unfinished: 1, saving: 1, unverified: 3 })
    .mockResolvedValueOnce({ retired: 3, unfinished: 1, saving: 1, unverified: 0 });
  const browser = new BrowserState(api);
  const props = { browser, onClose: () => {} };
  dialog = mount(CredentialStatusDialog, { target: document.body, props });
  flushSync();

  await vi.waitFor(() =>
    expect([...document.querySelectorAll("dd")].map((n) => n.textContent)).toEqual(["3", "1", "1", "3"]));
  expect(text()).toContain("cannot confirm it can reach");
  expect(text()).toContain("It may still exist there");
  expect(text()).toContain("not proof that its key was erased");
  expect(text()).toContain("enter the key again");

  const refresh = [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Refresh")!;
  refresh.click();
  await vi.waitFor(() =>
    expect([...document.querySelectorAll("dd")].map((n) => n.textContent)).toEqual(["3", "1", "1", "0"]));
  expect(text()).toContain("names the credential store this session is using");
  expect(text()).toContain("does not mean the keys were removed");
  expect(text()).not.toContain("cannot confirm it can reach");
});
