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
  vi.restoreAllMocks();
});

function job(type: string, state = "running", counters: Partial<JobDTO["counters"]> = {}): JobDTO {
  return {
    id: "presentation-job", type, state, source: "source/scope/", destination: "target/scope/",
    sourceBookmarkId: "a", destBookmarkId: "b", createdAt: "2026-09-12T00:00:00Z",
    startedAt: "2026-09-12T00:00:01Z", finishedAt: state === "running" ? "" : "2026-09-12T00:00:09Z",
    engine: "native", settings: '{"transfers":2,"checkers":4}', error: "", recoveryRecords: 0,
    counters: {
      objectsPlanned: 10, objectsCopied: 3, objectsVerified: 0, objectsFailed: 0,
      bytesPlanned: 80, bytesCopied: 24, overwrites: 0, overwritesRefused: 0,
      nativeRouted: 0, objectsDeleted: 0, ...counters,
    },
  };
}

async function show(value: JobDTO) {
  const api = newFakeApi();
  api.jobs = vi.fn(async () => [structuredClone(value)]);
  api.job = vi.fn(async () => structuredClone(value));
  api.cancelJob = vi.fn(async () => { throw new Error("unexpected cancellation"); });
  api.rerunJob = vi.fn(async () => { throw new Error("unexpected rerun"); });
  const browser = new BrowserState(api);
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  await vi.waitFor(() => expect(document.querySelectorAll(".job")).toHaveLength(1));
  expect(api.jobs).toHaveBeenCalledWith("", false, 50);
  return { api, browser, row: document.querySelector<HTMLElement>(".job")! };
}

function text(row: HTMLElement, selector: string): string {
  return row.querySelector(selector)?.textContent?.replace(/\s+/g, " ").trim() ?? "";
}

const deleteTypes = ["delete_prefix", "empty_container", "delete_container", "delete_segments"];

it.each([
  ["copy", "Copy"], ["upload", "Upload"], ["download", "Download"],
  ["delete_prefix", "Delete prefix"], ["empty_container", "Empty container"],
  ["delete_container", "Delete container"], ["delete_selected", "Delete selected"],
  ["delete_segments", "Delete manifest + segments"],
])("visibly labels the actual %s operation as %s", async (type, label) => {
  const { api, row } = await show(job(type));
  expect(text(row, ".operation")).toBe(label);
  expect(text(row, ".badge")).toBe("running");
  expect(api.cancelJob).not.toHaveBeenCalled();
  expect(api.rerunJob).not.toHaveBeenCalled();
});

it.each(deleteTypes.flatMap((type) => [
  { type, state: "running" }, { type, state: "partial" },
]))("uses settled-member wording without copied bytes for $type $state", async ({ type, state }) => {
  const { row } = await show(job(type, state));
  expect(text(row, ".counts")).toBe("3/10 members settled");
  expect(text(row, ".counts")).not.toContain("24 B");
  expect(text(row, ".badge")).toBe(state);
  if (state === "running") {
    expect(row.querySelector(".progress")?.getAttribute("style")).toContain("30%");
    expect(row.querySelector<HTMLButtonElement>(".cancel")?.disabled).toBe(false);
  } else {
    expect(row.querySelector(".progress")).toBeNull();
    expect(row.querySelector(".cancel")).toBeNull();
  }
  expect(row.querySelector(".rerun")).toBeNull();
});

it("counts a selected-set deletion's settled members apart from its deletions", async () => {
  const { row } = await show(job("delete_selected", "running", { objectsDeleted: 2 }));
  expect(text(row, ".counts")).toBe("3/10 members settled · 2 deleted");
  expect(text(row, ".counts")).not.toContain("reported deleted");
  expect(row.querySelector(".progress")?.getAttribute("style")).toContain("30%");
});

it.each([
  ["", "Unknown operation"],
  ["mirror", "mirror"],
  ["mirror_preview", "mirror_preview"],
  ["delete", "delete"],
  ["DELETE_PREFIX", "DELETE_PREFIX"],
  ["delete_prefix_extra", "delete_prefix_extra"],
  ['<img src=x onerror="alert(1)">', '<img src=x onerror="alert(1)">'],
])("preserves a literal historical or unknown type %j as %j", async (type, label) => {
  const { row } = await show(job(type));
  expect(text(row, ".operation")).toBe(label);
  expect(row.querySelector(".operation img")).toBeNull();
  expect(text(row, ".counts")).toBe("3/10 obj · 24 B");
  expect(text(row, ".counts")).not.toContain("reported deleted");
});

