import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { AppSettingsDTO, RestoreResultDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi, settingsRefusal } from "../browser/fakeApi";
import SettingsDialog from "./SettingsDialog.svelte";

const original: AppSettingsDTO = {
  maxActiveJobs: 2, defaultProfile: "balanced", auditRetentionDays: 17, jobRetentionDays: 11, downloadObjectCeiling: 250_000,
  browserRetentionCeiling: 100_000, maxQueuedJobs: 16, maxConnsPerHost: 16,
  reclaimReplacedFragments: true, gpuAcceleration: true, railWidthPx: 0,
};
let dialog: ReturnType<typeof mount> | null = null;
const releasePending: (() => void)[] = [];

afterEach(async () => {
  for (const release of releasePending.splice(0)) release();
  await Promise.resolve();
  if (dialog) await unmount(dialog);
  dialog = null;
  document.body.innerHTML = "";
});

function deferred() {
  let resolve!: (value: AppSettingsDTO) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<AppSettingsDTO>((done, fail) => { resolve = done; reject = fail; });
  releasePending.push(() => resolve({ ...original }));
  return { promise, resolve, reject };
}

function fixture() {
  const api = newFakeApi();
  api.jobs = vi.fn(async () => []);
  api.appSettings = vi.fn(async () => ({ ...original }));
  api.saveAppSettings = vi.fn(async (settings) => ({ ...settings }));
  return { api, browser: new BrowserState(api) };
}

function select(): HTMLSelectElement | null {
  return [...document.querySelectorAll("label")].find((label) =>
    label.querySelector("span")?.textContent?.trim() === "Active-job budget")?.querySelector("select") ?? null;
}

function button(text: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((node) => node.textContent?.trim() === text);
  expect(found, `BUDGET-BUTTON: ${text}`).toBeDefined();
  return found!;
}

function feedback(): string {
  return document.querySelector(".budget-status")?.textContent ?? "";
}

// Opens the dialog and lets a settings read that is already answered land;
// one held by a test stays pending.
async function show(browser: BrowserState) {
  dialog = mount(SettingsDialog, { target: document.body, props: { settings: browser.settings, onClose: () => {} } });
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
    flushSync();
  }
}

function choose(value: string) {
  const control = select();
  expect(control).not.toBeNull();
  control!.value = value;
  control!.dispatchEvent(new Event("change", { bubbles: true }));
}

it("preserves the whole document and choices while awaiting acknowledgement", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  flushSync();
  expect(select()?.disabled).toBe(true);
  expect(browser.settings.document).toEqual(original);
  expect(api.saveAppSettings).toHaveBeenCalledExactlyOnceWith({ ...original, maxActiveJobs: 4 });
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
  expect(browser.settings.document).toEqual({ ...original, maxActiveJobs: 4 });
  expect([...select()!.options].map((option) => option.value)).toEqual(["1", "2", "3", "4"]);
});

it("shows a cold pending read then unavailable feedback and a read-only retry", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.appSettings = vi.fn().mockReturnValueOnce(reply.promise).mockResolvedValueOnce({ ...original });
  await show(browser);
  expect(feedback(), "BUDGET-LOADING").toContain("Loading settings");
  expect(api.saveAppSettings).not.toHaveBeenCalled();
  reply.reject(new Error("private cold Get failure"));
  await vi.waitFor(() => expect(browser.settings.operation).toBeNull());
  flushSync();
  expect(feedback()).toContain("Settings are unavailable");
  expect(document.body.textContent).not.toContain("private cold Get");
  expect(select()?.disabled ?? true).toBe(true);
  button("Refresh settings").click();
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  expect(api.appSettings).toHaveBeenCalledTimes(2);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
  expect(select()?.disabled).toBe(false);
});

