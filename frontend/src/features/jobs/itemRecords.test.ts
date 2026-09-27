import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, JobDTO, JobHistoryDTO, JobItemDTO, JobItemPageDTO, JobItemTotalsDTO } from "../../lib/api";
import { v1Offers } from "../../lib/v1.svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import JobsDrawer from "./JobsDrawer.svelte";

let drawer: ReturnType<typeof mount> | null = null;
afterEach(async () => {
  if (drawer) await unmount(drawer);
  drawer = null;
  document.body.innerHTML = "";
  v1Offers.rerun = false;
  vi.useRealTimers();
});

function job(id: string, state = "failed"): JobDTO {
  return { id, state, type: "copy", source: "source/", destination: "dest/", sourceBookmarkId: "a", destBookmarkId: "b",
    createdAt: "2000-01-01T00:00:00Z", startedAt: "", finishedAt: "", engine: "native", settings: "", error: "", recoveryRecords: 0,
    counters: { objectsPlanned: 0, objectsCopied: 0, objectsVerified: 0, objectsFailed: 0, bytesPlanned: 0, bytesCopied: 0,
      overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 0 } };
}

function item(index: number, key = `item-${index}`): JobItemDTO {
  return { id: (9007199254740993n + BigInt(index)).toString(), key, keyDisplay: JSON.stringify(key),
    source: { kind: "swift", bookmarkId: "a", container: "source", object: key, localPath: "", display: `"a" / "source" / ${JSON.stringify(key)}` },
    destination: { kind: "swift", bookmarkId: "b", container: "dest", object: key, localPath: "", display: `"b" / "dest" / ${JSON.stringify(key)}` },
    staging: null, planned: true, admitted: true, listedBytes: 10, createdAt: "2000-01-01T00:00:00Z", publicationRequired: false, publication: null,
    // Copy items replace nothing, so they carry no predecessor.
    predecessor: null,
    execution: { result: "failed", effect: "not_attempted", stage: "source_read", code: "forbidden", httpStatus: 403, copiedBytes: 0, createdAt: "" },
    verification: { result: "failed", effect: "not_attempted", stage: "verification", code: "missing", httpStatus: 404, copiedBytes: 0, createdAt: "" } };
}

function totals(overrides: Partial<JobItemTotalsDTO> = {}): JobItemTotalsDTO {
  return { status: "available", items: 0, planned: 0, admitted: 0, failed: 0, conflicts: 0, refused: 0,
    unresolved: 0, unverified: 0, problems: 0, ...overrides };
}

it("keeps early verification distinct from final publication evidence", async () => {
  const api = newFakeApi();
  const rows = ["confirmed", "substituted", "missing", "not reached", "historical"].map((name, index) => {
    const row = item(index, name);
    row.destination = { kind: "local", bookmarkId: "", container: "", object: "", localPath: `/download/${name}`, display: JSON.stringify(`/download/${name}`) };
    row.publicationRequired = name !== "historical";
    row.execution = { result: "copied", effect: "reported_applied", stage: "", code: "", httpStatus: 0, copiedBytes: 10, createdAt: "" };
    row.verification = { result: "verified", effect: "not_attempted", stage: "", code: "", httpStatus: 0, copiedBytes: 0, createdAt: "" };
    return row;
  });
  rows[0].publication = { ...rows[0].verification! };
  rows[1].publication = { ...rows[1].verification!, result: "failed", effect: "unresolved", stage: "verification", code: "unverified" };
  rows[3].execution = { ...rows[3].execution!, result: "failed", effect: "not_attempted", copiedBytes: 0, stage: "source_read", code: "missing" };
  rows[3].verification = null;
  api.jobItems = async () => ({ status: "available", items: rows, nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 5, planned: 5, admitted: 5, failed: 2, unresolved: 1, unverified: 2, problems: 2 });
  await show(api, [{ ...job("first", "partial"), type: "download", destBookmarkId: "" }]);
  expand();
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(5));
  const records = [...document.querySelectorAll(".item-record")];
  expect(records[0].querySelector(".publication")?.textContent).toContain("verified");
  expect(records[0].textContent).not.toContain("Unverified copy");
  expect(records[1].querySelectorAll(".observation")[1].textContent).toContain("verified");
  expect(records[1].querySelector(".publication")?.textContent).toContain("failed");
  expect(records[1].textContent).toContain("Unresolved effects");
  expect(records[1].textContent).toContain("Unverified copy");
  expect(records[1].textContent).toContain("10 reported copied bytes");
  expect(records[2].querySelectorAll(".observation")[1].textContent).toContain("verified");
  expect(records[2].querySelector(".publication")?.textContent).toContain("Required final check was not recorded");
  expect(records[2].textContent).toContain("Unverified copy");
  expect(records[2].textContent).not.toContain("Unresolved effects");
  expect(records[3].querySelector(".publication")?.textContent).toContain("Final check not reached");
  expect(records[3].textContent).not.toContain("Unverified copy");
  expect(records[4].querySelector(".publication")?.textContent).toContain("Not required for this item");
  expect(records[4].textContent).not.toContain("Unverified copy");
});

