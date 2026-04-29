import { readFileSync } from 'node:fs';
import type { PageData, DiffResult, DiffUrlChange, DiffFieldChange } from './types.js';

// Fields that can be compared between crawls
export const DIFF_FIELDS = [
  'statusCode',
  'title',
  'metaDescription',
  'canonical',
  'metaRobots',
  'h1',
  'wordCount',
  'indexable',
  'redirectChainLength',
] as const;

export type DiffField = typeof DIFF_FIELDS[number];

interface UrlMapping {
  pattern: RegExp;
  replacement: string;
}

/**
 * Parse a sed-like URL mapping: "s/pattern/replacement/"
 * Uses index-based extraction to handle delimiters in the replacement string.
 * Note: The pattern is treated as a regex. Use s|pattern|replacement| with
 * a delimiter that doesn't appear in your pattern/replacement to avoid issues.
 */
export function parseUrlMap(mapping: string): UrlMapping {
  // Support s/pattern/replacement/ or s|pattern|replacement|
  const delim = mapping[1];
  if (!mapping.startsWith('s') || !delim) {
    throw new Error(`Invalid URL mapping "${mapping}". Use sed syntax: s/old/new/`);
  }
  // Find the pattern: between 1st and 2nd delimiter
  const body = mapping.slice(2); // skip 's' + delim
  const secondDelim = body.indexOf(delim);
  if (secondDelim === -1) {
    throw new Error(`Invalid URL mapping "${mapping}". Use sed syntax: s/old/new/`);
  }
  const pattern = body.substring(0, secondDelim);
  // Replacement: everything between 2nd and optional 3rd delimiter (or end)
  const rest = body.substring(secondDelim + 1);
  // Strip trailing delimiter if present
  const replacement = rest.endsWith(delim) ? rest.slice(0, -1) : rest;
  if (!pattern) {
    throw new Error(`Invalid URL mapping "${mapping}". Pattern cannot be empty.`);
  }
  return {
    pattern: new RegExp(pattern, 'g'),
    replacement,
  };
}

/**
 * Apply URL mappings to transform old URLs for comparison.
 */
function applyMappings(url: string, mappings: UrlMapping[]): string {
  let result = url;
  for (const m of mappings) {
    result = result.replace(m.pattern, m.replacement);
  }
  return result;
}

/**
 * Read a JSONL file and return a map of URL -> PageData.
 */
function readCrawlFile(filePath: string): Map<string, PageData> {
  const content = readFileSync(filePath, 'utf-8');
  const map = new Map<string, PageData>();
  let lineNum = 0;
  let skipped = 0;
  for (const line of content.split('\n')) {
    lineNum++;
    const trimmed = line.trim();
    if (!trimmed) continue;
    try {
      const page: PageData = JSON.parse(trimmed);
      map.set(page.url, page);
    } catch {
      skipped++;
    }
  }
  if (skipped > 0) {
    process.stderr.write(`Warning: Skipped ${skipped} malformed line(s) in ${filePath}\n`);
  }
  return map;
}

/**
 * Extract a comparable value from a PageData for a given field.
 */
function getFieldValue(page: PageData, field: DiffField): string | number | boolean {
  switch (field) {
    case 'statusCode':
      return page.statusCode;
    case 'title':
      return page.title?.text || '';
    case 'metaDescription':
      return page.metaDescription?.text || '';
    case 'canonical':
      return page.canonical || '';
    case 'metaRobots':
      return page.metaRobots || '';
    case 'h1':
      return page.headings?.h1?.join(', ') || '';
    case 'wordCount':
      return page.wordCount ?? 0;
    case 'indexable':
      return page.indexability?.indexable ?? true;
    case 'redirectChainLength':
      return page.redirectChain?.length ?? 0;
    default:
      return '';
  }
}

/**
 * Compare two in-memory page arrays and produce a diff result.
 */
