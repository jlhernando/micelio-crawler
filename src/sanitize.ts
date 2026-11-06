/**
 * Output sanitizers for values that come from crawled sites (titles, URLs,
 * JSON-LD, headings...). Crawled content is untrusted: it must not run as a
 * spreadsheet formula or drive the operator's terminal.
 *
 * Machine-readable outputs (JSONL, SQLite) keep the raw value; these helpers
 * are applied only where a human tool interprets the text.
 */

// ── CSV formula injection ────────────────────────────────

/** Characters that make Excel / LibreOffice / Sheets treat a cell as a formula. */
const FORMULA_START = /^[=+\-@\t\r\n]/;
/** Plain numbers such as "-3", "+1.5" or "-12.5%" are data, not formulas. */
const PLAIN_NUMBER = /^[+-]?(\d+(\.\d*)?|\.\d+)(e[+-]?\d+)?%?$/i;

/**
 * Make one CSV cell safe to open in a spreadsheet. Strings that start with
 * = + - @ tab, CR or LF (also after leading spaces) get a leading apostrophe
 * so the app shows them as text. Numbers, booleans and plain numeric
 * strings are returned unchanged.
 */
export function csvSafeCell<T>(value: T): T | string {
  if (typeof value !== 'string' || value === '') return value;
  const trimmed = value.replace(/^ +/, '');
  if (!FORMULA_START.test(trimmed)) return value;
  if (PLAIN_NUMBER.test(trimmed)) return value;
  return `'${value}`;
}

/** csvSafeCell on every value of a flat record. Keys are left as they are. */
export function csvSafeRecord<V>(record: Record<string, V>): Record<string, V | string> {
  const out: Record<string, V | string> = {};
  for (const [key, value] of Object.entries(record)) out[csvSafeCell(key) as string] = csvSafeCell(value);
  return out;
}

// ── Terminal control sequences ───────────────────────────

/* eslint-disable no-control-regex */
// OSC: ESC ] ... (BEL | ESC \ | end). Covers hyperlinks (OSC 8), titles, OSC 52 clipboard.
const OSC = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?/g;
// CSI: ESC [ params intermediates final. Covers colours, cursor moves, screen clears.
const CSI = /\x1b\[[0-?]*[ -/]*[@-~]?/g;
// Other 2-byte escapes (ESC P / ESC X / ESC ^ / ESC _ strings, ESC c reset, etc.).
const ESC_OTHER = /\x1b[\x20-\x7e]?/g;
// Line breaks and tabs become one space so a value cannot forge extra report lines.
const BREAKS = /[\t\n\v\f\r\u0085\u2028\u2029]+/g;
// Remaining C0, DEL, C1 (incl. 8-bit CSI \x9b / OSC \x9d) and bidi overrides/isolates.
const CONTROL = /[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u202a-\u202e\u2066-\u2069]/g;
/* eslint-enable no-control-regex */

/**
 * Remove escape sequences, control characters and bidi controls from a value
 * before it is printed to a terminal. Safe for any value (non-strings are
 * converted with String()).
 */
export function termSafe(value: unknown): string {
  const s = typeof value === 'string' ? value : String(value ?? '');
  // Fast path: nothing to strip (the common case for real titles/URLs).
  // eslint-disable-next-line no-control-regex
  if (!/[\x00-\x1f\x7f-\x9f\u061c\u200e\u200f\u2028\u2029\u202a-\u202e\u2066-\u2069]/.test(s)) return s;
  return s
    .replace(OSC, '')
    .replace(CSI, '')
    .replace(ESC_OTHER, '')
    .replace(BREAKS, ' ')
    .replace(CONTROL, '');
}

/**
 * Like termSafe, but keeps line feeds: for whole report lines that the code
 * itself splits with "\n". Escape sequences, CR, bidi and other controls go.
 */
export function termSafeLines(value: unknown): string {
  const s = typeof value === 'string' ? value : String(value ?? '');
  return s.split('\n').map(termSafe).join('\n');
}

/**
 * Drop-in for `console` inside report printers: every string argument is
 * passed through termSafeLines, so no crawled value can reach the terminal
 * with escape sequences even where a call site forgot termSafe. Shadow the
 * global with `const console = safeConsole;` at the top of the printer.
 */
export const safeConsole = {
  log: (...args: unknown[]): void => {
    globalThis.console.log(...args.map((a) => (typeof a === 'string' ? termSafeLines(a) : a)));
  },
  error: (...args: unknown[]): void => {
    globalThis.console.error(...args.map((a) => (typeof a === 'string' ? termSafeLines(a) : a)));
  },
  warn: (...args: unknown[]): void => {
    globalThis.console.warn(...args.map((a) => (typeof a === 'string' ? termSafeLines(a) : a)));
  },
};
