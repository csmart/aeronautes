import { describe, expect, it, vi } from "vitest";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import type { AppSettingsDTO, BookmarkDTO } from "../../lib/api";

function bookmark(): BookmarkDTO {
  return {
    id: "",
    displayName: "Mock",
    authUrl: "http://mock/auth/v1.0",
    username: "test:tester",
    storageUrlOverride: "",
    tlsMode: "system",
    caPem: "",
    hasCa: false,
    hasCredential: false,
  };
}

async function connected(api = newFakeApi()): Promise<BrowserState> {
  const s = new BrowserState(api);
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
    expect(s.recentAuditEvents[0]?.operation).toBe("object.metadata.update");
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
      ackPublic: true,
    });
    expect(result.outcome).toBe("applied");
    expect(s.containerDetail?.readAcl).toBe(".r:*");
    expect(s.containerDetail?.versioningMode).toBe("history_location");
  });

  it("creates a container gated by advertised policies and opens it", async () => {
    const s = await connected();
    await expect(
      s.createContainer({ name: "newbox", policy: "nope", meta: {} }, s.portrait),
    ).rejects.toThrow(/policy/);

    await s.createContainer({ name: "newbox", policy: "gold", meta: {} }, s.portrait);
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
    expect(s.recentAuditEvents[0]?.operation).toBe("marker.delete");
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

    await s.deleteManifest("fixtures", "external-dlo", s.objectDetail!);
    expect(s.entries.some((e) => e.name === "external-dlo")).toBe(false);
    expect(s.recentAuditEvents[0]?.operation).toBe("manifest.delete");

    // Segments survive in the store.
    await s.openBackstage("external-dlo-segments", "wedding-video/", "gone");
    expect(s.entries).toHaveLength(3);
  });

  it("refuses to manifest-delete a plain object", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "empty-object"));
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());
    await expect(s.deleteManifest("fixtures", "empty-object", s.objectDetail!)).rejects.toThrow(/plain/);
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
      profile: "balanced",
      acknowledgeOverwrite: false,
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" , bufferSize: ""},
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
    expect(s.jobs[0].state).toBe("partial");
    expect(s.jobs[0].counters.overwritesRefused).toBe(1);
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
      profile: "balanced",
      acknowledgeOverwrite: false,
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" , bufferSize: ""},
    });
    expect(plan.summary.new).toBe(3);
    expect(plan.summary.destOnly).toBe(2);
    expect(plan.versionedDestination).toBe(true);
  });
});

describe("mutation identity binding (review-wave C-04)", () => {
  it("keeps the selection on the same identity across a filter change", async () => {
    const s = await connected();
    await s.openContainer("fixtures");
    const idx = s.entries.findIndex((e) => e.name === "external-dlo");
    s.select(idx);
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());

    // Narrow the filter: the identity survives at its new index.
    s.filter = "dlo";
    expect(s.selectedEntry?.name).toBe("external-dlo");
    expect(s.detailIdentity?.name).toBe("external-dlo");

    // Filter it OUT: selection and identity clear — no silent retarget.
    s.filter = "zzz-no-match";
    expect(s.selectedEntry).toBeNull();
    expect(s.detailIdentity).toBeNull();
  });

  it("metadata save binds the fetched identity, not the live index", async () => {
    const api = newFakeApi();
    const s = new BrowserState(api);
    const saved = await s.saveBookmark(bookmark(), "testing");
    await s.connect(saved.id);
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "empty-object"));
    await vi.waitFor(() => expect(s.objectDetail).not.toBeNull());
    const identity = s.detailIdentity;
    expect(identity?.name).toBe("empty-object");

    // Sabotage the index the old code would have read at submit time.
    s.selectedIndex = s.entries.findIndex((e) => e.name === "external-dlo");
    const result = await s.saveObjectMetadata({
      meta: { Team: "ops" },
      contentType: "",
      deleteAt: "",
    });
    expect(result.outcome).toBe("applied");
    // The edited object is the one the detail showed.
    const detail = await api.objectDetail("fixtures", "empty-object", s.connectedBookmarkId!);
    expect(detail.meta?.["Team"]).toBe("ops");
  });
});

