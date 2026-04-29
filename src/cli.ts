#!/usr/bin/env node

import { readFileSync, writeFileSync, accessSync, constants } from 'node:fs';
import { resolve, parse as parsePath, format as formatPath } from 'node:path';
import { execFile } from 'node:child_process';
import { Command } from 'commander';
import { mergeConfig } from './config.js';
import { crawl } from './orchestrator.js';
import { generateReport, printReport } from './reporter.js';
import { generateHtmlReport } from './html-report.js';
import { ResultWriter } from './writer.js';
import { parseExtractionRule, parseSearchRule } from './custom-extract.js';
import { formatError } from './utils.js';
import { diffCrawls, parseUrlMap, DIFF_FIELDS } from './diff.js';
import type { DiffField } from './diff.js';
import { printDiffReport, generateDiffHtml } from './diff-report.js';
import { runOAuthFlow, getAuthClient, getServiceAccountClient, deleteStoredToken } from './gsc-auth.js';
import { fetchGscData, mergeGscData, autoDetectProperty } from './gsc.js';
import { fetchGscFromBigQuery, estimateQueryCost } from './gsc-bq.js';
import { fetchGa4Data } from './ga4.js';
import { parseSegment, type Segment } from './segmentation.js';
import { runSchedule, listSchedules, type ScheduleConfig } from './scheduler.js';
import { parseCron, describeCron } from './cron-parser.js';
import { computeLinkIntelligence } from './link-intelligence.js';
import { generateSitemap, generateMultiSitemap, type SitemapOptions as SitemapGenOptions } from './sitemap-generator.js';
import { fetchCruxData } from './crux.js';
import { fetchPlausibleData } from './plausible.js';
import { testRobotsMultiAgent, KNOWN_USER_AGENTS } from './robots.js';
import { fetchHead, type FetchOptions as HeadFetchOptions } from './fetcher.js';
import { HeadResultWriter } from './head-writer.js';
import type { HeadResult } from './types.js';

// ── Typed CLI options ────────────────────────────────────

interface CommonOptions {
  concurrency: string;
  delay?: string;
  userAgent?: string;
  js: boolean;
  output: string;
  csv: boolean;
  header: Record<string, string>;
  cookie?: string;
  checkExternal: boolean;
  extract: ReturnType<typeof parseExtractionRule>[];
  search: ReturnType<typeof parseSearchRule>[];
  snippet: string[];
  psi: boolean;
  psiKey?: string;
  aiPrompt?: string;
  aiProvider?: string;
  aiModel?: string;
  aiKey?: string;
  html: boolean;
  open: boolean; // Commander negates --no-open → opts.open
  pageWeight: boolean;
  // Phase 11: GSC
  gsc: boolean;
  gscProperty?: string;
  gscDays: string;
  gscKeyFile?: string;
  gscBq?: string; // BigQuery dataset: project.dataset
  // Phase 11: GA4
  ga4: boolean;
  ga4Property?: string;
  ga4Days: string;
  ga4KeyFile?: string;
  // Phase 12: Segmentation
  segment: string[];
  // Phase 12: Database storage
  db?: string;
  resume: boolean;
  // Phase 12: Proxy
  proxy?: string;
  // Phase 13: N-Grams
  ngrams: boolean;
  // Phase 13: Embeddings
  embeddings: boolean;
  embeddingModel?: string;
  similarityThreshold: string;
  // Link Intelligence
  linkIntelligence: boolean;
  liMaxSuggestions: string;
  liNoCentrality: boolean;
  sitemapOut: boolean;
  crux: boolean;
  cruxKey?: string;
  cruxFormFactor?: string;
  plausible: boolean;
  plausibleSiteId?: string;
  plausibleApiKey?: string;
  plausibleDays: string;
  plausibleHost?: string;
  maxErrors: string;
  timeout: string;
  language?: string;
  // Robots.txt
  robots: boolean;  // Commander negates --no-robots → opts.robots
  showBlockedInternal: boolean;
}

interface SpiderOptions extends CommonOptions {
  depth: string;
  limit: string;
  include: string[];
  exclude: string[];
  allowedDomains: string;  // comma-separated string from CLI, split in handler
}

interface ListOptions extends CommonOptions {
  // List mode has no extra options beyond CommonOptions
}

interface SitemapOptions extends CommonOptions {
  limit: string;
}

// ── Validation helpers ───────────────────────────────────

const VALID_AI_PROVIDERS = ['openai', 'anthropic', 'ollama'] as const;

function parseIntStrict(value: string, name: string): number {
  const parsed = parseInt(value, 10);
  if (isNaN(parsed) || parsed < 0) {
    console.error(`Error: --${name} must be a non-negative integer, got "${value}"`);
    process.exit(1);
  }
  return parsed;
}

function parsePatterns(values: string[]): RegExp[] {
  return values.map((v) => {
    try {
      return new RegExp(v);
    } catch {
      console.error(`Error: Invalid regex pattern "${v}"`);
      process.exit(1);
    }
  });
}

function resolveFilePath(filePath: string): string {
  const resolved = resolve(filePath);
  if (resolved.includes('\0')) {
    console.error(`Error: Invalid file path "${filePath}"`);
    process.exit(1);
  }
  return resolved;
}

function collect(value: string, previous: string[]): string[] {
  return previous.concat([value]);
}

function collectHeaders(value: string, previous: Record<string, string>): Record<string, string> {
  const idx = value.indexOf(':');
  if (idx === -1) {
    console.error(`Error: Invalid header format "${value}". Use "Name: Value"`);
    process.exit(1);
  }
  previous[value.substring(0, idx).trim()] = value.substring(idx + 1).trim();
  return previous;
}

function collectExtractions(value: string, previous: ReturnType<typeof parseExtractionRule>[]) {
  try {
    return previous.concat([parseExtractionRule(value)]);
  } catch (err) {
    console.error(`Error: ${formatError(err)}`);
    process.exit(1);
  }
}

function collectSearches(value: string, previous: ReturnType<typeof parseSearchRule>[]) {
  try {
    return previous.concat([parseSearchRule(value)]);
  } catch (err) {
    console.error(`Error: Invalid search pattern "${value}": ${formatError(err)}`);
    process.exit(1);
  }
}

function resolveOutputPath(opts: CommonOptions): { outputPath: string; outputFormat: 'jsonl' | 'csv' } {
  const outputFormat = opts.csv ? 'csv' as const : 'jsonl' as const;
  let outputPath = opts.output;
  if (opts.csv && outputPath === 'output.jsonl') {
    outputPath = 'output.csv';
  }
  return { outputPath, outputFormat };
}

function validateAiProvider(provider: string | undefined): void {
  if (provider && !VALID_AI_PROVIDERS.includes(provider as typeof VALID_AI_PROVIDERS[number])) {
    console.error(`Error: --ai-provider must be one of: ${VALID_AI_PROVIDERS.join(', ')}. Got "${provider}"`);
    process.exit(1);
  }
}

// ── Shared config builder ────────────────────────────────

