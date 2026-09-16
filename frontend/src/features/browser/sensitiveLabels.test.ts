import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import FindPanel from "./FindPanel.svelte";
import AclEditor from "../inspector/AclEditor.svelte";
import ContainerEditPanel from "../inspector/ContainerEditPanel.svelte";
import ManifestDeleteDialog from "../inspector/ManifestDeleteDialog.svelte";
import JobsDrawer from "../jobs/JobsDrawer.svelte";
import TransferDialog from "../transfer/TransferDialog.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
const states = new Set<BrowserState>();

afterEach(async () => {
  for (const component of mounted) await unmount(component);
  for (const state of states) await state.cancelFind();
  mounted.clear();
  states.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}

function host() {
  const root = document.createElement("div");
  document.body.append(root);
  return root;
}

function button(root: HTMLElement, text: string) {
  const found = [...root.querySelectorAll("button")].find((node) => node.textContent?.trim() === text);
  expect(found, `button ${text}`).toBeDefined();
  return found!;
}

function click(node: HTMLElement) { node.click(); flushSync(); }

function edit(node: HTMLInputElement, value: string) {
  node.value = value;
  node.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function labelled(root: HTMLElement, text: string) {
  const label = [...root.querySelectorAll("label")].find((node) =>
    node.textContent?.replace(/\s+/g, " ").trim() === text);
  expect(label, `visible label ${text}`).toBeDefined();
  const control = label!.control;
  expect(control, `control for ${text}`).not.toBeNull();
  expect(root.contains(control), `control stays in its own instance: ${text}`).toBe(true);
  const activated = vi.fn();
  control!.addEventListener("click", activated, { once: true });
  // JSDOM models label activation, but not native label-click focus.
  click(label!);
  expect(activated).toHaveBeenCalledOnce();
  return control!;
}

function inputFor(root: HTMLElement, text: string) {
  const control = labelled(root, text);
  expect(control).toBeInstanceOf(HTMLInputElement);
  return control as HTMLInputElement;
}

async function fixture() {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  states.add(browser);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Label fixture",
    authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  api.startFind = vi.fn(api.startFind);
  api.startBenchmark = vi.fn(api.startBenchmark);
  api.auditSearch = vi.fn(api.auditSearch);
  api.updateContainer = vi.fn(api.updateContainer);
  api.startCopy = vi.fn(api.startCopy);
  api.deleteManifestWithSegments = vi.fn().mockResolvedValue({
    manifestDeleted: true, segmentsDeleted: 3, segmentsFailed: 0, basis: "backstage-forced",
  });
  return { api, browser, bookmark, root: host() };
}

describe("Find query label", () => {
  it("targets the literal query without starting a scan until submission", async () => {
    const f = await fixture();
    mounted.add(mount(FindPanel, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } }));
    flushSync();
    const query = inputFor(f.root, "Find query");
    expect(f.api.startFind).not.toHaveBeenCalled();
    edit(query, "  folder//λ  ");
    expect(f.api.startFind).not.toHaveBeenCalled();
    click(button(f.root, "Find"));
    await settle();
    expect(f.api.startFind).toHaveBeenCalledExactlyOnceWith("fixtures", "", "  folder//λ  ");
  });

  it("keeps identical labels attached to their own panel", async () => {
    const first = await fixture();
    const second = await fixture();
    for (const f of [first, second]) {
      mounted.add(mount(FindPanel, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } }));
    }
    flushSync();
    const firstInput = inputFor(first.root, "Find query");
    const secondInput = inputFor(second.root, "Find query");
    expect(firstInput).not.toBe(secondInput);
    edit(secondInput, "second");
    click(button(second.root, "Find"));
    await settle();
    expect(first.api.startFind).not.toHaveBeenCalled();
    expect(second.api.startFind).toHaveBeenCalledExactlyOnceWith("fixtures", "", "second");
    expect(firstInput.value).toBe("");
  });

  it("does not revive a closed panel through its labelled input", async () => {
    const f = await fixture();
    const closed = vi.fn();
    mounted.add(mount(FindPanel, { target: f.root, props: { browser: f.browser, onClose: closed } }));
    flushSync();
    click(f.root.querySelector<HTMLButtonElement>('[aria-label="Close find"]')!);
    edit(inputFor(f.root, "Find query"), "retired");
    click(button(f.root, "Find"));
    await settle();
    expect(closed).toHaveBeenCalledOnce();
    expect(f.api.startFind).not.toHaveBeenCalled();
  });
});

