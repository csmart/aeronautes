import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import AboutDialog from "./AboutDialog.svelte";
import { newFakeApi } from "./fakeApi";

const about = vi.hoisted(() => vi.fn());
vi.mock("../../lib/api", () => ({ liveApi: { about } }));

let dialog: ReturnType<typeof mount> | null = null;
afterEach(async () => {
  if (dialog) await unmount(dialog);
  dialog = null;
  document.body.innerHTML = "";
});

it.each([
  ["1.2.3", "", "a".repeat(40), "clean", "Version 1.2.3 (aaaaaaaaaaaa)"],
  ["dev", "1.2.3", "b".repeat(40), "clean", "Version 1.2.3 (dev bbbbbbbbbbbb)"],
  ["dev", "", "c".repeat(40), "dirty", "Version dev (cccccccccccc, modified)"],
  ["dev", "", "d".repeat(40), "unknown", "Version dev (dddddddddddd, may be modified)"],
  ["dev", "", "unknown", "unknown", "Version dev (source unknown)"],
])("shows the version %s, developed from %s, with its source %s / %s", async (version, developedFrom, sourceRevision, sourceDirty, shown) => {
  about.mockResolvedValue({ ...await newFakeApi().about(), version, developedFrom, sourceRevision, sourceDirty });
  dialog = mount(AboutDialog, { target: document.body, props: { onClose: () => {} } });
  flushSync();
  await vi.waitFor(() => expect(document.querySelector(".version")).not.toBeNull());
  expect(document.querySelector(".version")!.textContent!.replace(/\s+/g, " ").trim()).toBe(shown);
});