function buildCommonConfig(opts: CommonOptions): Record<string, unknown> {
  const { outputPath, outputFormat } = resolveOutputPath(opts);
  validateAiProvider(opts.aiProvider);

  // MINOR-5 fix: Validate early before building config
  if (opts.resume && !opts.db) {
    console.error('Error: --resume requires --db <path>');
    process.exit(1);
  }

  // Validate proxy URL format
  if (opts.proxy) {
    try {
      const proxyUrl = new URL(opts.proxy);
      const supportedProtocols = ['http:', 'https:', 'socks4:', 'socks5:'];
      if (!supportedProtocols.includes(proxyUrl.protocol)) {
        console.error(`Error: Unsupported proxy protocol "${proxyUrl.protocol}". Use http://, https://, socks4://, or socks5://`);
        process.exit(1);
      }
    } catch {
      console.error(`Error: Invalid proxy URL "${opts.proxy}". Format: http://host:port or socks5://host:port`);
      process.exit(1);
    }
  }

  const config: Record<string, unknown> = {
    concurrency: parseIntStrict(opts.concurrency, 'concurrency'),
    jsRendering: opts.js,
    outputPath,
    outputFormat,
    customHeaders: opts.header,
    cookies: opts.cookie || '',
    checkExternal: opts.checkExternal,
    customExtractions: opts.extract,
    customSearches: opts.search,
    snippetPaths: opts.snippet,
    psi: opts.psi,
    psiKey: opts.psiKey || '',
    aiPrompt: opts.aiPrompt || '',
    aiProvider: opts.aiProvider || '',
    aiModel: opts.aiModel || '',
    aiKey: opts.aiKey || '',
    htmlReport: opts.html,
    htmlOpen: opts.open !== false,
    pageWeight: opts.pageWeight,
    gsc: opts.gsc,
    gscProperty: opts.gscProperty || '',
    gscDays: Math.max(1, parseIntStrict(opts.gscDays, 'gsc-days')),
    gscKeyFile: opts.gscKeyFile || '',
    gscBqDataset: opts.gscBq || '',
    ga4: opts.ga4,
    ga4Property: opts.ga4Property || '',
    ga4Days: Math.max(1, parseIntStrict(opts.ga4Days, 'ga4-days')),
    ga4KeyFile: opts.ga4KeyFile || '',
    segmentRules: opts.segment.map(s => {
      const seg = parseSegment(s);
      return { name: seg.name, pattern: seg.pattern.source };
    }),
    dbPath: opts.db || '',
    resume: opts.resume,
    proxy: opts.proxy || '',
    ngrams: opts.ngrams,
    embeddings: opts.embeddings,
    embeddingModel: opts.embeddingModel || '',
    similarityThreshold: isNaN(parseFloat(opts.similarityThreshold)) ? 0.85 : parseFloat(opts.similarityThreshold),
    linkIntelligence: opts.linkIntelligence,
    liMaxSuggestions: isNaN(parseInt(opts.liMaxSuggestions, 10)) ? 50 : parseInt(opts.liMaxSuggestions, 10),
    liNoCentrality: opts.liNoCentrality || false,
    sitemapOut: opts.sitemapOut || false,
    crux: opts.crux || false,
    cruxKey: opts.cruxKey || '',
    cruxFormFactor: opts.cruxFormFactor || '',
    plausible: opts.plausible || false,
    plausibleSiteId: opts.plausibleSiteId || '',
    plausibleApiKey: opts.plausibleApiKey || '',
    plausibleDays: Math.max(1, parseIntStrict(opts.plausibleDays, 'plausible-days')),
    plausibleHost: opts.plausibleHost || 'https://plausible.io',
    maxErrors: parseIntStrict(opts.maxErrors, 'max-errors'),
    timeoutSeconds: parseIntStrict(opts.timeout, 'timeout'),
    language: opts.language || '',
    respectRobots: opts.robots !== false,
    showBlockedInternal: opts.showBlockedInternal || false,
  };
  if (opts.userAgent) config.userAgent = opts.userAgent;
  return config;
}

// ── Shared CLI options ───────────────────────────────────

function addCommonOptions(cmd: Command): Command {
  return cmd
    .option('-c, --concurrency <number>', 'Concurrent workers (1-20)', '3')
    .option('--delay <number>', 'Delay between requests in ms (overrides robots.txt Crawl-Delay)')
    .option('--user-agent <string>', 'Custom User-Agent string')
    .option('--js', 'Enable JavaScript rendering (slower, requires Playwright)', false)
    .option('-o, --output <path>', 'Output file path', 'output.jsonl')
    .option('--csv', 'Output as CSV instead of JSONL', false)
    .option('-H, --header <header>', 'Custom HTTP header (Name: Value)', collectHeaders, {})
    .option('--cookie <cookie>', 'Cookie string to send with requests')
    .option('--check-external', 'Check external links for broken URLs', false)
    .option('--extract <rule>', 'Custom CSS extraction (name:selector)', collectExtractions, [])
    .option('--search <pattern>', 'Search for text or /regex/ in source', collectSearches, [])
    .option('--snippet <file>', 'JS snippet to run in Playwright context', collect, [])
    .option('--psi', 'Enable PageSpeed Insights (rate limited without API key)', false)
    .option('--psi-key <key>', 'Google PageSpeed Insights API key')
    .option('--ai-prompt <prompt>', 'AI analysis prompt for each page')
    .option('--ai-provider <provider>', 'AI provider: openai, anthropic, or ollama')
    .option('--ai-model <model>', 'AI model override (default per provider)')
    .option('--ai-key <key>', 'AI API key (or use env var)')
    .option('--html', 'Generate interactive HTML dashboard report', false)
    .option('--no-open', 'Do not auto-open HTML report in browser')
    .option('--page-weight', 'Analyze page weight by fetching resource sizes via HEAD requests', false)
    .option('--gsc', 'Merge Google Search Console data (requires gsc-auth)', false)
    .option('--gsc-property <url>', 'GSC property URL (auto-detected if omitted)')
    .option('--gsc-days <number>', 'GSC data lookback period in days', '90')
    .option('--gsc-key-file <path>', 'Service account JSON key file for GSC')
    .option('--gsc-bq <dataset>', 'Use BigQuery bulk export (project.dataset format)')
    .option('--ga4', 'Merge Google Analytics 4 data', false)
    .option('--ga4-property <id>', 'GA4 property ID (numeric, e.g., 123456789)')
    .option('--ga4-days <number>', 'GA4 data lookback period in days', '90')
    .option('--ga4-key-file <path>', 'Service account JSON key file for GA4')
    .option('--segment <rule>', 'URL segment rule (name:pattern)', collect, [])
    .option('--db <path>', 'SQLite database path for storage mode (enables crawl resume)')
    .option('--resume', 'Resume an interrupted crawl (requires --db)', false)
    .option('--proxy <url>', 'Route requests through a proxy (http://host:port, socks5://host:port)')
    .option('--ngrams', 'Analyze word/phrase frequency (unigrams, bigrams, trigrams)', false)
    .option('--embeddings', 'Compute semantic similarity via text embeddings (requires --ai-provider)', false)
    .option('--embedding-model <model>', 'Embedding model override (default: text-embedding-3-small / nomic-embed-text)')
    .option('--similarity-threshold <number>', 'Cosine similarity threshold for grouping similar pages (0-1)', '0.85')
    .option('--link-intelligence', 'Compute advanced internal link metrics (click depth, HITS, centrality, suggestions)', false)
    .option('--li-max-suggestions <number>', 'Max internal linking suggestions to generate', '50')
    .option('--li-no-centrality', 'Skip betweenness/closeness centrality (faster for large sites)', false)
    .option('--sitemap-out', 'Generate an XML sitemap from crawled indexable pages', false)
    .option('--sitemap-changefreq <freq>', 'Default changefreq for generated sitemap (daily, weekly, monthly, etc.)')
    .option('--sitemap-priority <value>', 'Default priority for generated sitemap (0.0-1.0)')
    .option('--crux', 'Fetch Chrome User Experience Report (CrUX) real-user Web Vitals per URL', false)
    .option('--crux-key <key>', 'Google API key with CrUX API enabled')
    .option('--crux-form-factor <factor>', 'CrUX form factor: PHONE or DESKTOP (default: all)')
    .option('--plausible', 'Merge Plausible Analytics session/conversion data', false)
    .option('--plausible-site-id <domain>', 'Plausible site domain (e.g., example.com)')
    .option('--plausible-api-key <key>', 'Plausible API key (Bearer token)')
    .option('--plausible-days <number>', 'Plausible data lookback period in days', '30')
    .option('--plausible-host <url>', 'Plausible instance URL (default: https://plausible.io)')
    .option('--max-errors <number>', 'Stop crawl after N total errors (0 = unlimited)', '0')
    .option('--timeout <seconds>', 'Stop crawl after N seconds (0 = unlimited)', '0')
    .option('--language <code>', 'Language for n-gram stopwords (e.g., en, es, fr, de). Auto-detected if omitted')
    .option('--no-robots', 'Ignore robots.txt (crawl all URLs regardless of Disallow rules)')
    .option('--show-blocked-internal', 'Include internal URLs blocked by robots.txt in results', false);
}

// ── Command handlers ─────────────────────────────────────

