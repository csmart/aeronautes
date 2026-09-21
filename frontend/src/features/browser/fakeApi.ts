// Test-only Api implementation over the §58 static fixtures, reusing the
// delimiter-view mirror so navigation logic is exercised with the same
// semantics the live services produce. All state is copied per instance,
// so mutation tests never leak between cases. Not imported by
// application code.

import type {
  Api,
  AuditEventDTO,
  BookmarkDTO,
  CapabilitiesDTO,
  ContainerDetailDTO,
  ListingRowDTO,
  ListingScope,
  MutationResultDTO,
  ObjectDetailDTO,
  PrefixDetailDTO,
} from "../../lib/api";
import {
  accountFixture,
  containersFixture,
  fixtureObjects,
  largeObjectDetail,
  objectMetaFixture,
} from "../../lib/fixtures";
import { delimiterView, type FixtureObject } from "../../lib/listing";

interface ContainerState {
  meta: Record<string, string>;
  readAcl: string;
  writeAcl: string;
  versioningMode: string;
  versioningArchive: string;
  quotaBytes: number;
  quotaCount: number;
}

const capabilities: Omit<CapabilitiesDTO, "scope"> = {
  source: "advertised",
  maxFileSize: 5368709122,
  storagePolicies: [
    { name: "standard", default: true },
    { name: "gold", default: false },
    { name: "ec42", default: false },
  ],
  versioningFlags: ["x-versions-location", "x-history-location"],
  sloSupported: true,
};

function cleanMap(
  m: { [key: string]: string | undefined } | null | undefined,
): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(m ?? {})) {
    if (v !== undefined) {
      out[k] = v;
    }
  }
  return out;
}

