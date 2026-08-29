import { describe, expect, it } from "vitest";
import { BrowserState } from "./browserState.svelte";

describe("container modes (§52)", () => {
  it("logical hides confirmed stores and groups suspected ones", () => {
    const s = new BrowserState();
    const groups = s.containerGroups;
    expect(groups.main.map((c) => c.name)).toEqual(["fixtures"]);
    expect(groups.suspected.map((c) => c.name)).toEqual([
      "photos_segments",
      ".file-segments",
    ]);
    expect(groups.hiddenConfirmed).toBe(3);
  });

  it("all and backstage show every container", () => {
    const s = new BrowserState();
    for (const mode of ["all", "backstage"] as const) {
      s.mode = mode;
      expect(s.containerGroups.main).toHaveLength(6);
      expect(s.containerGroups.suspected).toHaveLength(0);
      expect(s.containerGroups.hiddenConfirmed).toBe(0);
    }
  });
});

describe("navigation", () => {
  it("drills into prefixes and back up to the account", () => {
    const s = new BrowserState();
    s.openContainer("fixtures");
    expect(s.inspectorTarget.kind).toBe("container");

    const implied = s.entries.find((e) => e.kind === "prefix" && e.prefix === "implied/");
    expect(implied).toBeDefined();
    s.activate(implied!);
    expect(s.prefix).toBe("implied/");
    expect(s.entries.map((e) => (e.kind === "object" ? e.name : e.prefix))).toEqual([
      "implied/child.txt",
    ]);

    s.up();
    expect(s.prefix).toBe("");
    expect(s.containerName).toBe("fixtures");
    s.up();
    expect(s.containerName).toBeNull();
    expect(s.inspectorTarget.kind).toBe("account");
  });

  it("activating an object does not navigate", () => {
    const s = new BrowserState();
    s.openContainer("fixtures");
    const obj = s.entries.find((e) => e.kind === "object" && e.name === "empty-object");
    s.activate(obj!);
    expect(s.prefix).toBe("");
  });

  it("selection drives the inspector target", () => {
    const s = new BrowserState();
    s.openContainer("fixtures");
    const idx = s.entries.findIndex((e) => e.kind === "object" && e.name === "external-dlo");
    s.selectedIndex = idx;
    const target = s.inspectorTarget;
    expect(target.kind).toBe("object");
    if (target.kind === "object") {
      expect(target.object.largeObject).toBe("dlo");
    }

    const pfx = s.entries.findIndex((e) => e.kind === "prefix" && e.prefix === "folder-marker/");
    s.selectedIndex = pfx;
    const prefixTarget = s.inspectorTarget;
    expect(prefixTarget.kind).toBe("prefix");
    if (prefixTarget.kind === "prefix") {
      expect(prefixTarget.prefix.markerBacked).toBe(true);
    }
  });

  it("filter narrows the working list and resets on navigation", () => {
    const s = new BrowserState();
    s.openContainer("fixtures");
    s.filter = "external";
    expect(s.entries.map((e) => (e.kind === "object" ? e.name : e.prefix))).toEqual([
      "external-dlo",
      "external-slo",
    ]);
    s.goTo("weird/");
    expect(s.filter).toBe("");
    expect(s.entries).toHaveLength(3);
  });
});