async function handleSpider(url: string, opts: SpiderOptions): Promise<void> {
  try {
    new URL(url);
  } catch {
    console.error(`Error: Invalid URL "${url}". Please provide a full URL (e.g., https://example.com)`);
    process.exit(1);
  }

  const delayExplicit = opts.delay !== undefined;
  const partial: Record<string, unknown> = {
    ...buildCommonConfig(opts),
    seedUrl: url,
    mode: 'spider',
    maxDepth: parseIntStrict(opts.depth, 'depth'),
    maxPages: parseIntStrict(opts.limit, 'limit'),
    delayMs: delayExplicit ? parseIntStrict(opts.delay!, 'delay') : undefined,
    delayExplicit,
    includePatterns: parsePatterns(opts.include),
    excludePatterns: parsePatterns(opts.exclude),
    allowedDomains: opts.allowedDomains ? opts.allowedDomains.split(',').map(d => d.trim()).filter(Boolean) : [],
  };

  await runCrawl(mergeConfig(partial));
}

async function handleList(file: string, opts: ListOptions): Promise<void> {
  let content: string;
  try {
    content = readFileSync(resolveFilePath(file), 'utf-8');
  } catch {
    console.error(`Error: Cannot read file "${file}"`);
    process.exit(1);
  }

  const urls = content
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith('#'));

  if (urls.length === 0) {
    console.error('Error: No valid URLs found in file');
    process.exit(1);
  }

  for (const url of urls) {
    try {
      new URL(url);
    } catch {
      console.error(`Error: Invalid URL "${url}" in file`);
      process.exit(1);
    }
  }

  const partial: Record<string, unknown> = {
    ...buildCommonConfig(opts),
    mode: 'list',
    urls,
    seedUrl: urls[0],
    maxDepth: 0,
    maxPages: urls.length,
    delayMs: parseIntStrict(opts.delay || '100', 'delay'),
    delayExplicit: true,
    includePatterns: [],
    excludePatterns: [],
  };

  await runCrawl(mergeConfig(partial));
}

async function handleSitemap(urls: string[], opts: SitemapOptions): Promise<void> {
  for (const url of urls) {
    try {
      new URL(url);
    } catch {
      console.error(`Error: Invalid URL "${url}". Provide full sitemap URLs (e.g., https://example.com/sitemap.xml)`);
      process.exit(1);
    }
  }

  const partial: Record<string, unknown> = {
    ...buildCommonConfig(opts),
    mode: 'sitemap',
    sitemapUrls: urls,
    seedUrl: urls[0],
    maxDepth: 0,
    maxPages: opts.limit !== '0' ? parseIntStrict(opts.limit, 'limit') : 100000,
    delayMs: opts.delay !== undefined ? parseIntStrict(opts.delay, 'delay') : 100,
    delayExplicit: opts.delay !== undefined,
    includePatterns: [],
    excludePatterns: [],
  };

  await runCrawl(mergeConfig(partial));
}

// ── Program setup ────────────────────────────────────────

const program = new Command();

program
  .name('micelio')
  .description('High-performance Node.js technical SEO crawler')
  .version('1.0.0');

// Spider subcommand (default)
addCommonOptions(
  program
    .command('spider', { isDefault: true })
    .description('Crawl a website by following links from a seed URL')
    .argument('<url>', 'Seed URL to crawl')
    .option('-d, --depth <number>', 'Max crawl depth (0-10)', '3')
    .option('-l, --limit <number>', 'Max pages to crawl (1-100000)', '500')
    .option('--include <pattern>', 'Only crawl URLs matching regex pattern', collect, [])
    .option('--exclude <pattern>', 'Skip URLs matching regex pattern', collect, [])
    .option('--allowed-domains <domains>', 'Allow crawling across these domains (comma-separated)', ''),
).action(handleSpider);

// List subcommand
addCommonOptions(
  program
    .command('list')
    .description('Crawl a list of URLs from a file (one URL per line)')
    .argument('<file>', 'File containing URLs (one per line)'),
).action(handleList);

// Sitemap subcommand
addCommonOptions(
  program
    .command('sitemap')
    .description('Crawl all URLs found in XML sitemap(s) or sitemap index(es)')
    .argument('<url...>', 'Sitemap XML or sitemap index URL(s)')
    .option('-l, --limit <number>', 'Max pages to crawl (0 = unlimited)', '0'),
).action(handleSitemap);

// Diff subcommand
interface DiffOptions {
  output?: string;
  html: boolean;
  open: boolean;
  fields?: string;
  urlMap: string[];
}

function collectUrlMaps(value: string, previous: string[]): string[] {
  return previous.concat([value]);
}

program
  .command('diff')
  .description('Compare two crawl JSONL files and report differences')
  .argument('<old>', 'Old crawl JSONL file')
  .argument('<new>', 'New crawl JSONL file')
  .option('-o, --output <path>', 'Save JSON diff result to file')
  .option('--html', 'Generate HTML comparison dashboard', false)
  .option('--no-open', 'Do not auto-open HTML report in browser')
  .option('--fields <fields>', 'Comma-separated fields to compare (default: all)')
  .option('--url-map <mapping>', 'URL mapping in sed syntax s/old/new/', collectUrlMaps, [])
  .action(handleDiff);

async function handleDiff(oldFile: string, newFile: string, opts: DiffOptions): Promise<void> {
  // Validate files exist and are readable (without loading full content)
  const oldPath = resolve(oldFile);
  const newPath = resolve(newFile);
  try {
    accessSync(oldPath, constants.R_OK);
  } catch {
    console.error(`Error: Cannot read old crawl file "${oldPath}"`);
    process.exit(1);
  }
  try {
    accessSync(newPath, constants.R_OK);
  } catch {
    console.error(`Error: Cannot read new crawl file "${newPath}"`);
    process.exit(1);
  }

  // Parse URL mappings
  const urlMappings = opts.urlMap.map((m) => {
    try {
      return parseUrlMap(m);
    } catch (err) {
      console.error(`Error: ${formatError(err)}`);
      process.exit(1);
    }
  });

  // Parse fields
  let fields: DiffField[] | undefined;
  if (opts.fields) {
    const requested = opts.fields.split(',').map((f) => f.trim());
    const validFields = [...DIFF_FIELDS] as string[];
    for (const f of requested) {
      if (!validFields.includes(f)) {
        console.error(`Error: Unknown diff field "${f}". Valid fields: ${validFields.join(', ')}`);
        process.exit(1);
      }
    }
    fields = requested as DiffField[];
  }

  // Run diff
  const result = diffCrawls(oldPath, newPath, { urlMappings, fields });

  // Print terminal report
  printDiffReport(result);

  // Save JSON output
  if (opts.output) {
    const outputPath = resolve(opts.output);
    writeFileSync(outputPath, JSON.stringify(result, null, 2), 'utf-8');
    process.stderr.write(`Diff result saved: ${outputPath}\n`);
  }

  // Generate HTML dashboard
  if (opts.html) {
    const html = generateDiffHtml(result);
    const basePath = opts.output ? resolve(opts.output) : resolve('diff-report.html');
    const parsed = parsePath(basePath);
    const htmlPath = resolve(formatPath({ ...parsed, base: undefined, ext: '.html' }));
    writeFileSync(htmlPath, html, 'utf-8');
    process.stderr.write(`HTML diff report: ${htmlPath}\n`);

    if (opts.open !== false) {
      const cmd = process.platform === 'darwin' ? 'open' : process.platform === 'win32' ? 'cmd' : 'xdg-open';
      const args = process.platform === 'win32' ? ['/c', 'start', '', htmlPath] : [htmlPath];
      execFile(cmd, args, (err) => {
        if (err) process.stderr.write(`Could not auto-open report: ${err.message}\n`);
      });
    }
  }
}

// ── GSC Auth subcommand ──────────────────────────────────

program
  .command('gsc-auth')
  .description('Authenticate with Google Search Console (OAuth2 flow)')
  .option('--client-id <id>', 'OAuth2 client ID')
  .option('--client-secret <secret>', 'OAuth2 client secret')
  .option('--revoke', 'Revoke stored token', false)
  .action(async (opts: { clientId?: string; clientSecret?: string; revoke: boolean }) => {
    if (opts.revoke) {
      deleteStoredToken();
      return;
    }
    await runOAuthFlow(opts.clientId, opts.clientSecret);
  });

