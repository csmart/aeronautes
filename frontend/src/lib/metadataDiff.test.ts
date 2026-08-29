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
    expect(metaFromRows(rows)).toEqual({ meta: { A: "1", B: "2" }, duplicate: "" });
  });

  it("drops blank keys and flags duplicates case-insensitively", () => {
    expect(metaFromRows([{ key: "  ", value: "x" }, { key: "A", value: "1" }])).toEqual({
      meta: { A: "1" },
      duplicate: "",
    });
    const dup = metaFromRows([
      { key: "Shoot", value: "a" },
      { key: "shoot", value: "b" },
    ]);
    expect(dup.duplicate).toBe("shoot");
  });
});
