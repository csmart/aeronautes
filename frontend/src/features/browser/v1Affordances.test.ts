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
  document.querySelector<HTMLButtonElement>('[title="V1 fixture"]')!.click(); await settle();
  [...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((b) => b.querySelector(".container-name")?.textContent?.trim() === "fixtures")!
    .click();
  await settle();
  return root;
}

it("keeps download and withholds copy, upload and bulk delete", async () => {
  await connectedScreen();
  const shown = labels();
  expect(shown).toContain("Download…");
  expect(shown).not.toContain("Copy to…");
  expect(shown).not.toContain("Upload…");
  expect(shown).not.toContain("Delete scope…");
  document.querySelector<HTMLButtonElement>(".drawer .bar")!.click();
  flushSync();
  expect(labels()).not.toContain("Benchmark…");
});

it("opens download and does not offer upload from that dialog", async () => {
  await connectedScreen();
  [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Download…")!.click();
  await settle();
  const dialog = document.querySelector('[role="dialog"]');
  expect(dialog?.getAttribute("aria-label")).toBe("Download");
  expect(dialog?.textContent).toContain("Choose a destination directory");
  expect(dialog?.textContent).not.toContain("Choose files");
});

it("offers marker creation and withholds manifest and marker deletion", async () => {
  await connectedScreen();
  const dlo = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((el) => el.querySelector(".name")?.textContent?.trim() === "external-dlo")!;
  dlo.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
  expect(labels()).not.toContain("Delete manifest…");
  const prefix = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
    .find((el) => el.querySelector(".name")?.textContent?.trim() === "implied/")!;
  prefix.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
  // ADR 0052 decision 8: creation is offered on an implied prefix; the
  // deletion half stays withheld (mutationDialogs.test.ts covers the
  // marker-backed prefix, where the withheld button is named in place).
  expect(labels()).toContain("Create marker object");
  expect(labels()).not.toContain("Delete marker object");
});

// ADR 0082: a capability the operator switched on for this process is
// offered and named as unproven; nothing else is offered with it.
it("offers a switched-on capability and says it is unproven", async () => {
  await connectedScreen(["Upload"]);
  const banner = document.querySelector('[role="alert"].unproven-banner');
  expect(banner?.textContent).toContain("Unproven:");
  expect(banner?.textContent).toContain("Upload");
  expect(banner?.textContent).toContain("not finished");
  const shown = labels();
  expect(shown).toContain("Upload…");
  expect(shown).toContain("Download…");
  expect(shown).not.toContain("Copy to…");
  expect(shown).not.toContain("Delete scope…");
});

// A token that names no capability is reported and offers nothing.
it("names an unrecognised token and offers nothing for it", async () => {
  await connectedScreen([], ["uplaod"]);
  const banner = document.querySelector('[role="alert"].unproven-banner');
  expect(banner?.textContent).toContain("Ignored, not a capability: uplaod");
  expect(banner?.textContent).not.toContain("Unproven:");
  expect(labels()).not.toContain("Upload…");
});

// With nothing switched on there is no banner at all.
it("shows no banner in an ordinary run", async () => {
  await connectedScreen();
  expect(document.querySelector(".unproven-banner")).toBeNull();
});
