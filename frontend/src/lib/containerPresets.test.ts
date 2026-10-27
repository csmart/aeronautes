import { describe, expect, it } from "vitest";
import {
  grantsWorldListing,
  grantsWorldRead,
  hasStaticWeb,
  withStaticWeb,
  withWorldListing,
  withWorldRead,
} from "./containerPresets";

describe("world read and listing read from the ACL", () => {
  it.each([
    [".r:*", true, false],
    [".r:*,.rlistings", true, true],
    [" .r:* , .rlistings ", true, true],
    [".rlistings,.r:*", true, true],
    [".r:-.example.com,.r:*", true, false],
    // Referrer grants, denials and near misses are not world read (ADR 0071).
    [".r:*.example.com", false, false],
    [".r:.example.com,.rlistings", false, false],
    [".r:*x", false, false],
    [".r:", false, false],
    [".rlistings", false, false],
    ["account:user", false, false],
    ["", false, false],
  ])("%j → read %s, listing %s", (acl, read, listing) => {
    expect(grantsWorldRead(acl)).toBe(read);
    expect(grantsWorldListing(acl)).toBe(listing);
  });
});

describe("ticking rewrites only the entries the tick stands for", () => {
  it("adds world read after the existing entries and leaves it alone when present", () => {
    expect(withWorldRead("", true)).toBe(".r:*");
    expect(withWorldRead("account:user", true)).toBe("account:user,.r:*");
    expect(withWorldRead(" .r:* , account:user", true)).toBe(" .r:* , account:user");
  });

  it("removes world read and the listing that depends on it, keeping other grants", () => {
    expect(withWorldRead(".r:*,.rlistings,account:user", false)).toBe("account:user");
    expect(withWorldRead(".r:*.example.com,.r:*", false)).toBe(".r:*.example.com");
    expect(withWorldRead("account:user", false)).toBe("account:user");
  });

  it("adds and removes the listing entry alone", () => {
    expect(withWorldListing(".r:*", true)).toBe(".r:*,.rlistings");
    expect(withWorldListing(".r:*,.rlistings", true)).toBe(".r:*,.rlistings");
    expect(withWorldListing(".r:*,.rlistings,account:user", false)).toBe(".r:*,account:user");
  });

  it("round-trips: the tick read back from the rewritten value is the tick that was set", () => {
    for (const acl of ["", "account:user", ".r:*.example.com", ".r:-.bad.example"]) {
      expect(grantsWorldRead(withWorldRead(acl, true))).toBe(true);
      expect(grantsWorldRead(withWorldRead(withWorldRead(acl, true), false))).toBe(false);
      expect(grantsWorldListing(withWorldListing(withWorldRead(acl, true), true))).toBe(true);
    }
  });
});

describe("static web from the metadata rows", () => {
  it("is on when any Web-* key is present, in any case", () => {
    expect(hasStaticWeb([])).toBe(false);
    expect(hasStaticWeb([{ key: "Owner", value: "alice" }])).toBe(false);
    expect(hasStaticWeb([{ key: "web-index", value: "index.html" }])).toBe(true);
    expect(hasStaticWeb([{ key: "Web-Error", value: "error.html" }])).toBe(true);
    expect(hasStaticWeb([{ key: "Website", value: "no" }])).toBe(false);
  });

  it("ticking adds the two defaults once and unticking removes every Web-* key only", () => {
    const rows = [{ key: "Owner", value: "alice" }];
    const on = withStaticWeb(rows, true);
    expect(on).toEqual([
      { key: "Owner", value: "alice" },
      { key: "Web-Index", value: "index.html" },
      { key: "Web-Listings", value: "true" },
    ]);
    expect(withStaticWeb(on, true)).toBe(on);
    const custom = [...rows, { key: "Web-Error", value: "error.html" }];
    expect(withStaticWeb(custom, true)).toBe(custom);
    expect(withStaticWeb([...on, { key: "Web-Listings-CSS", value: "style.css" }], false)).toEqual(rows);
  });
});
