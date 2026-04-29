import type { DiffResult } from './types.js';

/**
 * Print a terminal report summarizing the crawl diff.
 */
export function printDiffReport(result: DiffResult): void {
  const line = '\u2500'.repeat(60);
  const delta = (oldVal: number, newVal: number): string => {
    const diff = newVal - oldVal;
    if (diff > 0) return ` (+${diff})`;
    if (diff < 0) return ` (${diff})`;
    return '';
  };

  console.log(`\n${line}`);
  console.log('  Micelio \u2014 Crawl Comparison Report');
  console.log(line);

  // Overview
  console.log(`\n  Old crawl: ${result.oldFile} (${result.oldCount} URLs)`);
  console.log(`  New crawl: ${result.newFile} (${result.newCount} URLs)`);
  if (result.urlMappingsApplied > 0) {
    console.log(`  URL mappings applied: ${result.urlMappingsApplied}`);
  }

  // Summary
  const totalChanges = result.addedUrls.length + result.removedUrls.length + result.changedUrls.length;
  console.log(`\n  Summary:`);
  console.log(`    Total URLs:  ${result.oldCount} \u2192 ${result.newCount}${delta(result.oldCount, result.newCount)}`);
  console.log(`    Added:       ${result.addedUrls.length}`);
  console.log(`    Removed:     ${result.removedUrls.length}`);
  console.log(`    Changed:     ${result.changedUrls.length}`);
  console.log(`    Unchanged:   ${result.unchangedCount}`);

  // Field change summary
  const fieldEntries = Object.entries(result.fieldSummary).sort(([, a], [, b]) => b - a);
  if (fieldEntries.length > 0) {
    console.log(`\n  Changes by field:`);
    for (const [field, count] of fieldEntries) {
      console.log(`    ${field}: ${count} changes`);
    }
  }

  // Added URLs
  if (result.addedUrls.length > 0) {
    console.log(`\n  Added URLs (${result.addedUrls.length}):`);
    for (const url of result.addedUrls.slice(0, 20)) {
      console.log(`    + ${url}`);
    }
    if (result.addedUrls.length > 20) {
      console.log(`    ... and ${result.addedUrls.length - 20} more`);
    }
  }

  // Removed URLs
  if (result.removedUrls.length > 0) {
    console.log(`\n  Removed URLs (${result.removedUrls.length}):`);
    for (const url of result.removedUrls.slice(0, 20)) {
      console.log(`    - ${url}`);
    }
    if (result.removedUrls.length > 20) {
      console.log(`    ... and ${result.removedUrls.length - 20} more`);
    }
  }

  // Changed URLs
  if (result.changedUrls.length > 0) {
    console.log(`\n  Changed URLs (${result.changedUrls.length}):`);
    for (const change of result.changedUrls.slice(0, 30)) {
      console.log(`    ~ ${change.url}`);
      for (const c of change.changes) {
        const oldStr = String(c.oldValue).substring(0, 60);
        const newStr = String(c.newValue).substring(0, 60);
        console.log(`      ${c.field}: "${oldStr}" \u2192 "${newStr}"`);
      }
    }
    if (result.changedUrls.length > 30) {
      console.log(`    ... and ${result.changedUrls.length - 30} more`);
    }
  }

  if (totalChanges === 0) {
    console.log(`\n  No differences found between the two crawls.`);
  }

  console.log(`\n${line}\n`);
}

/**
 * Generate an HTML comparison dashboard.
 */
