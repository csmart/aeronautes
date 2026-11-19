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
    engine: "native", settings: "", recoveryRecords: 0, pause: "",
    error: "Deletion stopped; inspect retained admitted-attempt evidence at the recorded path.",
    counters: { objectsPlanned: 10, objectsCopied: 2, objectsVerified: 2, objectsFailed: 3,
      bytesPlanned: 100, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, objectsSame: 0, nativeRouted: 0, objectsDeleted: 0,
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
    browser.jobs.list = [{ id: "stale", state: "running", type: "upload", source: "local", destination: "dest/",
      sourceBookmarkId: "", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
      engine: "rclone+native", settings: "", error: "", recoveryRecords: 1, pause: "",
      counters: { objectsPlanned: 1, objectsCopied: 0, objectsVerified: 0, objectsFailed: 0,
        bytesPlanned: 8, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, objectsSame: 0, nativeRouted: 0, objectsDeleted: 0 },
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
    engine: "native", settings: "", error: "261 overwrites refused; 1 unverified", recoveryRecords: 1, pause: "",
    counters: { objectsPlanned: 263, objectsCopied: 2, objectsVerified: 1, objectsFailed: 1,
      bytesPlanned: 1000, bytesCopied: 16, overwrites: 0, overwritesRefused: 261, objectsSame: 0, nativeRouted: 1, objectsDeleted: 0 },
  };
  api.jobs = async () => [job];
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("partial"));
  const counts = document.querySelector(".counts")?.textContent;
  expect(counts).toContain("261 overwrites refused");
  expect(counts).toContain("1 failed");
  expect(counts).toMatch(/2\s*\/263/);
});

// A confirmed download or upload leaves what is already the same, and the
// row says how many, so a complete job that copied fewer than it planned
// reads as what happened (ADR 0122 decision 12).
it("counts the files a job left because they were already the same", async () => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "same", type: "download", state: "complete", source: "media/", destination: "local directory (picker-selected)",
    sourceBookmarkId: "a", destBookmarkId: "", createdAt: "", startedAt: "", finishedAt: "",
    engine: "rclone+native", settings: "", error: "", recoveryRecords: 0, pause: "",
    counters: { objectsPlanned: 5, objectsCopied: 2, objectsVerified: 5, objectsFailed: 0,
      bytesPlanned: 50, bytesCopied: 20, overwrites: 1, overwritesRefused: 0, objectsSame: 3, nativeRouted: 0, objectsDeleted: 0 },
  };
  api.jobs = async () => [job];
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".counts")?.textContent).toContain("3 already the same"));
  expect(document.querySelector(".counts")?.textContent).toMatch(/2\s*\/5/);
});

// ADR 0129: a job stopped after something landed is stored partial with the
// names it stopped counted incomplete, and reads as cancelled and incomplete,
// not as a failure; a real failure beside them still reads failed.
it("words a job stopped part-way as cancelled and incomplete", async () => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "stopped", type: "upload", state: "partial", source: "3 local files from /home/you", destination: "media/",
    sourceBookmarkId: "", destBookmarkId: "a", createdAt: "", startedAt: "", finishedAt: "",
    engine: "rclone+native", settings: "", error: "", recoveryRecords: 0, pause: "",
    counters: { objectsPlanned: 3, objectsCopied: 2, objectsVerified: 2, objectsFailed: 0, objectsIncomplete: 1,
      bytesPlanned: 30, bytesCopied: 20, overwrites: 0, overwritesRefused: 0, objectsSame: 0, nativeRouted: 0, objectsDeleted: 0 },
  };
  const broken: JobDTO = { ...job, id: "broken", counters: { ...job.counters, objectsFailed: 1, objectsIncomplete: 0 } };
  api.jobs = async () => [job, broken];
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelectorAll(".badge.partial")).toHaveLength(2));
  const [stopped, failed] = [...document.querySelectorAll(".job")];
  expect(stopped.querySelector(".badge")?.textContent).toBe("cancelled, incomplete");
  expect(stopped.querySelector(".counts")?.textContent).toContain("1 incomplete");
  expect(stopped.querySelector(".counts")?.textContent).not.toContain("failed");
  expect(failed.querySelector(".badge")?.textContent).toBe("partial");
  expect(failed.querySelector(".counts")?.textContent).toContain("1 failed");
});

