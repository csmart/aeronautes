import { expect, it } from "vitest";
import { newJobSettings, requestSettings } from "./jobSettings";

// Only an upload offers the large-object choice, and an unset one leaves
// the bookmark's in place (ADR 0111), so a request names it only when chosen.
it("sends an upload's large-object choice only when one is made", () => {
  expect(requestSettings(newJobSettings("auto"))).not.toHaveProperty("largeObjects");
  expect(requestSettings({ ...newJobSettings("auto"), largeObjects: "dlo" }).largeObjects).toBe("dlo");
});
