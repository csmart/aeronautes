import { describe, expect, it } from "vitest";
import { formatBytes, formatCount } from "./format";

describe("formatBytes", () => {
  it("formats with three significant figures, decimal units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(1000)).toBe("1.00 KB");
    expect(formatBytes(5_800_000)).toBe("5.80 MB");
    expect(formatBytes(42_700_000_000)).toBe("42.7 GB");
    expect(formatBytes(1_000_000_000)).toBe("1.00 GB");
    expect(formatBytes(123_456_789_012_345)).toBe("123 TB");
  });

  it("rejects nonsense", () => {
    expect(formatBytes(-1)).toBe("—");
    expect(formatBytes(NaN)).toBe("—");
  });
});

describe("formatCount", () => {
  it("compacts large counts", () => {
    expect(formatCount(912)).toBe("912");
    expect(formatCount(18_700)).toBe("18.7K");
    expect(formatCount(522_000)).toBe("522K");
    expect(formatCount(1_200_000)).toBe("1.2M");
    expect(formatCount(4_000_000)).toBe("4M");
  });
});
