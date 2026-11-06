// CSV built in the browser (log analysis exports). Log paths, user agents
// and referrers come from whoever sent the requests, so a cell starting
// with = + - @ (or a tab or carriage return) would run as a spreadsheet
// formula. Same rule as the Go exports (internal/csvsafe): such a cell
// gets a leading apostrophe; plain numbers like -3 stay numbers.

const NUMBER = /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/;
const TRIGGER = /^ *[=+\-@\t\r\n＝＋－＠]/;

export function csvCell(v: string | number | null | undefined): string {
  if (v == null) return '';
  let s = String(v);
  if (typeof v === 'string' && TRIGGER.test(s) && !NUMBER.test(s)) s = "'" + s;
  return /[",\r\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
}

export function toCSV(headers: string[], rows: (string | number | null | undefined)[][]): string {
  return [headers, ...rows].map(r => r.map(csvCell).join(',')).join('\r\n') + '\r\n';
}
