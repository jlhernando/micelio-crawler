import { resolve, extname } from 'node:path';
import { existsSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import type { Page } from 'playwright';
import { getBrowser } from './browser.js';
import { formatError } from './utils.js';

export interface SnippetFn {
  (page: Page): Promise<Record<string, unknown>>;
}

export interface LoadedSnippet {
  name: string;
  fn: SnippetFn;
}

const SUPPORTED_EXTENSIONS = new Set(['.js', '.mjs', '.cjs']);

/**
 * Load snippet files. Each file must export a default async function
 * that receives a Playwright Page and returns a Record<string, unknown>.
 *
 * Supported formats:
 * - ESM: export default async (page) => { ... }
 * - CJS: module.exports = async (page) => { ... }
 */
export async function loadSnippets(paths: string[]): Promise<LoadedSnippet[]> {
  const snippets: LoadedSnippet[] = [];

  for (const snippetPath of paths) {
    const absolutePath = resolve(snippetPath);
    const name = snippetPath.replace(/\.(js|mjs|cjs)$/, '').replace(/[/\\]/g, '_');

    // S2 fix: Validate file exists and has supported extension
    const ext = extname(absolutePath);
    if (!SUPPORTED_EXTENSIONS.has(ext)) {
      throw new Error(`Unsupported snippet file extension "${ext}". Use .js, .mjs, or .cjs`);
    }
    if (!existsSync(absolutePath)) {
      throw new Error(`Snippet file not found: "${absolutePath}"`);
    }

    try {
      const fileUrl = pathToFileURL(absolutePath).href;
      const mod = await import(fileUrl);
      const fn = mod.default || mod;

      if (typeof fn !== 'function') {
        throw new Error(`Snippet must export a function, got ${typeof fn}`);
      }

      snippets.push({ name, fn });
    } catch (err) {
      throw new Error(
        `Failed to load snippet "${snippetPath}": ${formatError(err)}`,
      );
    }
  }

  return snippets;
}

/**
 * Run all loaded snippets against a URL using Playwright.
 * Returns merged results from all snippets.
 */
export async function runSnippets(
  url: string,
  snippets: LoadedSnippet[],
  userAgent: string,
): Promise<Record<string, unknown>> {
  if (snippets.length === 0) return {};

  const browser = await getBrowser();
  const context = await browser.newContext({ userAgent });
  const page = await context.newPage();

  try {
    await page.goto(url, { waitUntil: 'networkidle', timeout: 30000 });

    const results: Record<string, unknown> = {};

    for (const snippet of snippets) {
      try {
        // B2 fix: Clear timeout timer to prevent leaks and unhandled rejections
        let timer: ReturnType<typeof setTimeout>;
        const result = await Promise.race([
          snippet.fn(page).finally(() => clearTimeout(timer)),
          new Promise<never>((_, reject) => {
            const SNIPPET_TIMEOUT_MS = 10_000;
            timer = setTimeout(() => reject(new Error(`Snippet timeout (${SNIPPET_TIMEOUT_MS / 1000}s)`)), SNIPPET_TIMEOUT_MS);
          }),
        ]);
        // Merge snippet results under the snippet name
        // E3 fix: Store primitive values too, not just objects
        results[snippet.name] = result ?? null;
      } catch (err) {
        results[snippet.name] = {
          error: formatError(err),
        };
      }
    }

    return results;
  } finally {
    await context.close();
  }
}
