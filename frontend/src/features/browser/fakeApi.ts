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
  ContainerSettingsDTO,
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
import { isStaticWebKey } from "../../lib/containerPresets";
import { corsKeys } from "../../lib/corsRows";

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

/** Container names whose HEAD the fake refuses, so a test can show what an
 * unchecked open container looks like; cleared by each test that sets it. */
export const fakeContainerDetailFailures = new Set<string>();

/** ACLs a fixture container starts with, as if set by another client: seeded
 * state, not observed facts, so the rows start unchecked. */
export const fakeContainerSeeds = new Map<string, { readAcl?: string; writeAcl?: string }>();

export function newFakeApi(): Api {
  let nextId = 1;
  const bookmarks: BookmarkDTO[] = [];
  const generations = new Map<string, number>();
  let connectedScope: ListingScope | null = null;

  // Instance copies: mutations must not leak into the shared fixtures.
  const containers = containersFixture.map((c) => ({ ...c }));
  const accountState = { meta: { ...accountFixture.meta }, quotaBytes: accountFixture.quotaBytes };
  // TempURL key slot presence by canonical name; the fixture account has its
  // first slot set, as the portrait has always shown. The fake keeps no
  // values at all, as nothing ever reads one back.
  const accountSlots = new Set<string>(["Temp-Url-Key"]);
  const containerSlots: Record<string, Set<string>> = {};
  function slotsOf(name: string): Set<string> {
    return (containerSlots[name] ??= new Set());
  }
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
  // A count-only scan answers the number of fixture objects under its prefix.
  const countScans = new Map<string, { container: string; prefix: string }>();
  let auditId = 1;

  function readScope(bookmarkId: string) {
    if (!bookmarkId) throw new Error("account scope changed: name the bookmark the read is for");
    return { bookmarkId, generation: generations.get(bookmarkId) ?? 1 };
  }

  // A write names the scope its view was built under; the backend refuses
  // one that no longer names the active connection (ADR 0079).
  function requireWriteScope(scope: ListingScope) {
    if (!connectedScope || !scope || scope.bookmarkId !== connectedScope.bookmarkId
      || scope.generation !== (generations.get(scope.bookmarkId) ?? 1)) {
      throw new Error("the connected account changed; nothing was written");
    }
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
      readAcl: fakeContainerSeeds.get(name)?.readAcl ?? "",
      writeAcl: fakeContainerSeeds.get(name)?.writeAcl ?? "",
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

  function accountFingerprint(): string {
    return JSON.stringify({ meta: accountState.meta, quotaBytes: accountState.quotaBytes });
  }
  function accountPortrait(scope: ListingScope) {
    return {
      scope: { ...scope },
      name: accountFixture.name,
      containers: containers.length,
      objects: accountFixture.objects,
      bytesUsed: accountFixture.bytes,
      quotaBytes: accountState.quotaBytes,
      meta: { ...accountState.meta },
      tempUrlKeySlots: [...accountSlots].sort(),
      editableFingerprint: accountFingerprint(),
    };
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
      // The real service derives this from class, length and entity tag; the
      // fake only has to be stable and to differ between objects, which is
      // what a caller handing it back is relying on.
      contentFingerprint: `${large?.kind ?? "plain"}:${large?.logicalBytes ?? stored.bytes}:${stored.etag}`,
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

  // The backend derives settings facts once from a container HEAD and
  // holds them per connection generation (ADR 0071); the fake mirrors both
  // so the badges, the banner and the check can be exercised against it.
  const settingsKnown = new Map<string, ContainerSettingsDTO>();
  const failingContainerDetail = fakeContainerDetailFailures;
  function deriveSettings(name: string, generation: number): ContainerSettingsDTO {
    const s = stateOf(name);
    const entries = s.readAcl.split(",").map((e) => e.trim());
    const publicRead = entries.includes(".r:*");
    return {
      publicRead,
      publicListing: publicRead && entries.includes(".rlistings"),
      referrerGrants: entries.filter((e) => e.startsWith(".r:") && e !== ".r:*" && e !== ".r:" && !e.startsWith(".r:-")).map((e) => e.slice(3)),
      sharedWrite: s.writeAcl.trim() !== "",
      staticWeb: Object.keys(s.meta).some(isStaticWebKey),
      versioningMode: s.versioningMode,
      versioningArchive: s.versioningArchive,
      quota: s.quotaBytes > 0 || s.quotaCount > 0,
      cors: false,
      readAcl: s.readAcl,
      writeAcl: s.writeAcl,
      observedAt: "2026-09-23T07:00:00Z",
      generation,
    };
  }
  function rememberSettings(name: string, bookmarkId: string): ContainerSettingsDTO {
    const scope = readScope(bookmarkId);
    const settings = deriveSettings(name, scope.generation);
    settingsKnown.set(`${scope.bookmarkId}:${scope.generation}:${name}`, settings);
    return settings;
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
      cors: Object.values(corsKeys).some((key) => (s.meta[key] ?? "") !== ""),
      staticWeb: Object.keys(s.meta).some(isStaticWebKey),
      tempUrlKeySlots: [...slotsOf(name)].sort(),
      meta: { ...s.meta },
      rawHeaders: { "X-Trans-Id": "txfake" },
      transId: "txfake",
      editableFingerprint: containerFingerprint(name),
      class: c.segmentRole,
      scope: { bookmarkId: "", generation: 0 },
      settings: deriveSettings(name, 0),
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
      enabledCapabilities: [],
      unrecognisedCapabilities: [],
    }),

    listBookmarks: async () => [...bookmarks],
    credentialStatus: async () => ({ retired: 0, unfinished: 0, saving: 0, unverified: 0, cleanupReason: "" }),

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

    // The fixture's one account answers for its own key; a bookmark being
    // edited may lean on its stored key as the backend allows.
    verifyBookmark: async (bookmark, secret) => {
      if (secret === "" && !bookmark.hasCredential) throw new Error("enter the API key to verify these details");
      if (secret !== "" && secret !== "testing") throw new Error("unauthorized: the endpoint rejected the user or key");
      if (!bookmark.authUrl.includes("mock")) throw new Error("auth URL: no such host");
      return { account: accountFixture.name, containers: containers.length, objects: accountFixture.objects, bytesUsed: accountFixture.bytes };
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
      return accountPortrait(connectedScope);
    },

    account: async (bookmarkId) => {
      const scope = connectedScope;
      if (!bookmarkId || scope === null || scope.bookmarkId !== bookmarkId) {
        throw new Error("not connected");
      }
      return accountPortrait(scope);
    },

    capabilities: async (bookmarkId) => {
      if (!bookmarkId || connectedScope === null) throw new Error("not connected");
      if (connectedScope.bookmarkId !== bookmarkId) throw new Error("the connected account changed");
      return { ...capabilities, scope: { ...connectedScope } };
    },

    containers: async (bookmarkId, marker, limit) => {
      const scope = readScope(bookmarkId);
      const rows = containers
        .map((c) => ({
          name: c.name,
          count: c.objectCount,
          bytes: c.bytes,
          class: c.segmentRole,
          settings: settingsKnown.get(`${scope.bookmarkId}:${scope.generation}:${c.name}`) ?? null,
        }))
        .sort((a, b) => (a.name < b.name ? -1 : 1))
        .filter((c) => c.name > marker)
        .slice(0, limit);
      const full = rows.length === limit;
      // The fake echoes the bookmark it was asked for, as the backend
      // does: a page states the account that answered it (ADR 0049).
      return { rows, nextMarker: full ? rows[rows.length - 1].name : "", effectiveLimit: limit, scope: readScope(bookmarkId) };
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
      return { rows, nextMarker: full ? rows[rows.length - 1].name : "", effectiveLimit: limit, scope: readScope(bookmarkId) };
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

    containerDetail: async (name, bookmarkId) => {
      const detail = containerDetailOf(name);
      if (failingContainerDetail.has(name)) throw new Error(`HEAD of ${name} refused by the fixture`);
      return { ...detail, scope: readScope(bookmarkId), settings: rememberSettings(name, bookmarkId) };
    },

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

    updateObjectMetadata: async (scope, container, name, baseFingerprint, edit): Promise<MutationResultDTO> => {
      requireWriteScope(scope);
      const stored = findObject(container, name);
      if (!stored) {
        throw new Error("object not found");
      }
      if (baseFingerprint !== "" && baseFingerprint !== objectFingerprint(container, name)) {
        record("object.metadata.update", `${container}/${name}`, "conflict");
        return { outcome: "conflict", mismatch: "", limitation: "", transIds: ["txfake"], objectAfter: null, containerAfter: null, accountAfter: null };
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
        accountAfter: null,
      };
    },

    updateContainer: async (scope, name, baseFingerprint, edit): Promise<MutationResultDTO> => {
      requireWriteScope(scope);
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
        return { outcome: "conflict", mismatch: "", limitation: "", transIds: ["txfake"], objectAfter: null, containerAfter: null, accountAfter: null };
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
        // The verified post-write state is a fresh observation under the
        // connected scope, as the backend records it (ADR 0071).
        containerAfter: {
          ...containerDetailOf(name),
          // The after-state names the scope the write was bound to, so a
          // caller can edit again from it (ADR 0059).
          scope: { ...scope },
          settings: connectedScope ? rememberSettings(name, connectedScope.bookmarkId) : deriveSettings(name, 0),
        },
        accountAfter: null,
      };
    },

    updateAccountMetadata: async (scope, baseFingerprint, edit): Promise<MutationResultDTO> => {
      requireWriteScope(scope);
      for (const key of Object.keys(edit.meta ?? {})) {
        const lower = key.toLowerCase();
        if (lower.startsWith("temp-url-key")) {
          throw new Error(`metadata key "${key}" is a TempURL secret slot and cannot be set through the metadata editor`);
        }
        if (lower === "quota-bytes") {
          throw new Error(`metadata key "${key}" is a quota; set it through its own field rather than a metadata entry`);
        }
      }
      if (baseFingerprint !== "" && baseFingerprint !== accountFingerprint()) {
        record("account.metadata.update", "account", "conflict");
        return { outcome: "conflict", mismatch: "", limitation: "", transIds: ["txfake"], objectAfter: null, containerAfter: null, accountAfter: null };
      }
      accountState.meta = cleanMap(edit.meta);
      accountState.quotaBytes = Math.max(edit.quotaBytes, 0);
      record("account.metadata.update", "account", "applied");
      return { outcome: "applied", mismatch: "", limitation: "", transIds: ["txfake", "txfake"], objectAfter: null, containerAfter: null, accountAfter: accountPortrait(scope) };
    },

    updateTempUrlKey: async (scope, edit): Promise<MutationResultDTO> => {
      requireWriteScope(scope);
      const slot = ["Temp-Url-Key", "Temp-Url-Key-2"].find((name) => name.toLowerCase() === edit.slot.toLowerCase());
      if (!slot) throw new Error(`tempurl key slot "${edit.slot}" is not one of Temp-URL-Key or Temp-URL-Key-2`);
      if (edit.clear && edit.secret !== "") throw new Error("a tempurl key clear carries no secret");
      if (!edit.clear && edit.secret === "") throw new Error("tempurl key secret is empty; use clear to remove the slot");
      if (!edit.clear && edit.secret.trim() !== edit.secret) {
        throw new Error("tempurl key secret has leading or trailing ASCII spaces or tabs, which HTTP does not preserve");
      }
      const resource = edit.container === "" ? "account" : edit.container;
      if (edit.container !== "" && !containers.some((c) => c.name === edit.container)) throw new Error("container not found");
      const slots = edit.container === "" ? accountSlots : slotsOf(edit.container);
      const presence = (names: Iterable<string>) => [...names].map((name) => name.toLowerCase()).sort().join(",");
      const nothing = { mismatch: "", limitation: "", objectAfter: null, containerAfter: null, accountAfter: null };
      if (presence(edit.baseSlots ?? []) !== presence(slots)) {
        record("tempurl.key.update", resource, "conflict");
        return { outcome: "conflict", transIds: ["txfake"], ...nothing };
      }
      if (edit.clear) slots.delete(slot);
      else slots.add(slot);
      record("tempurl.key.update", resource, "applied");
      const after = edit.container === ""
        ? { accountAfter: accountPortrait(scope) }
        : { containerAfter: { ...containerDetailOf(edit.container), scope: { ...scope }, settings: rememberSettings(edit.container, scope.bookmarkId) } };
      return { outcome: "applied", transIds: ["txfake", "txfake", "txfake"], ...nothing, ...after };
    },

    createContainer: async (scope, create) => {
      requireWriteScope(scope);
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

    createMarker: async (scope, container, prefix) => {
      requireWriteScope(scope);
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
      return { prefix, markerBacked: true, selfObject: objectDetailOf(container, prefix), scope: { ...scope } };
    },

    deleteMarker: async (scope, container, prefix, contentFingerprint): Promise<MutationResultDTO> => {
      requireWriteScope(scope);
      if (contentFingerprint === "") {
        throw new Error("deleting needs the content identity the caller was shown; a delete that cannot say what it meant to destroy is refused");
      }
      const stored = findObject(container, prefix);
      if (!stored) {
        record("marker.delete", `${container}/${prefix}`, "failed");
        throw new Error("no marker object at this prefix");
      }
      if (stored.bytes !== 0 || stored.contentType !== "application/directory") {
        record("marker.delete", `${container}/${prefix}`, "failed");
        throw new Error(`the object at ${container}/${prefix} is not a directory marker (${stored.contentType}, ${stored.bytes} bytes); refusing to delete it through the marker path`);
      }
      const detail = objectDetailOf(container, prefix);
      if (detail.contentFingerprint !== contentFingerprint) {
        record("marker.delete", `${container}/${prefix}`, "conflict");
        throw new Error(`${container}/${prefix} is not what you were shown: it now reports a plain of ${detail.contentLength} bytes with entity tag ${detail.etag}, so nothing was deleted (expected identity ${contentFingerprint}, found ${detail.contentFingerprint})`);
      }
      objects[container] = objects[container].filter((o) => o.name !== prefix);
      record("marker.delete", `${container}/${prefix}`, "applied");
      return { outcome: "applied", mismatch: "", limitation: "", transIds: ["txfake", "txfake"], objectAfter: null, containerAfter: null, accountAfter: null };
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

    deleteManifest: async (scope, container, name, contentFingerprint): Promise<MutationResultDTO> => {
      requireWriteScope(scope);
      if (contentFingerprint === "") {
        throw new Error("deleting needs the content identity the caller was shown; a delete that cannot say what it meant to destroy is refused");
      }
      const detail = objectDetailOf(container, name);
      if (detail.class === "plain") {
        record("manifest.delete", `${container}/${name}`, "failed");
        throw new Error(`${container}/${name} is a plain object, not a manifest; the manifest-only delete refuses it`);
      }
      if (detail.followedSymlink || detail.symlinkTarget) {
        record("manifest.delete", `${container}/${name}`, "failed");
        throw new Error(`${container}/${name} is a symlink: the identity shown is its target's`);
      }
      if (detail.contentFingerprint !== contentFingerprint) {
        record("manifest.delete", `${container}/${name}`, "conflict");
        throw new Error(`${container}/${name} is not what you were shown: it now reports a ${detail.class} of ${detail.contentLength} bytes with entity tag ${detail.etag}, so nothing was deleted (expected identity ${contentFingerprint}, found ${detail.contentFingerprint})`);
      }
      objects[container] = (objects[container] ?? []).filter((o) => o.name !== name);
      record("manifest.delete", `${container}/${name}`, "applied");
      return { outcome: "applied", mismatch: "", limitation: "", transIds: ["txfake", "txfake"], objectAfter: null, containerAfter: null, accountAfter: null };
    },

    deleteObject: async (scope, container, name, contentFingerprint): Promise<MutationResultDTO> => {
      requireWriteScope(scope);
      if (contentFingerprint === "") {
        throw new Error("deleting needs the content identity the caller was shown; a delete that cannot say what it meant to destroy is refused");
      }
      const detail = objectDetailOf(container, name);
      if (detail.class !== "plain") {
        record("object.delete", `${container}/${name}`, "failed");
        throw new Error(`${container}/${name} is a ${detail.class} manifest, not a plain object; the manifest deletion owns it`);
      }
      if (detail.followedSymlink || detail.symlinkTarget) {
        record("object.delete", `${container}/${name}`, "failed");
        throw new Error(`${container}/${name} is a symlink: the identity shown is its target's`);
      }
      if (detail.contentFingerprint !== contentFingerprint) {
        record("object.delete", `${container}/${name}`, "conflict");
        throw new Error(`${container}/${name} is not what you were shown: it now reports a plain of ${detail.contentLength} bytes with entity tag ${detail.etag}, so nothing was deleted (expected identity ${contentFingerprint}, found ${detail.contentFingerprint})`);
      }
      objects[container] = (objects[container] ?? []).filter((o) => o.name !== name);
      record("object.delete", `${container}/${name}`, "applied");
      return { outcome: "applied", mismatch: "", limitation: "", transIds: ["txfake", "txfake"], objectAfter: null, containerAfter: null, accountAfter: null };
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
        .filter((e) => q.bookmarkId === "" || e.bookmarkId === q.bookmarkId)
        .filter((e) => q.operationPrefix === "" || e.operation.startsWith(q.operationPrefix))
        .filter((e) => q.result === "" || e.result === q.result)
        .filter((e) => q.beforeId === 0 || e.id < q.beforeId)
        .slice(0, q.limit > 0 ? q.limit : 100),
    exportAudit: async (q) => ({
      count: audit
        .filter((e) => q.bookmarkId === "" || e.bookmarkId === q.bookmarkId)
        .filter((e) => q.operationPrefix === "" || e.operation.startsWith(q.operationPrefix))
        .filter((e) => q.result === "" || e.result === q.result).length,
      directory: "/home/operator/exports",
      fileName: "aeronautes-audit-20260102T000000Z.jsonl",
    }),
    exportSettings: async () => ({
      bookmarkCount: 2,
      directory: "/home/operator/exports",
      fileName: "aeronautes-settings-20260102T000000Z.json",
    }),
    importSettings: async () => ({
      settingsApplied: true,
      bookmarksTotal: 2,
      bookmarksCreated: 1,
      bookmarksSkipped: 1,
      bookmarksUnresolved: 0,
      complete: true,
      detail: "",
    }),

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
        largeObjectsAsDlo: false,
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
      laterBaselineRows: 2,
      requestCost: 86,
      strategy: "per-name-verified",
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
    startSelectedDelete: async (req) => {
      requireWriteScope(req.scope);
      const members = req.members ?? [];
      if (members.length === 0) throw new Error("nothing is selected; a selected-set deletion needs at least one object");
      const seen = new Set<string>();
      for (const member of members) {
        if (member.kind !== "object") {
          throw new Error(`${JSON.stringify(member.name)} is a ${member.kind} row, not an object; a prefix is deleted by the bulk deletion that owns it`);
        }
        if (member.name === "") throw new Error("a member needs its name; an empty name would address the container itself");
        if (seen.has(member.name)) throw new Error(`${JSON.stringify(member.name)} is selected twice; a member is deleted once`);
        seen.add(member.name);
      }
      if (req.confirm !== String(members.length)) {
        throw new Error(`confirmation ${JSON.stringify(req.confirm)} does not match the number of members, ${members.length}; type it exactly`);
      }
      // The fake deletes every member as listed and ends the job at once;
      // the real job compares each member first and records what it found.
      const names = new Set(members.map((m) => m.name));
      objects[req.container] = (objects[req.container] ?? []).filter((o) => !names.has(o.name));
      const id = `job-${jobs.length + 1}`;
      jobs.push({
        id, type: "delete_selected", state: "complete", source: req.container, destination: "(deleted)",
        sourceBookmarkId: req.scope.bookmarkId, destBookmarkId: "", createdAt: "2026-10-27T10:00:00Z",
        startedAt: "2026-10-27T10:00:01Z", finishedAt: "2026-10-27T10:00:02Z", engine: "native", settings: "", recoveryRecords: 0,
        counters: { objectsPlanned: members.length, objectsCopied: members.length, objectsVerified: 0, objectsFailed: 0,
          bytesPlanned: 0, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: members.length },
        error: "",
      });
      record("delete.selected.start", req.container, "applied");
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
      enabledCapabilities: [],
      unrecognisedCapabilities: [],
      rclonePin: "v1.74.3",
      license: "Apache-2.0 (this application)",
      licenses: [{ name: "example", license: "MIT", url: "https://example.com" }],
    }),
    appSettings: async () => ({
      maxActiveJobs: 2, defaultProfile: "auto", auditRetentionDays: 90, jobRetentionDays: 30, downloadObjectCeiling: 250_000,
      browserRetentionCeiling: 100_000, maxQueuedJobs: 16, maxConnsPerHost: 16,
      reclaimReplacedFragments: true, railWidthPx: 0,
    }),
    saveAppSettings: async (settings) => settings,
    startFind: async (container, prefix, query, bookmarkId, countOnly = false) => {
      readScope(bookmarkId);
      if (!countOnly && query.trim() === "") throw new Error("container and a non-empty query are required");
      const id = `find-${scans.size + 1}`;
      scans.set(id, bookmarkId);
      if (countOnly) countScans.set(id, { container, prefix });
      return id;
    },
    findStatus: async (id, bookmarkId) => {
      const count = countScans.get(id);
      if (count) {
        return {
          scanned: (objects[count.container] ?? []).filter((o) => o.name.startsWith(count.prefix)).length,
          matches: [], truncated: false, done: true, error: "", objectsPerSecond: 5000,
          scope: scanScope(id, bookmarkId),
        };
      }
      return {
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
      };
    },
    cancelFind: async (id, bookmarkId) => { scanScope(id, bookmarkId); },
    checkSegmentOwnership: async (req) => ({
      class: "dlo",
      target: "media_segments/owned/",
      proven: req.name === "owned.bin",
      segments: 0,
      inlineEntries: 0,
      reviewed: { class: "dlo", fingerprint: "dlo:0:etag", length: 0, etag: "", namespace: "media_segments/owned/" },
    }),
    deleteManifestWithSegments: async (req) => {
      if (req.confirm !== req.name) {
        throw new Error("confirmation does not match the object name");
      }
      if (!req.force) {
        throw new Error("deleting segments needs the Backstage force with typed confirmation");
      }
      // A job now (ADR 0095 decision 8): the manifest is removed and the job
      // ends at once; the real job compares the reviewed graph first.
      objects[req.container] = (objects[req.container] ?? []).filter((o) => o.name !== req.name);
      const id = `job-${jobs.length + 1}`;
      jobs.push({
        id, type: "delete_segments", state: "complete", source: `${req.container}/${req.name}`, destination: "(manifest + segments)",
        sourceBookmarkId: req.bookmarkId, destBookmarkId: "", createdAt: "2026-10-27T10:00:00Z",
        startedAt: "2026-10-27T10:00:01Z", finishedAt: "2026-10-27T10:00:02Z", engine: "native", settings: "", recoveryRecords: 0,
        counters: { objectsPlanned: 4, objectsCopied: 4, objectsVerified: 0, objectsFailed: 0,
          bytesPlanned: 0, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 4 },
        error: "",
      });
      record("segments.delete.start", `${req.container}/${req.name}`, "applied");
      return id;
    },
    recoveryStatus: async () => ({ jobs: 0, records: 0 }),
    interruptedDisclosures: async () => ({ entries: [], total: 0 }),
    dismissInterrupted: async () => ({ dismissed: 0 }),
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
        failed: 0, conflicts: 0, refused: 0, unresolved: 0, unverified: 0, problems: 0, issued: 0, deleted: 0, unsettled: 0 };
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