function button(text: string, root: ParentNode = document): HTMLButtonElement {
  const found = [...root.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === text);
  expect(found, `button ${text}`).toBeDefined();
  return found!;
}

function click(text: string) {
  button(text).click();
  flushSync();
}

async function show(api: Api, rows: JobDTO[] = [job("first")]) {
  api.jobs = vi.fn(async () => rows);
  api.job = vi.fn(async (id) => rows.find((j) => j.id === id) ?? null);
  const browser = new BrowserState(api);
  api.listBookmarks = async () => [{ id: "a", displayName: "Account A", authUrl: "http://mock/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }];
  await browser.loadBookmarks();
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  await vi.waitFor(() => expect(document.querySelectorAll(".job")).toHaveLength(rows.length));
  return browser;
}

function expand(id = "first") {
  document.querySelector<HTMLButtonElement>(`[aria-label="Item details for job ${id}"]`)!.click();
  flushSync();
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((ok, bad) => { resolve = ok; reject = bad; });
  return { promise, resolve, reject };
}

async function settleResponse() {
  // A task boundary drains the resolved read, Promise.all and Svelte continuations.
  // Checking in the first promise continuation can miss a later stale render.
  await new Promise<void>((resolve) => setTimeout(resolve, 0));
  flushSync();
}

it("does not offer re-run for a copy job in a v1 build", async () => {
  const api = newFakeApi();
  api.rerunJob = vi.fn(async () => { throw new Error("should not run"); });
  api.startCopy = vi.fn(async () => "unsafe-fallback");
  api.jobItems = async () => ({ status: "available", items: [item(0)], nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 1, failed: 1 });
  const interrupted = job("interrupted");
  interrupted.error = "interrupted by shutdown; data and recovery records retained, remote residue unverified; other counters show the last saved progress";
  interrupted.counters.objectsFailed = 1;
  await show(api, [interrupted]);
  expand("interrupted");
  await vi.waitFor(() => expect(document.querySelector(".item-record")).not.toBeNull());
  expect([...document.querySelectorAll("button")].map((b) => b.textContent?.trim())).not.toContain("Re-run");
  expect(api.rerunJob).not.toHaveBeenCalled();
  expect(api.startCopy).not.toHaveBeenCalled();
});

it.each([
  "re-run is unavailable because retained item records cannot be read; inspect retained data before starting a fresh Copy",
  "re-run is unavailable because item records were not captured; inspect retained data before starting a fresh Copy",
  "re-run is unavailable because prior item effects remain unresolved; inspect the retained item records before starting a fresh Copy",
  "re-run is unavailable because reported copies remain unverified; inspect the retained item records before starting a fresh Copy",
])("shows the backend rerun refusal without admitting fallback work: %s", async (reason) => {
  v1Offers.rerun = true;
  const api = newFakeApi();
  api.rerunJob = vi.fn(async () => { throw new Error(reason); });
  api.startCopy = vi.fn(async () => "unsafe-fallback");
  api.jobItems = async () => ({ status: "available", items: [item(0)], nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 1, failed: 1 });
  const interrupted = job("interrupted");
  interrupted.error = "interrupted by shutdown; data and recovery records retained, remote residue unverified; other counters show the last saved progress";
  interrupted.counters.objectsFailed = 1;
  await show(api, [interrupted]);
  expand("interrupted");
  await vi.waitFor(() => expect(document.querySelector(".item-record")).not.toBeNull());
  click("Re-run");
  await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toBe(reason));
  await settleResponse();
  expect(api.rerunJob).toHaveBeenCalledExactlyOnceWith("interrupted");
  expect(api.startCopy).not.toHaveBeenCalled();
  expect(document.querySelectorAll(".job")).toHaveLength(1);
  expect(document.querySelector(".error")?.textContent).toBe(interrupted.error);
  expect(document.querySelector(".item-record summary code")?.textContent).toBe('"item-0"');
});

it("reaches every item through older history while bounding both displayed pages", async () => {
  const api = newFakeApi();
  const retainedJobs = Array.from({ length: 153 }, (_, i) => job(`job-${i}`));
  const retained = Array.from({ length: 401 }, (_, i) => item(i));
  api.jobHistory = vi.fn(async (account, cursor, limit) => {
    expect(account).toBe("");
    expect(limit).toBe(50);
    const offset = cursor ? retainedJobs.findIndex((j) => j.id === cursor) + 1 : 0;
    const jobs = retainedJobs.slice(offset, offset + limit);
    return { jobs, nextCursor: offset + limit < retainedJobs.length ? jobs.at(-1)!.id : "" };
  });
  api.jobItems = vi.fn(async (id, cursor, limit) => {
    expect(id).toBe("job-152");
    expect(limit).toBe(100);
    const offset = cursor ? retained.findIndex((r) => r.id === cursor) + 1 : 0;
    const items = retained.slice(offset, offset + limit);
    return { status: "available", items, nextCursor: offset + limit < retained.length ? items.at(-1)!.id : "" };
  });
  api.jobItemTotals = async () => totals({ items: 401, planned: 401, admitted: 401, failed: 401, problems: 401 });
  const browser = await show(api, retainedJobs.slice(0, 50));
  api.job = async (id) => retainedJobs.find((j) => j.id === id) ?? null;
  click("Job history");
  for (let page = 1; page <= 4; page++) {
    await vi.waitFor(() => expect(document.querySelectorAll(".history-job")).toHaveLength(page === 4 ? 3 : 50));
    expect(document.body.textContent).toContain(`History page ${page}`);
    if (page < 4) click("Next history page");
  }
  await browser.loadJobs();
  expect(document.body.textContent).toContain("History page 4");
  expect(document.querySelectorAll(".history-job")).toHaveLength(3);
  document.querySelectorAll<HTMLButtonElement>(".history-details")[2].click();
  flushSync();
  const seen: string[] = [];
  for (let page = 1; page <= 5; page++) {
    await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(page === 5 ? 1 : 100));
    expect(document.body.textContent).toContain(`Item page ${page}`);
    seen.push(...[...document.querySelectorAll(".item-record summary code")].map((e) => e.textContent!));
    expect(document.querySelectorAll(".history-job")).toHaveLength(3);
    if (page < 5) click("Next item page");
  }
  expect(seen).toEqual(retained.map((r) => r.keyDisplay));
  expect(button("Next item page").disabled).toBe(true);
  expect(button("Next history page").disabled).toBe(true);
  expect(document.body.textContent).toContain("End of saved item records");
  click("First item page / refresh");
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(100));
  expect(document.querySelector(".item-record summary code")?.textContent).toBe('"item-0"');
  click("First history page / refresh");
  expect(document.querySelector(".item-records")).toBeNull();
  await vi.waitFor(() => expect(document.querySelectorAll(".history-job")).toHaveLength(50));
});

