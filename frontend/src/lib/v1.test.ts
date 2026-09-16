import { expect, it } from "vitest";
import { v1Offers } from "./v1";

it("keeps download and withholds deferred affordances", () => {
  expect(v1Offers.download).toBe(true);
  expect(v1Offers.copy).toBe(false);
  expect(v1Offers.upload).toBe(false);
  expect(v1Offers.bulkDelete).toBe(false);
  expect(v1Offers.markerEdit).toBe(false);
  expect(v1Offers.manifestDelete).toBe(false);
  expect(v1Offers.benchmark).toBe(false);
  expect(v1Offers.rerun).toBe(false);
});
