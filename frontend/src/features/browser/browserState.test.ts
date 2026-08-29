import { describe, expect, it, vi } from "vitest";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import type { BookmarkDTO } from "../../lib/api";

function bookmark(): BookmarkDTO {
  return {
    id: "",
    displayName: "Mock",
    authUrl: "http://mock/auth/v1.0",
    username: "test:tester",
    storageUrlOverride: "",
    tlsMode: "system",
    hasCredential: false,
  };
}

async function connected(): Promise<BrowserState> {
  const s = new BrowserState(newFakeApi());
  const saved = await s.saveBookmark(bookmark(), "testing");
  await s.connect(saved.id);
  return s;
}

describe("bookmarks and connect", () => {
  it("saves, lists, and connects", async () => {
    const s = await connected();
    expect(s.bookmarks).toHaveLength(1);
    expect(s.account?.name).toBe("AUTH_demo");
    expect(s.containers.length).toBe(6);
    expect(s.connectedBookmarkId).not.toBeNull();
  });

  it("connect failure surfaces the error and stays disconnected", async () => {
    const s = new BrowserState(newFakeApi());
    const saved = await s.saveBookmark(bookmark(), ""); // no credential
    await expect(s.connect(saved.id)).rejects.toThrow(/credential/);
    expect(s.connectError).toMatch(/credential/);
    expect(s.account).toBeNull();
  });
});

describe("container modes over the service classification (§52/§24.5)", () => {
  it("logical hides confirmed stores and groups suspected ones", async () => {
    const s = await connected();
    const groups = s.containerGroups;
    expect(groups.main.map((c) => c.name)).toEqual(["fixtures"]);
    expect(groups.suspected.map((c) => c.name)).toEqual([
      ".file-segments",
      "photos_segments",
    ]);
    expect(groups.hiddenBackstage).toBe(3);
  });

  it("all mode shows one flat list", async () => {
    const s = await connected();
    s.mode = "all";
    expect(s.containerGroups.main).toHaveLength(6);
    expect(s.containerGroups.suspected).toHaveLength(0);
    expect(s.containerGroups.hiddenBackstage).toBe(0);
  });
});

describe("navigation", () => {
  it("opens a container and drills prefixes", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    expect(s.entries.length).toBeGreaterThan(0);

    const implied = s.entries.find((e) => e.kind === "prefix" && e.name === "implied/");
    await s.activate(implied!);
    expect(s.prefix).toBe("implied/");
    expect(s.entries.map((e) => e.name)).toEqual(["implied/child.txt"]);

    await s.up();
    expect(s.prefix).toBe("");
    await s.up();
    expect(s.containerName).toBeNull();
  });

  it("selecting an object fetches its HEAD detail", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    const idx = s.entries.findIndex((e) => e.name === "external-dlo");
    s.select(idx);
    expect(s.detailLoading).toBe(true);
    await vi.waitFor(() => expect(s.detailLoading).toBe(false));
    expect(s.objectDetail?.class).toBe("dlo");
    expect(s.objectDetail?.manifest).toBe("external-dlo-segments/wedding-video/");
  });

  it("selecting a prefix clears object detail and classifies it", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "external-dlo"));
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());

    s.select(s.entries.findIndex((e) => e.name === "folder-marker/"));
    expect(s.objectDetail).toBeNull();
    await vi.waitFor(() => expect(s.prefixDetail).not.toBeNull());
    expect(s.prefixDetail?.markerBacked).toBe(true);

    s.select(s.entries.findIndex((e) => e.name === "implied/"));
    await vi.waitFor(() => expect(s.prefixDetail).not.toBeNull());
    expect(s.prefixDetail?.markerBacked).toBe(false);
    expect(s.prefixDetail?.selfObject).toBeNull();

    // The folder-suit trap: a real object at the prefix name surfaces.
    s.select(s.entries.findIndex((e) => e.name === "fake/"));
    await vi.waitFor(() => expect(s.prefixDetail).not.toBeNull());
    expect(s.prefixDetail?.markerBacked).toBe(false);
    expect(s.prefixDetail?.selfObject?.contentLength).toBe(21);
  });

  it("filter narrows visible entries without refetching", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.filter = "external";
    expect(s.visibleEntries.map((e) => e.name)).toEqual(["external-dlo", "external-slo"]);
    s.filter = "";
    expect(s.visibleEntries.length).toBe(s.entries.length);
  });
});

describe("selection-triggered detail (§14.2)", () => {
  it("opening a container fetches its HEAD detail", async () => {
    const s = await connected();
    await s.openContainer("photos_segments");
    await vi.waitFor(() => expect(s.containerDetail).not.toBeNull());
    expect(s.containerDetail?.storagePolicy).toBe("ec42");
    expect(s.containerDetail?.class).toBe("suspected");
  });

  it("selecting an SLO fetches its exact manifest for the segment table", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "external-slo"));
    await vi.waitFor(() => expect(s.sloManifest).not.toBeNull());
    expect(s.sloManifest?.entries).toHaveLength(3);
    expect(s.sloManifest?.entries?.[0].container).toBe("slo-parts");

    // Moving selection clears the table with the rest of the detail.
    s.select(s.entries.findIndex((e) => e.name === "empty-object"));
    expect(s.sloManifest).toBeNull();
  });
});
