import { describe, expect, it } from "vitest";
import { folderSummary } from "./folderSummary";

describe("folderSummary", () => {
  it("names the folder and its files", () => {
    expect(folderSummary({ folder: "photos", files: ["photos/a.jpg", "photos/b/c.jpg"], skipped: 0, emptyDirectories: 0 }))
      .toBe("Folder photos: 2 files to upload.");
    expect(folderSummary({ folder: "one", files: ["one/a"], skipped: 0, emptyDirectories: 0 }))
      .toBe("Folder one: 1 file to upload.");
  });

  it("says what the walk left out", () => {
    expect(folderSummary({ folder: "photos", files: ["photos/a.jpg"], skipped: 2, emptyDirectories: 1 })).toBe(
      "Folder photos: 1 file to upload; 2 entries skipped (links, other special entries and a download's own staging are left out); 1 empty directory produces nothing.",
    );
  });

  it("says when there is nothing to upload", () => {
    expect(folderSummary({ folder: "hollow", files: null as unknown as string[], skipped: 1, emptyDirectories: 3 })).toBe(
      "Folder hollow holds no file to upload; 1 entry skipped (links, other special entries and a download's own staging are left out); 3 empty directories produce nothing.",
    );
  });
});
