// The §58 canonical fixture namespace as static data for the static
// browser/inspector (§65 commit 11). Every name here exists because it
// exercises a classification the UI must get right; the live wiring
// replaces this module with BrowseService calls, and the same names come
// back from the SAIO fixture loader (§57.3).

import type { ContainerEntry } from "./types";
import type { FixtureObject } from "./listing";

export const accountFixture = {
  name: "AUTH_demo",
  containers: 6,
  objects: 24,
  bytes: 47_461_000_000,
  quotaBytes: 0, // unset
  meta: { "Book-Owner": "ops-team" } as Record<string, string>,
  tempURLKeys: 1,
};

/** Containers with their §12.2 segment-store classification. */
export const containersFixture: ContainerEntry[] = [
  { name: "fixtures", objectCount: 15, bytes: 47_460_000_000, policy: "standard", segmentRole: "normal" },
  { name: "external-dlo-segments", objectCount: 43, bytes: 42_700_000_000, policy: "standard", segmentRole: "confirmed" },
  { name: "slo-parts", objectCount: 3, bytes: 3_100_000_000, policy: "standard", segmentRole: "confirmed" },
  { name: "confirmed_segments", objectCount: 0, bytes: 0, policy: "standard", segmentRole: "confirmed" },
  // Named like a segment store but a LEGITIMATE normal container — the
  // heuristic must group it as "possible backstage", never hide it (§58).
  { name: "photos_segments", objectCount: 3, bytes: 900_000_000, policy: "ec42", segmentRole: "suspected" },
  { name: ".file-segments", objectCount: 2, bytes: 12_000_000, policy: "standard", segmentRole: "suspected" },
];

const AUG = "2026-08-12T10:2";