it("shows cold failed settings as unavailable rather than an absent control", async () => {
  const { api, browser } = fixture();
  api.appSettings = vi.fn(async () => { throw new Error("private read failure"); });
  await show(browser);
  expect(feedback(), "BUDGET-GET-FAILURE").toContain("Settings are unavailable");
  expect(button("Refresh settings").disabled).toBe(false);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it("qualifies a cached failed read and blocks writes until a successful refresh", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  api.appSettings = vi.fn().mockRejectedValueOnce(new Error("private refresh failure"))
    .mockResolvedValueOnce({ ...original, maxActiveJobs: 3 });
  await browser.settings.load();
  flushSync();
  expect(select()?.value).toBe("2");
  expect(select()?.disabled, "BUDGET-STALE-EDIT").toBe(true);
  expect(feedback()).toContain("stale");
  expect(feedback()).toContain("Last observed budget: 2");
  button("Refresh settings").click();
  await vi.waitFor(() => expect(select()?.value).toBe("3"));
  expect(select()?.disabled).toBe(false);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it.each([false, true])("retains an uncertain save and observes recovery without repeating it (applied %s)", async (applied) => {
  const { api, browser } = fixture();
  let provider = { ...original };
  let settled = false;
  const reply = deferred();
  api.appSettings = vi.fn(async () => ({ ...provider }));
  api.saveAppSettings = vi.fn(async (settings) => {
    if (applied) provider = { ...settings };
    try { return await reply.promise; } finally { settled = true; }
  });
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  flushSync();
  reply.reject(new Error(applied ? "owned applied Set reply lost" : "owned Set refused before effect"));
  await vi.waitFor(() => expect(settled).toBe(true));
  flushSync();
  expect.soft(feedback(), "BUDGET-SET-UNCONFIRMED").toContain("Budget change to 4 is unconfirmed");
  expect.soft(select()?.disabled, "BUDGET-UNCERTAIN-EDIT").toBe(true);
  expect(browser.settings.document).toEqual(original);
  expect(feedback()).toContain("Last observed budget: 2");
  expect(document.body.textContent).not.toMatch(/owned (?:applied|Set)/);
  button("Refresh settings").click();
  await vi.waitFor(() => expect(browser.settings.operation).toBeNull());
  flushSync();
  expect(select()?.value, "BUDGET-SAME-VALUE-READBACK").toBe(String(provider.maxActiveJobs));
  expect(select()?.disabled).toBe(false);
  expect(browser.settings.document).toEqual(provider);
  expect(feedback(), "BUDGET-READBACK-NOT-ACK").toContain("Budget change to 4 is unconfirmed");
  expect(feedback()).not.toContain("Budget change acknowledged");
  expect(api.appSettings).toHaveBeenCalledTimes(2);
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
});

// ADR 0105 decision 7: a save the service refuses is shown as refused, in the
// service's words, and leaves every control usable; and the control says what
// a larger budget multiplies.
it("shows a refused budget as refused, with the reason, and stays editable", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  expect(document.body.textContent).toContain("each job brings its own engine settings");
  api.saveAppSettings = vi.fn(async () => { throw settingsRefusal("the job store has stopped recording"); });
  choose("4");
  await vi.waitFor(() => expect(browser.settings.attempt?.state).toBe("refused"));
  flushSync();
  expect(feedback()).toContain("Budget change to 4 was refused: the job store has stopped recording.");
  expect(feedback()).not.toContain("unconfirmed");
  expect(feedback()).not.toContain("Refresh settings before another change");
  expect(select()?.value).toBe("2");
  expect(select()?.disabled).toBe(false);
  expect(reclaimBox()?.disabled).toBe(false);
});

// After a refused budget change, another write that holds the settings, here
// a restore waiting on its file dialog, shows the budget that was read, not
// the one refused.
it("shows the read budget while another write holds the settings", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  api.saveAppSettings = vi.fn(async () => { throw settingsRefusal("refused for this test"); });
  choose("4");
  await vi.waitFor(() => expect(browser.settings.attempt?.state).toBe("refused"));
  let pick!: () => void;
  api.importSettings = vi.fn(() => new Promise<RestoreResultDTO | null>((done) => { pick = () => done(null); }));
  const restoring = browser.settings.restore();
  flushSync();
  expect(browser.settings.operation).toBe("set");
  expect(select()?.value, "BUDGET-DURING-RESTORE").toBe("2");
  pick();
  await restoring;
});

it("does not replace uncertainty with a failed read or an automatic remount read", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  api.saveAppSettings = vi.fn(async () => { throw new Error("owned uncertain reply"); });
  choose("4");
  await vi.waitFor(() => expect(browser.settings.attempt?.state).toBe("unconfirmed"));
  api.appSettings = vi.fn(async () => { throw new Error("private recovery Get failure"); });
  button("Refresh settings").click();
  await vi.waitFor(() => expect(browser.settings.operation).toBeNull());
  flushSync();
  expect(feedback(), "BUDGET-RECOVERY-GET-FAILURE").toContain("Settings could not be refreshed");
  expect(feedback()).toContain("stale");
  await unmount(dialog!);
  dialog = null;
  await show(browser);
  expect(api.appSettings, "BUDGET-EXPLICIT-READ").toHaveBeenCalledTimes(1);
  expect(feedback()).toContain("Budget change to 4 is unconfirmed");
  expect(select()?.disabled).toBe(true);
  expect(browser.settings.document).toEqual(original);
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
});

