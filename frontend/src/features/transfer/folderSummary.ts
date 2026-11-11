import type { PickedFilesDTO } from "../../lib/api";

/** One sentence for a folder pick (ADR 0103 decision 1): the folder's own name,
 * how many files it will upload, and what the walk left out. The file count is
 * the number of names the pick returned, which is every file it will upload;
 * the skipped and empty-directory counts are the backend walk's own, because
 * the names of those are only a sample. */
export function folderSummary(picked: Pick<PickedFilesDTO, "folder" | "files" | "skipped" | "emptyDirectories">, into: string): string {
  const files = picked.files?.length ?? 0;
  // The folder arrives as a folder (ADR 0103 decision 2), so the summary names
  // where it lands: a folder of its own name, not its contents loose in the
  // prefix.
  const parts = [files === 0
    ? `Nothing to upload: folder "${picked.folder}" holds no files`
    : `Folder "${picked.folder}": ${files} ${files === 1 ? "file" : "files"} to upload into ${into}${picked.folder}/`];
  if (picked.skipped > 0) {
    parts.push(`${picked.skipped} ${picked.skipped === 1 ? "entry" : "entries"} skipped (links, other special entries and a download's own staging are left out)`);
  }
  if (picked.emptyDirectories > 0) {
    parts.push(`${picked.emptyDirectories} empty ${picked.emptyDirectories === 1 ? "directory is" : "directories are"} not uploaded`);
  }
  return parts.join("; ") + ".";
}

/** Whether the pick needs the operator's attention: it uploads nothing, or
 * the walk left entries out. Either is shown as a warning, not a hint. */
export function folderWarns(picked: Pick<PickedFilesDTO, "files" | "skipped">): boolean {
  return (picked.files?.length ?? 0) === 0 || picked.skipped > 0;
}