describe("refreshing the open listing", () => {
  const settings = { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "", bufferSize: "" };
  const upload = (bookmarkId: string, container: string) => ({
    bookmarkId, container, prefix: "", selectionToken: "fake-selection", settings, acknowledgeOverwrite: false,
  });

  it("re-reads the current prefix in place and keeps the filter and the selection", async () => {
    const api = newFakeApi();
    const s = await connected(api);
    await s.openContainer("fixtures");
    s.filter = "external";
    s.select(s.visibleEntries.findIndex((e) => e.name === "external-slo"));
    await vi.waitFor(() => expect(s.detailLoading).toBe(false));
    const detail = s.objectDetail;
    const objects = vi.spyOn(api, "objects");
    await s.refreshEntries();
    expect(objects).toHaveBeenCalledWith(s.connectedBookmarkId, "fixtures", "", "", 1000);
    expect(s.filter).toBe("external");
    expect(s.selectedEntry?.name).toBe("external-slo");
    // The row's listing facts did not change, so the inspector's HEAD stands.
    expect(s.objectDetail).toBe(detail);
    expect(s.entriesRefreshing).toBe(false);
  });

  it("re-inspects a selected row whose listing facts changed and clears one that is gone", async () => {
    const api = newFakeApi();
    const s = await connected(api);
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "external-slo"));
    await vi.waitFor(() => expect(s.detailLoading).toBe(false));
    const detail = s.objectDetail;
    const listing = api.objects;
    api.objects = async (...args) => {
      const page = await listing(...args);
      return { ...page, rows: page.rows.map((row) => (row.name === "external-slo" ? { ...row, etag: "replaced" } : row)) };
    };
    await s.refreshEntries();
    expect(s.selectedEntry?.name).toBe("external-slo");
    expect(s.detailLoading).toBe(true);
    await vi.waitFor(() => expect(s.detailLoading).toBe(false));
    expect(s.objectDetail).not.toBe(detail);
    expect(s.objectDetail?.name).toBe("external-slo");

    api.objects = async (...args) => {
      const page = await listing(...args);
      return { ...page, rows: page.rows.filter((row) => row.name !== "external-slo") };
    };
    await s.refreshEntries();
    expect(s.selectedIndex).toBe(-1);
    expect(s.detailIdentity).toBeNull();
    expect(s.objectDetail).toBeNull();
  });

  it.each([
    ["content type", { contentType: "application/x-review-refresh" }],
    ["large-object listing", { sloListed: true }],
  ])("re-inspects a selected row when only its %s changed", async (_fact, change) => {
    const api = newFakeApi();
    const s = await connected(api);
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "empty-object"));
    await vi.waitFor(() => expect(s.detailLoading).toBe(false));
    const detail = s.objectDetail;
    const listing = api.objects;
    api.objects = async (...args) => {
      const page = await listing(...args);
      return { ...page, rows: page.rows.map((row) => (row.name === "empty-object" ? { ...row, ...change } : row)) };
    };
    await s.refreshEntries();
    expect(s.selectedEntry?.name).toBe("empty-object");
    expect(s.detailLoading).toBe(true);
    await vi.waitFor(() => expect(s.detailLoading).toBe(false));
    expect(s.objectDetail).not.toBe(detail);
  });

  it("drops a refresh reply that a navigation superseded", async () => {
    const api = newFakeApi();
    const s = await connected(api);
    await s.openContainer("fixtures");
    const listing = api.objects;
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    api.objects = async (...args) => {
      if (args[2] === "") await gate;
      return listing(...args);
    };
    const refresh = s.refreshEntries();
    await s.goTo("implied/");
    release();
    await refresh;
    expect(s.prefix).toBe("implied/");
    expect(s.entries.map((e) => e.name)).toEqual(["implied/child.txt"]);
    expect(s.entriesRefreshing).toBe(false);
  });

  it("refreshes the view when a job it started finishes in the open container", async () => {
    vi.useFakeTimers();
    try {
      const api = newFakeApi();
      const s = await connected(api);
      await s.openContainer("fixtures");
      const objects = vi.spyOn(api, "objects");
      const containers = vi.spyOn(api, "containers");
      const id = await s.startUpload(upload(s.connectedBookmarkId!, "fixtures"));
      await vi.advanceTimersByTimeAsync(1500);
      expect(objects).not.toHaveBeenCalled();
      await api.cancelJob(id);
      await vi.advanceTimersByTimeAsync(1500);
      expect(objects).toHaveBeenCalledWith(s.connectedBookmarkId, "fixtures", "", "", 1000);
      expect(containers).toHaveBeenCalledTimes(1);
      // The watch ended with the job.
      await vi.advanceTimersByTimeAsync(4500);
      expect(objects).toHaveBeenCalledTimes(1);
      expect(containers).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("reloads only the container column when the finished job wrote to another container", async () => {
    vi.useFakeTimers();
    try {
      const api = newFakeApi();
      const s = await connected(api);
      await s.openContainer("fixtures");
      const objects = vi.spyOn(api, "objects");
      const containers = vi.spyOn(api, "containers");
      const id = await s.startUpload(upload(s.connectedBookmarkId!, "photos"));
      await api.cancelJob(id);
      await vi.advanceTimersByTimeAsync(1500);
      expect(containers).toHaveBeenCalledTimes(1);
      expect(objects).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("gives up on a job it can no longer read and leaves the view alone", async () => {
    vi.useFakeTimers();
    try {
      const api = newFakeApi();
      const s = await connected(api);
      await s.openContainer("fixtures");
      const objects = vi.spyOn(api, "objects");
      const job = vi.spyOn(api, "job").mockRejectedValue(new Error("job not found"));
      await s.startUpload(upload(s.connectedBookmarkId!, "fixtures"));
      await vi.advanceTimersByTimeAsync(1500 * 6);
      expect(job).toHaveBeenCalledTimes(3);
      expect(objects).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("the newest listing read owns the lifecycle", () => {
  function gated(api: ReturnType<typeof newFakeApi>) {
    // Each objects() call parks until the test releases it, so the test
    // chooses the order replies arrive in.
    const listing = api.objects;
    const parked: Array<() => void> = [];
    api.objects = (...args) =>
      new Promise((resolve, reject) => {
        parked.push(() => listing(...args).then(resolve, reject));
      });
    return parked;
  }

  it("a failed refresh retires the selection, so a retry cannot pair old detail with new rows", async () => {
    const api = newFakeApi();
    const s = await connected(api);
    await s.openContainer("fixtures");
    s.select(s.entries.findIndex((e) => e.name === "external-slo"));
    await vi.waitFor(() => expect(s.detailLoading).toBe(false));
    const listing = api.objects;
    api.objects = async () => { throw new Error("listing unavailable"); };
    await s.refreshEntries();
    expect(s.entries).toEqual([]);
    expect(s.entriesError).toMatch(/listing unavailable/);
    expect(s.entriesLoading).toBe(false);
    expect(s.selectedIndex).toBe(-1);
    expect(s.detailIdentity).toBeNull();
    expect(s.objectDetail).toBeNull();

    // The retry lists a different object where the old one sat; nothing
    // from the old selection may attach to it.
    api.objects = async (...args) => {
      const page = await listing(...args);
      return { ...page, rows: page.rows.filter((row) => row.name !== "external-slo") };
    };
    await s.refreshEntries();
    expect(s.entries.length).toBeGreaterThan(0);
    expect(s.selectedEntry).toBeNull();
    expect(s.detailIdentity).toBeNull();
    expect(s.objectDetail).toBeNull();
  });

  it.each([
    ["navigation first", [0, 1]],
    ["refresh first", [1, 0]],
  ])("a refresh overtaking a navigation finishes it (%s)", async (_order, release) => {
    const api = newFakeApi();
    const s = await connected(api);
    await s.openContainer("fixtures");
    const parked = gated(api);
    const navigation = s.goTo("implied/");
    expect(s.entriesLoading).toBe(true);
    const refresh = s.refreshEntries();
    expect(parked).toHaveLength(2);
    expect(s.entriesLoading).toBe(true);
    parked[release[0]]();
    await Promise.resolve();
    parked[release[1]]();
    await Promise.all([navigation, refresh]);
    expect(s.prefix).toBe("implied/");
    expect(s.entries.map((e) => e.name)).toEqual(["implied/child.txt"]);
    expect(s.entriesLoading).toBe(false);
    expect(s.entriesRefreshing).toBe(false);
    expect(s.entriesError).toBeNull();
  });
});

describe("rail width persistence", () => {
  it("writes the latest width asked for while an earlier settings write was in flight", async () => {
    const api = newFakeApi();
    const s = new BrowserState(api);
    await s.loadAppSettings();
    let release!: (settings: AppSettingsDTO) => void;
    const first = new Promise<AppSettingsDTO>((resolve) => { release = resolve; });
    const saves: number[] = [];
    api.saveAppSettings = vi.fn(async (settings: AppSettingsDTO) => {
      saves.push(settings.railWidthPx);
      return saves.length === 1 ? first : settings;
    });
    const pending = s.saveRailWidth(80);
    await s.saveRailWidth(96);
    expect(saves).toEqual([80]);
    release({ ...s.appSettings!, railWidthPx: 80 });
    await pending;
    await vi.waitFor(() => expect(saves).toEqual([80, 96]));
    await vi.waitFor(() => expect(s.appSettings?.railWidthPx).toBe(96));
    expect(s.appSettingsOperation).toBeNull();
  });

  it("waits for a settings read in flight and then writes the width", async () => {
    const api = newFakeApi();
    const s = new BrowserState(api);
    const settings = await api.appSettings();
    let release!: (settings: AppSettingsDTO) => void;
    api.appSettings = () => new Promise<AppSettingsDTO>((resolve) => { release = resolve; });
    const save = vi.spyOn(api, "saveAppSettings");
    const reading = s.loadAppSettings();
    await s.saveRailWidth(120);
    expect(save).not.toHaveBeenCalled();
    release(settings);
    await reading;
    await vi.waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ railWidthPx: 120 })));
  });
});