it("rejects a second same-turn change and restores the admitted choice", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  choose("3");
  flushSync();
  expect.soft(api.saveAppSettings, "BUDGET-DUPLICATE-DOM").toHaveBeenCalledTimes(1);
  expect(select()?.value, "BUDGET-ADMITTED-CHOICE").toBe("4");
  expect(feedback()).toContain("Saving budget 4");
  choose("3");
  flushSync();
  expect(select()?.value).toBe("4");
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
  expect(feedback()).toContain("Budget change acknowledged");
});

it("keeps one settings owner through job polling, close and reopen", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  flushSync();
  await browser.jobs.load();
  await unmount(dialog!);
  dialog = null;
  await show(browser);
  expect.soft(api.appSettings, "BUDGET-REMOUNT-GET-OWNER").toHaveBeenCalledTimes(1);
  expect(select()?.disabled, "BUDGET-REMOUNT-SET-OWNER").toBe(true);
  expect(feedback()).toContain("Saving budget 4");
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
  expect(feedback()).toContain("Budget change acknowledged");
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
});

it("blocks writes and duplicate refresh clicks while a cached Get is pending", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  const reply = deferred();
  api.appSettings = vi.fn(() => reply.promise);
  button("Refresh settings").click();
  button("Refresh settings").click();
  choose("3");
  flushSync();
  expect(api.appSettings, "BUDGET-DUPLICATE-GET-DOM").toHaveBeenCalledTimes(1);
  expect(api.saveAppSettings, "BUDGET-PENDING-GET-DOM").not.toHaveBeenCalled();
  expect(select()?.disabled).toBe(true);
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
});

// ADR 0081 decision 5's option, as the operator reaches it: the control
// states which way a replacement will go, saves the other way, and shows
// the last observed value until the save is acknowledged.
function reclaimBox(): HTMLInputElement | null {
  return document.querySelector<HTMLInputElement>("input.reclaim-fragments");
}

function reclaimFeedback(): string {
  return statusOf(reclaimBox()!);
}

it("offers the replaced-segment option and says which way it is set", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(reclaimBox()).not.toBeNull());
  expect(reclaimBox()!.checked, "RECLAIM-DEFAULT").toBe(true);
  expect(reclaimFeedback()).toContain("removed as the new object is written");

  // Turning it off saves false and nothing else.
  reclaimBox()!.checked = false;
  reclaimBox()!.dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(api.saveAppSettings).toHaveBeenCalledTimes(1));
  expect(api.saveAppSettings).toHaveBeenCalledWith({ ...original, reclaimReplacedFragments: false });
  await vi.waitFor(() => expect(reclaimBox()!.checked).toBe(false));
  expect(reclaimFeedback()).toContain("are kept");
});

