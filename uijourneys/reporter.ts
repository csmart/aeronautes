import { appendFileSync } from "node:fs";
import type { Reporter, TestCase, TestResult, FullResult } from "@playwright/test/reporter";

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
    console.log(`${result.status === "passed" ? "ok  " : "FAIL"} ${title}`);
    if (result.status !== "passed") {
      this.failed++;
      const details = result.errors.map((e) => e.stack ?? e.message ?? String(e)).join("\n\n");
      appendFileSync(`${process.env.AERONAUTES_PRIVATE}/failures.txt`, `== ${title} (${result.status})\n${details}\n\n`);
    }
  }

  onEnd(result: FullResult) {
    console.log(`UI journeys ${result.status}; ${this.failed} failed; details are kept in ${process.env.AERONAUTES_PRIVATE}`);
  }

  printsToStdio() {
    return true;
  }
}
