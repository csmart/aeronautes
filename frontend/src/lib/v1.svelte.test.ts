import { afterEach, expect, it } from "vitest";
import { applyEnabledCapabilities, v1Offers } from "./v1.svelte";

const shipped = { ...v1Offers };
afterEach(() => Object.assign(v1Offers, shipped));

it("keeps download, marker creation and upload and withholds deferred affordances", () => {
  expect(v1Offers.download).toBe(true);
  expect(v1Offers.markerCreate).toBe(true);
  // Returned to the release on 16 October 2026: upload is no longer behind
  // the switch, so the shipped default is on and no token can turn it off.
  expect(v1Offers.upload).toBe(true);
  expect(v1Offers.copy).toBe(false);
  expect(v1Offers.bulkDelete).toBe(false);
  expect(v1Offers.markerDelete).toBe(false);
  expect(v1Offers.manifestDelete).toBe(false);
  expect(v1Offers.benchmark).toBe(false);
  expect(v1Offers.rerun).toBe(false);
});

// ADR 0082: the affordances follow the capabilities the process reports,
// one whole capability at a time, and nothing else changes.
it("offers a reported capability's whole affordance set", () => {
  applyEnabledCapabilities(["Bulk delete"]);
  expect(v1Offers.bulkDelete).toBe(true);
  expect(v1Offers.copy).toBe(false);
  expect(v1Offers.benchmark).toBe(false);
});

it("follows job re-run with Copy, as the backend table classifies it", () => {
  applyEnabledCapabilities(["Copy"]);
  expect(v1Offers.copy).toBe(true);
  expect(v1Offers.rerun).toBe(true);
});

it("offers both halves of marker and manifest editing together", () => {
  applyEnabledCapabilities(["Marker and manifest deletion"]);
  expect(v1Offers.markerDelete).toBe(true);
  expect(v1Offers.manifestDelete).toBe(true);
});

// "Upload" is in here on purpose: it names a capability the release has
// RETURNED, so the backend cannot report it and the name must change nothing
// rather than being treated as a switch for an affordance that is already on.
it("changes nothing for a name it does not know, including Mirror and a returned capability", () => {
  applyEnabledCapabilities(["Mirror", "mirror", "copz", "", "Copy ", "Upload"]);
  expect(v1Offers).toEqual(shipped);
});

it("changes nothing when the process reports none", () => {
  applyEnabledCapabilities(undefined);
  applyEnabledCapabilities([]);
  expect(v1Offers).toEqual(shipped);
});
