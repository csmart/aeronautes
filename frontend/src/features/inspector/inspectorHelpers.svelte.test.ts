import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import MetadataEditor from "./MetadataEditor.svelte";
import CopyValue from "./CopyValue.svelte";
import { keySuggestions } from "../../lib/metadataKeys";
import type { MetaRow } from "../../lib/metadataDiff";

// ADR 0125: metadata key suggestions and copying a value.

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

async function settle() {
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
}

function editor(rows: MetaRow[]) {
  const props = $state({ base: {}, rows, suggestions: keySuggestions("container", ["Team"]) });
  mounted.add(mount(MetadataEditor, { target: document.body, props }));
  flushSync();
  const keyField = (n: number) => document.querySelectorAll<HTMLInputElement>('input[aria-label="metadata key"]')[n];
  const listed = () => [...document.querySelectorAll('[role="option"]')].map((o) => o.querySelector(".type-mono")!.textContent);
  const type = (field: HTMLInputElement, text: string) => {
    field.value = text;
    field.dispatchEvent(new Event("input", { bubbles: true }));
    flushSync();
  };
  const key = (field: HTMLInputElement, name: string) => {
    const event = new KeyboardEvent("keydown", { key: name, bubbles: true, cancelable: true });
    field.dispatchEvent(event);
    flushSync();
    return event;
  };
  return { props, keyField, listed, type, key };
}

describe("metadata key suggestions in the editor", () => {
  it("lists every suggestion on focus, then those holding what was typed anywhere", () => {
    const f = editor([{ key: "", value: "" }]);
    f.keyField(0).dispatchEvent(new FocusEvent("focus"));
    flushSync();
    expect(f.listed()).toHaveLength(keySuggestions("container", ["Team"]).length);
    f.type(f.keyField(0), "LISTINGS");
    expect(f.listed()).toEqual(["Web-Listings", "Web-Listings-CSS", "Web-Listings-Label"]);
    expect(document.querySelector('[role="option"] mark')?.textContent).toBe("Listings");
  });

  it("takes a suggestion with the arrow keys and Enter, keeping those keys from the window", () => {
    const f = editor([{ key: "", value: "" }]);
    f.keyField(0).dispatchEvent(new FocusEvent("focus"));
    f.type(f.keyField(0), "listings");
    expect(f.key(f.keyField(0), "ArrowDown").defaultPrevented).toBe(true);
    const enter = f.key(f.keyField(0), "Enter");
    expect(enter.defaultPrevented).toBe(true);
    expect(f.props.rows[0].key).toBe("Web-Listings-CSS");
    expect(f.listed()).toEqual([]);
  });

  it("takes one by pointer, closes on Escape, and leaves out keys the item has", () => {
    const f = editor([{ key: "Web-Index", value: "index.html" }, { key: "", value: "" }]);
    f.keyField(1).dispatchEvent(new FocusEvent("focus"));
    f.type(f.keyField(1), "web-i");
    expect(f.listed()).toEqual([]);
    f.type(f.keyField(1), "team");
    document.querySelector<HTMLElement>('[role="option"]')!.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true }));
    flushSync();
    expect(f.props.rows[1].key).toBe("Team");
    f.type(f.keyField(1), "web");
    expect(f.listed().length).toBeGreaterThan(0);
    const escape = f.key(f.keyField(1), "Escape");
    expect(escape.defaultPrevented).toBe(true);
    expect(f.listed()).toEqual([]);
  });
});

describe("copying a value", () => {
  it("copies the row's exact value and says so, or says why not", async () => {
    vi.useFakeTimers();
    const copy = vi.fn(async () => {});
    mounted.add(mount(CopyValue, { target: document.body, props: { label: "ETag", value: "  d41d8cd98f00b204e9800998ecf8427e", copy } }));
    flushSync();
    const button = document.querySelector<HTMLButtonElement>('button[aria-label="Copy ETag"]')!;
    button.click();
    await settle();
    expect(copy).toHaveBeenCalledWith("  d41d8cd98f00b204e9800998ecf8427e");
    expect(document.querySelector('[role="status"]')?.textContent).toBe("Copied");
    vi.advanceTimersByTime(1600);
    flushSync();
    expect(document.querySelector('[role="status"]')).toBeNull();
    copy.mockRejectedValueOnce(new Error("no clipboard"));
    button.click();
    await settle();
    expect(document.querySelector('[role="status"]')?.textContent).toBe("Not copied: no clipboard");
  });
});
