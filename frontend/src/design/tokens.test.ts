import { afterEach, describe, expect, it, vi } from "vitest";
import { getTheme, resolvedTheme, setTheme } from "./tokens";

function stubSystemPreference(dark: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: dark && query.includes("dark"),
    media: query,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
}

afterEach(() => {
  document.documentElement.removeAttribute("data-theme");
  vi.unstubAllGlobals();
});

describe("theme choice", () => {
  it("defaults to system with no attribute", () => {
    expect(getTheme()).toBe("system");
  });

  it("round-trips explicit choices through the attribute", () => {
    setTheme("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(getTheme()).toBe("dark");

    setTheme("light");
    expect(getTheme()).toBe("light");
  });

  it("system choice removes the attribute", () => {
    setTheme("dark");
    setTheme("system");
    expect(document.documentElement.hasAttribute("data-theme")).toBe(false);
    expect(getTheme()).toBe("system");
  });

  it("treats an unknown attribute value as system", () => {
    document.documentElement.setAttribute("data-theme", "mauve");
    expect(getTheme()).toBe("system");
  });
});

describe("resolvedTheme", () => {
  it("prefers an explicit choice over the OS", () => {
    stubSystemPreference(true);
    setTheme("light");
    expect(resolvedTheme()).toBe("light");
  });

  it("follows the OS when the choice is system", () => {
    stubSystemPreference(true);
    expect(resolvedTheme()).toBe("dark");
    stubSystemPreference(false);
    expect(resolvedTheme()).toBe("light");
  });
});
