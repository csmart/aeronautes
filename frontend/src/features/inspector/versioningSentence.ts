import type { ContainerSettingsDTO } from "../../lib/api";

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