it("shows global and per-job retained records without changing a completed result", async () => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "finished", type: "copy", state: "complete", source: "source/file", destination: "dest/file",
    sourceBookmarkId: "a", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
    engine: "native", settings: "", error: "", recoveryRecords: 3, pause: "",
    counters: { objectsPlanned: 1, objectsCopied: 1, objectsVerified: 1, objectsFailed: 0,
      bytesPlanned: 8, bytesCopied: 8, overwrites: 0, overwritesRefused: 0, objectsSame: 0, nativeRouted: 1, objectsDeleted: 0 },
  };
  api.jobs = async () => [job];
  api.recoveryStatus = async () => ({ jobs: 7, records: 21 });
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".recovery")?.textContent)
    .toMatch(/21 recovery records kept for\s+7 finished jobs in all accounts/));
  expect(document.querySelector(".recovery")?.textContent).toMatch(/what\s+they track on the store is unchecked/);
  // The row names its count as records, never a bare number beside the
  // deleted or failed counts (ADR 0020), and its details give the sentence.
  expect(document.querySelector(".counts .recovery-tag")?.textContent).toBe("3 recovery records");
  document.querySelector<HTMLButtonElement>(`[aria-label="Item details for job ${job.id}"]`)!.click();
  flushSync();
  expect(document.querySelector(".recovery-row")?.textContent)
    .toContain("3 recovery records kept; what they track on the store is unchecked");
  expect(document.querySelector(".badge")?.textContent).toBe("complete");
  expect(document.body.textContent).toContain("Automatic recovery cleanup is unavailable");
  expect(document.body.textContent).toMatch(/These count records, not objects, bytes, proven orphans or failed transfers/);
  expect([...document.querySelectorAll("button")].some((b) => /clear|reclaim|clean up/i.test(b.textContent ?? ""))).toBe(false);
});

it("shows upload cleanup issues without inventing a failed transfer", async () => {
  const api = newFakeApi();
  const job: JobDTO = {
    id: "upload", type: "upload", state: "partial", source: "1 local file", destination: "dest/live/",
    sourceBookmarkId: "", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
    engine: "rclone+native", settings: "", recoveryRecords: 4, pause: "",
    error: "1 post-commit issue (first: live/file: staging-cleanup: delete staging object failed)",
    counters: { objectsPlanned: 1, objectsCopied: 1, objectsVerified: 1, objectsFailed: 0,
      bytesPlanned: 8, bytesCopied: 8, overwrites: 0, overwritesRefused: 0, objectsSame: 0, nativeRouted: 0, objectsDeleted: 0 },
  };
  api.jobs = async () => [job];
  api.recoveryStatus = async () => ({ jobs: 1, records: 4 });
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("partial"));
  expect(document.querySelector("p.error")?.textContent).toBe(job.error);
  expect(document.querySelector(".counts")?.textContent).toMatch(/1\s*\/1/);
  expect(document.querySelector(".counts")?.textContent).not.toContain("failed");
  expect(document.querySelector(".counts .recovery-tag")?.textContent).toBe("4 recovery records");
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
    .toMatch(/4 recovery records kept for\s+2 finished jobs in all accounts/));
  expect(document.body.textContent).not.toContain("Retained counts are unknown");
});

it("keeps the global notice when no recent jobs carry retained records", async () => {
  const api = newFakeApi();
  api.jobs = async () => [];
  api.recoveryStatus = async () => ({ jobs: 1, records: 2 });
  const browser = new BrowserState(api);
  browser.jobs.bookmarkFilter = "another-account";
  show(browser);
  await vi.waitFor(() => expect(document.querySelector(".recovery")?.textContent)
    .toMatch(/2 recovery records kept for\s+1 finished job in all accounts/));
  expect(document.body.textContent).toContain("No jobs in this view");
});

it("does not describe a successful zero-record read as verified remote cleanup", async () => {
  show(new BrowserState(newFakeApi()));
  await vi.waitFor(() => expect(document.querySelector(".recovery")?.textContent)
    .toMatch(/0 recovery records kept for\s+0 finished jobs in all accounts/));
  expect(document.querySelector(".recovery")?.textContent).toMatch(/what\s+they track on the store is unchecked/);
  expect(document.body.textContent).toContain("Automatic recovery cleanup is unavailable");
});

