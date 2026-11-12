import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { BookmarkDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BookmarkEditor from "./BookmarkEditor.svelte";

// Slice C: the bookmark editor's auth-type selector. The Go store, DTO and
// bindings already carry the Keystone v3 identity (slice B); this proves the
// editor reveals the v3 fields and hands them to a save, and that a v1
// bookmark carries none (ADR 0100).

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

function field(placeholder: string, value: string) {
  const input = document.querySelector<HTMLInputElement>(`input[placeholder="${placeholder}"]`)!;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function fieldByLabel(labelPrefix: string, value: string) {
  const span = [...document.querySelectorAll<HTMLSpanElement>("label.field > span")]
    .find((s) => s.textContent?.trim().startsWith(labelPrefix))!;
  const input = span.parentElement!.querySelector("input")!;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function key(value: string) {
  const input = document.querySelector<HTMLInputElement>('input[type="password"]')!;
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

// The auth-type <select> is the one offering the Keystone v3 option (the
// other select is TLS).
function selectAuth(version: number) {
  const sel = [...document.querySelectorAll<HTMLSelectElement>("select")]
    .find((s) => [...s.options].some((o) => o.textContent?.trim() === "Keystone v3"))!;
  sel.value = String(version);
  sel.dispatchEvent(new Event("change", { bubbles: true }));
  flushSync();
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error("Missing button: " + label);
  return found;
}

function v3Fields() {
  return {
    project: document.querySelector('input[placeholder="my-project"]'),
    region: document.querySelector('input[placeholder="RegionOne"]'),
  };
}

function show(existing: BookmarkDTO | null = null) {
  const api = newFakeApi();
  const state = new BrowserState(api);
  const save = vi.spyOn(api, "saveBookmark");
  mounted.add(mount(BookmarkEditor, { target: document.body, props: { browser: state, existing, onClose: vi.fn() } }));
  flushSync();
  return { api, state, save };
}

describe("keystone v3 auth-type selector", () => {
  it("reveals the v3 identity fields only when Keystone v3 is chosen", () => {
    show();
    expect(v3Fields().project).toBeNull();
    expect(v3Fields().region).toBeNull();

    selectAuth(3);
    expect(v3Fields().project).not.toBeNull();
    expect(v3Fields().region).not.toBeNull();

    selectAuth(1);
    expect(v3Fields().project).toBeNull();
    expect(v3Fields().region).toBeNull();
  });

  it("saves a v3 bookmark with its identity; blank domains cross empty for the backend to default", () => {
    const f = show();
    field("Prod AU", "Keystone AU");
    selectAuth(3);
    field("https://keystone.example/v3", "https://keystone.example/v3");
    field("account:user", "keystoneuser");
    key("keystone-pass");
    field("my-project", "demo");
    field("RegionOne", "RegionOne");
    button("Save").click();

    expect(f.save).toHaveBeenCalledWith(
      expect.objectContaining({
        authVersion: 3,
        username: "keystoneuser",
        projectName: "demo",
        region: "RegionOne",
        userDomain: "",
        projectDomain: "",
        storageUrlOverride: "",
      }),
      "keystone-pass",
    );
  });

  it("carries explicit domains when the operator sets them", () => {
    const f = show();
    field("Prod AU", "Keystone AU");
    selectAuth(3);
    field("https://keystone.example/v3", "https://keystone.example/v3");
    field("account:user", "keystoneuser");
    key("keystone-pass");
    field("my-project", "demo");
    field("RegionOne", "RegionOne");
    fieldByLabel("User domain", "ldap-users");
    fieldByLabel("Project domain", "ops");
    button("Save").click();

    expect(f.save).toHaveBeenCalledWith(
      expect.objectContaining({ authVersion: 3, userDomain: "ldap-users", projectDomain: "ops" }),
      "keystone-pass",
    );
  });

  it("names the user and the project by ID, leaving the username and project blank (ADR 0113)", () => {
    const f = show();
    field("Prod AU", "Keystone IDs");
    selectAuth(3);
    field("https://keystone.example/v3", "https://keystone.example");
    key("keystone-pass");
    field("RegionOne", "RegionOne");
    fieldByLabel("User ID", "u-1");
    fieldByLabel("Project ID", "p-1");
    // jsdom submits a form whatever its required fields hold, so the
    // attribute a real browser enforces is checked directly.
    expect(document.querySelector<HTMLInputElement>('input[placeholder="account:user"]')!.required).toBe(false);
    expect(document.querySelector<HTMLInputElement>('input[placeholder="my-project"]')!.required).toBe(false);
    button("Save").click();

    expect(f.save).toHaveBeenCalledWith(
      expect.objectContaining({ authVersion: 3, username: "", userId: "u-1", projectName: "", projectId: "p-1" }),
      "keystone-pass",
    );
  });

  it("keeps a v1 bookmark free of Keystone fields", () => {
    const f = show();
    field("Prod AU", "TempAuth");
    field("https://swift.example/auth/v1.0", "https://swift.example/auth/v1.0");
    field("account:user", "acct:user");
    key("swift-key");
    button("Save").click();

    expect(f.save).toHaveBeenCalledWith(
      expect.objectContaining({ authVersion: 1, userDomain: "", projectName: "", projectDomain: "", userId: "", projectId: "", region: "" }),
      "swift-key",
    );
  });

  it("drops the v3 identity if the operator switches back to v1 before saving", () => {
    const f = show();
    field("Prod AU", "Switched");
    selectAuth(3);
    field("https://keystone.example/v3", "https://keystone.example/v3");
    field("account:user", "keystoneuser");
    key("secret");
    field("my-project", "demo");
    field("RegionOne", "RegionOne");
    selectAuth(1);
    field("https://swift.example/auth/v1.0", "https://swift.example/auth/v1.0");
    button("Save").click();

    expect(f.save).toHaveBeenCalledWith(
      expect.objectContaining({ authVersion: 1, projectName: "", region: "" }),
      "secret",
    );
  });

  it("opens an existing v3 bookmark with its identity shown", () => {
    show({
      id: "bm-k", displayName: "Saved Keystone", authUrl: "https://keystone.example/v3", username: "keystoneuser",
      storageUrlOverride: "", authVersion: 3, userDomain: "Default", projectName: "demo", projectDomain: "Default",
      region: "RegionOne", tlsMode: "system", caPem: "", hasCa: false, hasCredential: true,
    });
    expect(v3Fields().project).not.toBeNull();
    expect((v3Fields().project as HTMLInputElement).value).toBe("demo");
    expect((v3Fields().region as HTMLInputElement).value).toBe("RegionOne");
  });
});
