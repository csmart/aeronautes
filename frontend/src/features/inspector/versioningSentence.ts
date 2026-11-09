import type { ContainerSettingsDTO, DeletePreviewDTO } from "../../lib/api";

/** What a delete does to the content at a name under the container's
 * versioning mode, measured against the owned Swift on 27 October 2026
 * (docs/experiments/2026-10-27-delete-under-versioning.md): a versions
 * location archives on overwrite and never on delete. undefined while the
 * facts are being read, null when they could not be read. */
export function versioningSentence(f: ContainerSettingsDTO | null | undefined): string {
  if (f === undefined) return "Checking how this container keeps versions…";
  if (f === null) return "How this container keeps versions could not be read; assume the content is destroyed.";
  switch (f.versioningMode) {
    case "versions_location":
      return `This container keeps versions in ${f.versioningArchive}, but a delete archives nothing: the current content is destroyed, and if an earlier version is archived the name reverts to it.`;
    case "history_location":
      return `This container keeps history in ${f.versioningArchive}: the current content moves there with a deletion marker, and the name stops resolving.`;
    case "enabled":
      return "This container uses in-container versioning, which this release does not manage; the delete follows the cluster's own rules.";
    default:
      return "This container keeps no versions: the content is destroyed.";
  }
}

/** Whose earlier versions a bulk deletion of this container destroys, from the
 * preview's scan of the account's containers (ADR 0102 decision 3): the owners
 * it found, that the scan could not cover the account, or both. "" when no
 * container archives here and the scan covered the account. At most five owners
 * are named, with the rest counted. */
export function archiveOwnersSentence(preview: Pick<DeletePreviewDTO, "archiveOf" | "archiveUnestablished">): string {
  const owners = preview.archiveOf ?? [];
  const parts: string[] = [];
  if (owners.length > 0) {
    const named = owners.slice(0, 5).join(", ") + (owners.length > 5 ? ` and ${owners.length - 5} more` : "");
    const keeps = owners.length === 1 ? "it keeps" : "they keep";
    parts.push(`This container is the versions archive of ${named}: the earlier versions ${keeps} here are destroyed by this deletion.`);
  }
  if (preview.archiveUnestablished) {
    parts.push(`Whether another container keeps its versions here could not be established: ${preview.archiveUnestablished}.`);
  }
  return parts.join(" ");
}
