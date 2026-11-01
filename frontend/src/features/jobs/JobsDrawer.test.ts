import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { JobDTO, InterruptedDisclosureDTO } from "../../lib/api";
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

it.each([undefined, false, true])("qualifies deletion counters and retains history reasons (flag %s)", async (incomplete) => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "delete-accounting", type: "delete_container", state: "partial", source: "selected/", destination: "(deleted)",
    sourceBookmarkId: "a", destBookmarkId: "", createdAt: "2000-01-01T00:00:00Z", startedAt: "", finishedAt: "",
    engine: "native", settings: "", recoveryRecords: 0,
    error: "Deletion stopped; inspect retained admitted-attempt evidence at the recorded path.",
    counters: { objectsPlanned: 10, objectsCopied: 2, objectsVerified: 2, objectsFailed: 3,
      bytesPlanned: 100, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 0,
      ...(incomplete === undefined ? {} : { deletionAccountingIncomplete: incomplete }) },
  };
  api.jobs = async () => [job];
  api.jobHistory = async () => ({ jobs: [job], nextCursor: "" });
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("partial"));
  expect(document.querySelector(".error")?.textContent).toBe(job.error);
  const counts = document.querySelector(".counts")?.textContent;
  const qualifier = "Final failure and unresolved totals are unavailable";
  if (incomplete) {
    expect(counts).toContain("Last confirmed progress");
    expect(counts).toContain("3 reported failures");
    expect(counts).not.toMatch(/2\s*\/10/);
    expect(counts).not.toContain("3 failed");
    expect(document.body.textContent).toContain(qualifier);
  } else {
    expect(counts).toMatch(/2\s*\/10/);
    expect(counts).toContain("3 failed");
    expect(document.body.textContent).not.toContain(qualifier);
  }
  expect(document.querySelector(".progress")).toBeNull();
  document.querySelector<HTMLButtonElement>(`[aria-label="Item details for job ${job.id}"]`)!.click();
  flushSync();
  expect(document.querySelector(".config")?.textContent?.includes("verified")).toBe(!incomplete);
  [...document.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find((button) => button.textContent?.trim() === "Job history")!.click();
  flushSync();
  await vi.waitFor(() => expect(document.querySelectorAll(".history-job")).toHaveLength(1));
  expect(document.querySelector(".history-job")?.textContent).toContain(job.error);
  expect(document.querySelector(".history-job")?.textContent?.includes(qualifier)).toBe(Boolean(incomplete));
});

it.each([false, true])("shows unavailable status over a stale active count (%s)", async (stale) => {
  const api = newFakeApi();
  api.jobs = async () => { throw new Error("final result could not be saved; inspect retained data before retrying"); };
  const browser = new BrowserState(api);
  if (stale) {
    browser.jobs = [{ id: "stale", state: "running", type: "upload", source: "local", destination: "dest/",
      sourceBookmarkId: "", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
      engine: "rclone+native", settings: "", error: "", recoveryRecords: 1,
      counters: { objectsPlanned: 1, objectsCopied: 0, objectsVerified: 0, objectsFailed: 0,
        bytesPlanned: 8, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 0 },
    }];
  }
  show(browser);
  await vi.waitFor(() => expect(document.querySelector(".bar")?.textContent).toContain("Job status unavailable"));
  expect(document.querySelector(".bar")?.textContent).not.toContain("No active transfers");
  expect(document.querySelector(".bar")?.textContent).not.toContain("1 active job");
  expect(document.querySelector(".rows")).toBeNull();
  expect(document.body.textContent).toContain("inspect retained data before retrying");
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  expect(document.querySelector(".bar")?.textContent).toContain("Job status unavailable");
});

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

