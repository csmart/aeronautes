// The runner's side of the control listener (ADR 0112 decision 2): it
// scripts the native dialogs, opens the run's accounts and containers, and
// tears them down. The token is read from the environment and never printed.
import { rmSync, writeFileSync } from "node:fs";
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

// endRun marks that the journey asked for the application to stop, which
// the runner reads to start it again, and takes the mark back if the stop
// was not accepted, so a later crash nobody asked for is not hidden.
async function endRun(path: string): Promise<void> {
  const mark = join(process.env.AERONAUTES_PRIVATE!, "restart");
  writeFileSync(mark, "");
  try {
    await call(path, {});
  } catch (error) {
    rmSync(mark, { force: true });
    throw error;
  }
}

export const control = {
  pickFolder: (dir: string) => call("/pick", { dir }),
  pickFiles: (files: string[]) => call("/pick", { files }),
  // The next chooser, of either kind, is closed with nothing chosen.
  cancelPick: () => call("/pick", { cancel: true }),
  answer: (question: "overwrite" | "public", answer: boolean) => call("/confirm", { question, answer }),
  account: (label: string): Promise<{ bookmark?: string }> => call("/account", { label, authUrl: process.env.AERONAUTES_MOCK }),
  container: (label: string, name: string, segmented = false): Promise<{ container: string }> =>
    call("/container", { label, name, segmented }),
  // Creates a container of exactly the name another account's container
  // was given, so a journey can hold the same name on two accounts. It is
  // refused, as any create is, where that name is already taken.
  sameNameAs: (label: string, name: string): Promise<{ container: string }> =>
    call("/same-name", { label, name }),
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
  quit: () => endRun("/quit"),
  // When this start of the application began, which tells it from the next.
  started: async (): Promise<string> => (await call("/started", {})).started,
  // Stops the application with no shutdown, as a crash does; the runner
  // starts it again as after quit.
  crash: () => endRun("/crash"),
  // Starts the run's teardown and waits for its end: on a real store it can
  // take longer than any one request may stay open.
  teardown: async (): Promise<{ failures: number }> => {
    await call("/teardown", {});
    const deadline = Date.now() + 60 * 60 * 1000;
    for (;;) {
      const status = await call("/teardown-status", {});
      if (status.done) return { failures: status.failures };
      if (Date.now() > deadline) throw new Error("the run's teardown did not end within an hour");
      await new Promise((resolve) => setTimeout(resolve, 2000));
    }
  },
};