it("keeps showing the last observed setting until a save is acknowledged", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(reclaimBox()).not.toBeNull());
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  reclaimBox()!.checked = false;
  reclaimBox()!.dispatchEvent(new Event("change", { bubbles: true }));
  flushSync();
  // The click is not the state: until the backend answers, the control
  // still shows what was read.
  expect(reclaimBox()!.checked, "RECLAIM-PENDING").toBe(true);
  reply.resolve({ ...original, reclaimReplacedFragments: false });
  await vi.waitFor(() => expect(reclaimBox()!.checked).toBe(false));
});

function numberInput(label: string): HTMLInputElement | null {
  return [...document.querySelectorAll("label")].find((node) =>
    node.querySelector("span")?.textContent?.trim() === label)?.querySelector("input") ?? null;
}

// A control is described by its note, then its status.
function statusOf(input: HTMLInputElement): string {
  const status = (input.getAttribute("aria-describedby") ?? "").split(" ").at(-1) ?? "";
  return document.getElementById(status)?.textContent?.trim() ?? "";
}

// The four numeric settings are one control written once. Each saves its own
// field and nothing else, says what the saved value means, and shows the
// service's refusal beside itself while it goes on showing what was last read.
it.each([
  ["Download ceiling", "downloadObjectCeiling", 12_000, "ceiling 12,000 objects"],
  ["Browser retention", "browserRetentionCeiling", 50_000, "keeps 50,000 rows"],
  ["Queue depth", "maxQueuedJobs", 32, "32 waiting at most"],
  ["Connection cap", "maxConnsPerHost", 8, "8 connections per host on the next connect"],
] as const)("saves %s as its own field and shows a refusal beside it", async (label, field, value, sentence) => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(numberInput(label)).not.toBeNull());
  const input = numberInput(label)!;
  input.value = String(value);
  input.dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(statusOf(input)).toBe(sentence));
  expect(api.saveAppSettings).toHaveBeenCalledExactlyOnceWith({ ...original, [field]: value });

  // Above every setting's range, so the product refuses it as the stub does.
  api.saveAppSettings = vi.fn(async () => { throw settingsRefusal(`${label} out of range`); });
  input.value = "999999999";
  input.dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(statusOf(input)).toBe(`${label} out of range`));
  expect(input.value).toBe(String(value));

  // A number that is not whole is not sent: the bridge would refuse to
  // decode it, and that refusal looks like a lost reply.
  input.value = "1.5";
  input.dispatchEvent(new Event("change", { bubbles: true }));
  flushSync();
  expect(statusOf(input)).toBe(`${label} takes a whole number`);
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
  expect(input.value).toBe(String(value));
});

// A lost reply to another setting's save leaves every control waiting for an
// explicit read, as one for the budget does, and a reopened dialog does not
// make that read on its own.
it("treats a lost reply to another setting like a lost budget reply", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(numberInput("Queue depth")).not.toBeNull());
  api.saveAppSettings = vi.fn(async () => { throw new Error("owned lost reply"); });
  const input = numberInput("Queue depth")!;
  input.value = "32";
  input.dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(browser.settings.status).toBe("unconfirmed"));
  flushSync();
  expect(feedback()).toContain("Refresh settings before another change");
  expect(select()?.disabled).toBe(true);
  expect(select()?.value, "BUDGET-UNTOUCHED").toBe("2");
  await unmount(dialog!);
  dialog = null;
  await show(browser);
  expect(api.appSettings, "OTHER-SETTING-EXPLICIT-READ").toHaveBeenCalledTimes(1);
  expect(browser.settings.status).toBe("unconfirmed");
});
