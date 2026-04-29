import type * as cheerio from 'cheerio';
import type { CustomExtractionRule, CustomSearchRule } from './types.js';

/**
 * Run custom CSS extractions against the page using existing cheerio instance.
 * Q2 fix: Accept $ instead of raw HTML to avoid double cheerio.load().
 */
export function runCustomExtractions(
  $: cheerio.CheerioAPI,
  rules: CustomExtractionRule[],
): Record<string, string[]> {
  if (rules.length === 0) return {};

  const results: Record<string, string[]> = {};

  for (const rule of rules) {
    const values: string[] = [];
    try {
      $(rule.selector).each((_, el) => {
        const text = $(el).text().trim();
        if (text) values.push(text);
      });
    } catch {
      // Invalid selector — return empty
    }
    results[rule.name] = values;
  }

  return results;
}

/**
 * Run custom search patterns against the page HTML source.
 */
export function runCustomSearches(
  html: string,
  rules: CustomSearchRule[],
): Record<string, boolean> {
  if (rules.length === 0) return {};

  const results: Record<string, boolean> = {};

  for (const rule of rules) {
    try {
      if (rule.isRegex) {
        // S1 fix: Test regex on a truncated sample to limit ReDoS exposure
        const regex = new RegExp(rule.pattern);
        const REGEX_SAMPLE_LIMIT = 500_000;
        const sample = html.length > REGEX_SAMPLE_LIMIT ? html.slice(0, REGEX_SAMPLE_LIMIT) : html;
        results[rule.name] = regex.test(sample);
      } else {
        results[rule.name] = html.includes(rule.pattern);
      }
    } catch {
      // Invalid regex — treat as not found
      results[rule.name] = false;
    }
  }

  return results;
}

/**
 * Parse --extract flag value: "name:selector" or "name:css:selector"
 */
export function parseExtractionRule(value: string): CustomExtractionRule {
  const firstColon = value.indexOf(':');
  if (firstColon === -1) {
    throw new Error(`Invalid extraction format "${value}". Use "name:selector" or "name:css:selector"`);
  }

  const name = value.substring(0, firstColon);
  if (!name) {
    throw new Error(`Invalid extraction format "${value}". Name cannot be empty. Use "name:selector"`);
  }
  const rest = value.substring(firstColon + 1);

  // Check if second segment is a type specifier
  const secondColon = rest.indexOf(':');
  if (secondColon !== -1) {
    const maybeType = rest.substring(0, secondColon).toLowerCase();
    if (maybeType === 'css') {
      return { name, type: 'css', selector: rest.substring(secondColon + 1) };
    }
  }

  // Default to CSS
  return { name, type: 'css', selector: rest };
}

/**
 * Parse --search flag value: "pattern" or "/regex/"
 */
export function parseSearchRule(value: string): CustomSearchRule {
  if (value.startsWith('/') && value.endsWith('/') && value.length > 2) {
    const pattern = value.slice(1, -1);
    // Validate regex
    new RegExp(pattern);
    return { name: pattern, pattern, isRegex: true };
  }
  return { name: value, pattern: value, isRegex: false };
}
