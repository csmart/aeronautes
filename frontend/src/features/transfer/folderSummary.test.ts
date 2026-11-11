import { describe, expect, it } from "vitest";
import { folderSummary, folderWarns } from "./folderSummary";

describe("folderSummary", () => {
  it("names the folder and its files", () => {
    expect(folderSummary({ folder: "photos", files: ["photos/a.jpg", "photos/b/c.jpg"], skipped: 0, emptyDirectories: 0 }, "media/trip/"))
      .toBe('Folder "photos": 2 files to upload into media/trip/photos/.');
    expect(folderSummary({ folder: "one", files: ["one/a"], skipped: 0, emptyDirectories: 0 }, "media/"))
      .toBe('Folder "one": 1 file to upload into media/one/.');
  });

  it("says what the walk left out", () => {
    expect(folderSummary({ folder: "photos", files: ["photos/a.jpg"], skipped: 2, emptyDirectories: 1 }, "media/")).toBe(
      'Folder "photos": 1 file to upload into media/photos/; 2 entries skipped (links, other special entries and a download\'s own staging are left out); 1 empty directory is not uploaded.',
    );
  });

  it("says when there is nothing to upload", () => {
    expect(folderSummary({ folder: "hollow", files: null as unknown as string[], skipped: 1, emptyDirectories: 3 }, "media/")).toBe(
      'Nothing to upload: folder "hollow" holds no files; 1 entry skipped (links, other special entries and a download\'s own staging are left out); 3 empty directories are not uploaded.',
    );
  });

  // The operator missed an empty folder's notice in the 0.5.0 test run, shown
  // in the hint style: nothing to upload, or entries left out, is a warning.
  it("warns when nothing will upload or entries were left out", () => {
    expect(folderWarns({ files: ["a"], skipped: 0 })).toBe(false);
    expect(folderWarns({ files: [], skipped: 0 })).toBe(true);
    expect(folderWarns({ files: null as unknown as string[], skipped: 0 })).toBe(true);
    expect(folderWarns({ files: ["a"], skipped: 1 })).toBe(true);
  });
});
