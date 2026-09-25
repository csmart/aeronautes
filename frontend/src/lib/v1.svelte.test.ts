import { afterEach, expect, it } from "vitest";
import { applyEnabledCapabilities, v1Offers } from "./v1.svelte";

const shipped = { ...v1Offers };
afterEach(() => Object.assign(v1Offers, shipped));

it("keeps download and marker creation and withholds deferred affordances", () => {
  expect(v1Offers.download).toBe(true);
  expect(v1Offers.markerCreate).toBe(true);
  expect(v1Offers.copy).toBe(false);
  expect(v1Offers.upload).toBe(false);
  expect(v1Offers.bulkDelete).toBe(false);
  expect(v1Offers.markerDelete).toBe(false);
  expect(v1Offers.manifestDelete).toBe(false);
  expect(v1Offers.benchmark).toBe(false);
  expect(v1Offers.rerun).toBe(false);
});

// ADR 0082: the affordances follow the capabilities the process reports,
// one whole capability at a time, and nothing else changes.
it("offers a reported capability's whole affordance set", () => {
  applyEnabledCapabilities(["Upload"]);
  expect(v1Offers.upload).toBe(true);
  expect(v1Offers.copy).toBe(false);
  expect(v1Offers.bulkDelete).toBe(false);
});

it("follows job re-run with Copy, as the backend table classifies it", () => {
  applyEnabledCapabilities(["Copy"]);
  expect(v1Offers.copy).toBe(true);
  expect(v1Offers.rerun).toBe(true);
});

it("offers both halves of marker and manifest editing together", () => {
  applyEnabledCapabilities(["Marker and manifest editing"]);
  expect(v1Offers.markerDelete).toBe(true);
  expect(v1Offers.manifestDelete).toBe(true);
});

it("changes nothing for a name it does not know, including Mirror", () => {
  applyEnabledCapabilities(["Mirror", "mirror", "uplaod", "", "Upload "]);
  expect(v1Offers).toEqual(shipped);
});

it("changes nothing when the process reports none", () => {
  applyEnabledCapabilities(undefined);
  applyEnabledCapabilities([]);
  expect(v1Offers).toEqual(shipped);
});
