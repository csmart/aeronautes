// Only benchmark remains behind the release capability switch (ADR 0082).
// The process reports it after the first render, so the offer is reactive.
export const v1Offers = $state({
  benchmark: false,
  // Mirror refuses every start (ADR 0020); its comparison preview alone read
  // as broken, so by the operator's decision of 11 November 2026 the copy
  // dialog offers no Mirror tab until Mirror can run.
  mirrorPreview: false,
});

/** Mirror has no enabling token; only the reported benchmark is offered. */
export function applyEnabledCapabilities(names: readonly string[] | undefined) {
  if (names?.includes("Benchmark")) v1Offers.benchmark = true;
}
