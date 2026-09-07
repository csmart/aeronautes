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

describe("delimiterView (real-Swift grouping, §14.1)", () => {
  it("splits objects and child prefixes at the delimiter", () => {
    const rows = delimiterView(
      [obj("empty-object", 0), obj("normal/hello.txt"), obj("implied/child.txt")],
      "",
    );
    expect(rows).toEqual([
      expect.objectContaining({ kind: "object", name: "empty-object" }),
      { kind: "prefix", prefix: "implied/" },
      { kind: "prefix", prefix: "normal/" },
    ]);
  });

  it("folds every boundary object into one prefix row", () => {
    // A lone marker, a marker with children, and a folder-ish real
    // object all look identical in the listing; PrefixDetail tells
    // them apart on selection.
    const rows = delimiterView(
      [
        marker("folder-marker/"),
        marker("both/"),
        obj("both/x.txt"),
        obj("fake/", 120),
        obj("fake/child.txt"),
      ],
      "",
    );
    expect(rows).toEqual([
      { kind: "prefix", prefix: "both/" },
      { kind: "prefix", prefix: "fake/" },
      { kind: "prefix", prefix: "folder-marker/" },
    ]);
  });

  it("skips the query prefix's own self object", () => {
    const objects = [obj("fake/", 120), obj("fake/child.txt")];
    expect(delimiterView(objects, "fake/")).toEqual([
      expect.objectContaining({ kind: "object", name: "fake/child.txt" }),
    ]);
  });

  it.each(["external", "  ", "  scope //雪 %2F "])("keeps a plain object exactly matching %j", (prefix) => {
    expect(delimiterView([obj(prefix), obj(prefix + "!"), obj(prefix + "/child"), obj("outside")], prefix)).toEqual([
      expect.objectContaining({ kind: "object", name: prefix }),
      expect.objectContaining({ kind: "object", name: prefix + "!" }),
      { kind: "prefix", prefix: prefix + "/" },
    ]);
  });

  it("walks repeated slashes one level at a time", () => {
    const objects = [obj("weird/repeated//slash")];
    expect(delimiterView(objects, "weird/")).toEqual([
      { kind: "prefix", prefix: "weird/repeated/" },
    ]);
    expect(delimiterView(objects, "weird/repeated/")).toEqual([
      { kind: "prefix", prefix: "weird/repeated//" },
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
  it.each([
    { prefix: "external", parent: "", crumbs: [] },
    { prefix: "  ", parent: "", crumbs: [] },
    { prefix: "  scope //雪 %2F", parent: "  scope //", crumbs: [
      { label: "  scope ", prefix: "  scope /" }, { label: "", prefix: "  scope //" },
    ] },
  ])("keeps incomplete prefix $prefix separate from folder navigation", ({ prefix, parent, crumbs }) => {
    expect(parentPrefix(prefix)).toBe(parent);
    expect(breadcrumbs(prefix)).toEqual(crumbs);
  });

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
