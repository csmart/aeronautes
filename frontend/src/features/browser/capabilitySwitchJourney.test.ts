// The capability switch through a mounted browser screen (ADR 0082):
// what a build offers, what it says about it, and what it must keep
// disclosing. Built by the reviewer during a review and retained as
// maintained coverage: the inline-SLO case is the regression for
// a review finding — the manifest-only path discloses the inline data it will
// destroy from the manifest the inspector read, not from the ownership
// check that bulk deletion owns.
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

// The transfer buttons name what they act on (ADR 0108: "Download folder…",
// "Copy selected…"); these tests are about which controls exist and what they
// open, so they know them as "Download…" and "Copy to…". The wording itself
// is tested in transferLabels.test.ts.
function transferLabel(text: string): string {
  return text.replace(/^Download (container|folder|object|selected)…$/, "Download…").replace(/^Copy (container|folder|object|selected)…$/, "Copy to…");
}

function button(label: string) {
  const found = [...document.querySelectorAll("button")].find((el) => transferLabel(el.textContent?.trim() ?? "") === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}

function labels() {
  return [...document.querySelectorAll("button")].map((el) => transferLabel(el.textContent?.trim() ?? ""));
}

async function prepare(enabled: string[] = [], unrecognised: string[] = []) {
  Object.assign(api, newFakeApi());
  const info = { ...(await api.info()), enabledCapabilities: enabled, unrecognisedCapabilities: unrecognised };
  api.info = async () => info;
  await api.saveBookmark({ id: "", displayName: "Reviewer fixture", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false,
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

it("keeps a deferred control hidden until Info settles while the kept ones are there at once", async () => {
  const info = await prepare(["Benchmark"]);
  let resolve!: (value: Info) => void;
  api.info = () => new Promise<Info>((yes) => { resolve = yes; });
  await render();
  // Benchmark is the one capability still behind the switch, so its banner
  // waits for Info to name it; nothing switch-fed appears before then.
  expect(document.querySelector(".unproven-banner")).toBeNull();
  // A capability the release KEEPS does not wait for Info: copy was returned
  // on 31 October 2026 and upload on 16 October, so their controls are there
  // before the process has said anything, which is the difference the return
  // makes.
  expect(labels()).toContain("Copy to…");
  expect(labels()).toContain("Upload…");
  resolve(info);
  await settle();
  expect(document.querySelector(".unproven-banner")?.textContent).toContain("Benchmark");
  await connect();
  // Bulk delete was returned on 1 November 2026, so its scope control is a
  // kept one and present now without any switch naming it.
  expect(labels()).toContain("Empty or delete container…");
  // The switched-on deferred control appears once Info has named it; it lives
  // in the jobs drawer, and the banner keeps saying its safety is not finished.
  document.querySelector<HTMLButtonElement>(".drawer .bar")!.click();
  await settle();
  expect(labels()).toContain("Benchmark…");
  expect(document.querySelector(".unproven-banner")?.textContent).toContain("not finished");
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
  expect(labels()).toContain("Copy to…");
  // Bulk delete is kept now, so its scope control survives an Info failure
  // like the other returns; benchmark, still behind the switch, does not.
  expect(labels()).toContain("Empty or delete container…");
  document.querySelector<HTMLButtonElement>(".drawer .bar")!.click();
  await settle();
  expect(labels()).not.toContain("Benchmark…");
});

it("accepts the null lists produced by the ordinary Go Info response", async () => {
  const info = await prepare();
  api.info = async () => ({ ...info, enabledCapabilities: null, unrecognisedCapabilities: null });
  await render();
  await connect();
  expect(document.querySelector(".unproven-banner")).toBeNull();
  expect(labels()).toContain("Upload…");
  expect(labels()).toContain("Copy to…");
});

it("renders all enabled capability names and keeps unknown text inert", async () => {
  const enabled = ["Benchmark"];
  const unknown = '<img src=x onerror="throw 1">';
  await prepare(enabled, [unknown, "__proto__", "mirror"]);
  await render();
  await connect();
  const banner = document.querySelector(".unproven-banner");
  for (const name of enabled) expect(banner?.textContent).toContain(name);
  // An unrecognised token is shown verbatim and never parsed as markup, so a
  // hostile capability name cannot run script through the banner.
  expect(banner?.textContent).toContain(unknown);
  expect(banner?.querySelector("img")).toBeNull();
  expect(labels()).toContain("Upload…");
  expect(labels()).toContain("Copy to…");
  // Bulk delete, marker and manifest deletion are all kept now, so their
  // controls are present whether or not the switch named them.
  expect(labels()).toContain("Empty or delete container…");
  document.querySelector<HTMLButtonElement>(".drawer .bar")!.click();
  await settle();
  expect(labels()).toContain("Benchmark…");
  expect(v1Offers.rerun).toBe(true);
  expect(v1Offers.markerDelete).toBe(true);
  expect(v1Offers.manifestDelete).toBe(true);
});

it("retains the inline-SLO destruction disclosure on the manifest-only path", async () => {
  await prepare(["Marker and manifest deletion"]);
  const originalSLO = api.sloManifest;
  api.sloManifest = async (...args) => {
    const value = await originalSLO(...args);
    return { ...value, entries: [...(value.entries ?? []), {
      kind: "data", container: "", object: "", etag: "", size: 0,
      range: "", nestedSlo: false, dataSize: 10,
    }] };
  };
  api.checkSegmentOwnership = vi.fn(async () => (
    { class: "slo", target: "slo-parts/backup/", proven: false, segments: 3, inlineEntries: 1, reviewed: { class: "slo", fingerprint: "slo:0:etag", length: 0, etag: "" } }
  ));
  await render();
  await connect();
  const filter = document.querySelector<HTMLInputElement>('input[placeholder="filter this list…"]');
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
  // Bulk delete returned on 1 November 2026 (ADR 0052 decision 14), so the
  // segment-deletion option is offered, and it starts ticked (ADR 0120
  // decision 3), which asks its ownership check once.
  const option = dialog?.querySelector<HTMLInputElement>('input[type="checkbox"]') ?? null;
  expect(option?.checked).toBe(true);
  expect(api.checkSegmentOwnership).toHaveBeenCalledOnce();
  // Unticked, the deletion is manifest-only and says what that destroys.
  option!.click(); await settle();
  expect(button("Delete manifest only").disabled).toBe(false);
  expect(dialog?.textContent).toContain("1 inline data entry");
  expect(dialog?.textContent).toContain("will be destroyed");
  expect(api.checkSegmentOwnership).toHaveBeenCalledOnce();
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
  const filter = document.querySelector<HTMLInputElement>('input[placeholder="filter this list…"]');
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
  // The manifest-only deletion, reached by unticking the segments option.
  dialog!.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click(); await settle();
  expect(dialog?.textContent).toContain("unknown here");
  expect(dialog?.textContent).toContain("destroyed with it");
  expect(dialog?.textContent).not.toContain("Every referenced segment object survives");
});