// ── Schedule subcommand ──────────────────────────────────

interface ScheduleOptions {
  cron: string;
  webhook?: string;
  webhookHeader: Record<string, string>;
  maxRuns: string;
  outputDir: string;
}

addCommonOptions(
  program
    .command('schedule')
    .description('Run crawls on a cron schedule (e.g., every Monday at 9am)')
    .argument('<url>', 'URL to crawl on schedule')
    .option('--cron <expression>', 'Cron schedule expression (e.g., "0 9 * * 1" or @daily)', '')
    .option('--webhook <url>', 'POST notification URL on crawl completion')
    .option('--webhook-header <header>', 'Custom header for webhook (Name: Value)', collectHeaders, {})
    .option('--max-runs <number>', 'Stop after N runs (0 = unlimited)', '0')
    .option('--output-dir <path>', 'Directory for timestamped output files', './crawls')
    .option('-d, --depth <number>', 'Max crawl depth (0-10)', '3')
    .option('-l, --limit <number>', 'Max pages to crawl (1-100000)', '500')
    .option('--include <pattern>', 'Only crawl URLs matching regex pattern', collect, [])
    .option('--exclude <pattern>', 'Skip URLs matching regex pattern', collect, []),
).action(async (url: string, opts: ScheduleOptions & SpiderOptions) => {
  // Validate URL
  try {
    new URL(url);
  } catch {
    console.error(`Error: Invalid URL "${url}". Please provide a full URL (e.g., https://example.com)`);
    process.exit(1);
  }

  // Validate cron expression
  if (!opts.cron) {
    console.error('Error: --cron is required. Example: --cron "0 9 * * 1" (Monday 9am)');
    console.error('  Shortcuts: @hourly, @daily, @weekly, @monthly');
    process.exit(1);
  }

  try {
    parseCron(opts.cron);
  } catch (err) {
    console.error(`Error: ${(err as Error).message}`);
    process.exit(1);
  }

  // Validate webhook URL if provided
  if (opts.webhook) {
    try {
      new URL(opts.webhook);
    } catch {
      console.error(`Error: Invalid webhook URL "${opts.webhook}"`);
      process.exit(1);
    }
  }

  const maxRuns = parseIntStrict(opts.maxRuns, 'max-runs');

  // Build crawl arguments to pass to child process
  // We reconstruct the spider command args from the parsed options
  const crawlArgs: string[] = ['spider', url];

  // Spider-specific options
  crawlArgs.push('-d', opts.depth);
  crawlArgs.push('-l', opts.limit);
  for (const pattern of opts.include) crawlArgs.push('--include', pattern);
  for (const pattern of opts.exclude) crawlArgs.push('--exclude', pattern);

  // Common options
  crawlArgs.push('-c', opts.concurrency);
  if (opts.delay !== undefined) crawlArgs.push('--delay', opts.delay);
  if (opts.userAgent) crawlArgs.push('--user-agent', opts.userAgent);
  if (opts.js) crawlArgs.push('--js');
  if (opts.csv) crawlArgs.push('--csv');
  for (const [key, value] of Object.entries(opts.header)) crawlArgs.push('-H', `${key}: ${value}`);
  if (opts.cookie) crawlArgs.push('--cookie', opts.cookie);
  if (opts.checkExternal) crawlArgs.push('--check-external');
  for (const ext of opts.extract) crawlArgs.push('--extract', `${ext.name}:${ext.selector}`);
  for (const search of opts.search) {
    const prefix = search.isRegex ? '/' : '';
    const suffix = search.isRegex ? '/' : '';
    crawlArgs.push('--search', `${prefix}${search.pattern}${suffix}`);
  }
  for (const snippet of opts.snippet) crawlArgs.push('--snippet', snippet);
  if (opts.psi) crawlArgs.push('--psi');
  if (opts.psiKey) crawlArgs.push('--psi-key', opts.psiKey);
  if (opts.aiPrompt) crawlArgs.push('--ai-prompt', opts.aiPrompt);
  if (opts.aiProvider) crawlArgs.push('--ai-provider', opts.aiProvider);
  if (opts.aiModel) crawlArgs.push('--ai-model', opts.aiModel);
  if (opts.aiKey) crawlArgs.push('--ai-key', opts.aiKey);
  if (opts.html) crawlArgs.push('--html');
  // Always force --no-open for scheduled crawls (unattended, no browser windows)
  crawlArgs.push('--no-open');
  if (opts.pageWeight) crawlArgs.push('--page-weight');
  if (opts.gsc) crawlArgs.push('--gsc');
  if (opts.gscProperty) crawlArgs.push('--gsc-property', opts.gscProperty);
  if (opts.gscDays !== '90') crawlArgs.push('--gsc-days', opts.gscDays);
  if (opts.gscKeyFile) crawlArgs.push('--gsc-key-file', opts.gscKeyFile);
  if (opts.gscBq) crawlArgs.push('--gsc-bq', opts.gscBq);
  if (opts.ga4) crawlArgs.push('--ga4');
  if (opts.ga4Property) crawlArgs.push('--ga4-property', opts.ga4Property);
  if (opts.ga4Days !== '90') crawlArgs.push('--ga4-days', opts.ga4Days);
  if (opts.ga4KeyFile) crawlArgs.push('--ga4-key-file', opts.ga4KeyFile);
  for (const seg of opts.segment) crawlArgs.push('--segment', seg);
  if (opts.db) crawlArgs.push('--db', opts.db);
  if (opts.resume) crawlArgs.push('--resume');
  if (opts.proxy) crawlArgs.push('--proxy', opts.proxy);
  if (opts.ngrams) crawlArgs.push('--ngrams');
  if (opts.embeddings) {
    crawlArgs.push('--embeddings');
    if (opts.embeddingModel) crawlArgs.push('--embedding-model', opts.embeddingModel);
    if (opts.similarityThreshold && opts.similarityThreshold !== '0.85') crawlArgs.push('--similarity-threshold', opts.similarityThreshold);
  }
  if (opts.linkIntelligence) {
    crawlArgs.push('--link-intelligence');
    if (opts.liMaxSuggestions && opts.liMaxSuggestions !== '50') crawlArgs.push('--li-max-suggestions', opts.liMaxSuggestions);
    if (opts.liNoCentrality) crawlArgs.push('--li-no-centrality');
  }
  if (opts.sitemapOut) crawlArgs.push('--sitemap-out');
  if (opts.crux) {
    crawlArgs.push('--crux');
    if (opts.cruxKey) crawlArgs.push('--crux-key', opts.cruxKey);
    if (opts.cruxFormFactor) crawlArgs.push('--crux-form-factor', opts.cruxFormFactor);
  }
  if (opts.maxErrors && opts.maxErrors !== '0') crawlArgs.push('--max-errors', opts.maxErrors);
  if (opts.timeout && opts.timeout !== '0') crawlArgs.push('--timeout', opts.timeout);
  const allowedDomains = (opts as SpiderOptions).allowedDomains;
  if (allowedDomains) crawlArgs.push('--allowed-domains', allowedDomains);

  const webhookOpts = opts.webhook
    ? { url: opts.webhook, headers: opts.webhookHeader }
    : undefined;

  const scheduleConfig: ScheduleConfig = {
    url,
    cron: opts.cron,
    maxRuns,
    outputDir: resolve(opts.outputDir),
    webhook: webhookOpts,
    crawlArgs,
  };

  await runSchedule(scheduleConfig);
});

// ── UI subcommand ────────────────────────────────────────

program
  .command('ui')
  .description('Launch the interactive web dashboard for Micelio')
  .option('-p, --port <number>', 'Port number for the UI server', '3100')
  .option('--no-open', 'Do not auto-open the browser')
  .action(async (opts: { port: string; open: boolean }) => {
    const port = parseInt(opts.port, 10);
    if (isNaN(port) || port < 1 || port > 65535) {
      console.error(`Error: Invalid port "${opts.port}". Must be 1-65535.`);
      process.exit(1);
    }
    const { startUiServer } = await import('./ui-server.js');
    await startUiServer(port, opts.open !== false);
  });

