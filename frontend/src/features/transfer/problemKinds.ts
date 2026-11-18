// A transfer preview's problem kind in the operator's words (ADR 0126
// decision 1). The code stays in the report and the job's record; the
// dialog shows these words beside the whole reason.
const WORDS: Record<string, string> = {
  "destination-overlap": "Overlaps a running download",
  "destination-unavailable": "Folder not available",
  "destination-path": "Folder not usable",
  exists: "Already exists",
  "local-exists": "Already exists",
  collision: "Names collide",
  "case-fold": "Names differ only by case",
  "file-vs-prefix": "File and folder share a name",
  "path-escape": "Name not allowed here",
  "name-form": "Name not allowed here",
  "unportable-name": "Name not portable",
  portability: "Name not portable",
  "name-length": "Name too long",
  unmaterializable: "Cannot be a local file",
  "not-a-file": "Not a regular file",
  unreadable: "Cannot be read",
  "source-name": "Source name not usable",
  "source-path": "Source not usable",
  "selection-ceiling": "Too many objects",
  "selection-missing": "No longer at the source",
  "empty-directories": "Empty folders",
  skipped: "Skipped",
};

export function problemKindWords(kind: string): string {
  const words = WORDS[kind];
  if (words) return words;
  const spaced = kind.replaceAll("-", " ");
  return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}
