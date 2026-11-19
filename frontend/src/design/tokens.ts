// Theme control for the §4 token system (tokens.css). An explicit choice
// lives for the window's lifetime; keeping it would belong in the settings
// file (ADR 0109), not the database.

/** An explicit theme choice, or "system" to follow the OS. */
export type Theme = "light" | "dark" | "system";

const THEME_ATTR = "data-theme";

/** The current choice: the attribute if set, otherwise "system". */
export function getTheme(): Theme {
  const value = document.documentElement.getAttribute(THEME_ATTR);
  return value === "light" || value === "dark" ? value : "system";
}

/**
 * Apply a theme choice. "system" removes the attribute so the
 * prefers-color-scheme blocks in tokens.css take over.
 */
export function setTheme(theme: Theme): void {
  if (theme === "system") {
    document.documentElement.removeAttribute(THEME_ATTR);
  } else {
    document.documentElement.setAttribute(THEME_ATTR, theme);
  }
}

/**
 * The theme actually rendered right now: an explicit choice wins,
 * otherwise the OS preference.
 */
export function resolvedTheme(): "light" | "dark" {
  const theme = getTheme();
  if (theme !== "system") {
    return theme;
  }
  return matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}