it.each(deleteTypes)("keeps %s reported progress distinct from a retained deletion suffix", async (type) => {
  const { row } = await show(job(type, "running", { objectsDeleted: 7 }));
  expect(row.querySelector(".progress")?.getAttribute("style")).toContain("30%");
  expect(text(row, ".counts")).toBe("3/10 members settled · 7 deleted");
  expect(text(row, ".counts")).not.toMatch(/(?:7|10)\/10/);
});

it.each([
  { planned: 0, copied: 3, expected: "3 members settled", progress: null },
  { planned: -1, copied: 0, expected: "0 members settled", progress: null },
  { planned: 10, copied: 0, expected: "0/10 members settled", progress: "0%" },
])("preserves zero counts and optional planned totals ($planned planned, $copied reported)", async ({ planned, copied, expected, progress }) => {
  const { row } = await show(job("delete_prefix", "running", { objectsPlanned: planned, objectsCopied: copied }));
  expect(text(row, ".counts")).toBe(expected);
  if (progress === null) expect(row.querySelector(".progress")).toBeNull();
  else expect(row.querySelector(".progress")?.getAttribute("style")).toContain(progress);
});

it.each(deleteTypes)("keeps incomplete %s accounting as last confirmed without final totals", async (type) => {
  const value = {
    ...job(type, "partial", { deletionAccountingIncomplete: true, objectsFailed: 2, objectsDeleted: 7 }),
    error: "Retained admitted-attempt evidence is incomplete.",
  };
  const { row } = await show(value);
  expect(text(row, ".counts")).toBe("Last confirmed progress: 3 processed · 2 reported failures");
  expect(text(row, ".counts")).not.toContain("/10");
  expect(text(row, ".counts")).not.toContain("reported deleted");
  expect(row.textContent).toContain("Deletion accounting is incomplete. Final failure and unresolved totals are unavailable.");
  expect(text(row, ".error")).toBe(value.error);
  expect(row.querySelector(".progress")).toBeNull();
  row.querySelector<HTMLButtonElement>(".details")!.click();
  flushSync();
  expect(text(row, ".config")).not.toContain("verified");
});

it.each([
  { refused: 1, qualifier: "1 overwrite refused" },
  { refused: 2, qualifier: "2 overwrites refused" },
])("retains failed and refused counts separately ($refused refused)", async ({ refused, qualifier }) => {
  const { row } = await show(job("delete_container", "partial", { objectsFailed: 2, overwritesRefused: refused }));
  expect(text(row, ".counts")).toContain("3/10");
  expect(text(row, ".counts")).toContain("2 failed");
  expect(text(row, ".counts")).toContain(qualifier);
  expect(row.querySelector(".progress")).toBeNull();
});

it.each([
  { type: "copy", progress: "30%" },
  { type: "upload", progress: "30%" },
  { type: "download", progress: "30%" },
  { type: "mirror", progress: "70%" },
  { type: "mirror_preview", progress: "30%" },
  { type: "delete", progress: "70%" },
])("leaves $type transfer counts and existing progress semantics unchanged", async ({ type, progress }) => {
  const { row } = await show(job(type, "running", { objectsDeleted: 7, objectsFailed: 1 }));
  expect(text(row, ".counts")).toBe("3/10 obj · 24 B · 7 deleted · 1 failed");
  expect(row.querySelector(".progress")?.getAttribute("style")).toContain(progress);
});

it.each(["copy", "upload", "download", ...deleteTypes])("keeps active %s diagnostics available without a mutation", async (type) => {
  const { api, row } = await show(job(type));
  const details = row.querySelector<HTMLButtonElement>(".details")!;
  expect(details.getAttribute("aria-label")).toBe("Item details for job presentation-job");
  details.click();
  flushSync();
  await vi.waitFor(() => expect(row.querySelector(".item-records")?.textContent).toContain("Job still active"));
  expect(details.getAttribute("aria-expanded")).toBe("true");
  expect(text(row, ".config")).toContain("native");
  expect(text(row, ".config")).toContain("transfers2");
  expect(text(row, ".config")).toContain("checkers4");
  expect(api.cancelJob).not.toHaveBeenCalled();
  expect(api.rerunJob).not.toHaveBeenCalled();
});

