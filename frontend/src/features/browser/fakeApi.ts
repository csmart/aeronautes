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

const capabilities: CapabilitiesDTO = {
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
  let auditId = 1;

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
      deleteAt: objectDeleteAt[`${container}/${name}`] ?? "",
      meta: { ...(objectMeta[`${container}/${name}`] ?? {}) },
      rawHeaders: { "X-Trans-Id": "txfake" },
      transId: "txfake",
      editableFingerprint: objectFingerprint(container, name),
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
    };
  }

  return {
    info: async () => ({
      name: "Aeronautes",
      version: "test",
      wails: "test",
      secretBackend: "session",
    }),

    listBookmarks: async () => [...bookmarks],

    saveBookmark: async (bookmark, secret) => {
      const saved = { ...bookmark };
      if (saved.id === "") {
        saved.id = `bm-${nextId++}`;
        bookmarks.push(saved);
      } else {
        const at = bookmarks.findIndex((b) => b.id === saved.id);
        if (at === -1) {
          throw new Error("bookmark not found");
        }
        saved.hasCredential = bookmarks[at].hasCredential;
        bookmarks[at] = saved;
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
    },

    connect: async (bookmarkId) => {
      const bookmark = bookmarks.find((b) => b.id === bookmarkId);
      if (!bookmark) {
        throw new Error("bookmark not found");
      }
      if (!bookmark.hasCredential) {
        throw new Error("no credential on record");
      }
      return {
        name: accountFixture.name,
        containers: containers.length,
        objects: accountFixture.objects,
        bytesUsed: accountFixture.bytes,
        quotaBytes: 0,
        meta: accountFixture.meta,
        tempUrlKeySlots: ["Temp-Url-Key"],
      };
    },

    capabilities: async () => ({ ...capabilities }),

    containers: async (marker, limit) => {
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
      return { rows, nextMarker: full ? rows[rows.length - 1].name : "" };
    },

    objects: async (container, prefix, marker, limit) => {
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
      return { rows, nextMarker: full ? rows[rows.length - 1].name : "" };
    },

    objectDetail: async (container, name) => objectDetailOf(container, name),

    prefixDetail: async (container, prefix): Promise<PrefixDetailDTO> => {
      const self = findObject(container, prefix);
      if (!self) {
        return { prefix, markerBacked: false, selfObject: null };
      }
      return {
        prefix,
        markerBacked: self.bytes === 0 && self.contentType === "application/directory",
        selfObject: objectDetailOf(container, prefix),
      };
    },

    containerDetail: async (name) => containerDetailOf(name),

    sloManifest: async (_container, name) => {
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
      };
    },

    updateObjectMetadata: async (container, name, baseFingerprint, edit): Promise<MutationResultDTO> => {
      const stored = findObject(container, name);
      if (!stored) {
        throw new Error("object not found");
      }
      if (baseFingerprint !== "" && baseFingerprint !== objectFingerprint(container, name)) {
        record("object.metadata.update", `${container}/${name}`, "conflict");
        return { outcome: "conflict", mismatch: "", transIds: ["txfake"], objectAfter: null, containerAfter: null };
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
        return { outcome: "conflict", mismatch: "", transIds: ["txfake"], objectAfter: null, containerAfter: null };
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
      return { prefix, markerBacked: true, selfObject: objectDetailOf(container, prefix) };
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

    dloSegments: async (manifest) => {
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

    auditRecent: async (limit) => [...audit].reverse().slice(0, limit),
  };
}
