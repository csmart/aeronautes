import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { JobDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import JobsDrawer from "./JobsDrawer.svelte";

let drawer: ReturnType<typeof mount> | null = null;
const releasePending: (() => void)[] = [];

afterEach(async () => {
  if (drawer) await unmount(drawer);
  drawer = null;
  for (const release of releasePending.splice(0)) release();
  await Promise.resolve();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

function deferred<T>(fallback: T) {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  releasePending.push(() => resolve(fallback));
  return { promise, resolve, reject };
}

function job(id = "job-a", state = "running"): JobDTO {
  return {
    id, type: "copy", state, source: "source/file", destination: "dest/file",
    sourceBookmarkId: "a", destBookmarkId: "b", createdAt: "", startedAt: "", finishedAt: "",
    engine: "native", settings: "", error: "", recoveryRecords: 0, pause: "",
    counters: { objectsPlanned: 1, objectsCopied: 0, objectsVerified: 0, objectsFailed: 0,
      bytesPlanned: 8, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, objectsSame: 0,
      nativeRouted: 0, objectsDeleted: 0 },
  };
}

async function show(browser: BrowserState) {
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  await vi.waitFor(() => expect(document.querySelectorAll(".job").length).toBeGreaterThan(0));
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((node) => (node.getAttribute("aria-label") ?? node.textContent?.trim()) === label);
  expect(found, `button ${label}`).toBeDefined();
  return found!;
}

function cancelButton(jobId = "job-a"): HTMLButtonElement {
  const row = [...document.querySelectorAll(".job")].find((node) =>
    node.querySelector(".details")?.getAttribute("aria-label") === `Item details for job ${jobId}`);
  const found = row?.querySelector<HTMLButtonElement>(".cancel");
  expect(found, `Cancel for ${jobId}`).toBeTruthy();
  return found!;
}

function notice(jobId = "job-a") {
  return [...document.querySelectorAll(".cancellation-notice")]
    .find((node) => node.getAttribute("aria-label") === `Cancellation request for job ${jobId}`);
}

async function expectUnconfirmed(jobId = "job-a") {
  await vi.waitFor(() => expect(notice(jobId)?.querySelector('[role="alert"]')?.textContent ?? "")
    .toContain("Cancellation could not be confirmed"));
}

async function expectRequested(jobId = "job-a") {
  await vi.waitFor(() => expect(notice(jobId)?.querySelector('[role="status"]')?.textContent ?? "")
    .toContain("Cancellation requested."));
}

it.each(["cancelled", "partial", "complete", "failed"])(
  "requests the exact job and preserves the provider's %s outcome", async (terminal) => {
    const api = newFakeApi();
    let current = job();
    api.jobs = vi.fn(async () => [current]);
    api.cancelJob = vi.fn(async () => { current = job("job-a", terminal); });
    const browser = new BrowserState(api);
    await show(browser);
    cancelButton().click();
    await vi.waitFor(() => expect(document.querySelector(".badge")?.textContent).toBe(terminal));
    expect(api.cancelJob).toHaveBeenCalledExactlyOnceWith("job-a");
    expect(browser.jobs.list[0].counters).toEqual(current.counters);
    expect(document.querySelector(".cancel")).toBeNull();
    await expectRequested();
    button("Dismiss cancellation notice for job job-a").click();
    flushSync();
    expect(notice()).toBeUndefined();
    expect(browser.jobs.list[0].state).toBe(terminal);
    expect(api.cancelJob).toHaveBeenCalledTimes(1);
  },
);

it("guards rapid duplicate clicks and keeps another job independently cancellable", async () => {
  const api = newFakeApi();
  const first = deferred<void>(undefined);
  const second = deferred<void>(undefined);
  api.jobs = vi.fn(async () => [job(), job("job-b")]);
  api.cancelJob = vi.fn((id) => id === "job-a" ? first.promise : second.promise);
  await show(new BrowserState(api));
  const cancel = cancelButton();
  cancel.click();
  cancel.click();
  flushSync();
  expect(api.cancelJob).toHaveBeenCalledExactlyOnceWith("job-a");
  expect(cancel.disabled).toBe(true);
  expect(notice()?.textContent).toContain("Requesting cancellation");
  expect(notice()?.querySelector(".dismiss-cancellation")).toBeNull();
  expect(cancelButton("job-b").disabled).toBe(false);
  cancelButton("job-b").click();
  flushSync();
  expect(vi.mocked(api.cancelJob).mock.calls).toEqual([["job-a"], ["job-b"]]);
  expect(cancelButton("job-b").disabled).toBe(true);
  first.resolve();
  await expectRequested();
  expect(notice("job-b")?.textContent).toContain("Requesting cancellation");
  second.resolve();
  await expectRequested("job-b");
});

it.each([false, true])("acknowledges a request without inventing a terminal result (read fails %s)", async (readFails) => {
  const api = newFakeApi();
  api.jobs = vi.fn().mockResolvedValueOnce([job()]).mockImplementation(async () => {
    if (readFails) throw new Error("owned status read failure");
    return [job()];
  });
  api.cancelJob = vi.fn(async () => {});
  const browser = new BrowserState(api);
  await show(browser);
  cancelButton().click();
  await expectRequested();
  expect(browser.jobs.list[0].state).toBe("running");
  expect(browser.jobs.list[0].counters.objectsCopied).toBe(0);
  expect(browser.jobs.list[0].counters.objectsVerified).toBe(0);
  expect(notice()?.textContent).toContain("The job's reported state determines its outcome");
  expect(notice()?.textContent).not.toContain("cancelled");
  expect(Boolean(browser.jobs.error)).toBe(readFails);
  expect(api.cancelJob).toHaveBeenCalledExactlyOnceWith("job-a");
  if (!readFails) expect(cancelButton().disabled).toBe(true);
});

it("keeps the request pending while its existing status refresh is unresolved", async () => {
  const api = newFakeApi();
  const refresh = deferred<JobDTO[]>([job()]);
  api.jobs = vi.fn().mockResolvedValueOnce([job()]).mockReturnValue(refresh.promise);
  api.cancelJob = vi.fn(async () => {});
  await show(new BrowserState(api));
  cancelButton().click();
  await vi.waitFor(() => expect(api.jobs).toHaveBeenCalledTimes(2));
  expect(notice()?.textContent).toContain("waiting for the request and status refresh");
  expect(cancelButton().disabled).toBe(true);
  refresh.resolve([job()]);
  await expectRequested();
  expect(document.querySelector(".badge")?.textContent).toBe("running");
});

it.each([false, true])("retains rejected cancellation feedback through a status refresh (read fails %s)", async (readFails) => {
  const api = newFakeApi();
  api.jobs = vi.fn().mockResolvedValueOnce([job()]).mockImplementation(async () => {
    if (readFails) throw new Error("owned status read failure");
    return [job()];
  });
  api.cancelJob = vi.fn(async () => { throw new Error("token=private-cancellation-detail"); });
  const browser = new BrowserState(api);
  await show(browser);
  cancelButton().click();
  await vi.waitFor(() => expect(api.jobs).toHaveBeenCalledTimes(2));
  await expectUnconfirmed();
  expect(api.cancelJob).toHaveBeenCalledExactlyOnceWith("job-a");
  expect(browser.jobs.list[0].state).toBe("running");
  expect(notice()?.textContent).toContain("The job may still be running");
  expect(document.body.textContent).not.toContain("private-cancellation-detail");
  expect(Boolean(browser.jobs.error)).toBe(readFails);
  await browser.jobs.load();
  flushSync();
  expect(notice()?.querySelector('[role="alert"]')?.textContent).toContain("Cancellation could not be confirmed");
  expect(api.cancelJob).toHaveBeenCalledTimes(1);
});

it("refreshes read-only and retries an unconfirmed request only on an explicit action", async () => {
  const api = newFakeApi();
  api.jobs = vi.fn(async () => [job()]);
  api.cancelJob = vi.fn().mockRejectedValueOnce(new Error("owned refusal")).mockResolvedValue(undefined);
  await show(new BrowserState(api));
  cancelButton().click();
  await expectUnconfirmed();
  button("Refresh job status").click();
  await vi.waitFor(() => expect(api.jobs).toHaveBeenCalledTimes(3));
  expect(api.cancelJob).toHaveBeenCalledTimes(1);
  expect(notice()?.textContent).toContain("Cancellation could not be confirmed");
  button("Retry cancellation for job job-a").click();
  await expectRequested();
  expect(vi.mocked(api.cancelJob).mock.calls).toEqual([["job-a"], ["job-a"]]);
  expect(document.querySelectorAll(".cancellation-notice")).toHaveLength(1);
  expect(notice()?.querySelector('[role="alert"]')).toBeNull();
});

it.each(["absent", "terminal", "unavailable"])("does not offer retry when the job is %s", async (state) => {
  const api = newFakeApi();
  api.jobs = vi.fn().mockResolvedValueOnce([job()]).mockImplementation(async () => {
    if (state === "unavailable") throw new Error("owned status read failure");
    return state === "terminal" ? [job("job-a", "partial")] : [];
  });
  api.cancelJob = vi.fn(async () => { throw new Error("owned refusal"); });
  await show(new BrowserState(api));
  cancelButton().click();
  await expectUnconfirmed();
  await vi.waitFor(() => expect(api.jobs).toHaveBeenCalledTimes(2));
  flushSync();
  expect(notice()?.querySelector(".retry-cancellation")).toBeNull();
  expect(notice()?.textContent).toContain("Refresh status or select the job's account before retrying");
  expect(api.cancelJob).toHaveBeenCalledTimes(1);
});

it("retains attribution across account filters, tabs, collapsed views and a missing row", async () => {
  const api = newFakeApi();
  const pending = deferred<void>(undefined);
  api.jobs = vi.fn(async (filter) => filter === "" ? [job()] : []);
  api.cancelJob = vi.fn(() => pending.promise);
  const browser = new BrowserState(api);
  await show(browser);
  cancelButton().click();
  browser.jobs.bookmarkFilter = "another-account";
  await browser.jobs.load();
  flushSync();
  expect(document.body.textContent).toContain("No jobs in this view");
  expect(notice()).toBeDefined();
  expect(notice()?.textContent).toContain("Requesting cancellation");
  button("Recent changes").click();
  flushSync();
  expect(notice()?.textContent).toContain("job-a");
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  expect(document.querySelector(".bar")?.textContent).toContain("1 cancellation notice");
  pending.reject(new Error("owned refusal after filter change"));
  await pending.promise.catch(() => {});
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  await expectUnconfirmed();
  expect(notice()?.textContent).not.toContain("another-account");
  expect(document.querySelector(".job")).toBeNull();
  expect(api.cancelJob).toHaveBeenCalledExactlyOnceWith("job-a");
});

it("uses the admitted literal job ID when a pending row is replaced", async () => {
  const api = newFakeApi();
  const pending = deferred<void>(undefined);
  const literalId = 'job-"<script>owned</script>\n';
  let current = job(literalId);
  api.jobs = vi.fn(async () => [current]);
  api.cancelJob = vi.fn(() => pending.promise);
  const browser = new BrowserState(api);
  await show(browser);
  cancelButton(literalId).click();
  browser.jobs.list[0].id = "replacement-job";
  current = job("replacement-job");
  flushSync();
  pending.resolve();
  await expectRequested(literalId);
  expect(notice(literalId)?.textContent).toContain(literalId);
  expect(notice("replacement-job")).toBeUndefined();
  expect(document.querySelector("script")).toBeNull();
  expect(api.cancelJob).toHaveBeenCalledExactlyOnceWith(literalId);
});

it.each(["requested", "unconfirmed"])("bounds %s notices without silent eviction and frees only an explicitly dismissed slot", async (state) => {
  const api = newFakeApi();
  let current = Array.from({ length: 50 }, (_, i) => job(`job-${i}`));
  api.jobs = vi.fn(async () => current);
  api.cancelJob = vi.fn(async () => {
    if (state === "unconfirmed") throw new Error("owned refusal");
  });
  const browser = new BrowserState(api);
  await show(browser);
  for (const item of current) cancelButton(item.id).click();
  if (state === "requested") await expectRequested("job-49");
  else await expectUnconfirmed("job-49");
  expect(document.querySelectorAll(".cancellation-notice")).toHaveLength(50);
  current = [job("job-0"), job("new-job")];
  await browser.jobs.load();
  flushSync();
  expect(cancelButton("new-job").disabled).toBe(true);
  expect(document.body.textContent).toContain("Cancellation notice limit reached");
  expect(notice("job-49")).toBeDefined();
  if (state === "unconfirmed") {
    button("Retry cancellation for job job-0").click();
    await expectUnconfirmed("job-0");
    expect(document.querySelectorAll(".cancellation-notice")).toHaveLength(50);
  }
  button("Dismiss cancellation notice for job job-1").click();
  flushSync();
  expect(notice("job-1")).toBeUndefined();
  expect(document.querySelectorAll(".cancellation-notice")).toHaveLength(49);
  expect(cancelButton("new-job").disabled).toBe(false);
  expect(document.body.textContent).toContain("Dismissing a notice only hides feedback");
  cancelButton("new-job").click();
  if (state === "requested") await expectRequested("new-job");
  else await expectUnconfirmed("new-job");
  expect(document.querySelectorAll(".cancellation-notice")).toHaveLength(50);
  expect(browser.jobs.list.every((item) => item.state === "running")).toBe(true);
  expect(vi.mocked(api.cancelJob).mock.calls.filter(([id]) => id === "new-job")).toEqual([["new-job"]]);
});

it("does not dismiss pending notices or admit a new job when every slot is pending", async () => {
  const api = newFakeApi();
  const pending = deferred<void>(undefined);
  let current = Array.from({ length: 50 }, (_, i) => job(`job-${i}`));
  api.jobs = vi.fn(async () => current);
  api.cancelJob = vi.fn(() => pending.promise);
  const browser = new BrowserState(api);
  await show(browser);
  for (const item of current) cancelButton(item.id).click();
  current = [job("new-job")];
  await browser.jobs.load();
  flushSync();
  expect(document.querySelectorAll(".cancellation-notice")).toHaveLength(50);
  expect(document.querySelector(".dismiss-cancellation")).toBeNull();
  expect(cancelButton("new-job").disabled).toBe(true);
  cancelButton("new-job").click();
  expect(api.cancelJob).toHaveBeenCalledTimes(50);
  expect(document.body.textContent).toContain("Wait for a pending reply or dismiss a settled notice");
});

it("does not start a rejection refresh after the drawer is disposed", async () => {
  const api = newFakeApi();
  const pending = deferred<void>(undefined);
  api.jobs = vi.fn(async () => [job()]);
  api.cancelJob = vi.fn(() => pending.promise);
  const browser = new BrowserState(api);
  const cancel = vi.spyOn(browser, "cancelJob");
  await show(browser);
  cancelButton().click();
  await unmount(drawer!);
  drawer = null;
  pending.reject(new Error("owned late refusal"));
  await expect(cancel.mock.results[0].value).rejects.toThrow("owned late refusal");
  expect(api.jobs).toHaveBeenCalledTimes(1);
  expect(document.querySelector(".cancellation-notice")).toBeNull();
});