it("shows where each effective setting came from (§45 provenance)", async () => {
  const settings = JSON.stringify({
    profile: "balanced",
    chunkSize: "5Gi",
    transfers: 12,
    checkers: 8,
    bufferSize: "16Mi",
    origins: { transfers: "override", checkers: "profile", chunkSize: "calculated", bufferSize: "default" },
  });
  const { row } = await show({ ...job("copy"), settings });
  row.querySelector<HTMLButtonElement>(".details")!.click();
  flushSync();
  const config = text(row, ".config");
  expect(config).toContain("transfers12set for this job");
  expect(config).toContain("checkers8from profile");
  expect(config).toContain("chunkSize5Gicalculated");
  expect(config).toContain("bufferSize16Midefault");
});

it("shows no provenance note for a settings record without origins", async () => {
  const { row } = await show({ ...job("copy"), settings: '{"transfers":2,"checkers":4}' });
  row.querySelector<HTMLButtonElement>(".details")!.click();
  flushSync();
  const config = text(row, ".config");
  expect(config).toContain("transfers2");
  expect(config).not.toContain("set for this job");
  expect(config).not.toContain("from profile");
});

it("marks a job with no recorded settings as not recorded, not defaults", async () => {
  const { row } = await show({ ...job("copy"), settings: "" });
  row.querySelector<HTMLButtonElement>(".details")!.click();
  flushSync();
  const config = text(row, ".config");
  expect(config).toContain("settingsnot recorded");
  expect(config).not.toContain("defaults");
});

it("marks an unreadable settings record as not recorded", async () => {
  const { row } = await show({ ...job("copy"), settings: "{not valid json" });
  row.querySelector<HTMLButtonElement>(".details")!.click();
  flushSync();
  const config = text(row, ".config");
  expect(config).toContain("settingsnot recorded");
  expect(config).not.toContain("defaults");
});

it("shows defaults when a settings record carries no non-default values", async () => {
  const { row } = await show({ ...job("copy"), settings: '{"transfers":0,"checkers":0}' });
  row.querySelector<HTMLButtonElement>(".details")!.click();
  flushSync();
  const config = text(row, ".config");
  expect(config).toContain("settingsdefaults");
  expect(config).not.toContain("not recorded");
});

it("marks a settings record whose value cannot be displayed as not recorded", async () => {
  // Parses and is a top-level object, but String() on the field throws; the
  // record is unreadable, so expanding the job must show not recorded, not crash.
  const { row } = await show({ ...job("copy"), settings: '{"transfers":{"toString":null}}' });
  row.querySelector<HTMLButtonElement>(".details")!.click();
  flushSync();
  const config = text(row, ".config");
  expect(config).toContain("settingsnot recorded");
  expect(config).not.toContain("defaults");
});

it.each(["running", "complete"])("keeps unknown rate and later rate/ETA qualification for %s Copy", async (state) => {
  const { browser, row } = await show(job("copy", state));
  expect(browser.jobs.rates["presentation-job"]).toBeUndefined();
  expect(text(row, ".when")).not.toMatch(/NaN|Infinity|~|\/s/);
  browser.jobs.rates = { "presentation-job": 8 };
  flushSync();
  if (state === "running") {
    expect(text(row, ".when")).toBe("8 B/s · ~7s");
    expect(row.querySelector<HTMLButtonElement>(".cancel")?.disabled).toBe(false);
    expect(row.querySelector(".rerun")).toBeNull();
  } else {
    expect(text(row, ".when")).toBe("2026-09-12 00:00:09");
    expect(row.querySelector(".cancel")).toBeNull();
    // Job re-run returned with copy on 31 October 2026 (ADR 0052 decision 12),
    // so a complete copy job now offers it.
    expect(row.querySelector(".rerun")).not.toBeNull();
    expect(row.querySelector(".progress")).toBeNull();
  }
});
