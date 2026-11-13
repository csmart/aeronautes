const consequence =
  "Objects here may be segments of larger objects, and deleting one can break the object it belongs to.";

/** What to say where a container is, or may be, a segment store (ADR 0117):
 * one sentence for the listing and for every delete confirmation opened
 * there, and nothing for an ordinary container. A suspected container is
 * known only by its name, so its sentence claims no more than that. The
 * sentence is words only: the class behind it never allows or refuses a
 * delete. */
export function segmentStoreCaution(containerClass: string): string {
  switch (containerClass) {
    case "suspected":
      return `This container is named like a segment store. ${consequence}`;
    case "declared":
    case "confirmed":
      return `This container is a segment store. ${consequence}`;
    default:
      return "";
  }
}