it("pages every captured upload path and preserves missing observations without rerun", async () => {
  const api = newFakeApi();
  const upload = { ...job("upload", "partial"), type: "upload", sourceBookmarkId: "", destBookmarkId: "b" };
  const retained = Array.from({ length: 401 }, (_, index) => {
    const row = item(index, `scope/étude-${index}\n<script>`);
    const path = `/owned/Éléments/étude-${index}\n<script>`;
    row.source = { kind: "local", bookmarkId: "", container: "", object: "", localPath: path, display: JSON.stringify(path) };
    row.execution = index % 3 === 2 ? null : { result: "copied", effect: index % 3 === 1 ? "unresolved" : "reported_applied",
      stage: index % 3 === 1 ? "staging" : "", code: index % 3 === 1 ? "io" : "", httpStatus: 0, copiedBytes: 9, createdAt: "2000-01-01T00:00:00Z" };
    row.verification = index % 3 === 0 ? { result: "verified", effect: "not_attempted", stage: "", code: "", httpStatus: 0, copiedBytes: 0, createdAt: "2000-01-01T00:00:00Z" } : null;
    return row;
  });
  api.jobItems = vi.fn(async (id, cursor, limit) => {
    expect(id).toBe("upload");
    expect(limit).toBe(100);
    const offset = cursor ? retained.findIndex((row) => row.id === cursor) + 1 : 0;
    const items = retained.slice(offset, offset + limit);
    return { status: "available", items, nextCursor: offset + limit < retained.length ? items.at(-1)!.id : "" };
  });
  api.jobItemTotals = async () => totals({ items: 401, planned: 401, admitted: 401, unresolved: 267, unverified: 134, problems: 134 });
  api.rerunJob = vi.fn(async () => "unexpected");
  api.startUpload = vi.fn(async () => "unexpected");
  api.pickUploadFiles = vi.fn(async () => null);
  await show(api, [upload]);
  expand("upload");
  const seen: string[] = [];
  for (let page = 0; page < 5; page++) {
    const expected = retained.slice(page * 100, page * 100 + 100);
    await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe(expected[0].keyDisplay));
    const elements = [...document.querySelectorAll(".item-record")];
    expect(elements).toHaveLength(expected.length);
    expect(document.body.textContent).toContain(`Item page ${page + 1}`);
    elements.forEach((element, index) => {
      const row = expected[index];
      expect(element.querySelector("summary code")?.textContent).toBe(row.keyDisplay);
      expect(element.querySelector(".locations")?.textContent).toContain(row.source.display);
      expect(element.querySelectorAll(".observation")).toHaveLength(2);
      expect(element.querySelectorAll(".observation")[0].textContent).toContain(row.execution ? "copied" : "No observation recorded");
      expect(element.querySelectorAll(".observation")[1].textContent).toContain(row.verification ? "verified" : "No observation recorded");
      seen.push(row.source.display);
    });
    expect(document.querySelector(".item-records script")).toBeNull();
    expect(document.querySelector(".rerun")).toBeNull();
    if (page < 4) click("Next item page");
  }
  expect(seen).toEqual(retained.map((row) => row.source.display));
  expect(button("Next item page").disabled).toBe(true);
  expect(api.rerunJob).not.toHaveBeenCalled();
  expect(api.startUpload).not.toHaveBeenCalled();
  expect(api.pickUploadFiles).not.toHaveBeenCalled();
});

