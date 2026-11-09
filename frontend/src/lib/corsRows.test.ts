import { describe, expect, it } from "vitest";
import { corsProblem, corsValue, normaliseCorsValue, withCorsValue } from "./corsRows";

const rows = [
  { key: "Keep", value: "untouched" },
  { key: "access-control-allow-origin", value: "https://a.example" },
];

describe("CORS rows (ADR 0101)", () => {
  it("reads a field from its row in any letter case and reports an absent row as empty", () => {
    expect(corsValue(rows, "allowOrigin")).toBe("https://a.example");
    expect(corsValue(rows, "maxAge")).toBe("");
  });

  it("rewrites only its own row: in place, appended, or removed when emptied", () => {
    expect(withCorsValue(rows, "allowOrigin", "*")).toEqual([
      { key: "Keep", value: "untouched" },
      { key: "access-control-allow-origin", value: "*" },
    ]);
    expect(withCorsValue(rows, "maxAge", "600")).toEqual([...rows, { key: "Access-Control-Max-Age", value: "600" }]);
    expect(withCorsValue(rows, "allowOrigin", "")).toEqual([{ key: "Keep", value: "untouched" }]);
    expect(withCorsValue(rows, "maxAge", "")).toBe(rows);
  });

  it("normalises to single spaces between tokens", () => {
    expect(normaliseCorsValue("  https://a.example   *  ")).toBe("https://a.example *");
    expect(normaliseCorsValue("   ")).toBe("");
  });

  it("accepts the shapes the proxy reads and names the token it refuses", () => {
    expect(corsProblem("allowOrigin", "* null https://app.example:8443 http://localhost https://[::1]:8080")).toBeNull();
    expect(corsProblem("allowOrigin", "app.example")).toContain('"app.example"');
    for (const notAnOrigin of ["https://app.example?query", "https://app.example#fragment", "https://user@app.example", "https://app.example/path"]) {
      expect(corsProblem("allowOrigin", notAnOrigin)).toContain(`"${notAnOrigin}"`);
    }
    expect(corsProblem("maxAge", "600")).toBeNull();
    expect(corsProblem("maxAge", "")).toBeNull();
    expect(corsProblem("maxAge", "soon")).toContain("whole number");
    expect(corsProblem("maxAge", "1 2")).toContain("whole number");
    expect(corsProblem("exposeHeaders", "Etag X-Timestamp")).toBeNull();
    expect(corsProblem("exposeHeaders", "bad/header")).toContain('"bad/header"');
  });
});
