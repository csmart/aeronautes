import { describe, expect, it } from "vitest";
import { keySuggestions, matchingKeys, splitMatch } from "./metadataKeys";

const keys = (list: { key: string }[]) => list.map((s) => s.key);

// ADR 0125 decisions 2 and 3: keys are found by any part of the name,
// ignoring case; the item's own keys, and keys with editors of their own,
// are never offered.
describe("metadata key suggestions", () => {
  it("offers a container the store's keys, then keys seen this session", () => {
    const all = keySuggestions("container", ["Team", "web-index", "Temp-URL-Key"]);
    expect(keys(all)).toContain("Web-Listings-CSS");
    expect(keys(all).at(-1)).toBe("Team");
    // A seen key the store's list has, in any case, and a TempURL key are not added.
    expect(keys(all).filter((k) => k.toLowerCase() === "web-index")).toHaveLength(1);
    expect(keys(all)).not.toContain("Temp-URL-Key");
  });

  it("matches anywhere in the key, ignoring case", () => {
    const all = keySuggestions("container", []);
    expect(keys(matchingKeys(all, "listings", []))).toEqual(["Web-Listings", "Web-Listings-CSS", "Web-Listings-Label"]);
    expect(keys(matchingKeys(all, "ORIGIN", []))).toEqual(["Access-Control-Allow-Origin"]);
    expect(matchingKeys(all, "", [])).toHaveLength(all.length);
  });

  it("leaves out the keys the item already has", () => {
    const all = keySuggestions("container", []);
    expect(keys(matchingKeys(all, "listings", ["web-listings"]))).toEqual(["Web-Listings-CSS", "Web-Listings-Label"]);
  });

  it("offers an object Mtime and an account only what was seen", () => {
    expect(keys(keySuggestions("object", []))).toEqual(["Mtime"]);
    expect(keys(keySuggestions("account", ["Owner"]))).toEqual(["Owner"]);
  });

  it("splits a key around what was typed, to mark it", () => {
    expect(splitMatch("Web-Listings-CSS", "list")).toEqual(["Web-", "List", "ings-CSS"]);
    expect(splitMatch("Mtime", "")).toEqual(["Mtime", "", ""]);
  });
});