it("keeps unrecorded download history distinct and requires its own dialog", async () => {
  const api = newFakeApi();
  const download = { ...job("download", "complete"), type: "download", destBookmarkId: "" };
  api.jobItems = async () => ({ status: "not_captured", items: [], nextCursor: "" });
  api.jobItemTotals = async () => totals({ status: "not_captured" });
  api.rerunJob = vi.fn(async () => "unexpected");
  api.startDownload = vi.fn(async () => "unexpected");
  await show(api, [download]);
  expand("download");
  await vi.waitFor(() => expect(document.querySelector(".item-records")?.textContent).toContain("Item records were not captured"));
  expect(document.querySelector(".item-record")).toBeNull();
  expect(document.querySelector(".totals")).toBeNull();
  expect(document.querySelector(".rerun")).toBeNull();
  expect(api.rerunJob).not.toHaveBeenCalled();
  expect(api.startDownload).not.toHaveBeenCalled();
});

it("pages exact download destinations and temporary paths without acquiring rerun authority", async () => {
  vi.useFakeTimers();
  const api = newFakeApi();
  const download = { ...job("download", "partial"), type: "download", destBookmarkId: "" };
  const root = "/owned/Éléments";
  const prefix = "scope\n<script>/";
  const retained = Array.from({ length: 401 }, (_, index) => {
    const marker = index === 400;
    const name = marker ? "" : "étude-" + index + ".bin";
    const row = item(index, prefix + name);
    const path = marker ? root : root + "/" + name;
    row.destination = { kind: "local", bookmarkId: "", container: "", object: "", localPath: path, display: JSON.stringify(path) };
    const temporary = index % 2 === 0 ? root + "/.aeronautes-part-" + index : root + "/.aeronautes-stage-download/" + name;
    row.staging = marker ? null : { kind: "local", bookmarkId: "", container: "", object: "", localPath: temporary, display: JSON.stringify(temporary) };
    row.listedBytes = marker ? 0 : 9;
    row.execution = index % 4 === 3 ? null : { result: "copied", effect: index % 4 === 1 ? "unresolved" : "reported_applied",
      stage: index % 4 === 1 ? "cleanup" : "", code: index % 4 === 1 ? "io" : "", httpStatus: 0,
      copiedBytes: marker ? 0 : 9, createdAt: "2000-01-01T00:00:00Z" };
    row.verification = index % 4 < 2 ? { result: "verified", effect: "not_attempted", stage: "", code: "", httpStatus: 0,
      copiedBytes: 0, createdAt: "2000-01-01T00:00:00Z" } : null;
    return row;
  });
  api.jobItems = vi.fn(async (id, cursor, limit) => {
    expect(id).toBe("download");
    expect(limit).toBe(100);
    const offset = cursor ? retained.findIndex((row) => row.id === cursor) + 1 : 0;
    const items = retained.slice(offset, offset + limit);
    return { status: "available", items, nextCursor: offset + limit < retained.length ? items.at(-1)!.id : "" };
  });
  api.jobItemTotals = async () => totals({ items: 401, planned: 401, admitted: 401, unresolved: 200, unverified: 100, problems: 100 });
  api.rerunJob = vi.fn(async () => "unexpected");
  api.startDownload = vi.fn(async () => "unexpected");
  api.pickDownloadDir = vi.fn(async () => null);
  await show(api, [download]);
  expand("download");
  const seen: string[] = [];
  for (let page = 0; page < 5; page++) {
    if (page === 4) {
      await vi.advanceTimersByTimeAsync(1500);
      flushSync();
    }
    const expected = retained.slice(page * 100, page * 100 + 100);
    await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe(expected[0].keyDisplay));
    const elements = [...document.querySelectorAll(".item-record")];
    expect(elements).toHaveLength(expected.length);
    expect(document.body.textContent).toContain("Item page " + (page + 1));
    elements.forEach((element, index) => {
      const row = expected[index];
      expect(element.querySelector("summary code")?.textContent).toBe(row.keyDisplay);
      expect(element.querySelector(".locations")?.textContent).toContain(row.source.display);
      expect(element.querySelector(".locations")?.textContent).toContain(row.destination.display);
      if (row.staging) expect(element.querySelector(".locations")?.textContent).toContain(row.staging.display);
      else expect(element.querySelector(".locations")?.textContent).not.toContain(".aeronautes-part-");
      expect(element.querySelectorAll(".observation")).toHaveLength(2);
      expect(element.querySelectorAll(".observation")[0].textContent).toContain(row.execution ? "copied" : "No observation recorded");
      expect(element.querySelectorAll(".observation")[1].textContent).toContain(row.verification ? "verified" : "No observation recorded");
      seen.push(row.destination.display);
    });
    expect(document.querySelector(".item-records script")).toBeNull();
    expect(document.querySelector(".item-records")?.textContent).not.toContain("\n<script>");
    expect(document.querySelector(".rerun")).toBeNull();
    if (page < 4) click("Next item page");
  }
  expect(seen).toEqual(retained.map((row) => row.destination.display));
  const counts = Object.fromEntries([...document.querySelectorAll(".totals div")].map((element) =>
    [element.querySelector("dt")!.textContent, element.querySelector("dd")!.textContent]));
  expect(counts["Unresolved effects"]).toBe("200");
  expect(counts["Unverified copies"]).toBe("100");
  expect(button("Next item page").disabled).toBe(true);
  expect(api.rerunJob).not.toHaveBeenCalled();
  expect(api.startDownload).not.toHaveBeenCalled();
  expect(api.pickDownloadDir).not.toHaveBeenCalled();
});