it("shows upload cleanup issues without inventing a failed transfer", async () => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "upload", type: "upload", state: "partial", source: "1 local file", destination: "dest/live/",
    sourceBookmarkId: "", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
    engine: "rclone+native", settings: "", recoveryRecords: 4,
    error: "1 post-commit issue (first: live/file: staging-cleanup: delete staging object failed)",
    counters: { objectsPlanned: 1, objectsCopied: 1, objectsVerified: 1, objectsFailed: 0,
      bytesPlanned: 8, bytesCopied: 8, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 0 },
  };
  api.jobs = async () => [job];
  api.recoveryStatus = async () => ({ jobs: 1, records: 4 });
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("partial"));
  expect(document.querySelector("p.error")?.textContent).toBe(job.error);
  expect(document.querySelector(".counts")?.textContent).toMatch(/1\s*\/1/);
  expect(document.querySelector(".counts")?.textContent).not.toContain("failed");
  expect(document.querySelector(".recovery-row")?.textContent)
    .toContain("4 recovery records retained; remote residue is unverified");
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

it("shows interrupted-job detail and dismisses one through the drawer", async () => {
  const api = newFakeApi();
  const entry: InterruptedDisclosureDTO = {
    jobId: "j1", type: "download", source: "acc/src", destination: "/home/me/dl",
    createdAt: "2026-10-19T00:00:00Z", interruptedAt: "2026-10-19T00:00:01Z",
    interruptedState: "running", attempted: true, itemsCaptured: true,
    items: { items: 3, admitted: 3, copied: 1, verified: 0, copiedBytes: 4096, failed: 1, conflicts: 0, refused: 0, unresolved: 2, unverified: 0, deleted: 0, unsettled: 0 },
    recoveryRecords: 0, inconclusiveCommits: 0, placesLocalResidue: true,
  };
  api.interruptedDisclosures = vi.fn()
    .mockResolvedValueOnce({ entries: [entry], total: 1 })
    .mockResolvedValue({ entries: [], total: 0 });
  api.dismissInterrupted = vi.fn().mockResolvedValue({ dismissed: 1 });
  const browser = new BrowserState(api);
  await browser.loadInterruptedDisclosures();
  show(browser);

  const section = document.querySelector(".interrupted")!;
  expect(section).not.toBeNull();
  // Admission, item facts, and the two residue facets are reported honestly.
  expect(section.textContent).toContain("Work had begun when the run ended");
  expect(section.textContent).toContain("3 recorded members");
  expect(section.textContent).toContain("1 copied");
  expect(section.textContent).toContain("1 failed");
  expect(section.textContent).toContain("2 unresolved");
  expect(section.textContent).toContain("keeps no recovery records");
  expect(section.textContent).toContain("Files may remain");

  const dismiss = [...section.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Dismiss")!;
  dismiss.click();
  await vi.waitFor(() => expect(api.dismissInterrupted).toHaveBeenCalledWith(["j1"]));
  // The reload shows nothing left, so the section goes away.
  await vi.waitFor(() => expect(document.querySelector(".interrupted")).toBeNull());
});

it("does not claim no request was made for a queued bulk-delete interruption", async () => {
  const api = newFakeApi();
  const entry: InterruptedDisclosureDTO = {
    jobId: "q1", type: "empty_container", source: "acc/c", destination: "(emptied)",
    createdAt: "2026-10-19T00:00:00Z", interruptedAt: "2026-10-19T00:00:01Z",
    interruptedState: "queued", attempted: false, itemsCaptured: true,
    items: { items: 0, admitted: 0, copied: 0, verified: 0, copiedBytes: 0, failed: 0, conflicts: 0, refused: 0, unresolved: 0, unverified: 0, deleted: 0, unsettled: 0 },
    recoveryRecords: 0, inconclusiveCommits: 0, placesLocalResidue: false,
  };
  api.interruptedDisclosures = vi.fn().mockResolvedValue({ entries: [entry], total: 1 });
  const browser = new BrowserState(api);
  await browser.loadInterruptedDisclosures();
  show(browser);
  const section = document.querySelector(".interrupted")!;
  // A sweep makes listing and container requests that are not member steps, so
  // absent item evidence must not be stated as "no request was made".
  expect(section.textContent).toContain("Nothing is recorded as attempted");
  expect(section.textContent).not.toContain("no request was made");
  expect(section.textContent).not.toContain("Never started");
});
