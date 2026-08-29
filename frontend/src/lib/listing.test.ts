import { describe, expect, it } from "vitest";
import {
  breadcrumbs,
  delimiterView,
  parentPrefix,
  type FixtureObject,
} from "./listing";

function obj(name: string, bytes = 10, contentType = "text/plain"): FixtureObject {
  return { name, bytes, etag: "e", lastModified: "2026-08-01T00:00:00Z", contentType };
}

const marker = (name: string): FixtureObject => obj(name, 0, "application/directory");

describe("delimiterView", () => {
  it("splits objects and child prefixes at the delimiter", () => {
    const rows = delimiterView(
      [obj("empty-object", 0), obj("normal/hello.txt"), obj("implied/child.txt")],
      "",
    );
    expect(rows).toEqual([
      expect.objectContaining({ kind: "object", name: "empty-object" }),
      expect.objectContaining({ kind: "prefix", prefix: "implied/", markerBacked: false }),
      expect.objectContaining({ kind: "prefix", prefix: "normal/", markerBacked: false }),
    ]);
  });

  it("marks a lone folder marker as a marker-backed prefix", () => {
    const rows = delimiterView([marker("folder-marker/")], "");
    expect(rows).toEqual([
      { kind: "prefix", prefix: "folder-marker/", markerBacked: true },
    ]);
  });

  it("merges a marker with its children into one row", () => {
    const rows = delimiterView([marker("both/"), obj("both/x.txt")], "");
    expect(rows).toEqual([{ kind: "prefix", prefix: "both/", markerBacked: true }]);
  });

  it("keeps a folder-ish real object as an object row", () => {
    const rows = delimiterView([obj("fake-folder/", 120)], "");
    expect(rows).toEqual([
      expect.objectContaining({ kind: "object", name: "fake-folder/", bytes: 120 }),
    ]);
  });

  it("lists a folder-ish object alongside the prefix its children imply", () => {
    const rows = delimiterView([obj("fake/", 120), obj("fake/child.txt")], "");
    expect(rows).toEqual([
      expect.objectContaining({ kind: "object", name: "fake/" }),
      expect.objectContaining({ kind: "prefix", prefix: "fake/", markerBacked: false }),
    ]);
  });

  it("walks repeated slashes one level at a time", () => {
    const objects = [obj("weird/repeated//slash")];
    expect(delimiterView(objects, "weird/")).toEqual([
      { kind: "prefix", prefix: "weird/repeated/", markerBacked: false },
    ]);
    expect(delimiterView(objects, "weird/repeated/")).toEqual([
      { kind: "prefix", prefix: "weird/repeated//", markerBacked: false },
    ]);
    expect(delimiterView(objects, "weird/repeated//")).toEqual([
      expect.objectContaining({ kind: "object", name: "weird/repeated//slash" }),
    ]);
  });

  it("keeps hostile names byte-exact", () => {
    const rows = delimiterView(
      [obj("weird/a+b.txt"), obj("weird/percent%name"), obj("unicode/日本語.txt")],
      "weird/",
    );
    expect(rows.map((r) => (r.kind === "object" ? r.name : r.prefix))).toEqual([
      "weird/a+b.txt",
      "weird/percent%name",
    ]);
  });
});

describe("prefix navigation helpers", () => {
  it("builds breadcrumbs from a prefix", () => {
    expect(breadcrumbs("")).toEqual([]);
    expect(breadcrumbs("a/b/")).toEqual([
      { label: "a", prefix: "a/" },
      { label: "b", prefix: "a/b/" },
    ]);
    // Empty crumb between repeated slashes is a real navigable level.
    expect(breadcrumbs("a//")).toEqual([
      { label: "a", prefix: "a/" },
      { label: "", prefix: "a//" },
    ]);
  });

  it("computes parent prefixes", () => {
    expect(parentPrefix("")).toBe("");
    expect(parentPrefix("a/")).toBe("");
    expect(parentPrefix("a/b/")).toBe("a/");
    expect(parentPrefix("a//")).toBe("a/");
  });
});