it.each([
  ["partial", false, false], ["partial", false, true],
  ["partial", true, false], ["partial", true, true],
  ["running", false, false], ["running", false, true],
  ["running", true, false], ["running", true, true],
] as const)("preserves item pages during unchanged %s polls (fresh=%s, pending=%s)", async (state, fresh, pending) => {
  vi.useFakeTimers();
  const api = newFakeApi();
  const download = { ...job("download", state), type: "download", destBookmarkId: "" };
  const first = item(0, "scope\n<script>/étude-0.bin");
  const last = item(400, "scope\n<script>/");
  const tail: JobItemPageDTO = { status: "available", items: [last], nextCursor: "" };
  const delayed = deferred<JobItemPageDTO>();
  api.jobItems = vi.fn(async (id, cursor, limit) => {
    expect(id).toBe("download");
    expect(limit).toBe(100);
    if (!cursor) return { status: "available", items: [first], nextCursor: first.id };
    expect(cursor).toBe(first.id);
    return pending ? delayed.promise : tail;
  });
  api.jobItemTotals = vi.fn(async () => totals({ items: 2 }));
  const browser = await show(api, [download]);
  if (fresh) api.jobs = vi.fn(async () => [{ ...download, counters: { ...download.counters, bytesCopied: 17 } }]);
  expand("download");
  await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe(first.keyDisplay));
  click("Next item page");
  if (!pending) {
    await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe(last.keyDisplay));
  }
  vi.mocked(api.jobs).mockClear();
  vi.mocked(api.job).mockClear();
  vi.mocked(api.jobItems).mockClear();
  vi.mocked(api.jobItemTotals).mockClear();
  await vi.advanceTimersByTimeAsync(1500);
  flushSync();
  expect(api.jobs).toHaveBeenCalledOnce();
  expect(api.job).toHaveBeenCalledExactlyOnceWith("download", "a");
  expect(browser.jobs[0].counters.bytesCopied).toBe(fresh ? 17 : 0);
  expect(document.body.textContent).toContain("Item page 2");
  expect(api.jobItems).not.toHaveBeenCalled();
  expect(api.jobItemTotals).not.toHaveBeenCalled();
  if (pending) {
    expect(document.querySelector(".item-records")?.getAttribute("aria-busy")).toBe("true");
    expect(document.querySelector(".item-record")).toBeNull();
    delayed.resolve(tail);
    await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe(last.keyDisplay));
  }
  expect(document.querySelector("summary code")?.textContent).toBe(last.keyDisplay);
  expect(document.querySelector(".item-records")?.getAttribute("aria-busy")).toBe("false");
  expect(button("Next item page").disabled).toBe(true);
});

