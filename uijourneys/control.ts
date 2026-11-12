// The runner's side of the control listener (ADR 0112 decision 2): it
// scripts the native dialogs, opens the run's accounts and containers, and
// tears them down. The token is read from the environment and never printed.
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
  teardown: (): Promise<{ failures: number }> => call("/teardown", {}),
};
