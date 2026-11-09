// CORS settings as a typed section over the three container metadata rows
// the proxy reads (ADR 0101 decision 7). As with the presets (ADR 0090), a
// field is read from its row and writing it rewrites only that row, so the
// structured and the raw view of one value never disagree. A row the
// operator did not touch is submitted verbatim whatever it holds; the shape
// checks apply to a field the operator changed, and the raw rows stay the
// way to write a value the checks would refuse (§19.2).

import type { MetaRow } from "./metadataDiff";

export const corsKeys = {
  allowOrigin: "Access-Control-Allow-Origin",
  maxAge: "Access-Control-Max-Age",
  exposeHeaders: "Access-Control-Expose-Headers",
} as const;

export type CorsField = keyof typeof corsKeys;

export const corsFields: readonly CorsField[] = ["allowOrigin", "maxAge", "exposeHeaders"];

function rowIndex(rows: MetaRow[], field: CorsField): number {
  const key = corsKeys[field].toLowerCase();
  return rows.findIndex((row) => row.key.toLowerCase() === key);
}

/** The field's row value, "" when the row is absent. */
export function corsValue(rows: MetaRow[], field: CorsField): string {
  const i = rowIndex(rows, field);
  return i === -1 ? "" : rows[i].value;
}

/** Rewrite one field's row and nothing else: a non-empty value sets or
 * replaces the row where it stands, an empty value removes it, since Swift
 * keeps no empty metadata value. */
export function withCorsValue(rows: MetaRow[], field: CorsField, value: string): MetaRow[] {
  const i = rowIndex(rows, field);
  if (value === "") {
    return i === -1 ? rows : rows.filter((_, index) => index !== i);
  }
  if (i === -1) return [...rows, { key: corsKeys[field], value }];
  return rows.map((row, index) => (index === i ? { ...row, value } : row));
}

/** The space-separated form the proxy reads: tokens joined by one space,
 * which also keeps a typed value inside the literal-header rule. */
export function normaliseCorsValue(value: string): string {
  return value.trim().split(/\s+/).filter((token) => token !== "").join(" ");
}

// scheme://host[:port] and nothing else: the host is a name or a bracketed
// IPv6 literal, so user information, a path, a query or a fragment is not an
// origin. A value the proxy would still match can be written as a raw row.
const originToken = /^[a-z][a-z0-9+.-]*:\/\/(?:\[[0-9a-f:.]+\]|[a-z0-9.-]+)(?::\d+)?$/i;
const headerToken = /^[!#$%&'*+.^_`|~0-9a-z-]+$/i;

/** Why a changed field's value is not the shape the proxy expects, or null:
 * origins are `*`, `null` or `scheme://host[:port]` separated by spaces, the
 * maximum age is a whole number of seconds, exposed headers are header names
 * separated by spaces. */
export function corsProblem(field: CorsField, value: string): string | null {
  const tokens = normaliseCorsValue(value).split(" ").filter((token) => token !== "");
  switch (field) {
    case "allowOrigin":
      for (const token of tokens) {
        if (token !== "*" && token !== "null" && !originToken.test(token)) {
          return `allowed origin "${token}" is not *, null or scheme://host[:port]`;
        }
      }
      return null;
    case "maxAge":
      return tokens.length <= 1 && tokens.every((token) => /^\d+$/.test(token))
        ? null
        : "the preflight cache must be a whole number of seconds";
    case "exposeHeaders":
      for (const token of tokens) {
        if (!headerToken.test(token)) return `exposed header "${token}" is not a header name`;
      }
      return null;
  }
}
