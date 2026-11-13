import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { fakeContainerDetailFailures, fakeContainerSeeds, newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

// ADR 0071: a container's settings are facts one HEAD reported. They appear
// as row badges once the container has been selected, checked or edited,
// never from listing; a world-readable open container gets a banner; a
// failed HEAD leaves the row unchecked with the reason; and facts do not
// outlive the connection generation they were observed under.

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  Object.assign(api, newFakeApi());
  fakeContainerDetailFailures.clear();
  fakeContainerSeeds.clear();
  vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockImplementation(function (this: HTMLElement) {
    return this.closest("[hidden]") ? null : this.parentElement;
  });
});
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  fakeContainerDetailFailures.clear();
  fakeContainerSeeds.clear();
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 32; i++) { await Promise.resolve(); flushSync(); }
}
function get<T extends HTMLElement = HTMLElement>(selector: string, root: ParentNode = document): T {
  const found = root.querySelector<T>(selector);
  if (!found) throw new Error(`Missing element: ${selector}`);
  return found;
}
function click(element: HTMLElement) { element.click(); flushSync(); }
function row(name: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((r) => r.querySelector(".container-name")?.firstChild?.textContent?.trim() === name);
  if (!found) throw new Error(`Missing container row ${name}`);
  return found;
}
function badges(name: string): string[] {
  return [...row(name).querySelectorAll<HTMLElement>(".setting-badge")].map((b) => b.textContent?.trim() ?? "");
}

const fixtureBookmark = { id: "", displayName: "Settings fixture", authUrl: "https://example.test/auth",
  username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false };

async function connectFixture() {
  fakeContainerSeeds.set("fixtures", { readAcl: ".r:*,.rlistings" });
  fakeContainerSeeds.set("photos_segments", { readAcl: ".r:-.example.com", writeAcl: "test2:tester2" });
  const saved = await api.saveBookmark(fixtureBookmark, "test-only");
  const component = mount(BrowserScreen, { target: document.body });
  mounted.add(component);
  flushSync(); await settle();
  click(get(`[aria-label="${fixtureBookmark.displayName}"]`)); await settle();
  // These cases read rows of the "Possible backstage" group, which starts
  // collapsed (ADR 0117).
  click(get(".group-toggle")); await settle();
  return saved;
}

describe("container settings badges", () => {
  it("shows nothing for an unchecked row and the facts once the container is selected", async () => {
    await connectFixture();
    expect(badges("fixtures")).toEqual([]);
    expect(document.querySelector(".public-banner")).toBeNull();
    click(row("fixtures")); await settle();
    expect(badges("fixtures")).toEqual(["public + listing"]);
    const banner = get(".public-banner");
    expect(banner.textContent).toContain("anyone with the URL can read objects in");
    expect(banner.textContent).toContain("and can list its object names");
    expect(get(".setting-badge.danger", row("fixtures")).title).toContain("read ACL .r:*,.rlistings");
    // A private container selected afterwards shows no banner and no badge.
    click(row(".file-segments")); await settle();
    expect(document.querySelector(".public-banner")).toBeNull();
    expect(badges(".file-segments")).toEqual([]);
    // The earlier fact stays on its row: it was observed, not listed.
    expect(badges("fixtures")).toEqual(["public + listing"]);
  }, 20_000);

  it("checks every loaded row on request, counts as it goes, and leaves a failed HEAD unchecked with its reason", async () => {
    await connectFixture();
    fakeContainerDetailFailures.add(".file-segments");
    click(get('[title="HEAD every loaded container once to show its access and feature settings"]'));
    await settle();
    expect(get(".check-status").textContent).toContain("checked 6 of 6");
    expect(badges("fixtures")).toEqual(["public + listing"]);
    expect(badges("photos_segments")).toEqual(["shared write"]);
    const unchecked = get(".setting-badge.muted", row(".file-segments"));
    expect(unchecked.textContent).toBe("unchecked");
    expect(unchecked.title).toContain("HEAD of .file-segments refused by the fixture");
    // A denial is never a badge.
    expect(badges("photos_segments")).not.toContain("referrer");
  }, 20_000);

  it("shows why the open container could not be checked instead of staying silent", async () => {
    await connectFixture();
    fakeContainerDetailFailures.add("fixtures");
    click(row("fixtures")); await settle();
    expect(document.querySelector(".public-banner")).toBeNull();
    expect(get(".settings-notice").textContent).toContain("could not be checked: HEAD of fixtures refused by the fixture");
    expect(badges("fixtures")).toEqual([]);
  }, 20_000);

  it("never shows a fact answered under another connection generation", async () => {
    await connectFixture();
    const detail = api.containerDetail;
    // A reply whose scope and fact are one generation behind this connection.
    api.containerDetail = async (name, bookmarkId) => {
      const d = await detail(name, bookmarkId);
      return { ...d, scope: { ...d.scope, generation: d.scope.generation + 1 },
        settings: { ...d.settings, generation: d.settings.generation + 1 } };
    };
    click(row("fixtures")); await settle();
    expect(badges("fixtures")).toEqual([]);
    expect(document.querySelector(".public-banner")).toBeNull();
    // ADR 0075: the detail body is refused with the fact, not shown beside it.
    expect(get(".settings-notice").textContent).toContain("does not match this connection");
    click(get('[title="HEAD every loaded container once to show its access and feature settings"]'));
    await settle();
    // The only mark is the muted "unchecked" with the reason; no fact badge.
    expect(badges("fixtures")).toEqual(["unchecked"]);
    expect(get(".setting-badge.muted", row("fixtures")).title).toContain("connection generation");
  }, 20_000);

  it("drops the facts when the bookmark connects under a new generation", async () => {
    const saved = await connectFixture();
    click(row("fixtures")); await settle();
    expect(badges("fixtures")).toEqual(["public + listing"]);
    // A saved secret is a new generation for this bookmark in the fake, as a
    // rekey is in the product; the reconnected listing carries no facts.
    await api.saveBookmark({ ...saved }, "rotated");
    click(get(`[aria-label="${fixtureBookmark.displayName}"]`)); await settle();
    expect(badges("fixtures")).toEqual([]);
    expect(document.querySelector(".public-banner")).toBeNull();
  }, 20_000);
});
