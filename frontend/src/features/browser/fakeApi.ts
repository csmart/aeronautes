// Test-only Api implementation over the §58 static fixtures, reusing the
// delimiter-view mirror so navigation logic is exercised with the same
// semantics the live services produce. Not imported by application code.

import type {
  Api,
  BookmarkDTO,
  ListingRowDTO,
  ObjectDetailDTO,
} from "../../lib/api";
import {
  accountFixture,
  containersFixture,
  fixtureObjects,
  largeObjectDetail,
  objectMetaFixture,
} from "../../lib/fixtures";
import { delimiterView } from "../../lib/listing";

export function newFakeApi(): Api {
  let nextId = 1;
  const bookmarks: BookmarkDTO[] = [];

  return {
    info: async () => ({ name: "Aeronautes", version: "test", wails: "test" }),

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
        containers: containersFixture.length,
        objects: accountFixture.objects,
        bytesUsed: accountFixture.bytes,
        quotaBytes: 0,
        meta: accountFixture.meta,
        tempUrlKeySlots: ["Temp-Url-Key"],
      };
    },

    containers: async (marker, limit) => {
      const rows = containersFixture
        .map((c) => ({ name: c.name, count: c.objectCount, bytes: c.bytes }))
        .sort((a, b) => (a.name < b.name ? -1 : 1))
        .filter((c) => c.name > marker)
        .slice(0, limit);
      const full = rows.length === limit;
      return { rows, nextMarker: full ? rows[rows.length - 1].name : "" };
    },

    objects: async (container, prefix, marker, limit) => {
      const view = delimiterView(fixtureObjects[container] ?? [], prefix);
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

    prefixDetail: async (container, prefix) => {
      const self = (fixtureObjects[container] ?? []).find((o) => o.name === prefix);
      if (!self) {
        return { prefix, markerBacked: false, selfObject: null };
      }
      return {
        prefix,
        markerBacked: self.bytes === 0 && self.contentType === "application/directory",
        selfObject: {
          name: self.name,
          class: "plain",
          contentLength: self.bytes,
          contentType: self.contentType,
          etag: self.etag,
          lastModified: self.lastModified,
          manifest: "",
          deleteAt: "",
          meta: {},
          rawHeaders: {},
          transId: "txfake",
        },
      };
    },

    objectDetail: async (container, name) => {
      const stored = (fixtureObjects[container] ?? []).find((o) => o.name === name);
      if (!stored) {
        throw new Error("object not found");
      }
      const large = largeObjectDetail[name];
      const detail: ObjectDetailDTO = {
        name,
        class: large?.kind ?? "plain",
        contentLength: large?.logicalBytes ?? stored.bytes,
        contentType: stored.contentType,
        etag: stored.etag,
        lastModified: stored.lastModified,
        manifest: large?.kind === "dlo" ? large.manifest : "",
        deleteAt: "",
        meta: objectMetaFixture[name] ?? {},
        rawHeaders: { "X-Trans-Id": "txfake" },
        transId: "txfake",
      };
      return detail;
    },
  };
}
