import { test, type Locator, type Page } from "@playwright/test";
import { randomBytes } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:net";
import { join } from "node:path";
import { control } from "../control.ts";
import { connect, deleteBookmark, expect, localFolder } from "../helpers.ts";

// A bookmark's key reaches no log and no export (ADR 0099, ADR 0127
// decision 3, §51). The window journey checks the settings file and the
// database; this one checks everywhere else a key could be written on its
// way to a refusal, which is where keys leak: the application's own output,
// the log file kept from Settings at debug, an exported audit log, and the
// page once reloaded. The key is one no file could hold by chance.
//
// A wrong user is refused by the store, which writes no log line; a sign-in
// that gets no answer is retried with a warning, so the key also goes to an
// address where nothing listens, and the log file is required to have
// grown, or its holding no key would prove nothing.
test("a refused key reaches no log, no export and no page", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "bookmarks are made in the editor on the mock only");
  const key = `journey-key-${randomBytes(12).toString("hex")}`;
  const name = "journey secrets";
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  const tile = rail.getByRole("button", { name, exact: true });
  await connect(page, "mock");
  let settings = await openSettings(page);
  const before = { level: await settings.getByLabel("Log level").inputValue(), file: await settings.getByLabel("Keep a log file").isChecked() };
  try {
    await setLog(settings, "debug", true);
    const logFile = (await settings.locator(".log-path .path").textContent())!;
    await settings.getByRole("button", { name: "Close settings" }).click();

    await rail.getByTitle("Add bookmark", { exact: true }).click();
    await page.getByLabel("Display name").fill(name);
    await page.getByRole("textbox", { name: /^Auth URL/ }).fill(process.env.AERONAUTES_MOCK!);
    await page.getByLabel("Username").fill("test:nobody");
    await page.getByLabel(/^API key/).fill(key);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await tile.click();
    await expect(page.getByText(/^connect failed: /)).toBeVisible();

    const logged = existsSync(logFile) ? statSync(logFile).size : 0;
    await rail.getByRole("button", { name: `Edit ${name}`, exact: true }).click();
    await page.getByRole("textbox", { name: /^Auth URL/ }).fill(`http://127.0.0.1:${await closedPort()}/auth/v1.0`);
    // A changed address needs the key again.
    await page.getByLabel(/^API key/).fill(key);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await tile.click();
    await expect(page.getByText(/^connect failed: /)).toBeVisible({ timeout: 2 * 60 * 1000 });
    await expect.poll(() => readFileSync(logFile, "utf8").slice(logged)).toMatch(/sign-in got no answer/);

    await page.getByRole("button", { name: /jobs & changes/ }).click();
    await page.getByRole("tab", { name: "Recent changes" }).click();
    const into = localFolder();
    await control.pickFolder(into);
    await page.getByRole("button", { name: "Export log…" }).click();
    // Saving a bookmark and a refused sign-in are not audited, so the
    // export holds whatever the run's trail holds, which may be nothing
    // when this journey runs alone.
    await expect(page.getByText(/^Exported \d+ events? to /)).toBeVisible();
    const exported = readdirSync(into).filter((file) => file.endsWith(".jsonl"));
    expect(exported, "one export").toHaveLength(1);

    await page.reload();
    await expect(tile).toBeVisible();
    await rail.getByRole("button", { name: `Edit ${name}`, exact: true }).click();
    await expect(page.getByLabel(/^API key/)).toHaveValue("");
    const html = await page.content();
    await page.getByRole("button", { name: "Cancel", exact: true }).click();

    const kept = [logFile, `${logFile}.1`].filter((file) => existsSync(file));
    const places: [string, string][] = [
      ["the application's output", readFileSync(join(process.env.AERONAUTES_PRIVATE!, "app.log"), "utf8")],
      ...kept.map((file): [string, string] => [file.endsWith(".1") ? "the older log file" : "the log file", readFileSync(file, "utf8")]),
      ["the exported audit log", readFileSync(join(into, exported[0]), "utf8")],
      ["the reloaded page", html],
    ];
    for (const [place, text] of places) expect(text.includes(key), `${place} holds the key`).toBe(false);
  } finally {
    // Later journeys share these settings and this rail.
    await page.goto("/");
    await expect(rail).toBeVisible();
    if (await tile.count()) await deleteBookmark(page, name);
    settings = await openSettings(page);
    await setLog(settings, before.level, before.file);
    await settings.getByRole("button", { name: "Close settings" }).click();
  }
});

async function openSettings(page: Page): Promise<Locator> {
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Settings" });
  await expect(dialog).toBeVisible();
  return dialog;
}

async function setLog(settings: Locator, level: string, file: boolean): Promise<void> {
  await settings.getByLabel("Log level").selectOption(level);
  await expect(settings.getByText(`${level} in force`, { exact: true })).toBeVisible();
  const keep = settings.getByLabel("Keep a log file");
  if ((await keep.isChecked()) !== file) await keep.click();
  await expect(keep).toBeChecked({ checked: file });
  await expect(settings.getByText(file ? "being written" : "off", { exact: true })).toBeVisible();
}

// closedPort is a loopback port nothing listens on, found by listening on
// one and closing it again.
async function closedPort(): Promise<number> {
  const server = createServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as { port: number }).port;
  await new Promise((resolve) => server.close(resolve));
  return port;
}
