import { describe, expect, it } from "vitest";
import type { BookmarkDTO } from "./api";
import { endpointOf, hoverText, matchesBookmark, sortBookmarks } from "./bookmarkRail";

function bookmark(id: string, displayName: string, authUrl: string, username = "acct:user", tlsMode = "system"): BookmarkDTO {
  return { id, displayName, authUrl, username, storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode, caPem: "", hasCa: false, hasCredential: true };
}

describe("endpointOf", () => {
  it("is the auth URL's host, or the raw value when it does not parse", () => {
    expect(endpointOf(bookmark("a", "A", "https://swift.example.net:8443/auth/v1.0"))).toBe("swift.example.net:8443");
    expect(endpointOf(bookmark("b", "B", "not a url"))).toBe("not a url");
  });
});

describe("sortBookmarks", () => {
  it("sorts by name without regard to case, whichever cloud each points at", () => {
    const sorted = sortBookmarks([
      bookmark("1", "zeta", "https://b.example/auth/v1.0"),
      bookmark("2", "Alpha", "https://b.example/auth/v1.0"),
      bookmark("3", "Mid", "https://a.example/auth/v1.0"),
      bookmark("4", "beta", "https://b.example/auth/v1.0"),
    ]);
    expect(sorted.map((b) => b.displayName)).toEqual(["Alpha", "beta", "Mid", "zeta"]);
  });

  it("keeps two bookmarks of one name in a stable order by id", () => {
    const twice = sortBookmarks([
      bookmark("bm-9", "Same", "https://c.example/auth/v1.0"),
      bookmark("bm-2", "same", "https://c.example/auth/v1.0"),
    ]);
    expect(twice.map((b) => b.id)).toEqual(["bm-2", "bm-9"]);
    expect(sortBookmarks([])).toEqual([]);
  });

  it("orders numbered names the way people read them", () => {
    const sorted = sortBookmarks([
      bookmark("1", "site 10", "https://d.example/auth/v1.0"),
      bookmark("2", "site 2", "https://d.example/auth/v1.0"),
    ]);
    expect(sorted.map((b) => b.displayName)).toEqual(["site 2", "site 10"]);
  });
});

describe("matchesBookmark", () => {
  const b = bookmark("1", "Prod AU", "https://swift.au.example/auth/v1.0", "tenant:chris");

  it("matches the name, the username or the endpoint, ignoring case and outer spaces", () => {
    expect(matchesBookmark(b, "")).toBe(true);
    expect(matchesBookmark(b, "  ")).toBe(true);
    expect(matchesBookmark(b, "prod")).toBe(true);
    expect(matchesBookmark(b, "CHRIS")).toBe(true);
    expect(matchesBookmark(b, " au.example ")).toBe(true);
    expect(matchesBookmark(b, "staging")).toBe(false);
  });
});

describe("hoverText", () => {
  it("names the bookmark, the account and the endpoint, and warns when TLS is off", () => {
    expect(hoverText(bookmark("1", "Prod AU", "https://swift.au.example/auth/v1.0", "tenant:chris")))
      .toBe("Prod AU\ntenant:chris at swift.au.example");
    expect(hoverText(bookmark("2", "Lab", "http://lab.local:8080/auth/v1.0", "test:tester", "insecure")))
      .toBe("Lab\ntest:tester at lab.local:8080\nTLS verification DISABLED");
  });
});
