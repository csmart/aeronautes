import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
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

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((node) => (node.getAttribute("aria-label") ?? node.textContent?.trim()) === label);
  expect(found, `button ${label}`).toBeDefined();
  return found!;
}

function openAuditTab(browser: BrowserState) {
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  button("Recent changes").click();
  flushSync();
}

it("archives the audit log and reports the file written", async () => {
  const api = newFakeApi();
  api.exportAudit = vi.fn(async () => ({
    count: 3, directory: "/home/operator/exports", fileName: "aeronautes-audit-x.jsonl",
  }));
  const browser = new BrowserState(api);
  browser.audit.filterOp = "transfer.";
  openAuditTab(browser);

  button("Export log…").click();
  await vi.waitFor(() =>
    expect(document.body.textContent).toContain(
      "Exported 3 events to /home/operator/exports/aeronautes-audit-x.jsonl",
    ),
  );
  // The archive uses the viewer's current filters, not just the shown page.
  expect(api.exportAudit).toHaveBeenCalledWith(
    expect.objectContaining({ operationPrefix: "transfer.", limit: 0, beforeId: 0 }),
  );
});

it("reports a cancelled dialog without an error", async () => {
  const api = newFakeApi();
  api.exportAudit = vi.fn(async () => null);
  const browser = new BrowserState(api);
  openAuditTab(browser);

  button("Export log…").click();
  await vi.waitFor(() => expect(document.body.textContent).toContain("Export cancelled; nothing was written."));
  expect(document.querySelector('[role="alert"]')?.textContent ?? "").not.toContain("could not be exported");
});

it("reports a failed export", async () => {
  const api = newFakeApi();
  api.exportAudit = vi.fn(async () => {
    throw new Error("disk full");
  });
  const browser = new BrowserState(api);
  openAuditTab(browser);

  button("Export log…").click();
  await vi.waitFor(() => expect(document.body.textContent).toContain("The audit log could not be exported."));
});
