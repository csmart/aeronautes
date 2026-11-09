import type { PickedFilesDTO } from "../../lib/api";

/** One sentence for a folder pick (ADR 0103 decision 1): the folder's own name,
 * how many files it will upload, and what the walk left out. The counts come
 * from the backend's walk, never from the names it returned. */
export function folderSummary(picked: Pick<PickedFilesDTO, "folder" | "files" | "skipped" | "emptyDirectories">): string {
  const files = picked.files?.length ?? 0;
  const parts = [files === 0
    ? `Folder ${picked.folder} holds no file to upload`
    : `Folder ${picked.folder}: ${files} ${files === 1 ? "file" : "files"} to upload`];
  if (picked.skipped > 0) {
    parts.push(`${picked.skipped} ${picked.skipped === 1 ? "entry" : "entries"} skipped (links and other special entries are never followed)`);
  }
  if (picked.emptyDirectories > 0) {
    parts.push(`${picked.emptyDirectories} empty ${picked.emptyDirectories === 1 ? "directory produces" : "directories produce"} nothing`);
  }
  return parts.join("; ") + ".";
}