/** The main fixture container's deep namespace (§58). */
export const fixtureObjects: Record<string, FixtureObject[]> = {
  fixtures: [
    { name: "normal/hello.txt", bytes: 12, etag: "5eb63bbbe01eeed093cb22bb8f5acdc3", lastModified: `${AUG}1:00Z`, contentType: "text/plain" },
    { name: "empty-object", bytes: 0, etag: "d41d8cd98f00b204e9800998ecf8427e", lastModified: `${AUG}1:30Z`, contentType: "application/octet-stream" },
    { name: "folder-marker/", bytes: 0, etag: "d41d8cd98f00b204e9800998ecf8427e", lastModified: `${AUG}2:00Z`, contentType: "application/directory" },
    { name: "implied/child.txt", bytes: 2_048, etag: "9a0364b9e99bb480dd25e1f0284c8555", lastModified: `${AUG}2:10Z`, contentType: "text/plain" },
    // A real object wearing a folder suit + a child implying the same
    // prefix (§14.1 trap: the listing folds both into one prefix row).
    { name: "fake/", bytes: 21, etag: "6f1ed002ab5595859014ebf0951522d9", lastModified: `${AUG}2:15Z`, contentType: "text/plain" },
    { name: "fake/child.txt", bytes: 5, etag: "6f1ed002ab5595859014ebf0951522d8", lastModified: `${AUG}2:16Z`, contentType: "text/plain" },
    { name: "unicode/日本語.txt", bytes: 512, etag: "0d5cb1cbf5ec1e5c6f6cbba6c92b02d4", lastModified: `${AUG}2:20Z`, contentType: "text/plain" },
    { name: "weird/a+b.txt", bytes: 64, etag: "1a79a4d60de6718e8e5b326e338ae533", lastModified: `${AUG}3:00Z`, contentType: "text/plain" },
    { name: "weird/percent%name", bytes: 96, etag: "2b00042f7481c7b056c4b410d28f33cf", lastModified: `${AUG}3:10Z`, contentType: "application/octet-stream" },
    { name: "weird/repeated//slash", bytes: 128, etag: "3c59dc048e8850243be8079a5c74d079", lastModified: `${AUG}3:20Z`, contentType: "application/octet-stream" },
    { name: "external-dlo", bytes: 0, etag: "d41d8cd98f00b204e9800998ecf8427e", lastModified: `${AUG}4:00Z`, contentType: "video/mp4", largeObject: "dlo" },
    { name: "external-slo", bytes: 1_024, etag: "9f2b8c6e4d2a1f2b7e9d3c8a4b1f0e21", lastModified: `${AUG}4:10Z`, contentType: "application/x-tar", largeObject: "slo" },
  ],
  "external-dlo-segments": [
    { name: "wedding-video/0000000000000001", bytes: 1_000_000_000, etag: "a94f2c1e000000000000000000000001", lastModified: `${AUG}0:00Z`, contentType: "application/octet-stream" },
    { name: "wedding-video/0000000000000002", bytes: 1_000_000_000, etag: "b8de91a4000000000000000000000002", lastModified: `${AUG}0:01Z`, contentType: "application/octet-stream" },
    { name: "wedding-video/0000000000000003", bytes: 1_000_000_000, etag: "c13a7b6e000000000000000000000003", lastModified: `${AUG}0:02Z`, contentType: "application/octet-stream" },
  ],
  "slo-parts": [
    { name: "backup/part-000001", bytes: 1_000_000_000, etag: "f0e1d2c300000000000000000000ff01", lastModified: `${AUG}0:03Z`, contentType: "application/octet-stream" },
    { name: "backup/part-000002", bytes: 1_000_000_000, etag: "f0e1d2c300000000000000000000ff02", lastModified: `${AUG}0:04Z`, contentType: "application/octet-stream" },
    { name: "backup/part-000003", bytes: 1_100_000_000, etag: "f0e1d2c300000000000000000000ff03", lastModified: `${AUG}0:05Z`, contentType: "application/octet-stream" },
  ],
  confirmed_segments: [],
  photos_segments: [
    { name: "sunrise.jpg", bytes: 300_000_000, etag: "11f0284c855511f0284c855511f02840", lastModified: `${AUG}5:00Z`, contentType: "image/jpeg" },
    { name: "sunset.jpg", bytes: 300_000_000, etag: "22f0284c855511f0284c855511f02841", lastModified: `${AUG}5:01Z`, contentType: "image/jpeg" },
    { name: "harbour.jpg", bytes: 300_000_000, etag: "33f0284c855511f0284c855511f02842", lastModified: `${AUG}5:02Z`, contentType: "image/jpeg" },
  ],
  ".file-segments": [
    { name: "blob/000001", bytes: 6_000_000, etag: "44f0284c855511f0284c855511f02843", lastModified: `${AUG}6:00Z`, contentType: "application/octet-stream" },
    { name: "blob/000002", bytes: 6_000_000, etag: "55f0284c855511f0284c855511f02844", lastModified: `${AUG}6:01Z`, contentType: "application/octet-stream" },
  ],
};

/** Static HEAD-level detail the inspector shows for large objects (§53). */
export const largeObjectDetail: Record<
  string,
  {
    kind: "dlo" | "slo";
    logicalBytes: number;
    manifest: string;
    segments: { name: string; bytes: number; etag: string }[];
  }
> = {
  "external-dlo": {
    kind: "dlo",
    logicalBytes: 3_000_000_000,
    manifest: "external-dlo-segments/wedding-video/",
    segments: fixtureObjects["external-dlo-segments"].map((s) => ({
      name: `external-dlo-segments/${s.name}`,
      bytes: s.bytes,
      etag: s.etag,
    })),
  },
  "external-slo": {
    kind: "slo",
    logicalBytes: 3_100_000_000,
    manifest: "slo-parts/backup/ (3 segments)",
    segments: fixtureObjects["slo-parts"].map((s) => ({
      name: `slo-parts/${s.name}`,
      bytes: s.bytes,
      etag: s.etag,
    })),
  },
};

/** Static user metadata per object name — bare metadata names, as the
 * live DTOs report them (the X-Object-Meta- prefix is wire detail). */
export const objectMetaFixture: Record<string, Record<string, string>> = {
  "external-dlo": {
    Shoot: "wedding",
    Location: "sydney",
    Camera: "sony-fx6",
  },
  "normal/hello.txt": { Origin: "fixture" },
};

/** The three saved endpoints on the rail. */
export const bookmarksFixture = [
  { id: "prod-au", label: "Prod AU", active: true },
  { id: "dr-eu", label: "DR EU", active: false },
  { id: "dev-saio", label: "Dev SAIO", active: false },
];
