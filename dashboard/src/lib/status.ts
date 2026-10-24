/**
 * HTTP status colour classes. Two styles exist on purpose: plain text (tables)
 * and a tinted badge (monitor feed, log tables). Both use the same hue per
 * status class: 2xx green, 3xx yellow, 4xx orange, 5xx red.
 */

/** Text colour for a status code in tables. */
export function statusTextClass(code: number): string {
  if (code >= 200 && code < 300) return 'text-success';
  if (code >= 300 && code < 400) return 'text-warning';
  if (code >= 400 && code < 500) return 'text-orange-500';
  if (code >= 500) return 'text-danger';
  return 'text-fg-2';
}

/** Tinted badge (background + text) for a status code. */
export function statusBadgeClass(code: number): string {
  if (code >= 200 && code < 300) return 'bg-green-500/20 text-green-400';
  if (code >= 300 && code < 400) return 'bg-yellow-500/20 text-yellow-400';
  if (code >= 400 && code < 500) return 'bg-orange-500/20 text-orange-400';
  if (code >= 500) return 'bg-red-500/20 text-red-400';
  return 'bg-surface-3 text-fg-2';
}
