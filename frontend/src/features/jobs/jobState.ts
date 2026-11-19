import type { JobDTO } from "../../lib/api";

// stateWords is how a finished job's state reads (ADR 0129). A job stopped
// after something landed is stored as partial, so it is never taken for one
// that changed nothing, and holds names a cancellation stopped part-way: it
// reads "cancelled, incomplete" rather than as a failure.
// realFailures is the failed names less those a cancellation stopped part-way,
// which the job also counts as failed and the window reads as incomplete.
export function realFailures(counters: { objectsFailed: number; objectsIncomplete?: number }): number {
  return counters.objectsFailed - (counters.objectsIncomplete ?? 0);
}

export function stateWords(job: Pick<JobDTO, "state" | "counters">): string {
  return job.state === "partial" && (job.counters?.objectsIncomplete ?? 0) > 0 ? "cancelled, incomplete" : job.state;
}