it("shows interrupted-job detail and dismisses one through the drawer", async () => {
  const api = newFakeApi();
  const entry: InterruptedDisclosureDTO = {
    jobId: "j1", type: "download", source: "acc/src", destination: "/home/me/dl",
    createdAt: "2026-10-19T00:00:00Z", interruptedAt: "2026-10-19T00:00:01Z",
    interruptedState: "running", attempted: true, itemsCaptured: true,
    items: { items: 3, admitted: 3, copied: 1, verified: 0, copiedBytes: 4096, failed: 1, conflicts: 0, refused: 0, unresolved: 2, unverified: 0, deleted: 0, unsettled: 0 },
    recoveryRecords: 0, placesLocalResidue: true,
  };
  api.interruptedDisclosures = vi.fn()
    .mockResolvedValueOnce({ entries: [entry], total: 1 })
    .mockResolvedValue({ entries: [], total: 0 });
  api.dismissInterrupted = vi.fn().mockResolvedValue({ dismissed: 1 });
  const browser = new BrowserState(api);
  await browser.jobs.loadInterrupted();
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
    recoveryRecords: 0, placesLocalResidue: false,
  };
  api.interruptedDisclosures = vi.fn().mockResolvedValue({ entries: [entry], total: 1 });
  const browser = new BrowserState(api);
  await browser.jobs.loadInterrupted();
  show(browser);
  const section = document.querySelector(".interrupted")!;
  // A sweep makes listing and container requests that are not member steps, so
  // absent item evidence must not be stated as "no request was made".
  expect(section.textContent).toContain("Nothing is recorded as attempted");
  expect(section.textContent).not.toContain("no request was made");
  expect(section.textContent).not.toContain("Never started");
});

function transferJob(over: Partial<JobDTO>): JobDTO {
  return {
    id: "transfer", type: "upload", state: "running", source: "local", destination: "dest/",
    sourceBookmarkId: "", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
    engine: "rclone", settings: "", error: "", recoveryRecords: 0, pause: "",
    counters: { objectsPlanned: 10, objectsCopied: 4, objectsVerified: 4, objectsFailed: 0,
      bytesPlanned: 100, bytesCopied: 40, overwrites: 0, overwritesRefused: 0, objectsSame: 0, nativeRouted: 0, objectsDeleted: 0 },
    ...over,
  };
}

function rowButtons(): string[] {
  return [...document.querySelectorAll<HTMLButtonElement>(".row-actions button:not(.details)")]
    .map((button) => button.textContent!.trim());
}

// ADR 0104 decision 6: what each state offers and what the row says while a
// pause or a resume is waiting. A deletion job and a finishing job never pause.
it.each([
  { name: "a running upload", job: { }, buttons: ["Pause", "Cancel"], note: null },
  { name: "a queued download", job: { type: "download", state: "queued" }, buttons: ["Pause", "Cancel"], note: null },
  { name: "a pause waiting for its next batch", job: { pause: "pausing" }, buttons: ["Resume", "Cancel"],
    note: "Pauses when the current batch finishes, unless it is the last." },
  { name: "a pause asked while queued", job: { state: "queued", pause: "pausing" }, buttons: ["Resume", "Cancel"],
    note: "Pauses before it transfers anything." },
  // A finishing job reaches no further unit, so the pause has nothing to
  // wait for and is not offered back.
  { name: "a pause asked before the job began verifying", job: { state: "verifying", pause: "pausing" }, buttons: ["Cancel"], note: null },
  { name: "a paused copy", job: { type: "copy", state: "paused" }, buttons: ["Resume", "Cancel"],
    note: "Paused. Nothing more is transferred until it is resumed." },
  { name: "a resume waiting for a slot", job: { state: "paused", pause: "resuming" }, buttons: ["Cancel"],
    note: "Resumes when a job slot is free." },
  { name: "a verifying upload", job: { state: "verifying" }, buttons: ["Cancel"], note: null },
  { name: "a running deletion", job: { type: "delete_prefix" }, buttons: ["Cancel"], note: null },
])("offers $name the controls its state allows", async ({ job, buttons, note }) => {
  const api = newFakeApi();
  api.jobs = async () => [transferJob(job)];
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".badge")).not.toBeNull());
  expect(rowButtons()).toEqual(buttons);
  expect(document.querySelector(".pause-note")?.textContent ?? null).toBe(note);
  // A paused job keeps what it had done on the row.
  expect(document.querySelector(".counts")?.textContent).toMatch(/4\s*\/10/);
});

it("pauses and resumes a job through the bridge and reports a refusal", async () => {
  const api = newFakeApi();
  let current = transferJob({});
  api.jobs = async () => [current];
  api.pauseJob = vi.fn(async () => { current = transferJob({ state: "paused" }); });
  api.resumeJob = vi.fn(async () => { throw new Error("job transfer is not paused in this process and cannot be resumed"); });
  show(new BrowserState(api));
  await vi.waitFor(() => expect(rowButtons()).toEqual(["Pause", "Cancel"]));

  const control = document.querySelector<HTMLButtonElement>(".row-actions .pause-resume")!;
  expect(control.getAttribute("aria-label")).toBe("Pause job transfer");
  control.focus();
  control.click();
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("paused"));
  expect(api.pauseJob).toHaveBeenCalledExactlyOnceWith("transfer");
  // The same button now resumes, and keeps the keyboard focus.
  expect(document.querySelector(".row-actions .pause-resume")).toBe(control);
  expect(control.getAttribute("aria-label")).toBe("Resume job transfer");
  expect(document.activeElement).toBe(control);

  control.click();
  await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent)
    .toBe("Upload local → dest/: job transfer is not paused in this process and cannot be resumed"));
  expect(api.resumeJob).toHaveBeenCalledExactlyOnceWith("transfer");
  // The refusal changed nothing, so the row still offers the resume.
  expect(rowButtons()).toEqual(["Resume", "Cancel"]);
});

