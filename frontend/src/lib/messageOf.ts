// messageOf is the text of an error for the operator: an Error's own
// message, or the value itself when something else was thrown.
export function messageOf(err: unknown): string {
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}
