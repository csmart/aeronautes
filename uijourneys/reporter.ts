import { appendFileSync } from "node:fs";
import type { FullResult, Reporter, TestCase, TestError, TestResult } from "@playwright/test/reporter";

// Prints only each journey's title and outcome. An assertion's message can
// quote page text, and page text can name a host, so every detail goes to
// the private directory instead (ADR 0112 decision 7).
export default class PrivateReporter implements Reporter {
  private failed = 0;

  onTestEnd(test: TestCase, result: TestResult) {
    const title = `${test.parent.project()?.name}: ${test.title}`;
    if (result.status === "skipped") {
      console.log(`skip ${title}`);
      return;
    }
    // A journey marked test.fail() names a known bug and is expected to
    // fail; it is reported as such and does not turn the run red, while one
    // that unexpectedly passes does, so the mark is removed with the fix.
    const outcome = test.outcome();
    if (outcome === "expected" && result.status !== "passed") {
      console.log(`bug  ${title} (fails as expected: a known bug)`);
      return;
    }
    console.log(`${outcome === "expected" ? "ok  " : "FAIL"} ${title}`);
    if (outcome !== "expected") {
      this.failed++;
      const details = result.errors.map((e) => e.stack ?? e.message ?? String(e)).join("\n\n");
      appendFileSync(`${process.env.AERONAUTES_PRIVATE}/failures.txt`, `== ${title} (${result.status})\n${details}\n\n`);
    }
  }

  // An error outside every journey, such as a teardown that could not
  // remove what the run made, fails the run; it is said here, with its
  // detail kept private, so a failed run never has nothing to show.
  onError(error: TestError) {
    this.failed++;
    console.log("FAIL the run's own setup or teardown");
    appendFileSync(`${process.env.AERONAUTES_PRIVATE}/failures.txt`, `== setup or teardown\n${error.stack ?? error.message ?? String(error)}\n\n`);
  }

  onEnd(result: FullResult) {
    console.log(`UI journeys ${result.status}; ${this.failed} failed; details are kept in ${process.env.AERONAUTES_PRIVATE}`);
  }

  printsToStdio() {
    return true;
  }
}
