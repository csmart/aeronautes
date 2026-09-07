import { describe, expect, it } from "vitest";
import { diffMeta, isDiffEmpty, metaFromRows, rowsFromMeta } from "./metadataDiff";

describe("diffMeta", () => {
  it("classifies added, changed, and removed keys", () => {
    const diff = diffMeta(
      { Shoot: "wedding", Location: "sydney", Old: "x" },
      { Shoot: "wedding", Location: "melbourne", Camera: "fx6" },
    );
    expect(diff).toEqual({
      added: ["Camera"],
      changed: ["Location"],
      removed: ["Old"],
    });
    expect(isDiffEmpty(diff)).toBe(false);
  });

  it("reports an empty diff for identical sets", () => {
    expect(isDiffEmpty(diffMeta({ A: "1" }, { A: "1" }))).toBe(true);
  });
});

describe("rows round-trip", () => {
  it("sorts rows and rebuilds the map", () => {
    const rows = rowsFromMeta({ B: "2", A: "1" });
    expect(rows).toEqual([
      { key: "A", value: "1" },
      { key: "B", value: "2" },
    ]);
    expect(metaFromRows(rows)).toEqual({ meta: { A: "1", B: "2" }, duplicate: null });
  });

  it("ignores only completely empty rows", () => {
    expect(metaFromRows([
      { key: "", value: "" },
      { key: "  ", value: "x" },
      { key: "", value: "has a value" },
      { key: "A", value: "" },
    ])).toEqual({
      meta: { "  ": "x", "": "has a value", A: "" },
      duplicate: null,
    });
  });

  it("flags duplicates case-insensitively", () => {
    const dup = metaFromRows([
      { key: "Shoot", value: "a" },
      { key: "shoot", value: "b" },
    ]);
    expect(dup.duplicate).toBe("shoot");
  });
});

describe("literal metadata identity", () => {
  it.each(["__proto__", "constructor", "toString"])("retains own %s through rows and JSON", (key) => {
    const original = { Keep: "untouched", [key]: "literal value" };
    const result = metaFromRows(rowsFromMeta(original));
    expect(Object.hasOwn(result.meta, key)).toBe(true);
    expect(result.meta[key]).toBe("literal value");
    expect(JSON.parse(JSON.stringify(result.meta))).toEqual(original);
    expect(Object.prototype.toString.call({})).toBe("[object Object]");
  });

  it.each([" leading", "trailing ", "  ", "", "雪", "a_b", "a:b"])("keeps the entered key %j", (key) => {
    const result = metaFromRows([{ key, value: " literal value " }]);
    expect(Object.keys(result.meta)).toEqual([key]);
    expect(result.meta[key]).toBe(" literal value ");
  });

  it("distinguishes a duplicate empty key from no duplicate", () => {
    const repeated = metaFromRows([{ key: "", value: "first" }, { key: "", value: "second" }]);
    const single = metaFromRows([{ key: "", value: "first" }]);
    expect(repeated.duplicate).toBe("");
    expect(single.duplicate).toBeNull();
  });

  it("ignores inherited and non-string entries in rows and diffs", () => {
    const base: Record<string, string | undefined> = Object.create({ inherited: "not metadata" });
    base.Keep = "same";
    base.Undefined = undefined;
    Object.defineProperty(base, "Number", { value: 42, enumerable: true });
    const working: Record<string, string | undefined> = Object.create({ inherited: "different" });
    working.Keep = "same";
    working.Missing = undefined;
    Object.defineProperty(working, "Object", { value: {}, enumerable: true });
    expect(rowsFromMeta(base)).toEqual([{ key: "Keep", value: "same" }]);
    expect(rowsFromMeta(working)).toEqual([{ key: "Keep", value: "same" }]);
    expect(diffMeta(base, working)).toEqual({ added: [], changed: [], removed: [] });
  });

  it.each(["__proto__", "constructor", "toString"])("classifies own %s as an addition", (key) => {
    const base = Object.create({ [key]: "inherited" });
    expect(diffMeta(base, { [key]: "new" })).toEqual({ added: [key], changed: [], removed: [] });
  });

  it.each(["__proto__", "constructor", "toString"])("does not let inherited %s hide a removal", (key) => {
    const working = Object.create({ [key]: "inherited" });
    expect(diffMeta({ [key]: "old" }, working)).toEqual({ added: [], changed: [], removed: [key] });
  });

  it("uses case-insensitive identity and the relevant side's spelling", () => {
    expect(diffMeta(
      { oWnEr: "same", Remove: "old", Change: "old" },
      { Owner: "same", change: "new", Added: "new" },
    )).toEqual({ added: ["Added"], changed: ["change"], removed: ["Remove"] });
  });

  it("treats undefined values as absent on either side", () => {
    expect(diffMeta(
      { Add: undefined, Gone: "old", Absent: undefined },
      { add: "new", Gone: undefined, Absent: undefined },
    )).toEqual({ added: ["add"], changed: [], removed: ["Gone"] });
  });
});
