import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, AppSettingsDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi, settingsRefusal } from "../browser/fakeApi";
import BrowserScreen from "../browser/BrowserScreen.svelte";
import SettingsDialog from "./SettingsDialog.svelte";

const live = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: live }));

const original: AppSettingsDTO = {
  maxActiveJobs: 2, defaultProfile: "balanced", auditRetentionDays: 17, jobRetentionDays: 11, downloadObjectCeiling: 250_000,
  browserRetentionCeiling: 100_000, maxQueuedJobs: 16, maxConnsPerHost: 16,
  reclaimReplacedFragments: true, gpuAcceleration: true, railWidthPx: 0,
};
let mounted: ReturnType<typeof mount> | null = null;

beforeEach(() => {
  Object.assign(live, newFakeApi());
});
afterEach(async () => {
  if (mounted) await unmount(mounted);
  mounted = null;
  document.body.innerHTML = "";
});

async function settle() {
  for (let i = 0; i < 16; i++) {
    await Promise.resolve();
    flushSync();
  }
}

async function show() {
  const api = newFakeApi();
  api.appSettings = vi.fn(async () => ({ ...original }));
  api.saveAppSettings = vi.fn(async (settings) => ({ ...settings }));
  const browser = new BrowserState(api);
  mounted = mount(SettingsDialog, { target: document.body, props: { settings: browser.settings, onClose: () => {} } });
  await settle();
  return api;
}

function control<T extends HTMLElement>(label: string, tag: "select" | "input"): T {
  const found = [...document.querySelectorAll("label")].find((node) =>
    node.querySelector("span")?.textContent?.trim() === label)?.querySelector<T>(tag);
  expect(found, `control ${label}`).toBeDefined();
  return found!;
}

function change(element: HTMLInputElement | HTMLSelectElement, value: string) {
  element.value = value;
  element.dispatchEvent(new Event("change", { bubbles: true }));
}

function statusOf(element: HTMLElement): string {
  const status = (element.getAttribute("aria-describedby") ?? "").split(" ").at(-1) ?? "";
  return document.getElementById(status)?.textContent?.trim() ?? "";
}

// A settings file that could not be read is said at the top, over the
// defaults in force; a save replaces the file, and the report goes with it
// (ADR 0109 decision 1).
it("says when the settings file could not be read, until a save", async () => {
  const api = newFakeApi();
  api.appSettings = vi.fn(async () => ({ ...original, fileProblem: "the settings file /cfg/settings.json could not be read" }));
  api.saveAppSettings = vi.fn(async ({ fileProblem: _report, ...saved }: AppSettingsDTO) => saved);
  const browser = new BrowserState(api);
  mounted = mount(SettingsDialog, { target: document.body, props: { settings: browser.settings, onClose: () => {} } });
  await settle();
  expect(document.querySelector('.body [role="alert"]')?.textContent).toContain("/cfg/settings.json could not be read");
  change(control<HTMLSelectElement>("Default performance profile", "select"), "aggressive");
  await settle();
  expect(api.saveAppSettings).toHaveBeenCalledOnce();
  expect(document.querySelector('.body [role="alert"]')).toBeNull();
});

it("saves the chosen default profile and shows it once acknowledged", async () => {
  const api = await show();
  const profile = control<HTMLSelectElement>("Default performance profile", "select");
  expect(profile.value).toBe("balanced");
  expect([...profile.options].map((option) => option.value)).toEqual(["auto", "balanced", "aggressive", "custom"]);
  change(profile, "aggressive");
  await vi.waitFor(() => expect(profile.value).toBe("aggressive"));
  expect(api.saveAppSettings).toHaveBeenCalledExactlyOnceWith({ ...original, defaultProfile: "aggressive" });
});

// The two retention windows are saved like the other numeric settings; the
// service's range is 0 to 3,650 days, and its refusal is shown in its words.
it.each([
  ["Job history kept (days)", "jobRetentionDays", "finished jobs kept 60 days"],
  ["Audit trail kept (days)", "auditRetentionDays", "events kept 60 days"],
] as const)("saves %s in range and shows the service's refusal of one outside it", async (label, field, sentence) => {
  const api = await show();
  const input = control<HTMLInputElement>(label, "input");
  change(input, "60");
  await vi.waitFor(() => expect(statusOf(input)).toBe(sentence));
  expect(api.saveAppSettings).toHaveBeenCalledExactlyOnceWith({ ...original, [field]: 60 });

  api.saveAppSettings = vi.fn(async () => { throw settingsRefusal("retention days out of range 0..3650"); });
  change(input, "4000");
  await vi.waitFor(() => expect(statusOf(input)).toBe("retention days out of range 0..3650"));
  expect(input.value).toBe("60");

  // An emptied field is not read as 0, which would switch the prune off.
  change(input, "");
  flushSync();
  expect(statusOf(input)).toBe(`${label} takes a whole number`);
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
});

it("opens from the rail's settings button and closes on Escape", async () => {
  mounted = mount(BrowserScreen, { target: document.body });
  await settle();
  // The fake starts with no bookmark, so the first-run welcome opens first.
  [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === "Not now")?.click();
  await settle();
  expect(document.querySelector('[role="dialog"]')).toBeNull();

  document.querySelector<HTMLButtonElement>('.rail button[aria-label="Settings"]')!.click();
  await settle();
  const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
  expect(dialog).not.toBeNull();
  expect(document.getElementById(dialog!.getAttribute("aria-labelledby")!)?.textContent).toBe("Settings");

  (document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await settle();
  expect(document.querySelector('[role="dialog"]')).toBeNull();
});

// The graphics card setting is saved at once and says it applies at the
// next start, since the webview takes its drawing policy when it is made.
it("saves turning the graphics card off and says when it applies", async () => {
  const api = await show();
  const gpu = control<HTMLInputElement>("Use the graphics card", "input");
  expect(gpu.checked).toBe(true);
  gpu.checked = false;
  gpu.dispatchEvent(new Event("change", { bubbles: true }));
  await settle();
  expect(api.saveAppSettings).toHaveBeenCalledWith(expect.objectContaining({ gpuAcceleration: false }));
  expect(statusOf(gpu)).toBe("off: the window draws in software from the next start");
});
