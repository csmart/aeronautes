// The capability switch through a mounted browser screen (ADR 0082):
// what a build offers, what it says about it, and what it must keep
// disclosing when one capability is switched on without another. Built
// by the reviewer for db:736 and retained as maintained coverage: the
// inline-SLO case is the regression for finding 2, and its paired
// control with bulk delete also enabled is what distinguishes a missing
// disclosure from a missing capability.
import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, Info } from "../../lib/api";
import { v1Offers } from "../../lib/v1.svelte";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));
const shipped = { ...v1Offers };
let component: ReturnType<typeof mount> | null = null;

afterEach(async () => {
  if (component) await unmount(component);
  component = null;
  document.body.innerHTML = "";
  Object.assign(v1Offers, shipped);
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 24; i++) { await Promise.resolve(); flushSync(); }
}

function button(label: string) {
  const found = [...document.querySelectorAll("button")].find((el) => el.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

function labels() {
  return [...document.querySelectorAll("button")].map((el) => el.textContent?.trim());
}

async function prepare(enabled: string[] = [], unrecognised: string[] = []) {
  Object.assign(api, newFakeApi());
  const info = { ...(await api.info()), enabledCapabilities: enabled, unrecognisedCapabilities: unrecognised };
  api.info = async () => info;
  await api.saveBookmark({ id: "", displayName: "Reviewer fixture", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false,
    hasCredential: false }, "test-only");
  return info;
}

async function render() {
  const target = document.createElement("div");
  document.body.append(target);
  component = mount(BrowserScreen, { target });
  flushSync();
  await settle();
}

async function connect() {
  const bookmark = document.querySelector<HTMLButtonElement>('[aria-label="Reviewer fixture"]');
  expect(bookmark).not.toBeNull();
  bookmark!.click();
  await settle();
  const container = [...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((el) => el.querySelector(".container-name")?.textContent?.trim() === "fixtures");
  expect(container).toBeDefined();
  container!.click();
  await settle();
}

it("keeps deferred controls hidden until Info settles and then opens the offered copy", async () => {
  const info = await prepare(["Copy"]);
  let resolve!: (value: Info) => void;
  api.info = () => new Promise<Info>((yes) => { resolve = yes; });
  await render();
  expect(labels()).not.toContain("Copy to…");
  // A capability the release KEEPS does not wait for Info: upload was
  // returned on 16 October 2026, so its control is there before the process
  // has said anything, which is the difference the return makes.
  expect(labels()).toContain("Upload…");
  expect(document.querySelector(".unproven-banner")).toBeNull();
  resolve(info);
  await settle();
  expect(document.querySelector(".unproven-banner")?.textContent).toContain("Copy");
  expect(button("Copy to…").disabled).toBe(true);
  await connect();
  expect(button("Copy to…").disabled).toBe(false);
  button("Copy to…").click();
  await settle();
  expect(document.querySelector('[role="dialog"]')?.getAttribute("aria-label")).toBe("Transfer");
  expect(document.querySelector(".unproven-banner")?.textContent).toContain("not finished");
  expect(labels()).not.toContain("Delete scope…");
});

it("keeps deferred controls closed and reports an Info failure while browsing still works", async () => {
  await prepare();
  api.info = vi.fn().mockRejectedValue(new Error("Info unavailable"));
  await render();
  expect(document.body.textContent).toContain("service bridge unavailable");
  expect(document.querySelector(".unproven-banner")).toBeNull();
  await connect();
  // Browsing AND the capabilities the release keeps survive an Info failure;
  // only the switched-on ones are lost with the answer that would name them.
  expect(labels()).toContain("Download…");
  expect(labels()).toContain("Upload…");
  expect(labels()).not.toContain("Copy to…");
  expect(labels()).not.toContain("Delete scope…");
});

it("accepts the null lists produced by the ordinary Go Info response", async () => {
  const info = await prepare();
  api.info = async () => ({ ...info, enabledCapabilities: null, unrecognisedCapabilities: null });
  await render();
  await connect();
  expect(document.querySelector(".unproven-banner")).toBeNull();
  expect(labels()).toContain("Upload…");
  expect(labels()).not.toContain("Copy to…");
});

it("renders all enabled capability names and keeps unknown text inert", async () => {
  const enabled = ["Copy", "Bulk delete", "Marker and manifest deletion", "Benchmark"];
  const unknown = '<img src=x onerror="throw 1">';
  await prepare(enabled, [unknown, "__proto__", "mirror"]);
  await render();
  await connect();
  const banner = document.querySelector(".unproven-banner");
  for (const name of enabled) expect(banner?.textContent).toContain(name);
  expect(banner?.textContent).toContain(unknown);
  expect(banner?.querySelector("img")).toBeNull();
  expect(labels()).toContain("Upload…");
  expect(labels()).toContain("Copy to…");
  expect(labels()).toContain("Delete scope…");
  document.querySelector<HTMLButtonElement>(".drawer .bar")!.click();
  await settle();
  expect(labels()).toContain("Benchmark…");
  expect(v1Offers.rerun).toBe(true);
  expect(v1Offers.markerDelete).toBe(true);
  expect(v1Offers.manifestDelete).toBe(true);
});

it.each([false, true])("retains inline-SLO destruction disclosure with Bulk delete enabled=%s", async (bulkEnabled) => {
  const enabled = ["Marker and manifest deletion", ...(bulkEnabled ? ["Bulk delete"] : [])];
  await prepare(enabled);
  const originalSLO = api.sloManifest;
  api.sloManifest = async (...args) => {
    const value = await originalSLO(...args);
    return { ...value, entries: [...(value.entries ?? []), {
      kind: "data", container: "", object: "", etag: "", size: 0,
      range: "", nestedSlo: false, dataSize: 10,
    }] };
  };
  // This mirrors the production gate: CheckSegmentOwnership belongs to
  // Bulk delete, while DeleteManifest belongs to marker/manifest editing.
  api.checkSegmentOwnership = vi.fn(async () => {
    if (!bulkEnabled) throw new Error("Bulk delete is not available in this release: it is deferred until its safety is proven, and this build does not offer it");
    return { class: "slo", target: "slo-parts/backup/", proven: false, segments: 3, inlineEntries: 1 };
  });
  await render();
  await connect();
  const filter = document.querySelector<HTMLInputElement>('input[placeholder="filter loaded rows…"]');
  expect(filter).not.toBeNull();
  filter!.value = "external-slo";
  filter!.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
  const row = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((el) => el.querySelector(".name")?.firstChild?.textContent?.trim() === "external-slo");
  expect(row).toBeDefined();
  row!.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
  await settle();
  expect(document.body.textContent).toContain("(inline data)");
  button("Delete manifest…").click();
  await settle();
  const dialog = document.querySelector('[role="alertdialog"][aria-label="Delete manifest"]');
  expect(dialog).not.toBeNull();
  // The manifest-only path never asks the ownership check, which is
  // classified with bulk deletion; the disclosure comes from the manifest
  // the inspector already read (ADR 0094 decision 5).
  expect(api.checkSegmentOwnership).not.toHaveBeenCalled();
  expect(button("Delete manifest only").disabled).toBe(false);
  expect(dialog?.textContent).toContain("1 inline data entry");
  expect(dialog?.textContent).toContain("will be destroyed");
  const option = dialog?.querySelector<HTMLInputElement>('input[type="checkbox"]') ?? null;
  expect(option !== null).toBe(bulkEnabled);
  if (option) {
    option.click(); await settle();
    expect(api.checkSegmentOwnership).toHaveBeenCalledOnce();
  }
});

// The fail-closed end of the same disclosure: when neither the gated
// ownership check nor the manifest read can answer, the dialog must not
// assert that everything survives. It says the answer is unknown and
// that any inline data is destroyed with the manifest.
it("never reports zero inline entries when nothing could answer", async () => {
  await prepare(["Marker and manifest deletion"]);
  api.sloManifest = vi.fn().mockRejectedValue(new Error("manifest unavailable"));
  api.checkSegmentOwnership = vi.fn().mockRejectedValue(
    new Error("Bulk delete is not available in this release: it is deferred until its safety is proven, and this build does not offer it"));
  await render();
  await connect();
  const filter = document.querySelector<HTMLInputElement>('input[placeholder="filter loaded rows…"]');
  filter!.value = "external-slo";
  filter!.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
  const row = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((el) => el.querySelector(".name")?.firstChild?.textContent?.trim() === "external-slo");
  expect(row).toBeDefined();
  row!.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
  await settle();
  button("Delete manifest…").click();
  await settle();
  const dialog = document.querySelector('[role="alertdialog"][aria-label="Delete manifest"]');
  expect(dialog).not.toBeNull();
  expect(dialog?.textContent).toContain("unknown here");
  expect(dialog?.textContent).toContain("destroyed with it");
  expect(dialog?.textContent).not.toContain("Every referenced segment object survives");
});