// One request per job at a time, and only per job: each job's control waits
// for its own request, a second press while it waits sends nothing, and
// another job's answer does not release it. The control stays focusable
// while it waits, marked busy rather than disabled, because disabling the
// focused button drops the keyboard focus in WebKitGTK.
it("keeps each job's pause request apart", async () => {
  const api = newFakeApi();
  api.jobs = async () => [transferJob({ id: "first" }), transferJob({ id: "second", type: "download" })];
  const answers = new Map<string, () => void>();
  api.pauseJob = vi.fn((id: string) => new Promise<void>((done) => { answers.set(id, done); }));
  show(new BrowserState(api));
  const control = (id: string) => document.querySelector<HTMLButtonElement>(`.row-actions .pause-resume[aria-label="Pause job ${id}"]`);
  const busy = (id: string) => control(id)?.getAttribute("aria-disabled") === "true";
  await vi.waitFor(() => expect(control("second")).not.toBeNull());
  control("first")!.focus();
  control("first")!.click();
  await vi.waitFor(() => expect(busy("first")).toBe(true));
  expect(control("first")!.disabled, "STAYS-FOCUSABLE").toBe(false);
  expect(document.activeElement).toBe(control("first"));
  control("first")!.click();
  expect(api.pauseJob, "NO-SECOND-REQUEST").toHaveBeenCalledTimes(1);
  expect(busy("second")).toBe(false);
  control("second")!.click();
  await vi.waitFor(() => expect(busy("second")).toBe(true));
  answers.get("second")!();
  await vi.waitFor(() => expect(busy("second")).toBe(false));
  expect(busy("first"), "FIRST-STILL-WAITING").toBe(true);
  answers.get("first")!();
  await vi.waitFor(() => expect(busy("first")).toBe(false));
  expect(api.pauseJob).toHaveBeenCalledTimes(2);
});

// A dialog's "Show in jobs" opens the drawer on the started job, expanded,
// and drops an account filter that would have hidden it.
it("opens on a revealed job and clears a filter that hides it", async () => {
  const api = newFakeApi();
  const job = transferJob({ id: "started-job", destBookmarkId: "b" });
  api.jobs = async (filter: string) => (filter === "" || filter === "b" ? [job] : []);
  const browser = new BrowserState(api);
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  browser.jobs.bookmarkFilter = "other";
  await browser.jobs.load();
  expect(browser.jobs.list).toHaveLength(0);

  await browser.jobs.reveal("started-job");
  flushSync();
  expect(browser.jobs.bookmarkFilter).toBe("");
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("running"));
  expect(document.querySelector('[role="tab"][aria-selected="true"]')?.textContent?.trim()).toBe("Jobs");
  expect(document.querySelector(`[aria-label="Item details for job started-job"]`)?.getAttribute("aria-expanded")).toBe("true");
});

// A failed job's error is one line in the row; its details hold the whole
// text and "Copy details" puts everything the row shows on the clipboard.
it("shows the whole error in the details and copies the job's details", async () => {
  const api = newFakeApi();
  const job = transferJob({
    id: "failed-copy", type: "copy", state: "partial", source: "testing/", destination: "testing/",
    settings: JSON.stringify({ transfers: 8 }),
    error: "execution failed (example: files/test.file0: engine exit 1: HTTP Error: 502: 502 Bad Gateway)",
  });
  api.jobs = async () => [job];
  const copy = vi.spyOn(api, "copyText");
  show(new BrowserState(api));
  await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe("partial"));
  expect(document.querySelector("p.error.one-line")?.getAttribute("title")).toBe(job.error);
  document.querySelector<HTMLButtonElement>(`[aria-label="Item details for job ${job.id}"]`)!.click();
  flushSync();
  expect(document.querySelector(".full-error")?.textContent).toBe(job.error);
  // Open details replace the one-line error rather than repeat it.
  expect(document.querySelector("p.error.one-line")).toBeNull();
  [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Copy details")!.click();
  await vi.waitFor(() => expect(document.querySelector(".copy-details [role=status]")?.textContent).toBe("Copied."));
  const text = copy.mock.calls[0][0];
  expect(text).toContain("(job failed-copy)");
  expect(text).toContain("testing/ → testing/");
  expect(text).toContain(`error: ${job.error}`);
  expect(text).toMatch(/settings: .*transfers=8/);
});
