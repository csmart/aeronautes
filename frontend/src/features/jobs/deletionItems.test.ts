import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, JobDTO, JobItemDTO, JobItemTotalsDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import JobsDrawer from "./JobsDrawer.svelte";

// ADR 0093 decisions 4 and 5: a selected-set deletion's items are deletion
// members with one outcome each, and a member whose verifying read observed
// a successor says so, never as a plain deletion.
let drawer: ReturnType<typeof mount> | null = null;
afterEach(async () => {
  if (drawer) await unmount(drawer);
  drawer = null;
  document.body.innerHTML = "";
});

function job(): JobDTO {
  return { id: "selected", state: "partial", type: "delete_selected", source: "fixtures", destination: "(deleted)", sourceBookmarkId: "a", destBookmarkId: "",
    createdAt: "2026-10-27T00:00:00Z", startedAt: "", finishedAt: "2026-10-27T00:00:09Z", engine: "native", settings: "", error: "", recoveryRecords: 0,
    counters: { objectsPlanned: 8, objectsCopied: 7, objectsVerified: 0, objectsFailed: 0, bytesPlanned: 0, bytesCopied: 0,
      overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 2 } };
}

function member(key: string, overrides: Partial<JobItemDTO>): JobItemDTO {
  return { id: key, key, keyDisplay: JSON.stringify(key),
    source: { kind: "swift", bookmarkId: "a", container: "fixtures", object: key, localPath: "", display: `"a" / "fixtures" / ${JSON.stringify(key)}` },
    destination: { kind: "", bookmarkId: "", container: "", object: "", localPath: "", display: '""' },
    staging: null, planned: true, admitted: true, listedBytes: 10, createdAt: "2026-10-27T00:00:01Z", publicationRequired: false, publication: null,
    predecessor: null, issuedAt: "2026-10-27T00:00:02Z", successor: null, execution: null, verification: null,
    listedEtag: "5eb63bbbe01eeed093cb22bb8f5acdc3", listedSlo: false, baseline: { class: "plain", length: 10, etag: "5eb63bbbe01eeed093cb22bb8f5acdc3" }, ...overrides };
}

const observation = (result: string, effect: string, stage = "", code = "", httpStatus = 0) =>
  ({ result, effect, stage, code, httpStatus, copiedBytes: 0, createdAt: "" });

const totals: JobItemTotalsDTO = { status: "available", items: 8, planned: 8, admitted: 8, failed: 0, conflicts: 1, refused: 1,
  unresolved: 2, unverified: 1, problems: 3, issued: 7, deleted: 2, unsettled: 1 };

async function show(api: Api) {
  const rows = [job()];
  api.jobs = vi.fn(async () => rows);
  api.job = vi.fn(async () => rows[0]);
  const browser = new BrowserState(api);
  api.listBookmarks = async () => [{ id: "a", displayName: "Account A", authUrl: "http://mock/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }];
  await browser.loadBookmarks();
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  await vi.waitFor(() => expect(document.querySelectorAll(".job")).toHaveLength(1));
  document.querySelector<HTMLButtonElement>('[aria-label="Item details for job selected"]')!.click();
  flushSync();
}

it("shows each member's outcome and what its verifying read observed", async () => {
  const api = newFakeApi();
  const items = [
    member("gone", { execution: observation("deleted", "reported_applied"), successor: { present: false, class: "", length: 0, etag: "", role: "after", createdAt: "" } }),
    member("replaced", { execution: observation("deleted", "reported_applied"), successor: { present: true, class: "plain", length: 3, etag: "abc123", role: "after", createdAt: "" } }),
    member("lost", { execution: observation("unverified", "unresolved", "verification", "unverified") }),
    member("changed", { execution: observation("conflict", "not_attempted", "source_read", "conflict"),
      successor: { present: true, class: "plain", length: 40, etag: "ffff", role: "comparison", createdAt: "" } }),
    member("link", { baseline: null, execution: observation("refused", "not_attempted", "source_read", "link") }),
    member("later", { issuedAt: "", execution: observation("not_attempted", "not_attempted") }),
    member("in-flight", {}),
    member("never", { issuedAt: "" }),
    member("read-first", { listedBytes: 0, listedEtag: "d41d8cd98f00b204e9800998ecf8427e", baseline: { class: "dlo", length: 2048, etag: "e1e1" },
      execution: observation("deleted", "reported_applied"), successor: { present: false, class: "", length: 0, etag: "", role: "after", createdAt: "" } }),
  ];
  api.jobItems = async () => ({ status: "available", items, nextCursor: "" });
  api.jobItemTotals = async () => totals;
  await show(api);
  await vi.waitFor(() => expect(document.querySelectorAll(".item-record")).toHaveLength(9));
  const outcome = (index: number) => document.querySelectorAll(".item-record")[index].querySelector(".outcome")?.textContent?.replace(/\s+/g, " ").trim();
  expect(outcome(0)).toBe("Outcome: Deleted; the verifying read found nothing at the name.");
  expect(outcome(1)).toContain("Deleted, but something stands at the name now: a plain of 3 bytes with entity tag abc123");
  expect(outcome(1)).toContain("not observed");
  expect(outcome(2)).toBe("Outcome: Unverified: the name may still hold what was shown (verification: unverified).");
  expect(outcome(3)).toBe("Outcome: Changed since it was listed: expected a plain of 10 bytes with entity tag 5eb63bbbe01eeed093cb22bb8f5acdc3; found a plain of 40 bytes with entity tag ffff; left as it is (source_read: conflict).");
  expect(outcome(4)).toBe("Outcome: Refused: the name holds a symbolic link, whose identity is its target's, and a link is not deleted; left as it is.");
  expect(outcome(5)).toBe("Outcome: Not attempted.");
  expect(outcome(6)).toBe("Outcome: Unresolved: issued with no outcome recorded, so its DELETE may have been sent.");
  expect(outcome(7)).toBe("Outcome: Not attempted.");
  expect(outcome(8)).toBe("Outcome: Deleted; the verifying read found nothing at the name. Its listing could not say what it held, so it was read first and compared as a dlo of 2048 bytes with entity tag e1e1.");
  const problems = [...document.querySelectorAll(".item-record")].map((record) => record.querySelector(".outcome .problem") !== null);
  expect(problems).toEqual([false, true, true, true, true, false, true, false, false]);
  const body = document.body.textContent ?? "";
  expect(body).not.toContain("Destination");
  expect(body).not.toContain("Unverified copy");
  expect(body).not.toContain("Final publication");
  const total = (label: string) => [...document.querySelectorAll(".totals dt")].find((dt) => dt.textContent === label)?.nextElementSibling?.textContent;
  expect(total("Deleted")).toBe("2");
  expect(total("Issued")).toBe("7");
  expect(total("Issued without an outcome")).toBe("1");
  expect(document.querySelectorAll(".item-record")[3].textContent).toContain("Listed entity tag: 5eb63bbbe01eeed093cb22bb8f5acdc3");
  expect(total("Unverified deletions")).toBe("1");
  expect(total("Unverified copies")).toBeUndefined();
  expect(document.querySelector(".job .operation")?.textContent).toBe("Delete selected");
  expect(document.querySelector(".job .counts")?.textContent?.replace(/\s+/g, " ").trim()).toBe("7/8 members settled · 2 deleted");
});