describe("ACL input labels", () => {
  it.each(["add entry", "raw"])("keeps Read and Write %s edits distinct through Save", async (mode) => {
    const f = await fixture();
    const detail = f.browser.containerDetail!;
    mounted.add(mount(ContainerEditPanel, { target: f.root,
      props: { browser: f.browser, detail, onDone: vi.fn() } }));
    flushSync();
    if (mode === "raw") for (const toggle of f.root.querySelectorAll<HTMLButtonElement>(".acl .mode")) click(toggle);
    const read = inputFor(f.root, `Read ACL (${mode})`);
    const write = inputFor(f.root, `Write ACL (${mode})`);
    expect(read).not.toBe(write);
    edit(read, "tenant:reader");
    edit(write, "tenant:writer");
    if (mode === "add entry") {
      for (const node of [read, write]) node.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
      flushSync();
    }
    expect(f.api.updateContainer).not.toHaveBeenCalled();
    click(button(f.root, "Save"));
    await settle();
    expect(f.api.updateContainer).toHaveBeenCalledOnce();
    expect(vi.mocked(f.api.updateContainer).mock.calls[0]).toEqual([
      "fixtures", detail.editableFingerprint, expect.objectContaining({
        readAcl: "tenant:reader", writeAcl: "tenant:writer", ackPublic: false,
      }),
    ]);
  });

  it("retargets each instance when switching between add and raw modes", () => {
    const first = host();
    const second = host();
    for (const root of [first, second]) mounted.add(mount(AclEditor,
      { target: root, props: { label: "Read ACL", value: "tenant:reader" } }));
    flushSync();
    const previous = inputFor(second, "Read ACL (add entry)");
    click(button(second, "raw"));
    const raw = inputFor(second, "Read ACL (raw)");
    expect(previous.isConnected).toBe(false);
    edit(raw, "tenant:second");
    click(button(second, "structured"));
    const entry = inputFor(second, "Read ACL (add entry)");
    expect(raw.isConnected).toBe(false);
    edit(entry, "tenant:third");
    entry.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    flushSync();
    click(button(second, "raw"));
    expect(inputFor(second, "Read ACL (raw)").value).toBe("tenant:second,tenant:third");
    click(button(first, "raw"));
    expect(inputFor(first, "Read ACL (raw)").value).toBe("tenant:reader");
  });

  it.each(["add entry", "raw"])("preserves public-read acknowledgement after a labelled %s edit", async (mode) => {
    const f = await fixture();
    mounted.add(mount(ContainerEditPanel, { target: f.root,
      props: { browser: f.browser, detail: f.browser.containerDetail!, onDone: vi.fn() } }));
    flushSync();
    if (mode === "raw") click(f.root.querySelector<HTMLButtonElement>(".acl .mode")!);
    const read = inputFor(f.root, `Read ACL (${mode})`);
    edit(read, ".r:*");
    if (mode === "add entry") read.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true }));
    flushSync();
    click(button(f.root, "Save"));
    await settle();
    expect(f.api.updateContainer).not.toHaveBeenCalled();
    expect(f.root.textContent).toContain("Tick the box below to confirm");
    click(f.root.querySelector<HTMLInputElement>(".public-ack input")!);
    click(button(f.root, "Save"));
    await settle();
    expect(f.api.updateContainer).toHaveBeenCalledOnce();
    expect(vi.mocked(f.api.updateContainer).mock.calls[0][2]).toMatchObject({ readAcl: ".r:*", ackPublic: true });
  });
});

async function drawer() {
  const f = await fixture();
  mounted.add(mount(JobsDrawer, { target: f.root, props: { browser: f.browser } }));
  flushSync();
  click(f.root.querySelector<HTMLButtonElement>(".bar")!);
  await settle();
  return f;
}

