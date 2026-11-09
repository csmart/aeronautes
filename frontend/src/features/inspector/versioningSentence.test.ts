import { describe, expect, it } from "vitest";
import { archiveOwnersSentence } from "./versioningSentence";

describe("archiveOwnersSentence", () => {
  it("says nothing for a container nobody archives into when the scan covered the account", () => {
    expect(archiveOwnersSentence({ archiveOf: [], archiveUnestablished: "" })).toBe("");
    expect(archiveOwnersSentence({ archiveOf: null as unknown as string[], archiveUnestablished: "" })).toBe("");
  });

  it("names the owners and what the deletion does to their versions", () => {
    expect(archiveOwnersSentence({ archiveOf: ["photos"], archiveUnestablished: "" })).toBe(
      "This container is the versions archive of photos: the earlier versions it keeps here are destroyed by this deletion.",
    );
    expect(archiveOwnersSentence({ archiveOf: ["a", "b", "c", "d", "e", "f", "g"], archiveUnestablished: "" })).toBe(
      "This container is the versions archive of a, b, c, d, e and 2 more: the earlier versions they keep here are destroyed by this deletion.",
    );
  });

  it("says when the scan could not answer, beside any owner it found", () => {
    expect(archiveOwnersSentence({ archiveOf: [], archiveUnestablished: "the account has 1,204 containers and this check reads at most 1,000" })).toBe(
      "Whether another container keeps its versions here could not be established: the account has 1,204 containers and this check reads at most 1,000.",
    );
    expect(archiveOwnersSentence({ archiveOf: ["photos"], archiveUnestablished: "a container's versioning could not be read" })).toBe(
      "This container is the versions archive of photos: the earlier versions it keeps here are destroyed by this deletion. "
        + "Whether another container keeps its versions here could not be established: a container's versioning could not be read.",
    );
  });
});
