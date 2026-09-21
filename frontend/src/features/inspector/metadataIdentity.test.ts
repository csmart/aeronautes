import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import ObjectEditPanel from "./ObjectEditPanel.svelte";
import ContainerEditPanel from "./ContainerEditPanel.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

async function settle() {
  for (let i = 0; i < 12; i++) {
    await Promise.resolve();
    flushSync();
  }
}

function button(root: HTMLElement, label: string) {
  const found = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error("Missing button: " + label);
  return found;
}

function edit(input: HTMLInputElement, value: string) {
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function rows(root: HTMLElement) {
  return [...root.querySelectorAll<HTMLDivElement>(".editor .row")].map((row) => ({
    key: row.querySelector<HTMLInputElement>(".key")!,
    value: row.querySelector<HTMLInputElement>(".val")!,
    remove: row.querySelector<HTMLButtonElement>(".remove")!,
  }));
}

type Kind = "object" | "container";
async function show(kind: Kind, meta: Record<string, string> = { Keep: "untouched" }) {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.account = { name: "AUTH_source", containers: 1, objects: 1, bytesUsed: 0,
    quotaBytes: 0, meta: {}, tempUrlKeySlots: [], scope: { bookmarkId: "source", generation: 1 } };
  await browser.loadContainers();
  await browser.openContainer("fixtures");
  if (kind === "object") {
    browser.select(browser.entries.findIndex((entry) => entry.name === "empty-object"));
    await settle();
    browser.objectDetail = { ...browser.objectDetail!, meta };
  } else {
    browser.containerDetail = { ...browser.containerDetail!, meta };
  }
  const objectSave = vi.spyOn(api, "updateObjectMetadata").mockRejectedValue(new Error("metadata input refused"));
  const containerSave = vi.spyOn(api, "updateContainer").mockRejectedValue(new Error("metadata input refused"));
  const root = document.createElement("div");
  document.body.append(root);
  const done = vi.fn();
  mounted.add(kind === "object"
    ? mount(ObjectEditPanel, { target: root, props: { browser, detail: browser.objectDetail!, onDone: done } })
    : mount(ContainerEditPanel, { target: root, props: { browser, detail: browser.containerDetail!, onDone: done } }));
  flushSync();
  const save = kind === "object" ? objectSave : containerSave;
  const payload = () => {
    const request = kind === "object" ? objectSave.mock.calls[0]?.[3] : containerSave.mock.calls[0]?.[2];
    if (!request) throw new Error("No metadata save request");
    return request.meta;
  };
  const add = (key: string, value: string) => {
    button(root, "+ add key").click();
    flushSync();
    const row = rows(root).at(-1)!;
    edit(row.key, key);
    edit(row.value, value);
  };
  return { root, browser, done, save, payload, add };
}

describe.each(["object", "container"] as const)("%s metadata identity", (kind) => {
  it("sends the complete working copy with inherited property names", async () => {
    const base = { Keep: "untouched", constructor: "old", toString: "retained", ["__proto__"]: "retained too" };
    const f = await show(kind, base);
    edit(rows(f.root).find((row) => row.key.value === "constructor")!.value, "changed");
    f.add("New", "added");
    expect(f.root.querySelector(".chip.changed")?.textContent).toBe("~ constructor");
    button(f.root, "Save").click();
    await settle();
    expect(f.save).toHaveBeenCalledOnce();
    expect(JSON.parse(JSON.stringify(f.payload()))).toEqual({ ...base, constructor: "changed", New: "added" });
    expect(Object.hasOwn(f.payload()!, "__proto__")).toBe(true);
    expect(f.done).not.toHaveBeenCalled();
    expect(f.root.textContent).toContain("metadata input refused");
    expect(rows(f.root).find((row) => row.key.value === "__proto__")?.value.value).toBe("retained too");
  });

  it.each(["__proto__", "constructor", "toString"])("shows %s as a new key before saving it", async (key) => {
    const f = await show(kind);
    f.add(key, "new value");
    expect(f.root.querySelector(".chip.added")?.textContent).toBe("+ " + key);
    expect(f.root.querySelector(".chip.changed")).toBeNull();
    expect(f.root.querySelector(".chip.removed")).toBeNull();
    button(f.root, "Save").click();
    await settle();
    expect(f.save).toHaveBeenCalledOnce();
    expect(JSON.parse(JSON.stringify(f.payload()))).toEqual({ Keep: "untouched", [key]: "new value" });
  });

  it.each(["same", "changed"])("a case-only rename with a %s value has an honest diff", async (value) => {
    const f = await show(kind, { Owner: "same", Keep: "untouched" });
    const row = rows(f.root).find((entry) => entry.key.value === "Owner")!;
    edit(row.key, "oWnEr");
    edit(row.value, value);
    expect(f.root.querySelector(".chip.added")).toBeNull();
    expect(f.root.querySelector(".chip.removed")).toBeNull();
    if (value === "same") expect(f.root.querySelector(".diff")).toBeNull();
    else expect(f.root.querySelector(".chip.changed")?.textContent).toBe("~ oWnEr");
    button(f.root, "Save").click();
    await settle();
    expect(JSON.parse(JSON.stringify(f.payload()))).toEqual({ oWnEr: value, Keep: "untouched" });
  });

  it("shows a deliberate removal and omits only that key", async () => {
    const f = await show(kind, { constructor: "remove", Keep: "untouched" });
    rows(f.root).find((row) => row.key.value === "constructor")!.remove.click();
    flushSync();
    expect(f.root.querySelector(".chip.removed")?.textContent).toBe("− constructor");
    expect(f.root.textContent).toContain("Removed keys are erased on save");
    button(f.root, "Save").click();
    await settle();
    expect(JSON.parse(JSON.stringify(f.payload()))).toEqual({ Keep: "untouched" });
  });

  it.each([" leading", "trailing ", "  ", "", "a_b", "a:b", "雪", "<img src=x>"])(
    "keeps literal key %j and its value visible after backend refusal",
    async (key) => {
      const f = await show(kind);
      f.add(key, " literal value ");
      button(f.root, "Save").click();
      await settle();
      expect(f.save).toHaveBeenCalledOnce();
      expect(JSON.parse(JSON.stringify(f.payload()))).toEqual({ Keep: "untouched", [key]: " literal value " });
      expect(rows(f.root).at(-1)?.key.value).toBe(key);
      expect(rows(f.root).at(-1)?.value.value).toBe(" literal value ");
      expect(f.root.textContent).toContain("metadata input refused");
      expect(f.root.querySelector("img")).toBeNull();
      expect(button(f.root, "Save").disabled).toBe(false);
      expect(f.done).not.toHaveBeenCalled();
    },
  );

  it("retains an empty value as an entry for backend admission", async () => {
    const f = await show(kind);
    f.add("Empty", "");
    button(f.root, "Save").click();
    await settle();
    expect(JSON.parse(JSON.stringify(f.payload()))).toEqual({ Keep: "untouched", Empty: "" });
    expect(rows(f.root).at(-1)?.key.value).toBe("Empty");
  });

  it("ignores an untouched empty row without dropping existing metadata", async () => {
    const f = await show(kind);
    f.add("", "");
    expect(f.root.querySelector(".diff")).toBeNull();
    button(f.root, "Save").click();
    await settle();
    expect(JSON.parse(JSON.stringify(f.payload()))).toEqual({ Keep: "untouched" });
  });

  it.each([["Owner", "OWNER"], ["constructor", "CONSTRUCTOR"], ["", ""]])(
    "refuses duplicate keys %j and %j without invoking the backend",
    async (first, second) => {
      const f = await show(kind);
      f.add(first, "first value");
      f.add(second, "second value");
      expect(f.root.querySelector(".dup")?.textContent ?? "").toContain("Duplicate key");
      button(f.root, "Save").click();
      await settle();
      expect(f.save).not.toHaveBeenCalled();
      expect(f.root.textContent).toContain("duplicate key");
      expect(rows(f.root).slice(-2).map((row) => [row.key.value, row.value.value])).toEqual([
        [first, "first value"], [second, "second value"],
      ]);
      expect(f.done).not.toHaveBeenCalled();
    },
  );
});
