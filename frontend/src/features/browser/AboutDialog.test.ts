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
  ["1.2.3", "a".repeat(40), "clean"],
  ["2.3.4", "b".repeat(40), "clean"],
  ["dev", "c".repeat(40), "dirty"],
  ["dev", "unknown", "unknown"],
])("shows the backend identity %s / %s / %s", async (version, sourceRevision, sourceDirty) => {
  about.mockResolvedValue({ ...await newFakeApi().about(), version, sourceRevision, sourceDirty });
  dialog = mount(AboutDialog, { target: document.body, props: { onClose: () => {} } });
  flushSync();
  await vi.waitFor(() => expect(document.body.textContent).toContain(sourceRevision));
  const lines = [...document.querySelectorAll(".line")].map((line) => line.textContent);
  expect(lines[0]).toContain(`Version ${version}`);
  expect(lines[1]).toContain(`Source ${sourceRevision}`);
  expect(lines[1]).toContain(sourceDirty);
});
