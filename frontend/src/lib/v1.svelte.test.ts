import { afterEach, expect, it } from "vitest";
import { applyEnabledCapabilities, v1Offers } from "./v1.svelte";

const shipped = { ...v1Offers };
afterEach(() => Object.assign(v1Offers, shipped));

// ADR 0082: the affordances follow the capabilities the process reports,
// one whole capability at a time, and nothing else changes.
it("offers a reported capability's whole affordance set", () => {
  expect(v1Offers.benchmark).toBe(false);
  applyEnabledCapabilities(["Benchmark"]);
  expect(v1Offers.benchmark).toBe(true);
  // Benchmark is the only capability still behind the switch; enabling it
  // flips its own affordance and leaves every other offer as shipped.
  expect({ ...v1Offers, benchmark: shipped.benchmark }).toEqual(shipped);
});

// "Upload" and "Object deletion" are in here on purpose: each names a
// capability the release has RETURNED, so the backend cannot report it and
// the name must change nothing rather than being treated as a switch for an
// affordance that is already on.
it("changes nothing for a name it does not know, including Mirror and a returned capability", () => {
  applyEnabledCapabilities(["Mirror", "mirror", "copz", "", "Copy ", "Upload", "Object deletion"]);
  expect(v1Offers).toEqual(shipped);
});

it("changes nothing when the process reports none", () => {
  applyEnabledCapabilities(undefined);
  applyEnabledCapabilities([]);
  expect(v1Offers).toEqual(shipped);
});
