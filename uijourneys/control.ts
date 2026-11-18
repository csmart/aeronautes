// The runner's side of the control listener (ADR 0112 decision 2): it
// scripts the native dialogs, opens the run's accounts and containers, and
// tears them down. The token is read from the environment and never printed.
import { writeFileSync } from "node:fs";
import { join } from "node:path";

async function call(path: string, body: object): Promise<any> {
  const response = await fetch(`${process.env.AERONAUTES_CONTROL}${path}`, {
    method: "POST",
    headers: { "X-Journey-Token": process.env.AERONAUTES_CONTROL_TOKEN ?? "", "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    throw new Error(`control ${path} answered ${response.status}: ${await response.text()}`);
  }
  return response.json();
}

export const control = {
  pickFolder: (dir: string) => call("/pick", { dir }),
  pickFiles: (files: string[]) => call("/pick", { files }),
  answer: (question: "overwrite" | "public", answer: boolean) => call("/confirm", { question, answer }),
  account: (label: string): Promise<{ bookmark?: string }> => call("/account", { label, authUrl: process.env.AERONAUTES_MOCK }),
  container: (label: string, name: string, segmented = false): Promise<{ container: string }> =>
    call("/container", { label, name, segmented }),
  // What one of the run's own containers holds, as the run's own session
  // lists it: the read a deletion is checked against.
  names: async (label: string, container: string): Promise<string[]> => (await call("/names", { label, container })).names,
  // Writes an object into one of the run's own containers from the run's
  // own session, as another program would, behind the application's back.
  put: (label: string, container: string, name: string, body: string) => call("/put", { label, container, name, body }),
  // What each native surface was asked since the last read: a picker's
  // title, or a question's title and message, as a person would read them.
  asked: (): Promise<{ kind: string; title: string; text: string }[]> => call("/asked", {}),
  // Quits the application as closing its window does; the runner starts it
  // again over the same state, ports and token. A restarted application
  // holds no saved key and has forgotten what the run created, so only a
  // mock-only run uses this and crash below.
  quit: () => {
    writeFileSync(join(process.env.AERONAUTES_PRIVATE!, "restart"), "");
    return call("/quit", {});
  },
  // When this start of the application began, which tells it from the next.
  started: async (): Promise<string> => (await call("/started", {})).started,
  // Stops the application with no shutdown, as a crash does; the runner
  // starts it again as after quit.
  crash: () => {
    writeFileSync(join(process.env.AERONAUTES_PRIVATE!, "restart"), "");
    return call("/crash", {});
  },
  teardown: (): Promise<{ failures: number }> => call("/teardown", {}),
};
