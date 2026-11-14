import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import TransferDialog from "./TransferDialog.svelte";
import { v1Offers } from "../../lib/v1.svelte";

// These cases exercise the Mirror comparison the release does not offer
// (v1Offers.mirrorPreview), so they turn it on for themselves.
beforeEach(() => { v1Offers.mirrorPreview = true; });
afterEach(() => { v1Offers.mirrorPreview = false; });

let dialog: ReturnType<typeof mount> | null = null;

afterEach(async () => {
  if (dialog) await unmount(dialog);
  dialog = null;
  document.body.innerHTML = "";
});

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll("button")].find((item) => item.textContent?.trim() === label);
  if (!found) throw new Error(`missing button ${label}`);
  return found;
}

function input(placeholder: string, value: string) {
  const field = document.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!;
  field.value = value;
  field.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function show(mode: "Copy" | "Mirror" = "Copy") {
  const api = newFakeApi();
  const plan = vi.spyOn(api, "planCopy");
  const start = vi.spyOn(api, "startCopy").mockResolvedValue("literal-copy");
  const mirror = vi.spyOn(api, "startMirror");
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source-bookmark";
  browser.containerName = " source container ";
  browser.prefix = " source prefix /";
  dialog = mount(TransferDialog, { target: document.body, props: { browser, onClose: vi.fn() } });
  flushSync();
  if (mode === "Mirror") {
    button("Mirror").click();
    flushSync();
  }
  input("container", "destination");
  return { api, browser, plan, start, mirror };
}

const scopes = [
  { name: "root", container: "destination", prefix: "" },
  { name: "leading space", container: "destination", prefix: "  prefix/" },
  { name: "space before delimiter", container: "destination", prefix: "prefix  /" },
  { name: "space component", container: "destination", prefix: "   /" },
  { name: "repeated separators", container: "destination", prefix: " a//b /" },
  { name: "literal escapes and unicode", container: "destination", prefix: " 日本語%2F +/" },
  { name: "leading container space", container: " destination", prefix: "scope/" },
  { name: "trailing container space", container: "destination ", prefix: "scope/" },
  { name: "space-only container", container: "   ", prefix: "scope/" },
];

for (const mode of ["Copy", "Mirror"] as const) {
  for (const scope of scopes) {
    it(`${mode} keeps ${scope.name} literal through the prepared request`, async () => {
      const f = show(mode);
      input("container", scope.container);
      input("folder path/ (optional)", scope.prefix);
      button("Preview").click();
      await vi.waitFor(() => expect(f.plan).toHaveBeenCalledOnce());
      const request = f.plan.mock.calls[0][0];
      expect(request).toMatchObject({
        sourceBookmarkId: "source-bookmark", sourceContainer: " source container ",
        sourcePrefix: " source prefix /", destContainer: scope.container, destPrefix: scope.prefix,
      });
      const prepared = structuredClone(request);
      await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
      if (mode === "Copy") {
        button("Start copy").click();
        await vi.waitFor(() => expect(f.start).toHaveBeenCalledOnce());
        expect(f.start.mock.calls[0][0]).toEqual(prepared);
      } else {
        expect(button("Mirror unavailable").disabled).toBe(true);
        expect(f.start).not.toHaveBeenCalled();
      }
      expect(f.mirror).not.toHaveBeenCalled();
    });
  }

  for (const prefix of ["   ", "  object prefix  ", "scope/ ", "日本語"]) {
    it(`${mode} retains unsupported prefix ${JSON.stringify(prefix)} and its service error`, async () => {
      const f = show(mode);
      const message = `prefix ${JSON.stringify(prefix)} must be empty or end with '/'`;
      f.plan.mockRejectedValue(new Error(message));
      input("folder path/ (optional)", prefix);
      button("Preview").click();
      await vi.waitFor(() => expect(document.querySelector(".error")?.textContent).toBe(message));
      expect(f.plan.mock.calls[0][0].destPrefix).toBe(prefix);
      expect(document.querySelector<HTMLInputElement>('input[placeholder="folder path/ (optional)"]')!.value).toBe(prefix);
      expect(document.querySelector(".preview")).toBeNull();
      expect(button(mode === "Copy" ? "Start copy" : "Mirror unavailable").disabled).toBe(true);
      expect(f.start).not.toHaveBeenCalled();
      expect(f.mirror).not.toHaveBeenCalled();
    });
  }
}

it("shows exact source and destination scope, distinguishing spaces from root", () => {
  show();
  input("container", "  destination  ");
  const source = document.querySelector('[aria-label="Source scope"]')!;
  const destination = document.querySelector('[aria-label="Destination scope"]')!;
  expect(source).not.toBeNull();
  expect(destination).not.toBeNull();
  expect(source?.textContent).toContain('" source container "');
  expect(source?.textContent).toContain('" source prefix /"');
  expect(destination?.textContent).toContain('"  destination  "');
  // The source is a folder, so the destination path starts as its name.
  expect(destination?.textContent).toContain('" source prefix /"');
  input("folder path/ (optional)", "");
  expect(destination?.textContent).toContain('"" (container root)');
  input("folder path/ (optional)", "   ");
  expect(destination?.textContent).toContain('"   "');
  expect(destination?.textContent).not.toContain("container root");
});

// Copying a folder copies the folder: the destination path starts as the
// folder's own name, written exactly as it is, and what is in the field when
// the copy is previewed is what is sent. Anything that is not a folder
// starts at the destination's top (0.5.0 test run).
it.each([
  ["a folder being browsed", "2026/", "photos/2026/", null],
  ["a folder at the container's top", "photos/", "photos/", null],
  ["a folder row selected at the top", "normal/", "", "normal/"],
  ['the folder named "/"', "/", "/", null],
  ["a whole container", "", "", null],
  ["an object selected in a folder", "", "normal/", "normal/hello.txt"],
])("starts the destination path of %s as %j", async (_what, expected, prefix, selected) => {
  const api = newFakeApi();
  const plan = vi.spyOn(api, "planCopy");
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth", username: "fixture:user",
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  if (prefix !== "") await browser.goTo(prefix);
  if (selected !== null) browser.select(browser.entries.findIndex((row) => row.name === selected));
  dialog = mount(TransferDialog, { target: document.body, props: { browser, onClose: vi.fn() } });
  flushSync();
  const field = document.querySelector<HTMLInputElement>('input[aria-label="Destination prefix"]')!;
  expect(field.value).toBe(expected);

  input("container", "elsewhere");
  button("Preview").click();
  await vi.waitFor(() => expect(plan).toHaveBeenCalledOnce());
  expect(plan.mock.calls[0][0]).toMatchObject({ destContainer: "elsewhere", destPrefix: expected });
});

it("copies a folder's contents alone when the destination path is cleared", async () => {
  const f = show();
  input("folder path/ (optional)", "");
  button("Preview").click();
  await vi.waitFor(() => expect(f.plan).toHaveBeenCalledOnce());
  expect(f.plan.mock.calls[0][0]).toMatchObject({ sourcePrefix: " source prefix /", destPrefix: "" });
});

it("keeps scope markup as literal text", () => {
  show();
  input("container", '<img src=x onerror="bad()">');
  input("folder path/ (optional)", "<script>bad()</script>/");
  const destination = document.querySelector('[aria-label="Destination scope"]');
  expect(destination).not.toBeNull();
  expect(destination?.textContent).toContain(JSON.stringify('<img src=x onerror="bad()">'));
  expect(destination?.textContent).toContain(JSON.stringify("<script>bad()</script>/"));
  expect(document.querySelector("img, script")).toBeNull();
});

it("disarms a prepared request when only prefix whitespace changes", async () => {
  const f = show();
  input("folder path/ (optional)", "scope/");
  button("Preview").click();
  await vi.waitFor(() => expect(button("Start copy").disabled).toBe(false));
  input("folder path/ (optional)", " scope/");
  expect(button("Start copy").disabled).toBe(true);
  button("Start copy").dispatchEvent(new MouseEvent("click", { bubbles: true }));
  expect(f.start).not.toHaveBeenCalled();
  button("Preview").click();
  await vi.waitFor(() => expect(f.plan).toHaveBeenCalledTimes(2));
  expect(f.plan.mock.calls[1][0].destPrefix).toBe(" scope/");
  const prepared = structuredClone(f.plan.mock.calls[1][0]);
  await vi.waitFor(() => expect(button("Start copy").disabled).toBe(false));
  button("Start copy").click();
  await vi.waitFor(() => expect(f.start).toHaveBeenCalledOnce());
  expect(f.start.mock.calls[0][0]).toEqual(prepared);
});

it("does not publish an old prefix error into the replacement scope", async () => {
  const f = show();
  let reject!: (reason: Error) => void;
  f.plan.mockReturnValueOnce(new Promise((_, fail) => { reject = fail; }));
  input("folder path/ (optional)", "   ");
  button("Preview").click();
  await vi.waitFor(() => expect(f.plan).toHaveBeenCalledOnce());
  input("folder path/ (optional)", "valid/");
  button("Preview").click();
  await vi.waitFor(() => expect(button("Start copy").disabled).toBe(false));
  reject(new Error("old space prefix refused"));
  await f.plan.mock.results[0].value.catch(() => {});
  flushSync();
  expect(document.querySelector(".error")).toBeNull();
  expect(button("Start copy").disabled).toBe(false);
  button("Start copy").click();
  await vi.waitFor(() => expect(f.start).toHaveBeenCalledOnce());
  expect(f.start.mock.calls[0][0].destPrefix).toBe("valid/");
});

// "/" is a folder named "/", not the container's top: the dialog says so
// and still allows it, because Swift allows such names (0.5.0 test run).
it("warns that a destination folder path starting with a slash is not the top", () => {
  show();
  expect(document.querySelector(".slash-warning")).toBeNull();
  input("folder path/ (optional)", "/");
  expect(document.querySelector(".slash-warning")?.textContent).toContain("Leave it empty to copy to the top of the container");
  input("folder path/ (optional)", "");
  expect(document.querySelector(".slash-warning")).toBeNull();
});
