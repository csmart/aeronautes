import { describe, expect, it } from "vitest";
import tokenCSS from "./tokens.css?raw";
import appCSS from "../app.css?raw";

type Declarations = Record<string, string>;
type Rule = { selector: string; declarations: Declarations };

function rules(css: string): Rule[] {
  return [...css.replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .map(([, selector, body]) => ({
      selector: selector.trim().replace(/\s+/g, " "),
      declarations: Object.fromEntries(body.split(";").filter((part) => part.includes(":"))
        .map((part) => {
          const colon = part.indexOf(":");
          return [part.slice(0, colon).trim(), part.slice(colon + 1).trim()];
        })),
    }));
}

const components = import.meta.glob<string>("../features/**/*.svelte", {
  query: "?raw", import: "default", eager: true,
});
const styles: Record<string, Rule[]> = { "app.css": rules(appCSS) };
for (const [path, source] of Object.entries(components)) {
  styles[path.split("/").at(-1)!] = rules(source.match(/<style>([\s\S]*?)<\/style>/)?.[1] ?? "");
}

function declarations(source: Rule[], selector: string): Declarations {
  const matched = source.filter((rule) => rule.selector.split(",").map((s) => s.trim()).includes(selector));
  expect(matched, `missing production selector ${selector}`).not.toHaveLength(0);
  return Object.assign({}, ...matched.map((rule) => rule.declarations));
}

function style(file: string, selector: string): Declarations {
  expect(styles, `missing production component ${file}`).toHaveProperty(file);
  return declarations(styles[file], selector);
}

function luminance(hex: string): number {
  expect(hex).toMatch(/^#[0-9a-f]{6}$/i);
  const channels = [1, 3, 5].map((offset) => {
    const value = parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
}

function contrast(a: string, b: string): number {
  const first = luminance(a);
  const second = luminance(b);
  return (Math.max(first, second) + 0.05) / (Math.min(first, second) + 0.05);
}

function colour(value: string, tokens: Declarations): string {
  const token = /^var\((--color-[a-z-]+)\)$/.exec(value)?.[1];
  expect(token, `expected an opaque production colour token, got ${value}`).toBeDefined();
  return tokens[token!];
}

type Pair = [name: string, file: string, foreground: string, background: string, backgroundFile?: string];

// Background owners come from the component markup, including transparent
// controls inside a differently coloured parent. These are not all token pairs.
const pairs: Pair[] = [
  ["body text", "app.css", "body", "body"],
  ["pane text", "app.css", "body", ".pane", "WorkingList.svelte"],
  ["input text", "WorkingList.svelte", ".field input", ".field input"],
  ["selected row text", "app.css", "body", ".entry.selected", "WorkingList.svelte"],
  ["rail captions", "BookmarkRail.svelte", ".bookmark", ".rail"],
  ["working-list sizes", "WorkingList.svelte", ".size", ".pane"],
  ["hovered sizes", "WorkingList.svelte", ".size", ".entry:hover"],
  ["selected sizes", "WorkingList.svelte", ".size", ".entry.selected"],
  ["status footer", "BrowserScreen.svelte", ".status", ".status"],
  ["container details", "ContainerColumn.svelte", ".container-detail", ".pane"],
  ["hovered container details", "ContainerColumn.svelte", ".container-detail", ".container-row:hover"],
  ["selected container details", "ContainerColumn.svelte", ".container-detail", ".container-row.selected"],
  ["planning badge", "JobsDrawer.svelte", ".badge.planning", ".badge.planning"],
  ["job item action", "JobsDrawer.svelte", ".more", ".drawer"],
  ["inspector resource kind", "Inspector.svelte", ".subtitle", ".pane"],
  ["load more", "ContainerColumn.svelte", ".load-more", ".pane"],
  ["inspector configure action", "Inspector.svelte", ".edit-btn", ".edit-btn"],
  ["create primary label", "CreateContainerDialog.svelte", ".primary", ".primary"],
  ["transfer primary label", "TransferDialog.svelte", "footer .primary", "footer .primary"],
  ["local transfer primary label", "LocalTransferDialog.svelte", "footer .primary", "footer .primary"],
  ["manifest force label", "ManifestDeleteDialog.svelte", ".danger", ".danger"],
  ["object delete label", "ObjectDeleteDialog.svelte", ".danger", ".danger"],
  ["selected delete label", "SelectedDeleteDialog.svelte", ".danger", ".danger"],
  ["delete label", "DeleteDialog.svelte", "footer .danger", "footer .danger"],
  ["editor warning", "MetadataEditor.svelte", ".warn", ".card", "ObjectEditPanel.svelte"],
  ["dialog warning", "TransferDialog.svelte", ".warn", ".dialog"],
  ["dialog success", "TransferDialog.svelte", ".started", ".dialog"],
  ["editor success", "BookmarkEditor.svelte", ".verified", ".verified"],
  ["inspector error", "Inspector.svelte", ".error", ".pane"],
  ["editor error", "MetadataEditor.svelte", ".dup", ".card", "ObjectEditPanel.svelte"],
  ["dialog error", "TransferDialog.svelte", ".error", ".dialog"],
  ["ACL remove", "AclEditor.svelte", ".x", ".chip"],
  ["hovered ACL remove", "AclEditor.svelte", ".x:hover", ".chip"],
  ["selected container mode", "ContainerColumn.svelte", '.mode[aria-selected="true"]', '.mode[aria-selected="true"]'],
  ["add bookmark glyph", "BookmarkRail.svelte", ".badge.plus", ".rail"],
  ["hovered add bookmark glyph", "BookmarkRail.svelte", ".badge.plus", ".bookmark:hover"],
  ["insecure bookmark warning", "BookmarkRail.svelte", ".insecure-flag", ".rail"],
  ["hovered insecure bookmark warning", "BookmarkRail.svelte", ".insecure-flag", ".bookmark:hover"],
  ["selected insecure bookmark warning", "BookmarkRail.svelte", ".insecure-flag", ".bookmark.active"],
  ["object conflict force action", "ObjectEditPanel.svelte", "button.danger", ".banner.conflict"],
  ["container conflict force action", "ContainerEditPanel.svelte", "button.danger", ".banner.conflict"],
  ["object reload action", "ObjectEditPanel.svelte", "button", "button"],
  ["container reload action", "ContainerEditPanel.svelte", "button", "button"],
];

// Every current inset or semantic soft-fill rule has an explicit text owner.
// A new surface must be inspected rather than silently escaping the inventory.
const softSurfaces: [file: string, selector: string, foreground?: string][] = [
  ["BookmarkEditor.svelte", ".error"],
  ["BookmarkEditor.svelte", ".verified"],
  ["BookmarkEditor.svelte", ".warning"],
  ["BookmarkRail.svelte", ".badge"],
  ["ContainerColumn.svelte", ".modes", ".mode"],
  ["BrowserScreen.svelte", ".unproven-banner"],
  ["ContainerColumn.svelte", ".class-chip"],
  ["CreateContainerDialog.svelte", ".error"],
  ["WorkingList.svelte", ".backstage-banner"],
  ["WorkingList.svelte", ".badge"],
  ["AclEditor.svelte", ".chip"],
  ["ContainerColumn.svelte", ".setting-badge.danger"],
  ["ContainerColumn.svelte", ".setting-badge.warning"],
  ["WorkingList.svelte", ".public-banner"],
  ["ContainerEditPanel.svelte", ".banner.conflict"],
  ["ContainerEditPanel.svelte", ".banner.mismatch"],
  ["Inspector.svelte", ".raw"],
  ["ManifestDeleteDialog.svelte", ".keeps.warn"],
  ["ManifestDeleteDialog.svelte", ".error"],
  ["ObjectDeleteDialog.svelte", ".keeps.warn"],
  ["ObjectDeleteDialog.svelte", ".error"],
  ["SelectedDeleteDialog.svelte", ".keeps.warn"],
  ["SelectedDeleteDialog.svelte", ".error"],
  ["MetadataEditor.svelte", ".chip.added"],
  ["MetadataEditor.svelte", ".chip.changed"],
  ["MetadataEditor.svelte", ".chip.removed"],
  ["ObjectEditPanel.svelte", ".banner.conflict"],
  ["ObjectEditPanel.svelte", ".banner.mismatch"],
  ["JobsDrawer.svelte", ".badge.applied, .badge.complete"],
  ["JobsDrawer.svelte", ".badge.conflict, .badge.verify_mismatch, .badge.partial, .badge.cancelled"],
  ["JobsDrawer.svelte", ".badge.failed"],
  ["BrowserScreen.svelte", ".interrupted-banner"],
  ["JobsDrawer.svelte", ".interrupted", ".interrupted-head"],
];

describe("normal-text contrast arithmetic", () => {
  it("has independent extreme, equal and symmetric controls", () => {
    expect(contrast("#000000", "#ffffff")).toBe(21);
    expect(contrast("#123456", "#123456")).toBe(1);
    expect(contrast("#123456", "#abcdef")).toBe(contrast("#abcdef", "#123456"));
  });

  it("does not round a sub-4.5 ratio into a pass", () => {
    expect(contrast("#777777", "#ffffff")).toBeLessThan(4.5);
    expect(contrast("#767676", "#ffffff")).toBeGreaterThan(4.5);
  });
});

describe("production theme and surface inventory", () => {
  it("keeps every automatic and explicit dark declaration equal", () => {
    const parsed = rules(tokenCSS);
    expect(declarations(parsed, ':root:not([data-theme="light"])'))
      .toEqual(declarations(parsed, ':root[data-theme="dark"]'));
    expect(declarations(parsed, ":root")["color-scheme"]).toBe("light");
    expect(declarations(parsed, ':root[data-theme="dark"]')["color-scheme"]).toBe("dark");
  });

  it("covers all current inset and semantic soft-fill rules", () => {
    const actual = Object.entries(styles).flatMap(([file, source]) => source
      .filter((rule) => /^var\(--color-(?:bg-inset|[a-z]+-soft)\)$/.test(rule.declarations.background))
      .map((rule) => `${file} ${rule.selector}`));
    expect(actual.sort()).toEqual(softSurfaces.map(([file, selector]) => `${file} ${selector}`).sort());
  });

  it.each([
    ["AclEditor.svelte", ".x"],
    ["ContainerColumn.svelte", ".mode"],
    ["BookmarkRail.svelte", ".badge.plus"],
    ["ObjectEditPanel.svelte", "button.danger"],
    ["ContainerEditPanel.svelte", "button.danger"],
  ])("retains the checked transparent override in %s %s", (file, selector) => {
    expect(style(file, selector).background).toMatch(/^(none|transparent)$/);
  });

  it.each(["input::placeholder", "textarea::placeholder"])("gives %s an opaque authored colour", (selector) => {
    const placeholder = style("app.css", selector);
    expect(placeholder.color).toBe("var(--color-text-faint)");
    expect(placeholder.opacity).toBe("1");
    expect(placeholder).not.toHaveProperty("appearance");
    expect(placeholder).not.toHaveProperty("forced-color-adjust");
  });
});

for (const [theme, selector] of [["light", ":root"], ["dark", ':root[data-theme="dark"]']]) {
  describe(`${theme} production normal text is at least 4.5:1`, () => {
    it.each(pairs)("%s", (_name, file, foreground, background, backgroundFile) => {
      const tokens = declarations(rules(tokenCSS), selector);
      const fg = colour(style(file, foreground).color, tokens);
      const bg = colour(style(backgroundFile ?? file, background).background, tokens);
      expect(contrast(fg, bg), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
    });

    it.each(softSurfaces)("%s %s text", (file, background, foreground) => {
      const tokens = declarations(rules(tokenCSS), selector);
      const fg = colour(style(file, foreground ?? background.split(",")[0]).color, tokens);
      const bg = colour(style(file, background.split(",")[0]).background, tokens);
      expect(contrast(fg, bg), `${fg} on ${bg}`).toBeGreaterThanOrEqual(4.5);
    });

    it.each(["--color-bg-card", "--color-bg-pane"])("placeholder text on %s", (background) => {
      const tokens = declarations(rules(tokenCSS), selector);
      const fg = colour(style("app.css", "input::placeholder").color, tokens);
      expect(contrast(fg, tokens[background])).toBeGreaterThanOrEqual(4.5);
    });
  });
}