// ── Schedules (list) subcommand ──────────────────────────

program
  .command('schedules')
  .description('List all scheduled crawls and their status')
  .action(() => {
    const schedules = listSchedules();
    if (schedules.length === 0) {
      console.log('No schedules found. Create one with: micelio schedule <url> --cron "..."');
      return;
    }

    console.log(`\n  Scheduled Crawls (${schedules.length})\n`);
    console.log('  ' + '-'.repeat(80));

    for (const s of schedules) {
      const status = s.lastStatus === 'success' ? '\x1b[32mOK\x1b[0m'
        : s.lastStatus === 'failed' ? '\x1b[31mFAIL\x1b[0m'
        : '\x1b[33mNEW\x1b[0m';

      const nextRunStr = s.nextRun
        ? new Date(s.nextRun).toLocaleString()
        : 'N/A';

      const lastRunStr = s.lastRun
        ? new Date(s.lastRun).toLocaleString()
        : 'never';

      const duration = s.lastDurationMs > 0
        ? `${(s.lastDurationMs / 1000).toFixed(0)}s`
        : '-';

      console.log(`  ${s.url}`);
      console.log(`    Schedule:  ${describeCron(s.cron)} (${s.cron})`);
      console.log(`    Last run:  ${lastRunStr} [${status}] ${s.lastPages} pages, ${duration}`);
      console.log(`    Next run:  ${nextRunStr}`);
      console.log(`    Total:     ${s.totalRuns} runs`);
      console.log(`    Output:    ${s.outputDir}`);
      console.log('  ' + '-'.repeat(80));
    }
    console.log('');
  });

// ── Robots.txt Multi-Agent Testing ───────────────────────

program
  .command('robots-test')
  .description('Test robots.txt access for multiple user-agents')
  .argument('<url>', 'Website URL to test (e.g., https://example.com)')
  .option('--agents <agents>', 'Comma-separated user-agents (default: major bots)', '')
  .option('--urls <file>', 'File with URLs to test (one per line)')
  .option('--all-agents', 'Test all known user-agents', false)
  .action(async (url: string, opts: { agents: string; urls?: string; allAgents: boolean }) => {
    try {
      new URL(url);
    } catch {
      console.error(`Error: Invalid URL "${url}". Provide a full URL (e.g., https://example.com)`);
      process.exit(1);
    }

    // Determine user-agents to test
    let agents: string[];
    if (opts.allAgents) {
      agents = [...KNOWN_USER_AGENTS];
    } else if (opts.agents) {
      agents = opts.agents.split(',').map(a => a.trim()).filter(Boolean);
    } else {
      // Default: top bots
      agents = ['Googlebot', 'Bingbot', 'GPTBot', 'CCBot', 'Applebot', 'AhrefsBot'];
    }

    // Load test URLs from file if provided
    let testUrls: string[] | undefined;
    if (opts.urls) {
      try {
        const content = readFileSync(resolveFilePath(opts.urls), 'utf-8');
        testUrls = content.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#'));
      } catch {
        console.error(`Error: Cannot read file "${opts.urls}"`);
        process.exit(1);
      }
    }

    process.stderr.write(`Testing robots.txt for ${url}...\n`);
    process.stderr.write(`User-agents: ${agents.join(', ')}\n\n`);

    const result = await testRobotsMultiAgent(url, agents, testUrls);

    // Display results
    if (result.robotstxtStatus !== 200) {
      console.log(`robots.txt status: ${result.robotstxtStatus || 'unreachable'} — all URLs allowed by default\n`);
    } else {
      console.log(`robots.txt: ${result.robotstxtUrl} (200 OK)\n`);
    }

    // Sitemaps
    if (result.sitemapUrls.length > 0) {
      console.log(`Sitemaps declared: ${result.sitemapUrls.length}`);
      for (const sm of result.sitemapUrls) {
        console.log(`  ${sm}`);
      }
      console.log('');
    }

    // Print matrix header
    const isTTY = process.stdout.isTTY;
    const agentColWidth = Math.max(12, ...agents.map(a => a.length)) + 2;
    const header = 'URL'.padEnd(60) + agents.map(a => a.padEnd(agentColWidth)).join('');
    console.log(header);
    console.log('-'.repeat(header.length));

    // Print matrix rows
    for (const testUrl of result.urls) {
      const urlDisplay = testUrl.length > 58 ? testUrl.substring(0, 55) + '...' : testUrl;
      let row = urlDisplay.padEnd(60);
      for (const agent of agents) {
        const entry = result.results.find(r => r.url === testUrl && r.agent === agent);
        if (isTTY) {
          const status = entry?.allowed !== false ? '\x1b[32m✓ Allow\x1b[0m' : '\x1b[31m✗ Block\x1b[0m';
          row += status.padEnd(agentColWidth + 9); // +9 for ANSI escape codes
        } else {
          const status = entry?.allowed !== false ? 'Allow' : 'BLOCK';
          row += status.padEnd(agentColWidth);
        }
      }
      console.log(row);
    }

    // Summary
    console.log('');
    const blocked = result.results.filter(r => !r.allowed);
    if (blocked.length === 0) {
      console.log('All URLs are allowed for all tested user-agents.');
    } else {
      console.log(`Blocked entries: ${blocked.length}/${result.results.length}`);

      // Show per-agent summary
      for (const agent of agents) {
        const agentBlocked = blocked.filter(r => r.agent === agent);
        if (agentBlocked.length > 0) {
          console.log(`  ${agent}: ${agentBlocked.length} URL(s) blocked`);
        }
      }
    }

    // Show relevant directives
    if (result.directives.length > 0) {
      console.log('\nDirectives found:');
      for (const d of result.directives) {
        console.log(`  [${d.agent}] ${d.directive}: ${d.path}`);
      }
    }
  });

// ── HEAD-only crawl mode ─────────────────────────────────

interface HeadOptions {
  concurrency: string;
  delay: string;
  userAgent?: string;
  output: string;
  csv: boolean;
  header: Record<string, string>;
  cookie?: string;
  proxy?: string;
}

