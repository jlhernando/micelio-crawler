/**
 * Theme colours for code that needs real colour strings (Chart.js, canvas,
 * WebGL, SVG attributes). They mirror the CSS variables in app.css; markup and
 * <style> blocks should use those variables (var(--color-success), text-success…)
 * instead. Change a colour in both places.
 */
export const C = {
  accent: '#aabddd',
  success: '#73d9ac',
  warning: '#e5b66e',
  danger: '#f58f98',
  blue: '#75a6ff',
  purple: '#b09aff',
  orange: '#f97316',
  surface: '#101112',
  surface2: '#171819',
  surface3: '#232527',
  border: '#303235',
  fg: '#f2f2f2',
  fg1: '#d2d4d7',
  fg2: '#a1a5ad',
} as const;

/** Status-class colours (charts and legends). */
export const STATUS_GROUP_HEX: Record<string, string> = {
  '2xx': C.success,
  '3xx': C.warning,
  '4xx': C.orange,
  '5xx': C.danger,
};

/** Categorical palette for link-map clusters (Universe UI and its WebGL engine). */
export const GRAPH_PALETTE = [C.blue, C.warning, C.success, '#f2a3c7', C.purple, '#7fd3e6', '#c9b48a', C.accent];
/** Metric colour ramp: low, mid, high. */
export const GRAPH_RAMP = ['#4a5468', C.blue, '#f2c46d'];
/** Colour of inbound links / sources in the link map. */
export const GRAPH_IN_COLOR = C.success;
