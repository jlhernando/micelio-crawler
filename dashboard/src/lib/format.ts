/**
 * Shared display formatters. Pages used to carry their own copies with small
 * differences; keep one version here so numbers read the same everywhere.
 */

const EMPTY = '—';

/** Bytes as "512 B", "12.3 KB", "4.5 MB", "1.25 GB". Zero or negative → "0 B". */
export function formatBytes(n: number | null | undefined): string {
  if (!n || n <= 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1073741824) return `${(n / 1048576).toFixed(1)} MB`;
  return `${(n / 1073741824).toFixed(2)} GB`;
}

/**
 * Durations: "450ms", "42s", "3m 5s", "2h 7m", "1d 3h 7m".
 * null/undefined/NaN → "—".
 */
export function formatDuration(ms: number | null | undefined): string {
  if (ms == null || !Number.isFinite(ms)) return EMPTY;
  if (ms > 0 && ms < 1000) return `${Math.round(ms)}ms`;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h ${m % 60}m`;
}

/** Short response-time style: "850ms", "1.2s". */
export function formatMs(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function toDate(v: string | null | undefined): Date | null {
  if (!v) return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** Date only: "24 Oct 2026". Empty or invalid → "—". */
export function formatDate(v: string | null | undefined): string {
  const d = toDate(v);
  return d ? d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' }) : EMPTY;
}

/** Date and time: "24 Oct 2026 14:05". Empty or invalid → "—". */
export function formatDateTime(v: string | null | undefined): string {
  const d = toDate(v);
  if (!d) return EMPTY;
  return d.toLocaleDateString('en-GB', { day: '2-digit', month: 'short', year: 'numeric' }) +
    ' ' + d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });
}

/** Relative age: "just now", "5m ago", "3h ago", "2d ago". */
export function timeAgo(v: string): string {
  const mins = Math.floor((Date.now() - new Date(v).getTime()) / 60000);
  if (mins < 1) return 'just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}

/** Share of n in total as a percentage: pct(1, 3) → "33.3%". Zero total → "0%". */
export function pct(n: number, total: number, digits = 1): string {
  if (!(total > 0)) return '0%';
  const v = (n / total) * 100;
  return (digits === 0 ? String(Math.round(v)) : v.toFixed(digits)) + '%';
}

/** Thousands-separated integer: 12345 → "12,345". */
export function formatNumber(n: number): string {
  return n.toLocaleString();
}

/** Compact count: 950 → "950", 1200 → "1.2K", 45000 → "45K", 2.5e6 → "2.5M". */
export function formatCompact(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M';
  if (n >= 10_000) return (n / 1_000).toFixed(0) + 'K';
  if (n >= 1_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'K';
  return n.toString();
}