program
  .command('head')
  .description('HEAD-request crawl: check status, headers, and redirects without downloading page content')
  .argument('<file>', 'File containing URLs (one per line)')
  .option('-c, --concurrency <number>', 'Concurrent workers (1-50)', '5')
  .option('--delay <number>', 'Delay between requests in ms', '0')
  .option('--user-agent <string>', 'Custom User-Agent string')
  .option('-o, --output <path>', 'Output file path', 'head-output.jsonl')
  .option('--csv', 'Output as CSV instead of JSONL', false)
  .option('-H, --header <header>', 'Custom HTTP header (Name: Value)', collectHeaders, {})
  .option('--cookie <cookie>', 'Cookie string to send with requests')
  .option('--proxy <url>', 'Route requests through a proxy')
  .action(async (file: string, opts: HeadOptions) => {
    let content: string;
    try {
      content = readFileSync(resolveFilePath(file), 'utf-8');
    } catch {
      console.error(`Error: Cannot read file "${file}"`);
      process.exit(1);
    }

    const urls = content.split('\n').map(l => l.trim()).filter(l => l && !l.startsWith('#'));
    if (urls.length === 0) {
      console.error('Error: No valid URLs found in file');
      process.exit(1);
    }

    for (const url of urls) {
      try { new URL(url); } catch {
        console.error(`Error: Invalid URL "${url}" in file`);
        process.exit(1);
      }
    }

    const concurrency = parseIntStrict(opts.concurrency, 'concurrency');
    if (concurrency < 1 || concurrency > 50) {
      console.error('Error: --concurrency must be between 1 and 50');
      process.exit(1);
    }
    const delayMs = parseIntStrict(opts.delay, 'delay');
    const userAgent = opts.userAgent || 'Micelio/1.0 (+https://github.com/micelio)';
    const outputFormat = opts.csv ? 'csv' as const : 'jsonl' as const;
    let outputPath = opts.output;
    if (opts.csv && outputPath === 'head-output.jsonl') {
      outputPath = 'head-output.csv';
    }

    // Proxy setup
    let dispatcher: import('undici').Dispatcher | undefined;
    if (opts.proxy) {
      try {
        const proxyUrl = new URL(opts.proxy);
        const supportedProtocols = ['http:', 'https:', 'socks4:', 'socks5:'];
        if (!supportedProtocols.includes(proxyUrl.protocol)) {
          console.error(`Error: Unsupported proxy protocol "${proxyUrl.protocol}". Use http://, https://, socks4://, or socks5://`);
          process.exit(1);
        }
      } catch {
        console.error(`Error: Invalid proxy URL "${opts.proxy}". Format: http://host:port or socks5://host:port`);
        process.exit(1);
      }
      const { ProxyAgent } = await import('undici');
      dispatcher = new ProxyAgent(opts.proxy);
    }

    const fetchOpts: HeadFetchOptions = {
      userAgent,
      customHeaders: opts.header,
      cookies: opts.cookie || '',
      dispatcher,
    };

    process.stderr.write(`HEAD crawl: ${urls.length} URLs\n`);
    process.stderr.write(`  Workers:    ${concurrency} concurrent\n`);
    process.stderr.write(`  Output:     ${outputPath} (${outputFormat})\n\n`);

    const writer = new HeadResultWriter(outputPath, outputFormat);
    const pLimit = (await import('p-limit')).default;
    const limit = pLimit(concurrency);
    const results: HeadResult[] = [];
    const startTime = Date.now();
    let completed = 0;
    let failed = 0;

    const tasks = urls.map(url => limit(async () => {
      if (delayMs > 0) {
        await new Promise(r => setTimeout(r, delayMs));
      }
      const result = await fetchHead(url, fetchOpts);
      writer.write(result);
      results.push(result);

      if (result.error) {
        failed++;
      } else {
        completed++;
      }

      const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
      process.stderr.write(
        `\r\x1b[K  [${completed + failed}/${urls.length}] ${elapsed}s - ${result.statusCode} ${url.substring(0, 80)}`
      );
    }));

    await Promise.all(tasks);
    await writer.close();

    // Close proxy dispatcher
    if (dispatcher && 'close' in dispatcher) {
      await (dispatcher as import('undici').ProxyAgent).close();
    }

    const durationSec = ((Date.now() - startTime) / 1000).toFixed(1);
    process.stderr.write(`\n\nHEAD crawl complete: ${results.length} URLs in ${durationSec}s (${failed} failed)\n`);
    process.stderr.write(`Output: ${outputPath}\n\n`);

    // Print summary report
    const statusDist: Record<number, number> = {};
    let redirectCount = 0;
    let noindexCount = 0;
    let hstsCount = 0;
    let cspCount = 0;
    let avgResponseTime = 0;
    const errors: { url: string; error: string }[] = [];

    for (const r of results) {
      statusDist[r.statusCode] = (statusDist[r.statusCode] || 0) + 1;
      if (r.redirectChain.length > 0) redirectCount++;
      if (r.xRobotsTag && /noindex/i.test(r.xRobotsTag)) noindexCount++;
      if (r.hsts) hstsCount++;
      if (r.csp) cspCount++;
      avgResponseTime += r.responseTimeMs;
      if (r.error) errors.push({ url: r.url, error: r.error });
    }
    avgResponseTime = results.length > 0 ? Math.round(avgResponseTime / results.length) : 0;

    console.log('  HEAD Crawl Summary');
    console.log('  ' + '-'.repeat(50));
    console.log(`  URLs checked:      ${results.length}`);
    console.log(`  Avg response time: ${avgResponseTime}ms`);
    console.log('');

    // Status codes
    console.log('  Status Codes:');
    const sortedStatuses = Object.entries(statusDist).sort(([a], [b]) => Number(a) - Number(b));
    for (const [code, count] of sortedStatuses) {
      const pct = ((count / results.length) * 100).toFixed(1);
      console.log(`    ${code}: ${count} (${pct}%)`);
    }
    console.log('');

    // SEO headers
    console.log('  SEO Headers:');
    console.log(`    Redirects:        ${redirectCount}`);
    console.log(`    X-Robots noindex: ${noindexCount}`);
    console.log(`    HSTS:             ${hstsCount}/${results.length}`);
    console.log(`    CSP:              ${cspCount}/${results.length}`);
    console.log('');

    // Errors
    if (errors.length > 0) {
      console.log(`  Errors (${errors.length}):`);
      for (const e of errors.slice(0, 10)) {
        console.log(`    ${e.url}: ${e.error}`);
      }
      if (errors.length > 10) {
        console.log(`    ... and ${errors.length - 10} more`);
      }
    }
  });

// ── Sitemap file writer ──────────────────────────────────

function writeMultiSitemapFiles(
  multiResult: import('./sitemap-generator.js').MultiSitemapResult,
  outputDir: ReturnType<typeof parsePath>,
  baseName: string,
): void {
  if (multiResult.sitemaps.length === 1) {
    const sitemapPath = resolve(formatPath({ ...outputDir, base: undefined, name: baseName, ext: '.xml' }));
    writeFileSync(sitemapPath, multiResult.sitemaps[0].xml, 'utf-8');
    process.stderr.write(`XML sitemap: ${sitemapPath} (${multiResult.sitemaps[0].urlCount} URLs)\n`);
  } else {
    for (let i = 0; i < multiResult.sitemaps.length; i++) {
      const suffix = i === 0 ? '' : `-${i + 1}`;
      const path = resolve(formatPath({ ...outputDir, base: undefined, name: `${baseName}${suffix}`, ext: '.xml' }));
      writeFileSync(path, multiResult.sitemaps[i].xml, 'utf-8');
      process.stderr.write(`XML sitemap ${i + 1}: ${path} (${multiResult.sitemaps[i].urlCount} URLs)\n`);
    }
    if (multiResult.index) {
      const indexPath = resolve(formatPath({ ...outputDir, base: undefined, name: baseName + '-index', ext: '.xml' }));
      writeFileSync(indexPath, multiResult.index, 'utf-8');
      process.stderr.write(`Sitemap index: ${indexPath} (${multiResult.sitemaps.length} sitemaps)\n`);
    }
  }
  process.stderr.write(`Total eligible URLs: ${multiResult.totalEligible}\n`);
}

// ── Run crawl ────────────────────────────────────────────

const VALID_CHANGEFREQ_VALUES = new Set(['always', 'hourly', 'daily', 'weekly', 'monthly', 'yearly', 'never']);

