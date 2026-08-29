import { describe, expect, it, vi } from "vitest";
import { BrowserState, suspectedSegmentStore } from "./browserState.svelte";
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

describe("container modes with the name heuristic", () => {
  it("logical groups suspected stores, hides nothing", async () => {
    const s = await connected();
    const groups = s.containerGroups;
    expect(groups.main.map((c) => c.name)).toEqual(["fixtures", "slo-parts"]);
    expect(groups.suspected.map((c) => c.name)).toEqual([
      ".file-segments",
      "confirmed_segments",
      "external-dlo-segments",
      "photos_segments",
    ]);
  });

  it("all mode shows one flat list", async () => {
    const s = await connected();
    s.mode = "all";
    expect(s.containerGroups.main).toHaveLength(6);
    expect(s.containerGroups.suspected).toHaveLength(0);
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

describe("suspectedSegmentStore heuristic", () => {
  it("matches sidecar and dotted names, not lookalikes", () => {
    expect(suspectedSegmentStore("photos_segments")).toBe(true);
    expect(suspectedSegmentStore(".file-segments")).toBe(true);
    expect(suspectedSegmentStore("my-segments")).toBe(true);
    expect(suspectedSegmentStore("segments-of-society")).toBe(false);
    expect(suspectedSegmentStore("fixtures")).toBe(false);
  });
});