export function newFakeApi(): Api {
  let nextId = 1;
  const bookmarks: BookmarkDTO[] = [];
  const generations = new Map<string, number>();
  let connectedScope: ListingScope | null = null;

  // Instance copies: mutations must not leak into the shared fixtures.
  const containers = containersFixture.map((c) => ({ ...c }));
  const objects: Record<string, FixtureObject[]> = Object.fromEntries(
    Object.entries(fixtureObjects).map(([name, list]) => [name, list.map((o) => ({ ...o }))]),
  );
  const objectMeta: Record<string, Record<string, string>> = {};
  for (const [name, meta] of Object.entries(objectMetaFixture)) {
    objectMeta[`fixtures/${name}`] = { ...meta };
  }
  const objectDeleteAt: Record<string, string> = {};
  const containerState: Record<string, ContainerState> = {};
  const audit: AuditEventDTO[] = [];
  const jobs: import("../../lib/api").JobDTO[] = [];
  const scans = new Map<string, string>();
  let auditId = 1;

  function readScope(bookmarkId: string) {
    if (!bookmarkId) throw new Error("account scope changed: name the bookmark the read is for");
    return { bookmarkId, generation: generations.get(bookmarkId) ?? 1 };
  }

  function scopedJob(id: string, bookmarkId: string) {
    readScope(bookmarkId);
    const job = jobs.find((row) => row.id === id);
    if (!job) throw new Error("job not found");
    if (job.sourceBookmarkId !== bookmarkId && job.destBookmarkId !== bookmarkId) {
      throw new Error("account scope changed: job is not for that bookmark");
    }
    return job;
  }

  function scanScope(id: string, bookmarkId: string) {
    const scope = readScope(bookmarkId);
    if (scans.get(id) !== bookmarkId) throw new Error("account scope changed: scan is not for that bookmark");
    return scope;
  }

  function stateOf(name: string): ContainerState {
    containerState[name] ??= {
      meta: {},
      readAcl: "",
      writeAcl: "",
      versioningMode: "off",
      versioningArchive: "",
      quotaBytes: 0,
      quotaCount: 0,
    };
    return containerState[name];
  }

  function record(operation: string, resource: string, result: string) {
    audit.push({
      id: auditId++,
      at: new Date().toISOString(),
      bookmarkId: "",
      operation,
      resource,
      summary: `${operation} on ${resource}`,
      result,
      transIds: ["txfake"],
    });
  }

  function findObject(container: string, name: string): FixtureObject | undefined {
    return (objects[container] ?? []).find((o) => o.name === name);
  }

  function objectFingerprint(container: string, name: string): string {
    const meta = objectMeta[`${container}/${name}`] ?? {};
    const o = findObject(container, name);
    return JSON.stringify([o?.contentType, objectDeleteAt[`${container}/${name}`] ?? "", ...Object.entries(meta).sort()]);
  }

  function containerFingerprint(name: string): string {
    const s = stateOf(name);
    return JSON.stringify([
      s.readAcl, s.writeAcl, s.versioningMode, s.versioningArchive,
      s.quotaBytes, s.quotaCount, ...Object.entries(s.meta).sort(),
    ]);
  }

  function objectDetailOf(container: string, name: string): ObjectDetailDTO {
    const stored = findObject(container, name);
    if (!stored) {
      throw new Error("object not found");
    }
    const large = largeObjectDetail[name];
    return {
      name,
      class: large?.kind ?? "plain",
      contentLength: large?.logicalBytes ?? stored.bytes,
      contentType: stored.contentType,
      etag: stored.etag,
      lastModified: stored.lastModified,
      manifest: large?.kind === "dlo" ? large.manifest : "",
      followedSymlink: false,
      symlinkTarget: "",
      deleteAt: objectDeleteAt[`${container}/${name}`] ?? "",
      meta: { ...(objectMeta[`${container}/${name}`] ?? {}) },
      rawHeaders: { "X-Trans-Id": "txfake" },
      transId: "txfake",
      editableFingerprint: objectFingerprint(container, name),
      scope: { bookmarkId: "", generation: 0 },
    };
  }

  function containerDetailOf(name: string): ContainerDetailDTO {
    const c = containers.find((x) => x.name === name);
    if (!c) {
      throw new Error("container not found");
    }
    const s = stateOf(name);
    return {
      name: c.name,
      objectCount: c.objectCount,
      bytesUsed: c.bytes,
      storagePolicy: c.policy,
      readAcl: s.readAcl,
      writeAcl: s.writeAcl,
      versioningMode: s.versioningMode,
      versioningArchive: s.versioningArchive,
      quotaBytes: s.quotaBytes,
      quotaCount: s.quotaCount,
      cors: false,
      staticWeb: false,
      tempUrlKeySlots: [],
      meta: { ...s.meta },
      rawHeaders: { "X-Trans-Id": "txfake" },
      transId: "txfake",
      editableFingerprint: containerFingerprint(name),
      class: c.segmentRole,
      scope: { bookmarkId: "", generation: 0 },
    };
  }

  return {
    info: async () => ({
      name: "Aeronautes",
      version: "test",
      sourceRevision: "test-source",
      sourceDirty: "unknown",
      wails: "test",
      secretBackend: "session",
    }),

    listBookmarks: async () => [...bookmarks],
    credentialStatus: async () => ({ retired: 0, unfinished: 0, saving: 0, unverified: 0 }),

    saveBookmark: async (bookmark, secret) => {
      const saved = { ...bookmark };
      if (saved.id === "") {
        saved.id = `bm-${nextId++}`;
        bookmarks.push(saved);
        generations.set(saved.id, 1);
      } else {
        const at = bookmarks.findIndex((b) => b.id === saved.id);
        if (at === -1) {
          throw new Error("bookmark not found");
        }
        const previous = bookmarks[at];
        const bindingChanged = (["authUrl", "username", "storageUrlOverride", "tlsMode", "caPem"] as const)
          .some((key) => saved[key] !== previous[key]);
        if (bindingChanged || secret !== "") generations.set(saved.id, (generations.get(saved.id) ?? 1) + 1);
        saved.hasCredential = previous.hasCredential;
        bookmarks[at] = saved;
        if (connectedScope?.bookmarkId === saved.id) connectedScope = null;
      }
      if (secret !== "") {
        saved.hasCredential = true;
      }
      return { ...saved };
    },

    deleteBookmark: async (id) => {
      const at = bookmarks.findIndex((b) => b.id === id);
      if (at === -1) {
        throw new Error("bookmark not found");
      }
      bookmarks.splice(at, 1);
      generations.delete(id);
      if (connectedScope?.bookmarkId === id) connectedScope = null;
    },

    connect: async (bookmarkId) => {
      const bookmark = bookmarks.find((b) => b.id === bookmarkId);
      if (!bookmark) {
        throw new Error("bookmark not found");
      }
      if (!bookmark.hasCredential) {
        throw new Error("no credential on record");
      }
      connectedScope = readScope(bookmarkId);
      return {
        scope: { ...connectedScope },
        name: accountFixture.name,
        containers: containers.length,
        objects: accountFixture.objects,
        bytesUsed: accountFixture.bytes,
        quotaBytes: 0,
        meta: accountFixture.meta,
        tempUrlKeySlots: ["Temp-Url-Key"],
      };
    },

    capabilities: async (bookmarkId) => {
      if (!bookmarkId || connectedScope === null) throw new Error("not connected");
      if (connectedScope.bookmarkId !== bookmarkId) throw new Error("the connected account changed");
      return { ...capabilities, scope: { ...connectedScope } };
    },

    containers: async (bookmarkId, marker, limit) => {
      const rows = containers
        .map((c) => ({
          name: c.name,
          count: c.objectCount,
          bytes: c.bytes,
          class: c.segmentRole,
        }))
        .sort((a, b) => (a.name < b.name ? -1 : 1))
        .filter((c) => c.name > marker)
        .slice(0, limit);
      const full = rows.length === limit;
      // The fake echoes the bookmark it was asked for, as the backend
      // does: a page states the account that answered it (ADR 0049).
      return { rows, nextMarker: full ? rows[rows.length - 1].name : "", scope: readScope(bookmarkId) };
    },

    objects: async (bookmarkId, container, prefix, marker, limit) => {
      const view = delimiterView(objects[container] ?? [], prefix);
      const rows: ListingRowDTO[] = view
        .map((e): ListingRowDTO =>
          e.kind === "object"
            ? {
                kind: "object",
                name: e.name,
                bytes: e.bytes,
                etag: e.etag,
                lastModified: e.lastModified,
                contentType: e.contentType,
                sloListed: e.largeObject === "slo",
              }
            : {
                kind: "prefix",
                name: e.prefix,
                bytes: 0,
                etag: "",
                lastModified: "",
                contentType: "",
                sloListed: false,
              },
        )
        .filter((r) => r.name > marker)
        .slice(0, limit);
      const full = rows.length === limit;
      // The fake echoes the bookmark it was asked for, as the backend
      // does: a page states the account that answered it (ADR 0049).
      return { rows, nextMarker: full ? rows[rows.length - 1].name : "", scope: { bookmarkId, generation: 1 } };
    },

    objectDetail: async (container, name, bookmarkId) => ({
      ...objectDetailOf(container, name),
      scope: readScope(bookmarkId),
    }),

    prefixDetail: async (container, prefix, bookmarkId): Promise<PrefixDetailDTO> => {
      const self = findObject(container, prefix);
      const scope = readScope(bookmarkId);
      if (!self) {
        return { prefix, markerBacked: false, selfObject: null, scope };
      }
      return {
        prefix,
        markerBacked: self.bytes === 0 && self.contentType === "application/directory",
        selfObject: objectDetailOf(container, prefix),
        scope,
      };
    },

    containerDetail: async (name, bookmarkId) => ({
      ...containerDetailOf(name),
      scope: readScope(bookmarkId),
    }),

    sloManifest: async (_container, name, bookmarkId) => {
      const large = largeObjectDetail[name];
      if (!large || large.kind !== "slo") {
        throw new Error("not an SLO");
      }
      return {
        entries: large.segments.map((s) => {
          const slash = s.name.indexOf("/");
          return {
            kind: "object",
            container: s.name.slice(0, slash),
            object: s.name.slice(slash + 1),
            etag: s.etag,
            size: s.bytes,
            range: "",
            nestedSlo: false,
            dataSize: 0,
          };
        }),
        scope: readScope(bookmarkId),
      };
    },

    updateObjectMetadata: async (container, name, baseFingerprint, edit): Promise<MutationResultDTO> => {
      const stored = findObject(container, name);
      if (!stored) {
        throw new Error("object not found");
      }
      if (baseFingerprint !== "" && baseFingerprint !== objectFingerprint(container, name)) {
        record("object.metadata.update", `${container}/${name}`, "conflict");
        return { outcome: "conflict", mismatch: "", limitation: "", transIds: ["txfake"], objectAfter: null, containerAfter: null };
      }
      objectMeta[`${container}/${name}`] = cleanMap(edit.meta);
      if (edit.contentType !== "") {
        stored.contentType = edit.contentType;
      }
      objectDeleteAt[`${container}/${name}`] = edit.deleteAt;
      record("object.metadata.update", `${container}/${name}`, "applied");
      return {
        outcome: "applied",
        mismatch: "",
        // A completed save carries its limit here too: the fake must not
        // model a stronger guarantee than the product gives (ADR 0046).
        limitation:
          "this does not establish that no concurrent edit was overwritten: the check before the write cannot see a change that lands after it",
        transIds: ["txfake", "txfake", "txfake"],
        objectAfter: objectDetailOf(container, name),
        containerAfter: null,
      };
    },

    updateContainer: async (name, baseFingerprint, edit): Promise<MutationResultDTO> => {
      if (!containers.some((c) => c.name === name)) {
        throw new Error("container not found");
      }
      if (
        edit.versioningMode !== "off" &&
        edit.versioningMode !== "" &&
        !capabilities.versioningFlags?.includes(edit.versioningMode === "history_location" ? "x-history-location" : "x-versions-location")
      ) {
        throw new Error("cluster does not advertise this versioning mode");
      }
      if (baseFingerprint !== "" && baseFingerprint !== containerFingerprint(name)) {
        record("container.update", name, "conflict");
        return { outcome: "conflict", mismatch: "", limitation: "", transIds: ["txfake"], objectAfter: null, containerAfter: null };
      }
      const s = stateOf(name);
      s.meta = cleanMap(edit.meta);
      s.readAcl = edit.readAcl;
      s.writeAcl = edit.writeAcl;
      s.versioningMode = edit.versioningMode === "" ? "off" : edit.versioningMode;
      s.versioningArchive = edit.versioningMode === "off" || edit.versioningMode === "" ? "" : edit.versioningArchive;
      s.quotaBytes = Math.max(edit.quotaBytes, 0);
      s.quotaCount = Math.max(edit.quotaCount, 0);
      record("container.update", name, "applied");
      return {
        outcome: "applied",
        mismatch: "",
        // The container edit path shares this shape and is deliberately
        // NOT qualified by this slice; it is named as open in ADR 0046.
        limitation: "",
        transIds: ["txfake", "txfake", "txfake"],
        objectAfter: null,
        containerAfter: containerDetailOf(name),
      };
    },

    createContainer: async (create) => {
      if (create.policy !== "" && !capabilities.storagePolicies?.some((p) => p.name === create.policy)) {
        throw new Error(`cluster does not advertise storage policy "${create.policy}"`);
      }
      if (containers.some((c) => c.name === create.name)) {
        throw new Error("already exists");
      }
      containers.push({
        name: create.name,
        objectCount: 0,
        bytes: 0,
        policy: create.policy === "" ? "standard" : create.policy,
        segmentRole:
          create.name.endsWith("_segments") || create.name.endsWith("-segments")
            ? "suspected"
            : "normal",
      });
      objects[create.name] = [];
      stateOf(create.name).meta = cleanMap(create.meta);
      record("container.create", create.name, "applied");
      return containerDetailOf(create.name);
    },

    createMarker: async (container, prefix) => {
      const existing = findObject(container, prefix);
      if (existing) {
        throw new Error("already exists or occupied");
      }
      (objects[container] ??= []).push({
        name: prefix,
        bytes: 0,
        etag: "d41d8cd98f00b204e9800998ecf8427e",
        lastModified: new Date().toISOString(),
        contentType: "application/directory",
      });
      record("marker.create", `${container}/${prefix}`, "applied");
      return { prefix, markerBacked: true, selfObject: objectDetailOf(container, prefix),
        scope: { bookmarkId: "", generation: 0 } };
    },

    deleteMarker: async (container, prefix) => {
      const stored = findObject(container, prefix);
      if (!stored) {
        record("marker.delete", `${container}/${prefix}`, "failed");
        throw new Error("no marker object at this prefix");
      }
      if (stored.bytes !== 0 || stored.contentType !== "application/directory") {
        record("marker.delete", `${container}/${prefix}`, "failed");
        throw new Error("not a directory marker; refusing");
      }
      objects[container] = objects[container].filter((o) => o.name !== prefix);
      record("marker.delete", `${container}/${prefix}`, "applied");
    },

    dloSegments: async (manifest, bookmarkId) => {
      const slash = manifest.indexOf("/");
      const container = manifest.slice(0, slash);
      const prefix = manifest.slice(slash + 1);
      const limit = 50;
      const all = (objects[container] ?? [])
        .filter((o) => o.name.startsWith(prefix))
        .sort((a, b) => (a.name < b.name ? -1 : 1));
      const segments = all.slice(0, limit).map(
        (o): ListingRowDTO => ({
          kind: "object",
          name: o.name,
          bytes: o.bytes,
          etag: o.etag,
          lastModified: o.lastModified,
          contentType: o.contentType,
          sloListed: false,
        }),
      );
      return {
        container,
        prefix,
        segments,
        hasMore: all.length > limit,
        previewBytes: segments.reduce((sum, s) => sum + s.bytes, 0),
        scope: readScope(bookmarkId),
      };
    },

    deleteManifest: async (container, name) => {
      const detail = objectDetailOf(container, name);
      if (detail.class === "plain") {
        record("manifest.delete", `${container}/${name}`, "failed");
        throw new Error("plain object; the manifest-only delete refuses it");
      }
      objects[container] = (objects[container] ?? []).filter((o) => o.name !== name);
      record("manifest.delete", `${container}/${name}`, "applied");
      return detail;
    },

    auditRecent: async (limit, bookmarkId) => {
      if (!bookmarkId) {
        throw new Error("name the bookmark the read is for");
      }
      return [...audit].reverse().slice(0, limit);
    },
    auditSearch: async (q) =>
      [...audit]
        .reverse()
        .filter((e) => q.operationPrefix === "" || e.operation.startsWith(q.operationPrefix))
        .filter((e) => q.result === "" || e.result === q.result)
        .filter((e) => q.beforeId === 0 || e.id < q.beforeId)
        .slice(0, q.limit > 0 ? q.limit : 100),

    // Transfers: the fake plans a canned preview and runs jobs through a
    // tiny scripted lifecycle so drawer logic is testable.
    planCopy: async (req) => ({
      summary: {
        new: 3,
        same: 2,
        changed: 1,
        unknown: 1,
        destOnly: 2,
        transferObjects: 5,
        transferBytes: 42_000_000,
        overwrites: 2,
        nativeRouted: 1,
        destOnlyBytes: 7_000_000,
      },
      dataPath: "eligible copies pass through this workstation: source download and destination upload",
      overwriteBytes: 512,
      versionedDestination: req.destContainer === "versioned",
      largeObjectsAsDlo: true,
    }),
    startMirror: async () => {
      throw new Error("Mirror is unavailable while concurrent changes cannot be protected");
    },
    startCopy: async (req) => {
      const id = `job-${jobs.length + 1}`;
      jobs.push({
        id,
        type: "copy",
        state: "running",
        source: `${req.sourceContainer}/${req.sourcePrefix}`,
        destination: `${req.destContainer}/${req.destPrefix}`,
        sourceBookmarkId: req.sourceBookmarkId,
        destBookmarkId: req.destBookmarkId,
        createdAt: "2026-08-29T10:00:00Z",
        startedAt: "2026-08-29T10:00:01Z",
        finishedAt: "",
        engine: "rclone+native",
        settings: "",
        recoveryRecords: 0,
        counters: {
          objectsPlanned: 5,
          objectsCopied: 2,
          objectsVerified: 0,
          objectsFailed: 0,
          bytesPlanned: 42_000_000,
          bytesCopied: 12_000_000,
          overwrites: 0, overwritesRefused: 1,
          nativeRouted: 1,
          objectsDeleted: 0,
        },
        error: "",
      });
      record("transfer.copy.start", `${req.sourceContainer} → ${req.destContainer}`, "applied");
      return id;
    },
    rerunJob: async (jobId) => {
      const original = jobs.find((j) => j.id === jobId);
      if (!original || !["complete", "partial", "cancelled", "failed"].includes(original.state)) {
        throw new Error("job is not re-runnable");
      }
      if (original.type !== "copy") {
        throw new Error("re-run supports copy jobs");
      }
      const id = `job-${jobs.length + 1}`;
      jobs.push({ ...original, id, state: "running", finishedAt: "", error: "" });
      return id;
    },
    cancelJob: async (jobId) => {
      const job = jobs.find((j) => j.id === jobId);
      if (!job || job.finishedAt !== "") {
        throw new Error(`job ${jobId} is not running`);
      }
      if (job.type === "copy" && job.counters.overwritesRefused > 0) {
        job.state = "partial";
        job.error = "1 overwrite refused: concurrent changes cannot be protected; context canceled";
      } else {
        job.state = "cancelled";
      }
      job.finishedAt = "2026-08-29T10:05:00Z";
    },
    pickUploadFiles: async () => ({ token: "fake-selection", files: ["fake-a.txt", "fake-b.txt"] }),
    pickDownloadDir: async () => ({ token: "fake-dir", dir: "/tmp/fake-dest" }),
    preflightUpload: async (req) => {
      if (req.selectionToken !== "fake-selection") {
        throw new Error("unknown or expired file selection");
      }
      const names = ["fake-a.txt", "fake-b.txt"];
      return {
        items: names.map((name) => ({
          path: "(picker-held)",
          name: req.prefix + name,
          size: 1024,
          route: "rclone",
        })),
        problems: [],
        totalBytes: 1024 * names.length,
      };
    },
    startUpload: async (req) => {
      if (req.selectionToken !== "fake-selection") {
        throw new Error("unknown or expired file selection");
      }
      const paths = ["fake-a.txt", "fake-b.txt"];
      const id = `job-${jobs.length + 1}`;
      jobs.push({
        id,
        type: "upload",
        state: "running",
        source: `${paths.length} local files`,
        destination: `${req.container}/${req.prefix}`,
        sourceBookmarkId: "",
        destBookmarkId: req.bookmarkId,
        createdAt: "2026-08-29T10:00:00Z",
        startedAt: "2026-08-29T10:00:01Z",
        finishedAt: "",
        engine: "rclone+native",
        settings: "",
        recoveryRecords: 0,
        counters: {
          objectsPlanned: paths.length,
          objectsCopied: 0,
          objectsVerified: 0,
          objectsFailed: 0,
          bytesPlanned: 1024 * paths.length,
          bytesCopied: 0,
          overwrites: 0, overwritesRefused: 0,
          nativeRouted: 0,
          objectsDeleted: 0,
        },
        error: "",
      });
      return id;
    },
    preflightDownload: async (req) => ({
      itemSamples: [
        { name: req.prefix + "a.txt", relPath: "a.txt", size: 512, route: "rclone", dir: false },
      ],
      problemSamples:
        req.destDirToken === "collides"
          ? [{ name: "a.txt", kind: "local-exists", detail: "already there", reject: true }]
          : [],
      totalBytes: 512,
      objects: 1,
      problemCount: req.destDirToken === "collides" ? 1 : 0,
      rejected: req.destDirToken === "collides",
    }),
    startDownload: async (req) => {
      const id = `job-${jobs.length + 1}`;
      jobs.push({
        id,
        type: "download",
        state: "running",
        source: `${req.container}/${req.prefix}`,
        destination: "local directory (picker-selected)",
        sourceBookmarkId: req.bookmarkId,
        destBookmarkId: "",
        createdAt: "2026-08-29T10:00:00Z",
        startedAt: "2026-08-29T10:00:01Z",
        finishedAt: "",
        engine: "rclone+native",
        settings: "",
        recoveryRecords: 0,
        counters: {
          objectsPlanned: 1,
          objectsCopied: 0,
          objectsVerified: 0,
          objectsFailed: 0,
          bytesPlanned: 512,
          bytesCopied: 0,
          overwrites: 0, overwritesRefused: 0,
          nativeRouted: 0,
          objectsDeleted: 0,
        },
        error: "",
      });
      return id;
    },
    previewDelete: async (req) => ({
      objects: 42,
      bytes: 9_000_000,
      possibleManifests: 2,
      strategy: "bulk-delete",
    }),
    startDelete: async (req) => {
      if (req.confirm !== req.container) {
        throw new Error("confirmation does not match the container name");
      }
      const id = `job-${jobs.length + 1}`;
      jobs.push({
        id,
        type: req.mode === "prefix" ? "delete_prefix" : req.mode === "empty" ? "empty_container" : "delete_container",
        state: "running",
        source: `${req.container}/${req.prefix}`,
        destination: "(deleted)",
        sourceBookmarkId: req.bookmarkId,
        destBookmarkId: "",
        createdAt: "2026-08-29T10:00:00Z",
        startedAt: "2026-08-29T10:00:01Z",
        finishedAt: "",
        engine: "native",
        settings: "",
        recoveryRecords: 0,
        counters: {
          objectsPlanned: 42,
          objectsCopied: 0,
          objectsVerified: 0,
          objectsFailed: 0,
          bytesPlanned: 9_000_000,
          bytesCopied: 0,
          overwrites: 0, overwritesRefused: 0,
          nativeRouted: 0,
          objectsDeleted: 0,
        },
        error: "",
      });
      return id;
    },
    startBenchmark: async (req) => {
      if (!req.confirm) throw new Error("confirmation does not match the target account");
      return "job-bench";
    },
    latestBenchmark: async (bookmarkId) => null,
    about: async () => ({
      name: "Aeronautes",
      version: "test",
      sourceRevision: "test-source",
      sourceDirty: "unknown",
      wails: "v3",
      secretBackend: "session",
      rclonePin: "v1.74.3",
      license: "Apache-2.0 (this application)",
      licenses: [{ name: "example", license: "MIT", url: "https://example.com" }],
    }),
    appSettings: async () => ({ maxActiveJobs: 2, defaultProfile: "auto", auditRetentionDays: 90, jobRetentionDays: 30 }),
    saveAppSettings: async (settings) => settings,
    startFind: async (_container, _prefix, _query, bookmarkId) => {
      readScope(bookmarkId);
      const id = `find-${scans.size + 1}`;
      scans.set(id, bookmarkId);
      return id;
    },
    findStatus: async (id, bookmarkId) => ({
      scanned: 1234,
      matches: [
        { name: "photos/report.jpg", bytes: 100 },
        { name: "docs/report.pdf", bytes: 2048 },
      ],
      truncated: false,
      done: true,
      error: "",
      objectsPerSecond: 5000,
      scope: scanScope(id, bookmarkId),
    }),
    cancelFind: async (id, bookmarkId) => { scanScope(id, bookmarkId); },
    checkSegmentOwnership: async (req) => ({
      class: "dlo",
      target: "media_segments/owned/",
      proven: req.name === "owned.bin",
      segments: 0,
      inlineEntries: 0,
    }),
    deleteManifestWithSegments: async (req) => {
      if (req.confirm !== req.name) {
        throw new Error("confirmation does not match the object name");
      }
      record("segments.delete", `${req.container}/${req.name}`, "applied");
      return { manifestDeleted: true, segmentsDeleted: 3, segmentsFailed: 0, basis: req.force ? "backstage-forced" : "exclusive-ownership" };
    },
    recoveryStatus: async () => ({ jobs: 0, records: 0 }),
    job: async (id, bookmarkId) => scopedJob(id, bookmarkId),
    jobHistory: async (bookmarkId, cursor, limit) => {
      const ordered = jobs.filter((j) => !bookmarkId || j.sourceBookmarkId === bookmarkId || j.destBookmarkId === bookmarkId)
        .slice().sort((a, b) => b.createdAt.localeCompare(a.createdAt) || b.id.localeCompare(a.id));
      const index = cursor ? ordered.findIndex((j) => j.id === cursor) : -1;
      if (cursor && index === -1) throw new Error("invalid job history page or cursor");
      const page = ordered.slice(index + 1, index + 1 + limit);
      return { jobs: page, nextCursor: index + 1 + page.length < ordered.length ? page.at(-1)!.id : "" };
    },
    jobItems: async (id, _cursor, _limit, bookmarkId) => {
      scopedJob(id, bookmarkId);
      return { status: "not_captured", items: [], nextCursor: "" };
    },
    jobItemTotals: async (id, bookmarkId) => {
      scopedJob(id, bookmarkId);
      return { status: "not_captured", items: 0, planned: 0, admitted: 0,
        failed: 0, conflicts: 0, refused: 0, unresolved: 0, unverified: 0, problems: 0 };
    },
    jobs: async (bookmarkId, activeOnly, limit) =>
      jobs
        .filter(
          (j) =>
            bookmarkId === "" ||
            j.sourceBookmarkId === bookmarkId ||
            j.destBookmarkId === bookmarkId,
        )
        .filter(
          (j) =>
            !activeOnly || !["complete", "partial", "cancelled", "failed"].includes(j.state),
        )
        .slice(0, limit)
        .reverse(),
  };
}