async function runCrawl(config: ReturnType<typeof mergeConfig>): Promise<void> {
  // Validate sitemap generation options
  if (config.sitemapChangefreq && !VALID_CHANGEFREQ_VALUES.has(config.sitemapChangefreq)) {
    console.error(`Error: Invalid --sitemap-changefreq "${config.sitemapChangefreq}". Valid values: ${[...VALID_CHANGEFREQ_VALUES].join(', ')}`);
    process.exit(1);
  }
  if (config.sitemapPriority) {
    const pVal = parseFloat(config.sitemapPriority);
    if (Number.isNaN(pVal) || pVal < 0 || pVal > 1) {
      console.error(`Error: Invalid --sitemap-priority "${config.sitemapPriority}". Must be between 0.0 and 1.0`);
      process.exit(1);
    }
  }

  const startTime = Date.now();
  try {
    const { pages, externalLinkResults, sitemapErrors, sitemapValidationWarnings, sitemapEntries, totalSitemapUrls, sitemapExtensionCounts, ngramStats, embeddingStats } = await crawl(config);
    const durationMs = Date.now() - startTime;

    // Phase 11: Merge GSC data if requested (--gsc or --gsc-bq)
    if (config.gsc || config.gscBqDataset) {
      await mergeGscIntoPages(pages, config);
    }

    // Phase 11: Merge GA4 data if requested
    if (config.ga4) {
      await mergeGa4IntoPages(pages, config);
    }

    // CrUX: Fetch real-user Web Vitals if requested
    if (config.crux) {
      await mergeCruxIntoPages(pages, config);
    }

    // Plausible Analytics: Merge session/conversion data if requested
    if (config.plausible) {
      await mergePlausibleIntoPages(pages, config);
    }

    // Re-write output if analytics enrichment was applied (data was merged post-crawl)
    const hasAnalyticsEnrichment = pages.some(p => p.gscData || p.ga4Data || p.cruxData || p.plausibleData);
    if (hasAnalyticsEnrichment) {
      const rewriter = new ResultWriter(config.outputPath, config.outputFormat);
      for (const page of pages) rewriter.write(page);
      await rewriter.close();
    }

    const stats = generateReport(pages, durationMs, externalLinkResults, totalSitemapUrls, config.gscDays, config.ga4Days, config.segmentRules, sitemapExtensionCounts, sitemapValidationWarnings, sitemapEntries);
    // Plausible: Set days in stats
    if (stats.plausibleStats) {
      stats.plausibleStats.days = config.plausibleDays;
    }
    // Phase 6: Attach sitemap errors
    if (stats.sitemapStats && sitemapErrors.length > 0) {
      stats.sitemapStats.sitemapErrors = sitemapErrors;
    }
    // Phase 13: Attach n-gram stats
    if (ngramStats) {
      stats.ngramStats = ngramStats;
    }
    // Phase 13.2: Attach embedding stats
    if (embeddingStats) {
      stats.embeddingStats = embeddingStats;
    }
    // Link Intelligence: compute post-crawl link metrics
    if (config.linkIntelligence && pages.length > 0) {
      process.stderr.write('Computing link intelligence metrics...\n');
      const seedUrl = config.mode === 'list' ? (config.urls[0] || '') : config.seedUrl;
      // Pass embedding vectors for semantic link suggestions when --embeddings is enabled.
      // Without embeddings, suggestions use graph-only signals (in-degree, PageRank, click depth).
      const embeddingVectors = embeddingStats?.vectors?.size ? embeddingStats.vectors : null;
      const liStats = computeLinkIntelligence(pages, seedUrl, {
        embeddings: embeddingVectors,
        pageRanks: stats.pageRankScores,
        maxSuggestions: config.liMaxSuggestions,
        noCentrality: config.liNoCentrality,
      });
      stats.linkIntelligenceStats = liStats;
      if (liStats) {
        process.stderr.write(
          `Link intelligence: click depth avg ${liStats.avgClickDepth}, ` +
          `${liStats.unreachablePagesCount} unreachable, ` +
          `${liStats.nearOrphansCount} near-orphans, ` +
          `${liStats.dilutionWarningsCount} dilution warnings, ` +
          `${liStats.linkSuggestionsCount} suggestions` +
          (liStats.centralitySkipped ? `, centrality ${liStats.centralitySkipReason}` : '') +
          (liStats.semanticLinkAnalysis ? `, ${liStats.semanticLinkAnalysis.weakLinksCount} weak links` : '') +
          '\n'
        );

        // Write link suggestions CSV if any suggestions were generated
        if (liStats.linkSuggestions.length > 0) {
          const parsed = parsePath(config.outputPath);
          const suggestionsPath = resolve(formatPath({ ...parsed, base: undefined, name: parsed.name + '-link-suggestions', ext: '.csv' }));
          const suggestionsContent = [
            'source_url,target_url,score,semantic_similarity,target_in_degree,target_click_depth,depth_reduction,target_pagerank,reason',
            ...liStats.linkSuggestions.map(s =>
              [
                `"${s.sourceUrl.replace(/"/g, '""')}"`,
                `"${s.targetUrl.replace(/"/g, '""')}"`,
                s.score,
                s.signals.semanticSimilarity ?? '',
                s.signals.targetInDegree,
                s.signals.targetClickDepth ?? '',
                s.signals.depthReduction ?? '',
                s.signals.targetPageRank,
                `"${s.reason.replace(/"/g, '""')}"`,
              ].join(',')
            ),
          ].join('\n') + '\n';
          writeFileSync(suggestionsPath, suggestionsContent, 'utf-8');
          process.stderr.write(`Link suggestions: ${suggestionsPath}\n`);
        }
      }
    }
    printReport(stats);

    // Generate XML sitemap from crawled indexable pages
    if (config.sitemapOut) {
      const sitemapGenOpts: SitemapGenOptions = {
        changefreq: config.sitemapChangefreq || undefined,
        priority: config.sitemapPriority || undefined,
      };
      const parsed = parsePath(config.outputPath);
      const baseName = parsed.name + '-sitemap';
      const baseUrl = config.seedUrl ? new URL(config.seedUrl).origin : '';
      const multiResult = generateMultiSitemap(pages, baseUrl, baseName, sitemapGenOpts);
      writeMultiSitemapFiles(multiResult, parsed, baseName);
    }

    if (config.htmlReport) {
      const seedUrl = config.mode === 'list' ? (config.urls[0] || 'list mode') : config.seedUrl;
      const html = generateHtmlReport(pages, stats, seedUrl);
      const parsed = parsePath(config.outputPath);
      const htmlPath = resolve(formatPath({ ...parsed, base: undefined, ext: '.html' }));
      writeFileSync(htmlPath, html, 'utf-8');
      process.stderr.write(`HTML report: ${htmlPath}\n`);

      if (config.htmlOpen) {
        const cmd = process.platform === 'darwin' ? 'open' : process.platform === 'win32' ? 'cmd' : 'xdg-open';
        const args = process.platform === 'win32' ? ['/c', 'start', '', htmlPath] : [htmlPath];
        execFile(cmd, args, (err) => {
          if (err) process.stderr.write(`Could not auto-open report: ${err.message}\n`);
        });
      }
    }
  } catch (err) {
    console.error('Crawl failed:', formatError(err));
    process.exit(1);
  }
}

/** Apply merged GSC data to page objects, returns match count. */
function applyGscToPages(
  pages: import('./types.js').PageData[],
  gscRaw: Map<string, import('./types.js').GscData>,
  crawledUrls: string[],
): number {
  const merged = mergeGscData(crawledUrls, gscRaw);
  let matchCount = 0;
  for (const page of pages) {
    const gsc = merged.get(page.url);
    if (gsc) {
      page.gscData = gsc;
      matchCount++;
    }
  }
  return matchCount;
}

async function mergeGscIntoPages(pages: import('./types.js').PageData[], config: ReturnType<typeof mergeConfig>): Promise<void> {
  const crawledUrls = pages.map(p => p.url);

  // BigQuery path: --gsc-bq takes precedence over API
  if (config.gscBqDataset) {
    process.stderr.write('Fetching GSC data from BigQuery bulk export...\n');
    const { estimatedMB, estimatedCost } = estimateQueryCost(crawledUrls.length, config.gscDays);
    process.stderr.write(`Estimated query size: ${estimatedMB} MB — Cost: ${estimatedCost}\n`);

    try {
      const bqResults = await fetchGscFromBigQuery({
        dataset: config.gscBqDataset,
        days: config.gscDays,
        urls: crawledUrls,
        keyFile: config.gscKeyFile || undefined,
      });

      const matchCount = applyGscToPages(pages, bqResults, crawledUrls);
      process.stderr.write(
        `GSC data merged (BigQuery): ${matchCount}/${pages.length} pages matched ` +
        `(${bqResults.size} URLs from BQ, last ${config.gscDays} days)\n`
      );
    } catch (err) {
      process.stderr.write(`Warning: BigQuery GSC fetch failed: ${formatError(err)}\n`);
    }
    return;
  }

  // API path: --gsc
  process.stderr.write('Fetching Google Search Console data...\n');

  // Get auth client
  let auth;
  if (config.gscKeyFile) {
    auth = getServiceAccountClient(config.gscKeyFile);
  } else {
    auth = getAuthClient();
    if (!auth) {
      process.stderr.write('Warning: No GSC token found. Run "micelio gsc-auth" first. Skipping GSC data.\n');
      return;
    }
  }

  // Determine property
  let property = config.gscProperty;
  if (!property) {
    const seedUrl = config.mode === 'list' ? (config.urls[0] || '') : config.seedUrl;
    if (!seedUrl) {
      process.stderr.write('Warning: Cannot auto-detect GSC property without a seed URL. Use --gsc-property.\n');
      return;
    }
    property = await autoDetectProperty(auth, seedUrl) ?? '';
    if (!property) {
      process.stderr.write(
        'Warning: Could not auto-detect GSC property. Use --gsc-property <url>.\n' +
        'Run "micelio gsc-auth" to see available properties.\n'
      );
      return;
    }
    process.stderr.write(`Auto-detected GSC property: ${property}\n`);
  }

  try {
    const gscRaw = await fetchGscData({
      auth,
      property,
      days: config.gscDays,
      urls: crawledUrls,
    });

    const matchCount = applyGscToPages(pages, gscRaw, crawledUrls);
    process.stderr.write(
      `GSC data merged: ${matchCount}/${pages.length} pages matched ` +
      `(${gscRaw.size} total URLs from GSC, last ${config.gscDays} days)\n`
    );
  } catch (err) {
    process.stderr.write(`Warning: GSC data fetch failed: ${formatError(err)}\n`);
  }
}

