import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { newFakeApi } from "./fakeApi";
import { v1Offers } from "../../lib/v1.svelte";
import BrowserScreen from "./BrowserScreen.svelte";

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

// The affordance flags are module state (ADR 0082 feeds them from what the
// process reports), so each case is restored to what the release ships.
const shipped = { ...v1Offers };

let component: ReturnType<typeof mount> | null = null;
afterEach(async () => {
  if (component) await unmount(component);
  component = null;
  document.body.innerHTML = "";
  vi.restoreAllMocks();
  Object.assign(v1Offers, shipped);
});

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function labels(): string[] {
  return [...document.querySelectorAll("button")].map((b) => b.textContent?.trim() ?? "");
}

async function connectedScreen(enabled: string[] = [], unrecognised: string[] = []) {
  Object.assign(api, newFakeApi());
  if (enabled.length > 0 || unrecognised.length > 0) {
    const info = api.info;
    api.info = async () => ({ ...(await info()), enabledCapabilities: enabled, unrecognisedCapabilities: unrecognised });
  }
  await api.saveBookmark({ id: "", displayName: "V1 fixture", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  const root = document.createElement("div"); document.body.append(root);
  component = mount(BrowserScreen, { target: root });
  flushSync(); await settle();
  document.querySelector<HTMLButtonElement>('[aria-label="V1 fixture"]')!.click(); await settle();
  [...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((b) => b.querySelector(".container-name")?.textContent?.trim() === "fixtures")!
    .click();
  await settle();
  return root;
}

it("keeps download, upload, copy and bulk delete and withholds benchmark", async () => {
  await connectedScreen();
  const shown = labels();
  expect(shown).toContain("Download…");
  expect(shown).toContain("Upload…");
  // Copy returned on 31 October 2026 (ADR 0052 decision 12): its control ships.
  expect(shown).toContain("Copy to…");
  // Bulk delete returned on 1 November 2026 (ADR 0052 decision 14): its scope
  // control ships like the other returns rather than waiting for the switch.
  expect(shown).toContain("Delete scope…");
  document.querySelector<HTMLButtonElement>(".drawer .bar")!.click();
  flushSync();
  expect(labels()).not.toContain("Benchmark…");
});

// LocalTransferDialog serves both of its modes in the release again, so each
// control must open its OWN mode: a shared dialog that opened the wrong half
// would still satisfy a test that only asserted a dialog appeared.
it("opens each local transfer control in its own mode", async () => {
  await connectedScreen();
  [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Download…")!.click();
  await settle();
  let dialog = document.querySelector('[role="dialog"]');
  expect(dialog?.getAttribute("aria-label")).toBe("Download");
  expect(dialog?.textContent).toContain("Choose a destination directory");
  expect(dialog?.textContent).not.toContain("Choose files");
  document.querySelector<HTMLButtonElement>('[role="dialog"] button.close')!.click();
  await settle();
  [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Upload…")!.click();
  await settle();
  dialog = document.querySelector('[role="dialog"]');
  expect(dialog?.getAttribute("aria-label")).toBe("Upload");
  expect(dialog?.textContent).toContain("Choose files");
  expect(dialog?.textContent).not.toContain("Choose a destination directory");
});

it("offers manifest deletion on a DLO and marker creation on an implied prefix", async () => {
  await connectedScreen();
  const dlo = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((el) => el.querySelector(".name")?.textContent?.trim() === "external-dlo")!;
  dlo.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
  // Manifest deletion is a returned capability (ADR 0052 decision 13), so it
  // is offered on a manifest.
  expect(labels()).toContain("Delete manifest…");
  const prefix = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((el) => el.querySelector(".name")?.textContent?.trim() === "implied/")!;
  prefix.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
  // Creation is offered on an implied prefix; such a prefix has no marker
  // object, so there is nothing for marker deletion to offer here regardless.
  expect(labels()).toContain("Create marker object");
  expect(labels()).not.toContain("Delete marker object");
});

// ADR 0082: a capability the operator switched on for this process is
// offered and named as unproven; nothing else is offered with it.
it("offers a switched-on capability and says it is unproven", async () => {
  await connectedScreen(["Benchmark"]);
  const banner = document.querySelector('[role="alert"].unproven-banner');
  expect(banner?.textContent).toContain("Unproven:");
  expect(banner?.textContent).toContain("Benchmark");
  expect(banner?.textContent).toContain("not finished");
  const shown = labels();
  expect(shown).toContain("Download…");
  // Kept by the release rather than by this switch, and not described as
  // unproven: the banner names only what the operator turned on.
  expect(shown).toContain("Upload…");
  expect(banner?.textContent).not.toContain("Upload");
  // Copy is kept by the release, so its control is present whether or not the
  // switch named it, and it is not in the unproven banner.
  expect(shown).toContain("Copy to…");
  expect(banner?.textContent).not.toContain("Copy");
  // Bulk delete is kept now too, so its scope control ships and is not called
  // unproven; the banner names only benchmark, which the operator turned on.
  expect(shown).toContain("Delete scope…");
  expect(banner?.textContent).not.toContain("Bulk delete");
  // The benchmark control is the one this switch turns on; it lives in the
  // jobs drawer.
  document.querySelector<HTMLButtonElement>(".drawer .bar")!.click();
  flushSync();
  expect(labels()).toContain("Benchmark…");
});

// A token that names no capability is reported and offers nothing.
it("names an unrecognised token and offers nothing for it", async () => {
  await connectedScreen([], ["copz"]);
  const banner = document.querySelector('[role="alert"].unproven-banner');
  expect(banner?.textContent).toContain("Ignored, not a capability: copz");
  expect(banner?.textContent).not.toContain("Unproven:");
  // Copy is kept, so its control stays present and an unrecognised token
  // changes nothing about it.
  expect(labels()).toContain("Copy to…");
});

// With nothing switched on there is no banner at all.
it("shows no banner in an ordinary run", async () => {
  await connectedScreen();
  expect(document.querySelector(".unproven-banner")).toBeNull();
});