it.each(["empty", "unavailable"])("keeps new download records %s distinct from historical uncaptured jobs", async (condition) => {
  const api = newFakeApi();
  const download = { ...job("download", "complete"), type: "download", destBookmarkId: "" };
  api.jobItems = async () => {
    if (condition === "unavailable") throw new Error("private fixture database failure");
    return { status: "available", items: [], nextCursor: "" };
  };
  api.jobItemTotals = async () => totals();
  api.rerunJob = vi.fn(async () => "unexpected");
  api.startDownload = vi.fn(async () => "unexpected");
  await show(api, [download]);
  expand("download");
  if (condition === "empty") {
    await vi.waitFor(() => expect(document.querySelector(".item-records")?.textContent).toContain("No item records have been saved"));
    expect(document.querySelector(".totals")).not.toBeNull();
  } else {
    await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain("Item records are unavailable"));
    expect(document.querySelector(".totals")).toBeNull();
  }
  expect(document.body.textContent).not.toContain("Item records were not captured");
  expect(document.body.textContent).not.toContain("private fixture database failure");
  expect(document.querySelector(".item-record")).toBeNull();
  expect(document.querySelector(".rerun")).toBeNull();
  expect(api.rerunJob).not.toHaveBeenCalled();
  expect(api.startDownload).not.toHaveBeenCalled();
});

it.each(["not_captured", "empty", "page-error", "totals-error", "mismatch", "null-rows", "unknown-status"])("distinguishes %s without fabricated totals", async (condition) => {
  const api = newFakeApi();
  api.jobItems = async () => {
    if (condition === "page-error") throw new Error("private-provider-payload");
    return { status: condition === "not_captured" ? "not_captured" : condition === "unknown-status" ? "surprise" : "available",
      items: condition === "null-rows" ? null : [], nextCursor: "" };
  };
  api.jobItemTotals = async () => {
    if (condition === "totals-error") throw new Error("private-provider-payload");
    return totals({ status: ["not_captured", "mismatch"].includes(condition) ? "not_captured" : "available" });
  };
  await show(api);
  expand();
  if (condition === "not_captured") {
    await vi.waitFor(() => expect(document.body.textContent).toContain("Item records were not captured"));
    expect(document.querySelector(".totals")).toBeNull();
  } else if (condition === "empty") {
    await vi.waitFor(() => expect(document.body.textContent).toContain("No item records have been saved"));
    expect(document.querySelector(".totals")).not.toBeNull();
  } else {
    await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain("Item records are unavailable"));
    expect(document.querySelector(".totals")).toBeNull();
  }
  expect(document.body.textContent).not.toContain("private-provider-payload");
  expect(document.querySelector(".item-record")).toBeNull();
});

it("renders escaped identities and both phases without summing overlapping facets", async () => {
  const api = newFakeApi();
  const rows = [item(0, ""), item(1, "line\n\x00\u202e<script>alert(1)</script>é�")];
  rows[1].keyDisplay = '"line\\n\\x00\\u202e<script>alert(1)</script>é�"';
  rows[1].source.display = '"a" / "source" / "line\\n\\x00\\u202e<script>alert(1)</script>é�"';
  rows[1].destination = { kind: "local", bookmarkId: "", container: "", object: "", localPath: "/local\nname", display: '"/local\\nname"' };
  rows[1].staging = { ...rows[1].source, object: "stage\nname", display: '"a" / "source" / "stage\\nname"' };
  rows[0].execution = null;
  rows[0].verification = null;
  rows[1].execution = { ...rows[1].execution!, result: "copied", copiedBytes: 9, effect: "unresolved", stage: "cleanup", code: "io", httpStatus: 0 };
  api.jobItems = async () => ({ status: "available", items: rows, nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 2, planned: 2, admitted: 2, failed: 1, unresolved: 2, unverified: 1, problems: 1 });
  await show(api);
  expand();
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(2));
  expect([...document.querySelectorAll("summary code")].map((e) => e.textContent)).toEqual(rows.map((r) => r.keyDisplay));
  document.querySelectorAll("details").forEach((d) => { d.open = true; });
  expect(document.querySelector(".item-records script")).toBeNull();
  expect(document.querySelector(".item-records")!.textContent).not.toMatch(/[\u0000\u202e]/u);
  expect(document.querySelectorAll(".locations")[1].textContent).toContain(rows[1].staging.display);
  expect(document.querySelectorAll(".locations")[1].textContent).toContain(rows[1].destination.display);
  const text = document.querySelector(".item-records")!.textContent!;
  expect(text).toContain("No observation recorded");
  expect(text).toContain("Unresolved effects");
  expect(text).toContain("Unverified copy");
  expect(text).toContain("9 reported copied bytes");
  expect(text).toContain("Verification:");
  expect(text).toContain("HTTP 404");
  expect(text).toContain("These counts overlap; do not add them together");
  const counts = Object.fromEntries([...document.querySelectorAll(".totals div")].map((e) => [e.querySelector("dt")!.textContent, e.querySelector("dd")!.textContent]));
  expect(counts["Failed names"]).toBe("1");
  expect(counts["Unresolved effects"]).toBe("2");
  expect(counts["Unverified copies"]).toBe("1");
});

