import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { JobDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import JobsDrawer from "./JobsDrawer.svelte";

let drawer: ReturnType<typeof mount> | null = null;

afterEach(async () => {
  if (drawer) await unmount(drawer);
  drawer = null;
  document.body.innerHTML = "";
  vi.useRealTimers();
});

function show(browser: BrowserState) {
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
}

it("shows refused overwrites separately from copied and failed objects", async () => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "partial", type: "copy", state: "partial", source: "source/", destination: "dest/",
    sourceBookmarkId: "a", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
    engine: "native", settings: "", error: "261 overwrites refused; 1 unverified", recoveryRecords: 1,
    counters: { objectsPlanned: 263, objectsCopied: 2, objectsVerified: 1, objectsFailed: 1,
      bytesPlanned: 1000, bytesCopied: 16, overwrites: 0, overwritesRefused: 261, nativeRouted: 1, objectsDeleted: 0 },
  };
  api.jobs = async () => [job];
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("partial"));
  const counts = document.querySelector(".counts")?.textContent;
  expect(counts).toContain("261 overwrites refused");
  expect(counts).toContain("1 failed");
  expect(counts).toMatch(/2\s*\/263/);
});

it("shows global and per-job retained records without changing a completed result", async () => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "finished", type: "copy", state: "complete", source: "source/file", destination: "dest/file",
    sourceBookmarkId: "a", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
    engine: "native", settings: "", error: "", recoveryRecords: 3,
    counters: { objectsPlanned: 1, objectsCopied: 1, objectsVerified: 1, objectsFailed: 0,
      bytesPlanned: 8, bytesCopied: 8, overwrites: 0, overwritesRefused: 0, nativeRouted: 1, objectsDeleted: 0 },
  };
  api.jobs = async () => [job];
  api.recoveryStatus = async () => ({ jobs: 7, records: 21 });
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".recovery")?.textContent)
    .toMatch(/21 recovery records retained across\s+7 finished jobs in all accounts/));
  expect(document.querySelector(".recovery-row")?.textContent)
    .toContain("3 recovery records retained; remote residue is unverified");
  expect(document.querySelector(".badge")?.textContent).toBe("complete");
  expect(document.body.textContent).toContain("Automatic recovery cleanup is unavailable");
  expect(document.body.textContent).toMatch(/records, not\s+objects or failed transfers/);
  expect([...document.querySelectorAll("button")].some((b) => /clear|reclaim|clean up/i.test(b.textContent ?? ""))).toBe(false);
});

it("shows an unavailable read as unknown and refreshes through the drawer poll", async () => {
  vi.useFakeTimers();
  const api = newFakeApi();
  api.recoveryStatus = vi.fn().mockRejectedValueOnce(new Error("private SQLite detail"))
    .mockResolvedValue({ jobs: 2, records: 4 });
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.body.textContent).toContain("Retained counts are unknown"));
  expect(document.body.textContent).not.toContain("0 recovery records");
  expect(document.body.textContent).not.toContain("private SQLite detail");
  await vi.advanceTimersByTimeAsync(1500);
  await vi.waitFor(() => expect(document.querySelector(".recovery")?.textContent)
    .toMatch(/4 recovery records retained across\s+2 finished jobs in all accounts/));
  expect(document.body.textContent).not.toContain("Retained counts are unknown");
});

it("keeps the global notice when no recent jobs carry retained records", async () => {
  const api = newFakeApi();
  api.jobs = async () => [];
  api.recoveryStatus = async () => ({ jobs: 1, records: 2 });
  const browser = new BrowserState(api);
  browser.jobsFilter = "another-account";
  show(browser);
  await vi.waitFor(() => expect(document.querySelector(".recovery")?.textContent)
    .toMatch(/2 recovery records retained across\s+1 finished job in all accounts/));
  expect(document.body.textContent).toContain("No jobs in this view");
});

it("does not describe a successful zero-record read as verified remote cleanup", async () => {
  show(new BrowserState(newFakeApi()));
  await vi.waitFor(() => expect(document.querySelector(".recovery")?.textContent)
    .toMatch(/0 recovery records retained across\s+0 finished jobs in all accounts/));
  expect(document.body.textContent).toContain("Remote residue is unverified");
  expect(document.body.textContent).toContain("Automatic recovery cleanup is unavailable");
});
