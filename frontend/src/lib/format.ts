// Display formatting for sizes and counts. Sizes follow the mockup style:
// decimal units, three significant figures ("42.7 GB", "1.00 GB").

const SIZE_UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

/** "0 B", "512 B", "5.8 MB", "42.7 GB", "1.00 GB". */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) {
    return "—";
  }
  if (bytes < 1000) {
    return `${bytes} B`;
  }
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < SIZE_UNITS.length - 1) {
    value /= 1000;
    unit++;
  }
  const digits = value >= 100 ? 0 : value >= 10 ? 1 : 2;
  return `${value.toFixed(digits)} ${SIZE_UNITS[unit]}`;
}

/** "912", "18.7K", "1.2M" — compact object counts for container rows. */
export function formatCount(count: number): string {
  if (!Number.isFinite(count) || count < 0) {
    return "—";
  }
  if (count < 1000) {
    return String(count);
  }
  if (count < 1_000_000) {
    return `${trimTrailingZero((count / 1000).toFixed(1))}K`;
  }
  return `${trimTrailingZero((count / 1_000_000).toFixed(1))}M`;
}

function trimTrailingZero(s: string): string {
  return s.endsWith(".0") ? s.slice(0, -2) : s;
}