export function diffPages(
  oldPagesArray: PageData[],
  newPagesArray: PageData[],
  options: { fields?: DiffField[] } = {},
): DiffResult {
  const fields = options.fields?.length ? options.fields : [...DIFF_FIELDS];
  const oldMap = new Map<string, PageData>();
  for (const p of oldPagesArray) oldMap.set(p.url, p);
  const newMap = new Map<string, PageData>();
  for (const p of newPagesArray) newMap.set(p.url, p);

  const addedUrls: string[] = [];
  const removedUrls: string[] = [];
  const changedUrls: DiffUrlChange[] = [];
  const fieldSummary: Record<string, number> = {};

  for (const url of newMap.keys()) {
    if (!oldMap.has(url)) addedUrls.push(url);
  }
  for (const url of oldMap.keys()) {
    if (!newMap.has(url)) removedUrls.push(url);
  }
  for (const [url, newPage] of newMap) {
    const oldPage = oldMap.get(url);
    if (!oldPage) continue;
    const changes: DiffFieldChange[] = [];
    for (const field of fields) {
      const oldVal = getFieldValue(oldPage, field);
      const newVal = getFieldValue(newPage, field);
      if (String(oldVal) !== String(newVal)) {
        changes.push({ field, oldValue: oldVal, newValue: newVal });
        fieldSummary[field] = (fieldSummary[field] || 0) + 1;
      }
    }
    if (changes.length > 0) changedUrls.push({ url, changes });
  }

  addedUrls.sort();
  removedUrls.sort();
  changedUrls.sort((a, b) => a.url.localeCompare(b.url));

  return {
    oldFile: 'in-memory',
    newFile: 'in-memory',
    oldCount: oldMap.size,
    newCount: newMap.size,
    addedUrls,
    removedUrls,
    changedUrls,
    unchangedCount: newMap.size - addedUrls.length - changedUrls.length,
    urlMappingsApplied: 0,
    fieldSummary,
  };
}

/**
 * Compare two crawl JSONL files and produce a diff result.
 */
export function diffCrawls(
  oldFile: string,
  newFile: string,
  options: {
    urlMappings?: UrlMapping[];
    fields?: DiffField[];
  } = {},
): DiffResult {
  const oldPages = readCrawlFile(oldFile);
  const newPages = readCrawlFile(newFile);
  const mappings = options.urlMappings || [];
  const fields = options.fields || [...DIFF_FIELDS];

  // Build mapped old URL -> original URL mapping
  const mappedOldMap = new Map<string, PageData>();
  let mappingsApplied = 0;
  for (const [url, page] of oldPages) {
    const mapped = applyMappings(url, mappings);
    if (mapped !== url) mappingsApplied++;
    mappedOldMap.set(mapped, page);
  }

  // Find added, removed, and changed URLs
  const addedUrls: string[] = [];
  const removedUrls: string[] = [];
  const changedUrls: DiffUrlChange[] = [];
  const fieldSummary: Record<string, number> = {};

  // URLs in new but not in old = added
  for (const url of newPages.keys()) {
    if (!mappedOldMap.has(url)) {
      addedUrls.push(url);
    }
  }

  // URLs in old but not in new = removed
  for (const url of mappedOldMap.keys()) {
    if (!newPages.has(url)) {
      removedUrls.push(url);
    }
  }

  // URLs in both = check for changes
  for (const [url, newPage] of newPages) {
    const oldPage = mappedOldMap.get(url);
    if (!oldPage) continue;

    const changes: DiffFieldChange[] = [];
    for (const field of fields) {
      const oldVal = getFieldValue(oldPage, field);
      const newVal = getFieldValue(newPage, field);
      if (String(oldVal) !== String(newVal)) {
        changes.push({ field, oldValue: oldVal, newValue: newVal });
        fieldSummary[field] = (fieldSummary[field] || 0) + 1;
      }
    }

    if (changes.length > 0) {
      changedUrls.push({ url, changes });
    }
  }

  // Sort for consistent output
  addedUrls.sort();
  removedUrls.sort();
  changedUrls.sort((a, b) => a.url.localeCompare(b.url));

  const unchangedCount = newPages.size - addedUrls.length - changedUrls.length;

  return {
    oldFile,
    newFile,
    oldCount: oldPages.size,
    newCount: newPages.size,
    addedUrls,
    removedUrls,
    changedUrls,
    unchangedCount,
    urlMappingsApplied: mappingsApplied,
    fieldSummary,
  };
}