describe("audit and benchmark labels", () => {
  it("keeps operation and result filters distinct without searching on label activation", async () => {
    const f = await drawer();
    click(button(f.root, "Recent changes"));
    await settle();
    vi.mocked(f.api.auditSearch).mockClear();
    const operation = inputFor(f.root, "Operation prefix");
    const result = labelled(f.root, "Result");
    expect(result).toBeInstanceOf(HTMLSelectElement);
    expect(f.api.auditSearch).not.toHaveBeenCalled();
    edit(operation, " delete.λ ");
    operation.dispatchEvent(new Event("change", { bubbles: true }));
    await settle();
    expect(f.api.auditSearch).toHaveBeenLastCalledWith({
      operationPrefix: " delete.λ ", result: "", bookmarkId: "", beforeId: 0, limit: 50,
    });
    (result as HTMLSelectElement).value = "failed";
    result.dispatchEvent(new Event("change", { bubbles: true }));
    await settle();
    expect(f.api.auditSearch).toHaveBeenCalledTimes(2);
    expect(f.api.auditSearch).toHaveBeenLastCalledWith({
      operationPrefix: " delete.λ ", result: "failed", bookmarkId: "", beforeId: 0, limit: 50,
    });
  });

  it("keeps audit labels within repeated drawers", async () => {
    const first = await drawer();
    const second = await drawer();
    for (const f of [first, second]) { click(button(f.root, "Recent changes")); await settle(); }
    const operation = inputFor(second.root, "Operation prefix");
    expect(operation).not.toBe(inputFor(first.root, "Operation prefix"));
    expect(labelled(second.root, "Result")).not.toBe(labelled(first.root, "Result"));
  });

  it.each([false, true])("does not offer a benchmark in a v1 build (second drawer %s)", async (duplicate) => {
    const first = duplicate ? await drawer() : null;
    const f = await drawer();
    const labels = (root: HTMLElement) => [...root.querySelectorAll("button")].map((b) => b.textContent?.trim());
    expect(labels(f.root)).not.toContain("Benchmark…");
    if (first) expect(labels(first.root)).not.toContain("Benchmark…");
    expect(f.api.startBenchmark).not.toHaveBeenCalled();
  });
});

async function manifest() {
  const f = await fixture();
  f.browser.select(f.browser.entries.findIndex((entry) => entry.name === "external-dlo"));
  await settle();
  f.api.checkSegmentOwnership = vi.fn().mockResolvedValue({
    class: "dlo", target: "backing/old/", proven: false, segments: 3, inlineEntries: 0,
  });
  mounted.add(mount(ManifestDeleteDialog, { target: f.root, props: {
    browser: f.browser, detail: f.browser.objectDetail!, segmentsAt: "backing/old/", onClose: vi.fn(),
  } }));
  await settle();
  click(f.root.querySelector<HTMLInputElement>('input[type="checkbox"]')!);
  return f;
}

describe("segment force label", () => {
  it.each([false, true])("preserves exact force arming and explicit deletion (second dialog %s)", async (duplicate) => {
    const first = duplicate ? await manifest() : null;
    const f = await manifest();
    const confirm = inputFor(f.root, "Type the object name to arm it:");
    if (first) expect(confirm).not.toBe(first.root.querySelector(".force-confirm"));
    expect(f.api.deleteManifestWithSegments).not.toHaveBeenCalled();
    const submit = button(f.root, "Delete manifest + segments");
    edit(confirm, "wrong");
    expect(submit.disabled).toBe(true);
    click(submit);
    expect(f.api.deleteManifestWithSegments).not.toHaveBeenCalled();
    edit(confirm, "external-dlo");
    expect(submit.disabled).toBe(false);
    expect(f.api.deleteManifestWithSegments).not.toHaveBeenCalled();
    click(submit);
    await settle();
    expect(f.api.deleteManifestWithSegments).toHaveBeenCalledExactlyOnceWith({
      bookmarkId: f.bookmark.id, container: "fixtures", name: "external-dlo", force: true, confirm: "external-dlo",
    });
    if (first) expect(first.api.deleteManifestWithSegments).not.toHaveBeenCalled();
  });
});

it("retains the existing destination names and captured literal Preview request", async () => {
  const f = await fixture();
  f.api.planCopy = vi.fn(f.api.planCopy);
  mounted.add(mount(TransferDialog, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } }));
  flushSync();
  const bookmark = f.root.querySelector<HTMLSelectElement>('[aria-label="Destination bookmark"]')!;
  const container = f.root.querySelector<HTMLInputElement>('[aria-label="Destination container"]')!;
  const prefix = f.root.querySelector<HTMLInputElement>('[aria-label="Destination prefix"]')!;
  expect(bookmark).toBeInstanceOf(HTMLSelectElement);
  expect(container).toBeInstanceOf(HTMLInputElement);
  expect(prefix).toBeInstanceOf(HTMLInputElement);
  edit(container, " literal container ");
  edit(prefix, " /literal//λ ");
  expect(f.api.planCopy).not.toHaveBeenCalled();
  expect(f.api.startCopy).not.toHaveBeenCalled();
  click(button(f.root, "Preview"));
  await settle();
  expect(f.api.planCopy).toHaveBeenCalledOnce();
  expect(vi.mocked(f.api.planCopy).mock.calls[0][0]).toMatchObject({
    destBookmarkId: f.bookmark.id, destContainer: " literal container ", destPrefix: " /literal//λ ",
  });
  expect(f.api.startCopy).not.toHaveBeenCalled();
});
