import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import ContainerEditPanel from "./ContainerEditPanel.svelte";
import type { FeatureStatesDTO } from "../../lib/api";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

async function settle() {
  for (let i = 0; i < 12; i++) {
    await Promise.resolve();
    flushSync();
  }
}

function button(root: HTMLElement, label: string) {
  const found = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error("Missing button: " + label);
  return found;
}

function edit(input: HTMLInputElement, value: string) {
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

async function show(readAcl = "", meta: Record<string, string> = { Keep: "untouched" }, features?: FeatureStatesDTO) {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.account = { name: "AUTH_source", containers: 1, objects: 1, bytesUsed: 0,
    quotaBytes: 0, meta: {}, tempUrlKeySlots: [], policyUsage: [], rawHeaders: {}, scope: { bookmarkId: "source", generation: 1 }, editableFingerprint: "" };
  await browser.loadContainers();
  await browser.openContainer("fixtures");
  browser.containerDetail = { ...browser.containerDetail!, meta, readAcl };
  if (features) {
    browser.capabilities = { scope: { bookmarkId: "source", generation: 1 }, source: "advertised", maxFileSize: 0, storagePolicies: [],
      versioningFlags: ["x-versions-location", "x-history-location"], sloSupported: true, features };
  }
  const save = vi.spyOn(api, "updateContainer").mockRejectedValue(new Error("not written in this test"));
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(ContainerEditPanel, { target: root, props: { browser, detail: browser.containerDetail!, onDone: vi.fn() } }));
  flushSync();
  // The three tick boxes, in the order the panel shows them: world read,
  // listing, static website.
  const ticks = () => [...root.querySelectorAll<HTMLInputElement>(".preset input")];
  const readAclInput = () => {
    if (!root.querySelector(".acl .rawline")) {
      // The first "raw" toggle belongs to the Read ACL editor.
      button(root, "raw").click();
      flushSync();
    }
    return root.querySelector<HTMLInputElement>(".acl .rawline")!;
  };
  const rows = () =>
    [...root.querySelectorAll<HTMLDivElement>(".editor .row")].map((row) => ({
      key: row.querySelector<HTMLInputElement>(".key")!.value,
      value: row.querySelector<HTMLInputElement>(".val")!.value,
    }));
  const payload = () => {
    const request = save.mock.calls[0]?.[3];
    if (!request) throw new Error("No container save request");
    return request;
  };
  // The three CORS fields, in the order the panel shows them: origins,
  // preflight cache, exposed headers.
  const corsInputs = () => [...root.querySelectorAll<HTMLInputElement>(".cors input")];
  return { root, save, ticks, readAclInput, rows, payload, corsInputs };
}

describe("CORS section (ADR 0101)", () => {
  it("typing an origin adds its row, and the save carries the row normalised", async () => {
    const f = await show();
    edit(f.corsInputs()[0], "  https://app.example   * ");
    expect(f.rows()).toContainEqual({ key: "Access-Control-Allow-Origin", value: "  https://app.example   * " });
    button(f.root, "Save").click();
    await settle();
    expect(f.payload().meta).toEqual({ Keep: "untouched", "Access-Control-Allow-Origin": "https://app.example *" });
  });

  it("a malformed value in a changed field blocks the save and names the problem", async () => {
    const f = await show();
    edit(f.corsInputs()[1], "soon");
    button(f.root, "Save").click();
    await settle();
    expect(f.save).not.toHaveBeenCalled();
    expect(f.root.textContent).toContain("whole number of seconds");
  });

  it("an untouched row the section would refuse is shown and submitted verbatim", async () => {
    const f = await show("", { Keep: "untouched", "Access-Control-Max-Age": "soon" });
    expect(f.corsInputs()[1].value).toBe("soon");
    edit(f.root.querySelector<HTMLInputElement>('input[type="number"]')!, "10");
    button(f.root, "Save").click();
    await settle();
    expect(f.payload()).toMatchObject({ quotaBytes: 10, meta: { Keep: "untouched", "Access-Control-Max-Age": "soon" } });
  });

  it("a raw edit after a structured one supersedes it: no check, no normalisation", async () => {
    const f = await show();
    edit(f.corsInputs()[1], "700");
    edit(f.corsInputs()[0], "https://app.example");
    const rawRow = (key: string) =>
      [...f.root.querySelectorAll<HTMLDivElement>(".editor .row")].find((row) => row.querySelector<HTMLInputElement>(".key")!.value === key)!;
    edit(rawRow("Access-Control-Max-Age").querySelector<HTMLInputElement>(".val")!, "soon");
    edit(rawRow("Access-Control-Allow-Origin").querySelector<HTMLInputElement>(".val")!, "https://a.example   https://b.example");
    button(f.root, "Save").click();
    await settle();
    expect(f.payload().meta).toEqual({
      Keep: "untouched",
      "Access-Control-Max-Age": "soon",
      "Access-Control-Allow-Origin": "https://a.example   https://b.example",
    });
  });

  it("a raw edit hands the row back even when its value returns to what the field wrote", async () => {
    const f = await show();
    edit(f.corsInputs()[1], "soon");
    const raw = () =>
      [...f.root.querySelectorAll<HTMLDivElement>(".editor .row")]
        .find((row) => row.querySelector<HTMLInputElement>(".key")!.value === "Access-Control-Max-Age")!
        .querySelector<HTMLInputElement>(".val")!;
    edit(raw(), "later");
    edit(raw(), "soon");
    button(f.root, "Save").click();
    await settle();
    expect(f.payload().meta).toEqual({ Keep: "untouched", "Access-Control-Max-Age": "soon" });
  });

  it("emptying a field removes its row", async () => {
    const f = await show("", { Keep: "untouched", "Access-Control-Allow-Origin": "*" });
    expect(f.corsInputs()[0].value).toBe("*");
    edit(f.corsInputs()[0], "");
    expect(f.rows()).toEqual([{ key: "Keep", value: "untouched" }]);
    button(f.root, "Save").click();
    await settle();
    expect(f.payload().meta).toEqual({ Keep: "untouched" });
  });
});