it.each([false, true])("discards a prior job's late item response (failure=%s)", async (failure) => {
  const api = newFakeApi();
  const old = deferred<JobItemPageDTO>();
  api.jobItems = vi.fn(async (id) => id === "old" ? old.promise : { status: "available", items: [item(1, "new job")], nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 1 });
  await show(api, [job("old"), job("new")]);
  expand("old");
  await vi.waitFor(() => expect(api.jobItems).toHaveBeenCalledWith("old", "", 100, "a"));
  expand("new");
  await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe('"new job"'));
  if (failure) old.reject(new Error("old failure"));
  else old.resolve({ status: "available", items: [item(0, "old job")], nextCursor: "" });
  await settleResponse();
  expect(document.querySelector("summary code")?.textContent).toBe('"new job"');
  expect(document.querySelector('[role="alert"]')).toBeNull();
  expect(document.body.textContent).not.toContain("old job");
});

it("discards a late next-item page after returning to the first page", async () => {
  const api = newFakeApi();
  const later = deferred<JobItemPageDTO>();
  const first = { status: "available", items: [item(0, "first page")], nextCursor: "9007199254740993" };
  api.jobItems = vi.fn(async (_id, cursor) => cursor ? later.promise : first);
  api.jobItemTotals = async () => totals({ items: 2 });
  await show(api);
  expand();
  await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe('"first page"'));
  click("Next item page");
  expect(document.querySelector(".item-record")).toBeNull();
  expect(document.body.textContent).toContain("Item page 2 · loading");
  click("First item page / refresh");
  await vi.waitFor(() => expect(document.querySelector("summary code")?.textContent).toBe('"first page"'));
  later.resolve({ status: "available", items: [item(1, "stale second page")], nextCursor: "" });
  await settleResponse();
  expect(document.querySelector("summary code")?.textContent).toBe('"first page"');
  expect(document.body.textContent).toContain("Item page 1");
});

it.each([false, true])("discards stale account history (failure=%s) and resets selected items", async (failure) => {
  const api = newFakeApi();
  const old = deferred<JobHistoryDTO>();
  let calls = 0;
  api.jobHistory = vi.fn(async (account) => {
    if (++calls === 2) return old.promise;
    return { jobs: [job(account || "all")], nextCursor: "older-cursor" };
  });
  api.jobItems = async () => ({ status: "available", items: [item(0)], nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 1 });
  const browser = await show(api);
  click("Job history");
  await vi.waitFor(() => expect(document.querySelector(".history-details")?.textContent).toContain("all"));
  document.querySelector<HTMLButtonElement>(".history-details")!.click();
  flushSync();
  await vi.waitFor(() => expect(document.querySelector(".item-record")).not.toBeNull());
  click("Next history page");
  expect(document.querySelector(".item-records")).toBeNull();
  const account = browser.bookmarks[0].id;
  const select = [...document.querySelectorAll<HTMLSelectElement>("select")].find((e) => e.parentElement?.textContent?.includes("Account"))!;
  select.value = account;
  select.dispatchEvent(new Event("change", { bubbles: true }));
  flushSync();
  await vi.waitFor(() => expect(document.querySelector(".history-details")?.textContent).toContain(account));
  expect(api.jobHistory).toHaveBeenLastCalledWith(account, "", 50);
  if (failure) old.reject(new Error("old account unavailable"));
  else old.resolve({ jobs: [job("wrong-account")], nextCursor: "" });
  await settleResponse();
  expect(document.body.textContent).not.toContain("wrong-account");
  expect(document.querySelector('[role="alert"]')).toBeNull();
  expect(document.body.textContent).toContain("History page 1");
  expect(document.querySelector(".item-records")).toBeNull();
});

it("refreshes the first item page when an older active job finishes outside the recent poll", async () => {
  vi.useFakeTimers();
  const api = newFakeApi();
  const older = job("older-active", "running");
  const items = Array.from({ length: 101 }, (_, i) => ({ ...item(i), execution: null, verification: null }));
  let finished = false;
  api.jobHistory = async () => ({ jobs: [older], nextCursor: "" });
  api.jobItems = vi.fn(async (_id, cursor) => ({ status: "available", items: cursor ? items.slice(100) : items.slice(0, 100),
    nextCursor: cursor ? "" : items[99].id }));
  api.jobItemTotals = vi.fn(async () => totals({ items: finished ? 102 : 101, admitted: 101, unresolved: 101 }));
  await show(api);
  api.job = vi.fn(async (id) => id === older.id ? { ...older, state: finished ? "failed" : "running" } : job(id));
  click("Job history");
  await vi.waitFor(() => expect(document.querySelector(".history-details")).not.toBeNull());
  document.querySelector<HTMLButtonElement>(".history-details")!.click();
  flushSync();
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(100));
  expect(document.body.textContent).toContain("Job still active. Records are incomplete");
  click("Next item page");
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(1));
  finished = true;
  await vi.advanceTimersByTimeAsync(1500);
  flushSync();
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(100));
  expect(api.job).toHaveBeenCalledWith("older-active", "a");
  expect(api.jobItems).toHaveBeenLastCalledWith("older-active", "", 100, "a");
  expect(document.body.textContent).toContain("Item page 1");
  expect(document.body.textContent).not.toContain("Job still active");
  expect(document.querySelector(".totals dd")?.textContent).toBe("102");
});

