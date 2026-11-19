import type { JobDTO } from "../../lib/api";

// stateWords is how a finished job's state reads (ADR 0129). A job stopped
// after something landed is stored as partial, so it is never taken for one
// that changed nothing, and holds names a cancellation stopped part-way: it
// reads "cancelled, incomplete" rather than as a failure.
export function stateWords(job: Pick<JobDTO, "state" | "counters">): string {
  return job.state === "partial" && (job.counters?.objectsIncomplete ?? 0) > 0 ? "cancelled, incomplete" : job.state;
}