export function generateDiffHtml(result: DiffResult): string {
  const resultJson = JSON.stringify(result).replace(/<\//g, '<\\/');

  return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Micelio Crawl Comparison</title>
<style>
:root { --bg: #0d1117; --bg2: #161b22; --fg: #c9d1d9; --border: #30363d; --green: #3fb950; --red: #f85149; --yellow: #d29922; --blue: #58a6ff; --radius: 6px; }
* { margin: 0; padding: 0; box-sizing: border-box; }
body { background: var(--bg); color: var(--fg); font-family: -apple-system, system-ui, 'Segoe UI', sans-serif; padding: 24px; line-height: 1.5; }
h1 { font-size: 1.5rem; margin-bottom: 16px; }
h2 { font-size: 1.1rem; margin: 24px 0 12px; color: var(--blue); }
.summary { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 12px; margin-bottom: 24px; }
.card { background: var(--bg2); border: 1px solid var(--border); border-radius: var(--radius); padding: 16px; text-align: center; }
.card .label { font-size: 0.8rem; color: #8b949e; text-transform: uppercase; }
.card .value { font-size: 1.8rem; font-weight: 700; }
.card .delta { font-size: 0.85rem; }
.delta-up { color: var(--green); }
.delta-down { color: var(--red); }
.delta-same { color: #8b949e; }
table { width: 100%; border-collapse: collapse; margin-top: 8px; font-size: 0.85rem; }
th { background: var(--bg2); padding: 8px 12px; text-align: left; border-bottom: 1px solid var(--border); position: sticky; top: 0; }
td { padding: 6px 12px; border-bottom: 1px solid var(--border); }
tr:hover { background: rgba(56,139,253,0.05); }
.url { color: var(--blue); word-break: break-all; }
.added { color: var(--green); }
.removed { color: var(--red); }
.changed { color: var(--yellow); }
.old-val { color: var(--red); text-decoration: line-through; }
.new-val { color: var(--green); }
.tabs { display: flex; gap: 4px; margin-bottom: 12px; }
.tab { padding: 8px 16px; border-radius: var(--radius) var(--radius) 0 0; cursor: pointer; border: 1px solid var(--border); background: var(--bg2); }
.tab.active { background: var(--bg); border-bottom-color: var(--bg); color: var(--blue); }
.tab-content { display: none; }
.tab-content.active { display: block; }
.field-summary { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 16px; }
.field-badge { background: var(--bg2); border: 1px solid var(--border); border-radius: var(--radius); padding: 4px 10px; font-size: 0.85rem; }
.field-badge .count { font-weight: 700; color: var(--yellow); }
</style>
</head>
<body>
<h1>Micelio Crawl Comparison</h1>
<div id="app"></div>
<script>
const R = ${resultJson};
const app = document.getElementById('app');

function esc(s) { const d = document.createElement('div'); d.textContent = String(s); return d.innerHTML; }

// Summary cards
const d = R.newCount - R.oldCount;
const deltaHtml = d > 0 ? '<span class="delta delta-up">+' + d + '</span>' : d < 0 ? '<span class="delta delta-down">' + d + '</span>' : '<span class="delta delta-same">no change</span>';

let html = '<div class="summary">';
html += '<div class="card"><div class="label">Old Crawl</div><div class="value">' + R.oldCount + '</div><div class="delta">' + esc(R.oldFile.split('/').pop()) + '</div></div>';
html += '<div class="card"><div class="label">New Crawl</div><div class="value">' + R.newCount + '</div><div class="delta">' + deltaHtml + '</div></div>';
html += '<div class="card"><div class="label">Added</div><div class="value added">' + R.addedUrls.length + '</div></div>';
html += '<div class="card"><div class="label">Removed</div><div class="value removed">' + R.removedUrls.length + '</div></div>';
html += '<div class="card"><div class="label">Changed</div><div class="value changed">' + R.changedUrls.length + '</div></div>';
html += '<div class="card"><div class="label">Unchanged</div><div class="value">' + R.unchangedCount + '</div></div>';
html += '</div>';

// Field summary
var fieldKeys = Object.keys(R.fieldSummary).sort(function(a, b) { return R.fieldSummary[b] - R.fieldSummary[a]; });
if (fieldKeys.length > 0) {
  html += '<h2>Changes by Field</h2><div class="field-summary">';
  for (var fi = 0; fi < fieldKeys.length; fi++) {
    html += '<div class="field-badge"><span class="count">' + R.fieldSummary[fieldKeys[fi]] + '</span> ' + esc(fieldKeys[fi]) + '</div>';
  }
  html += '</div>';
}

// Tabs
html += '<div class="tabs">';
html += '<div class="tab active" data-tab="changed">Changed (' + R.changedUrls.length + ')</div>';
html += '<div class="tab" data-tab="added">Added (' + R.addedUrls.length + ')</div>';
html += '<div class="tab" data-tab="removed">Removed (' + R.removedUrls.length + ')</div>';
html += '</div>';

// Changed tab
html += '<div id="tab-changed" class="tab-content active"><table><thead><tr><th>URL</th><th>Field</th><th>Old Value</th><th>New Value</th></tr></thead><tbody>';
for (var ci = 0; ci < Math.min(R.changedUrls.length, 500); ci++) {
  var c = R.changedUrls[ci];
  for (var cj = 0; cj < c.changes.length; cj++) {
    var ch = c.changes[cj];
    html += '<tr><td class="url">' + esc(c.url) + '</td><td>' + esc(ch.field) + '</td><td class="old-val">' + esc(String(ch.oldValue).substring(0, 100)) + '</td><td class="new-val">' + esc(String(ch.newValue).substring(0, 100)) + '</td></tr>';
  }
}
if (R.changedUrls.length > 500) html += '<tr><td colspan="4">... and ' + (R.changedUrls.length - 500) + ' more</td></tr>';
html += '</tbody></table></div>';

// Added tab
html += '<div id="tab-added" class="tab-content"><table><thead><tr><th>#</th><th>URL</th></tr></thead><tbody>';
for (var ai = 0; ai < Math.min(R.addedUrls.length, 500); ai++) {
  html += '<tr><td>' + (ai + 1) + '</td><td class="url added">' + esc(R.addedUrls[ai]) + '</td></tr>';
}
if (R.addedUrls.length > 500) html += '<tr><td colspan="2">... and ' + (R.addedUrls.length - 500) + ' more</td></tr>';
html += '</tbody></table></div>';

// Removed tab
html += '<div id="tab-removed" class="tab-content"><table><thead><tr><th>#</th><th>URL</th></tr></thead><tbody>';
for (var ri = 0; ri < Math.min(R.removedUrls.length, 500); ri++) {
  html += '<tr><td>' + (ri + 1) + '</td><td class="url removed">' + esc(R.removedUrls[ri]) + '</td></tr>';
}
if (R.removedUrls.length > 500) html += '<tr><td colspan="2">... and ' + (R.removedUrls.length - 500) + ' more</td></tr>';
html += '</tbody></table></div>';

app.innerHTML = html;

// Tab switching
document.querySelectorAll('.tab').forEach(function(tab) {
  tab.addEventListener('click', function() {
    document.querySelectorAll('.tab').forEach(function(t) { t.classList.remove('active'); });
    document.querySelectorAll('.tab-content').forEach(function(t) { t.classList.remove('active'); });
    tab.classList.add('active');
    document.getElementById('tab-' + tab.dataset.tab).classList.add('active');
  });
});
</script>
</body>
</html>`;
}
