/** A selection's members name objects, and folders by a name ending in "/"
 * that are taken with everything in them (ADR 0108 decision 7). */
export function countOfSelected(members: string[]): string {
  const folders = members.filter((name) => name.endsWith("/")).length;
  const objects = members.length - folders;
  const parts: string[] = [];
  if (objects > 0) parts.push(`${objects} ${objects === 1 ? "object" : "objects"}`);
  if (folders > 0) parts.push(`${folders} ${folders === 1 ? "folder" : "folders"}`);
  return parts.join(" and ");
}

/** The count and the first names, relative to the source folder, saying how
 * many more there are so a long selection never reads as a complete list. */
export function selectionSummary(members: string[], prefix: string): string {
  const shown = members.slice(0, 5).map((name) => name.slice(prefix.length));
  const more = members.length > shown.length ? `, and ${members.length - shown.length} more` : "";
  return `${countOfSelected(members)} selected: ${shown.join(", ")}${more}`;
}