async function mergeGa4IntoPages(pages: import('./types.js').PageData[], config: ReturnType<typeof mergeConfig>): Promise<void> {
  if (!config.ga4Property) {
    process.stderr.write(
      'Warning: --ga4-property is required for GA4 integration. ' +
      'Find your property ID in GA4: Admin > Property Settings.\n'
    );
    return;
  }

  process.stderr.write('Fetching Google Analytics 4 data...\n');

  const crawledUrls = pages.map(p => p.url);

  // Determine auth method
  let accessToken: string | undefined;
  if (!config.ga4KeyFile) {
    // Try to get OAuth token from stored GSC auth (same Google account)
    const auth = getAuthClient();
    if (auth) {
      try {
        const tokenResponse = await auth.getAccessToken();
        accessToken = tokenResponse?.token || tokenResponse?.res?.data?.access_token;
      } catch {
        // Fall through to ADC
      }
    }
    if (!accessToken) {
      process.stderr.write(
        'Warning: No GA4 credentials found. Use --ga4-key-file or run "micelio gsc-auth" first.\n' +
        'Note: The OAuth token must have analytics.readonly scope.\n'
      );
      return;
    }
  }

  try {
    const ga4Raw = await fetchGa4Data({
      propertyId: config.ga4Property,
      days: config.ga4Days,
      urls: crawledUrls,
      keyFile: config.ga4KeyFile || undefined,
      accessToken,
    });

    let matchCount = 0;
    for (const page of pages) {
      const ga4 = ga4Raw.get(page.url);
      if (ga4) {
        page.ga4Data = ga4;
        matchCount++;
      }
    }

    process.stderr.write(
      `GA4 data merged: ${matchCount}/${pages.length} pages matched ` +
      `(${ga4Raw.size} paths from GA4, last ${config.ga4Days} days)\n`
    );
  } catch (err) {
    process.stderr.write(`Warning: GA4 data fetch failed: ${formatError(err)}\n`);
  }
}

async function mergeCruxIntoPages(pages: import('./types.js').PageData[], config: ReturnType<typeof mergeConfig>): Promise<void> {
  if (!config.cruxKey) {
    process.stderr.write(
      'Warning: --crux-key is required for CrUX integration. ' +
      'Get an API key at https://console.cloud.google.com and enable the CrUX API.\n'
    );
    return;
  }

  process.stderr.write('Fetching Chrome User Experience Report (CrUX) data...\n');

  // Only fetch for 200 OK pages (others won't have CrUX data)
  const eligiblePages = pages.filter(p => p.statusCode === 200);
  const urls = eligiblePages.map(p => p.finalUrl);

  try {
    const cruxRaw = await fetchCruxData({
      apiKey: config.cruxKey,
      urls,
      formFactor: config.cruxFormFactor === 'PHONE' || config.cruxFormFactor === 'DESKTOP'
        ? config.cruxFormFactor
        : undefined,
    });

    let matchCount = 0;
    for (const page of pages) {
      const crux = cruxRaw.get(page.finalUrl);
      if (crux) {
        page.cruxData = crux;
        matchCount++;
      }
    }

    process.stderr.write(
      `CrUX data merged: ${matchCount}/${eligiblePages.length} pages with data ` +
      `(form factor: ${config.cruxFormFactor || 'all'})\n`
    );
  } catch (err) {
    process.stderr.write(`Warning: CrUX data fetch failed: ${formatError(err)}\n`);
  }
}

async function mergePlausibleIntoPages(pages: import('./types.js').PageData[], config: ReturnType<typeof mergeConfig>): Promise<void> {
  if (!config.plausibleSiteId || !config.plausibleApiKey) {
    process.stderr.write(
      'Warning: --plausible-site-id and --plausible-api-key are required for Plausible integration.\n'
    );
    return;
  }

  process.stderr.write('Fetching Plausible Analytics data...\n');

  const crawledUrls = pages.map(p => p.url);

  try {
    const plausibleRaw = await fetchPlausibleData({
      siteId: config.plausibleSiteId,
      apiKey: config.plausibleApiKey,
      days: config.plausibleDays,
      urls: crawledUrls,
      host: config.plausibleHost || undefined,
    });

    let matchCount = 0;
    for (const page of pages) {
      const data = plausibleRaw.get(page.url);
      if (data) {
        page.plausibleData = data;
        matchCount++;
      }
    }

    process.stderr.write(
      `Plausible data merged: ${matchCount}/${pages.length} pages matched ` +
      `(${plausibleRaw.size} paths from Plausible, last ${config.plausibleDays} days)\n`
    );
  } catch (err) {
    process.stderr.write(`Warning: Plausible data fetch failed: ${formatError(err)}\n`);
  }
}

// Generate sitemap from existing JSONL crawl output
program
  .command('generate-sitemap')
  .description('Generate XML sitemap(s) from a JSONL crawl output file')
  .argument('<input>', 'Input JSONL crawl file')
  .option('-o, --output <path>', 'Output sitemap file path', 'sitemap.xml')
  .option('--changefreq <freq>', 'Default changefreq (daily, weekly, monthly, etc.)')
  .option('--priority <value>', 'Default priority (0.0-1.0)')
  .action(async (inputFile: string, opts: { output: string; changefreq?: string; priority?: string }) => {
    const inputPath = resolve(inputFile);
    try {
      accessSync(inputPath, constants.R_OK);
    } catch {
      console.error(`Error: Cannot read input file "${inputPath}"`);
      process.exit(1);
    }

    // Read JSONL and parse pages
    const lines = readFileSync(inputPath, 'utf-8').split('\n').filter(Boolean);
    const pages: import('./types.js').PageData[] = [];
    let skippedLines = 0;
    for (const line of lines) {
      try { pages.push(JSON.parse(line)); } catch { skippedLines++; }
    }

    if (pages.length === 0) {
      console.error('Error: No valid pages found in input file.');
      process.exit(1);
    }

    process.stderr.write(`Read ${pages.length} pages from ${inputPath}\n`);
    if (skippedLines > 0) {
      process.stderr.write(`Warning: Skipped ${skippedLines} malformed line(s)\n`);
    }

    // Validate options
    if (opts.changefreq && !VALID_CHANGEFREQ_VALUES.has(opts.changefreq)) {
      console.error(`Error: Invalid --changefreq "${opts.changefreq}". Valid values: ${[...VALID_CHANGEFREQ_VALUES].join(', ')}`);
      process.exit(1);
    }
    if (opts.priority) {
      const pVal = parseFloat(opts.priority);
      if (Number.isNaN(pVal) || pVal < 0 || pVal > 1) {
        console.error(`Error: Invalid --priority "${opts.priority}". Must be between 0.0 and 1.0`);
        process.exit(1);
      }
    }

    const sitemapGenOpts: SitemapGenOptions = {
      changefreq: opts.changefreq,
      priority: opts.priority,
    };

    // Determine base URL from first page
    let baseUrl = '';
    try { baseUrl = new URL(pages[0].finalUrl || pages[0].url).origin; } catch { /* */ }

    const parsed = parsePath(resolve(opts.output));
    const baseName = parsed.name;
    const multiResult = generateMultiSitemap(pages, baseUrl, baseName, sitemapGenOpts);
    writeMultiSitemapFiles(multiResult, parsed, baseName);
  });

program.parse();
