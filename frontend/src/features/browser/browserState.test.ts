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

describe("mutations (§65.2)", () => {
  it("saves an object metadata working copy and refreshes state", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    await s.goTo("normal/");
    s.select(s.entries.findIndex((e) => e.name === "normal/hello.txt"));
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());

    const edit = {
      meta: { ...s.objectDetail!.meta, Camera: "fx6" },
      contentType: "",
      deleteAt: "",
    };
    const result = await s.saveObjectMetadata(edit);
    expect(result.outcome).toBe("applied");
    expect(s.objectDetail?.meta?.Camera).toBe("fx6");
    expect(s.objectDetail?.meta?.Origin).toBe("fixture");
    expect(s.auditEvents[0]?.operation).toBe("object.metadata.update");
  });

  it("conflicts on a stale fingerprint and applies with force", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    await s.goTo("normal/");
    s.select(s.entries.findIndex((e) => e.name === "normal/hello.txt"));
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());

    s.objectDetail = { ...s.objectDetail!, editableFingerprint: "stale" };
    const edit = { meta: { Only: "mine" }, contentType: "", deleteAt: "" };

    const refused = await s.saveObjectMetadata(edit);
    expect(refused.outcome).toBe("conflict");
    expect(s.objectDetail?.meta?.Only).toBeUndefined();

    const forced = await s.saveObjectMetadata(edit, true);
    expect(forced.outcome).toBe("applied");
    expect(s.objectDetail?.meta?.Only).toBe("mine");
  });

  it("saves a container edit and rejects a gated versioning mode", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    await vi.waitFor(() => expect(s.containerDetail).not.toBeNull());

    const result = await s.saveContainer({
      meta: { Team: "ops" },
      readAcl: ".r:*",
      writeAcl: "",
      versioningMode: "history_location",
      versioningArchive: "arch",
      quotaBytes: 0,
      quotaCount: 0,
    });
    expect(result.outcome).toBe("applied");
    expect(s.containerDetail?.readAcl).toBe(".r:*");
    expect(s.containerDetail?.versioningMode).toBe("history_location");
  });

  it("creates a container gated by advertised policies and opens it", async () => {
    const s = await connected();
    await expect(
      s.createContainer({ name: "newbox", policy: "nope", meta: {} }),
    ).rejects.toThrow(/policy/);

    await s.createContainer({ name: "newbox", policy: "gold", meta: {} });
    expect(s.containerName).toBe("newbox");
    expect(s.containers.some((c) => c.name === "newbox")).toBe(true);
    expect(s.containerDetail?.storagePolicy).toBe("gold");
  });

  it("creates and deletes markers with refreshed classification", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "implied/"));
    await vi.waitFor(() => expect(s.prefixDetail).not.toBeNull());
    expect(s.prefixDetail?.markerBacked).toBe(false);

    await s.createMarker("implied/");
    expect(s.prefixDetail?.markerBacked).toBe(true);

    await s.deleteMarker("implied/");
    // Children keep the prefix alive as implied.
    expect(s.entries.some((e) => e.kind === "prefix" && e.name === "implied/")).toBe(true);
    expect(s.auditEvents[0]?.operation).toBe("marker.delete");
  });
});

describe("large-object read semantics (§65.3)", () => {
  it("selecting a DLO fetches the bounded segment preview", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "external-dlo"));
    await vi.waitFor(() => expect(s.dloSegments).not.toBeNull());
    expect(s.dloSegments?.container).toBe("external-dlo-segments");
    expect(s.dloSegments?.segments).toHaveLength(3);
    expect(s.dloSegments?.hasMore).toBe(false);
  });

  it("jumps backstage with a banner and returns clean", async () => {
    const s = await connected();
    await s.openBackstage("external-dlo-segments", "wedding-video/", "fixtures/external-dlo");
    expect(s.containerName).toBe("external-dlo-segments");
    expect(s.prefix).toBe("wedding-video/");
    expect(s.backstageFrom).toBe("fixtures/external-dlo");
    expect(s.entries).toHaveLength(3);

    await s.openContainer("fixtures");
    expect(s.backstageFrom).toBeNull();
  });

  it("deletes only the manifest and refreshes the list", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "external-dlo"));
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());

    await s.deleteManifest();
    expect(s.entries.some((e) => e.name === "external-dlo")).toBe(false);
    expect(s.auditEvents[0]?.operation).toBe("manifest.delete");

    // Segments survive in the store.
    await s.openBackstage("external-dlo-segments", "wedding-video/", "gone");
    expect(s.entries).toHaveLength(3);
  });

  it("refuses to manifest-delete a plain object", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "empty-object"));
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());
    await expect(s.deleteManifest()).rejects.toThrow(/plain/);
    expect(s.entries.some((e) => e.name === "empty-object")).toBe(true);
  });
});

describe("jobs drawer state", () => {
  it("loads jobs, tracks active ones, and honours the bookmark filter", async () => {
    const s = new BrowserState(newFakeApi());
    await s.loadJobs();
    expect(s.jobs).toEqual([]);
    expect(s.activeJobs).toEqual([]);

    const id = await s.startCopy({
      sourceBookmarkId: "bm-1",
      sourceContainer: "media",
      sourcePrefix: "",
      destBookmarkId: "bm-2",
      destContainer: "backup",
      destPrefix: "",
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" },
    });
    expect(s.jobs.length).toBe(1);
    expect(s.activeJobs.length).toBe(1);
    expect(s.jobs[0].id).toBe(id);

    // Filtered to an uninvolved bookmark the job disappears; to either
    // end it stays (§47).
    s.jobsFilter = "bm-3";
    await s.loadJobs();
    expect(s.jobs).toEqual([]);
    s.jobsFilter = "bm-2";
    await s.loadJobs();
    expect(s.jobs.length).toBe(1);

    await s.cancelJob(id);
    expect(s.activeJobs).toEqual([]);
    expect(s.jobs[0].state).toBe("cancelled");
  });

  it("previews a copy plan with disclosures", async () => {
    const s = new BrowserState(newFakeApi());
    const plan = await s.planCopy({
      sourceBookmarkId: "bm-1",
      sourceContainer: "media",
      sourcePrefix: "",
      destBookmarkId: "bm-2",
      destContainer: "versioned",
      destPrefix: "",
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" },
    });
    expect(plan.summary.new).toBe(3);
    expect(plan.summary.destOnly).toBe(2);
    expect(plan.versionedDestination).toBe(true);
  });
});