describe("container presets", () => {
  it("ticking world read writes the exact ACL entry and still requires the acknowledgement", async () => {
    const f = await show();
    const [read, listing] = f.ticks();
    expect(read.checked).toBe(false);
    expect(listing.disabled).toBe(true);
    read.click();
    flushSync();
    expect(f.readAclInput().value).toBe(".r:*");
    expect(f.root.querySelector(".public-ack input")).not.toBeNull();
    button(f.root, "Save").click();
    await settle();
    expect(f.save).not.toHaveBeenCalled();
    expect(f.root.textContent).toContain("Tick the box below to confirm");
    f.root.querySelector<HTMLInputElement>(".public-ack input")!.click();
    flushSync();
    f.ticks()[1].click();
    flushSync();
    expect(f.readAclInput().value).toBe(".r:*,.rlistings");
    button(f.root, "Save").click();
    await settle();
    expect(f.payload()).toMatchObject({ readAcl: ".r:*,.rlistings", ackPublic: true });
  });

  it("unticking world read also drops the listing entry and keeps other grants", async () => {
    const f = await show(".r:*,.rlistings,account:user");
    const [read, listing] = f.ticks();
    expect(read.checked).toBe(true);
    expect(listing.checked).toBe(true);
    read.click();
    flushSync();
    expect(f.readAclInput().value).toBe("account:user");
    expect(f.ticks()[1].checked).toBe(false);
    expect(f.root.querySelector(".public-ack")).toBeNull();
  });

  it("a hand-typed ACL drives the ticks, and a referrer grant is not world read", async () => {
    const f = await show();
    const input = f.readAclInput();
    edit(input, " .r:* , .rlistings ");
    expect(f.ticks()[0].checked).toBe(true);
    expect(f.ticks()[1].checked).toBe(true);
    edit(input, ".r:*.example.com");
    expect(f.ticks()[0].checked).toBe(false);
    expect(f.ticks()[1].checked).toBe(false);
    // The backend gate does not treat a referrer grant as world read, so
    // the editor must not demand the acknowledgement for it either.
    expect(f.root.querySelector(".public-ack")).toBeNull();
    button(f.root, "Save").click();
    await settle();
    expect(f.payload()).toMatchObject({ readAcl: ".r:*.example.com", ackPublic: false });
  });

  it("ticking the website adds its keys as metadata rows, and the rows drive the tick", async () => {
    const f = await show();
    expect(f.ticks()[2].checked).toBe(false);
    f.ticks()[2].click();
    flushSync();
    expect(f.rows()).toEqual([
      { key: "Keep", value: "untouched" },
      { key: "Web-Index", value: "index.html" },
      { key: "Web-Listings", value: "true" },
    ]);
    expect(f.root.textContent).toContain('needs "Anyone can read objects" too');
    button(f.root, "Save").click();
    await settle();
    expect(f.payload().meta).toEqual({ Keep: "untouched", "Web-Index": "index.html", "Web-Listings": "true" });

    f.ticks()[2].click();
    flushSync();
    expect(f.rows()).toEqual([{ key: "Keep", value: "untouched" }]);

    button(f.root, "+ add key").click();
    flushSync();
    const added = [...f.root.querySelectorAll<HTMLDivElement>(".editor .row")].at(-1)!;
    edit(added.querySelector<HTMLInputElement>(".key")!, "web-error");
    edit(added.querySelector<HTMLInputElement>(".val")!, "error.html");
    expect(f.ticks()[2].checked).toBe(true);
  });

  it("opens with the ticks the container's current values imply", async () => {
    const f = await show(".r:*", { "Web-Index": "index.html" });
    const [read, listing, web] = f.ticks();
    expect(read.checked).toBe(true);
    expect(listing.checked).toBe(false);
    expect(listing.disabled).toBe(false);
    expect(web.checked).toBe(true);
    expect(f.root.textContent).not.toContain('needs "Anyone can read objects"');
  });
});

// ADR 0124: an editor whose feature the store's /info was read without is
// greyed with the reason; supported, or /info unread, greys nothing.
describe("what the store does not offer", () => {
  const all = (state: string): FeatureStatesDTO => ({ tempurl: state, staticweb: state, containerQuotas: state });

  it("greys the static website preset and the quotas, each with its reason", async () => {
    const f = await show("", { Keep: "untouched" }, all("unsupported"));
    const website = f.ticks()[2];
    expect(website.disabled).toBe(true);
    const quotas = [...f.root.querySelectorAll<HTMLInputElement>('input[type="number"]')];
    expect(quotas.map((q) => q.disabled)).toEqual([true, true]);
    const reasons = [...f.root.querySelectorAll(".not-offered")].map((p) => p.textContent);
    expect(reasons).toEqual([
      "This store's /info does not list staticweb: it would keep this setting and serve no website.",
      "This store's /info does not list container_quotas: it would keep these quotas and not enforce them.",
    ]);
  });

  it.each(["supported", "unknown"])("offers both when the feature is %s", async (state) => {
    const f = await show("", { Keep: "untouched" }, all(state));
    expect(f.ticks()[2].disabled).toBe(false);
    expect([...f.root.querySelectorAll<HTMLInputElement>('input[type="number"]')].map((q) => q.disabled)).toEqual([false, false]);
    expect(f.root.querySelector(".not-offered")).toBeNull();
  });
});