it.each(["missing", "error"])("hides retained pages when the job status becomes %s and recovers on a later read", async (fault) => {
  vi.useFakeTimers();
  const api = newFakeApi();
  api.jobItems = async () => ({ status: "available", items: [item(0)], nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 1 });
  await show(api);
  expand();
  await vi.waitFor(() => expect(document.querySelector(".item-record")).not.toBeNull());
  api.job = async () => {
    if (fault === "error") throw new Error("private-provider-payload");
    return null;
  };
  await vi.advanceTimersByTimeAsync(1500);
  flushSync();
  expect(document.querySelector('[role="alert"]')?.textContent).toContain("Item records are unavailable");
  expect(document.querySelector(".item-record")).toBeNull();
  expect(document.querySelector(".totals")).toBeNull();
  expect(document.body.textContent).not.toContain("private-provider-payload");
  api.job = async () => job("first");
  await vi.advanceTimersByTimeAsync(1500);
  flushSync();
  await vi.waitFor(() => expect(document.querySelector(".item-record")).not.toBeNull());
  expect(document.querySelector('[role="alert"]')).toBeNull();
});

it.each(["read-error", "null-rows"])("drops history on %s and offers a fresh first page", async (fault) => {
  const api = newFakeApi();
  let unavailable = true;
  api.jobHistory = async () => {
    if (unavailable) {
      if (fault === "read-error") throw new Error("invalid job history page or cursor");
      return { jobs: null, nextCursor: "" };
    }
    return { jobs: [], nextCursor: "" };
  };
  await show(api);
  click("Job history");
  await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain("Job history is unavailable"));
  expect(document.querySelector(".history-job")).toBeNull();
  expect(document.body.textContent).not.toContain("No retained jobs");
  unavailable = false;
  click("First history page / refresh");
  await vi.waitFor(() => expect(document.body.textContent).toContain("No retained jobs on this page"));
  expect(document.querySelector('[role="alert"]')).toBeNull();
});

it("shows active details, comparison-only rows, refusals and conflicts without inferring execution", async () => {
  const api = newFakeApi();
  const rows = [item(0, "comparison"), item(1, "refused"), item(2, "conflict"), item(3, "same")];
  rows[0].execution = null;
  rows[0].admitted = false;
  rows[0].planned = false;
  rows[1].execution = { ...rows[1].execution!, result: "refused", stage: "planning", code: "conflict" };
  rows[1].verification = null;
  rows[1].admitted = false;
  rows[2].execution = { ...rows[2].execution!, result: "conflict", effect: "unresolved", stage: "commit", code: "conflict", httpStatus: 412 };
  rows[3].execution = { ...rows[3].execution!, result: "same", code: "", stage: "", httpStatus: 0 };
  api.jobItems = async () => ({ status: "available", items: rows, nextCursor: "" });
  api.jobItemTotals = async () => totals({ items: 4, refused: 1, conflicts: 1 });
  await show(api, [job("first", "running")]);
  expand();
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(4));
  expect(document.body.textContent).toContain("Job still active");
  const elements = [...document.querySelectorAll(".item-record")];
  expect(elements[0].textContent).toContain("Comparison only; no execution admission");
  expect(elements[0].querySelector(".observation")?.textContent).toContain("No observation recorded");
  expect(elements[1].textContent).toContain("refused");
  expect(elements[1].textContent).not.toContain("Unresolved effects");
  expect(elements[2].textContent).toContain("Unresolved effects");
  expect(elements[2].textContent).toContain("HTTP 412");
  expect(elements[3].textContent).toContain("same");
});

it.each([false, true])("ignores an older recent-job response after changing account (failure=%s)", async (failure) => {
  const api = newFakeApi();
  const old = deferred<JobDTO[]>();
  api.jobs = vi.fn(async (account) => account === "old" ? old.promise : [job("new")]);
  const browser = new BrowserState(api);
  browser.jobsFilter = "old";
  const pending = browser.loadJobs();
  browser.jobsFilter = "new";
  await browser.loadJobs();
  if (failure) old.reject(new Error("stale failure"));
  else old.resolve([job("old")]);
  await pending;
  expect(browser.jobs.map((j) => j.id)).toEqual(["new"]);
  expect(browser.jobsError).toBeNull();
});
