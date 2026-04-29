/**
 * Crawl scheduler — runs crawls on a cron schedule.
 * Designed to run as a foreground process (in terminal, tmux, screen, or systemd).
 */

import { writeFileSync, readFileSync, mkdirSync, readdirSync, renameSync } from 'node:fs';
import { resolve, join, parse as parsePath, format as formatPath } from 'node:path';
import { fileURLToPath } from 'node:url';
import { homedir } from 'node:os';
import { createHash } from 'node:crypto';
import { parseCron, nextRun, describeCron, type CronExpression } from './cron-parser.js';
import { sendWebhook, type WebhookPayload, type WebhookOptions } from './webhook.js';

// ── Schedule state ──────────────────────────────────────

export interface ScheduleState {
  id: string;
  url: string;
  cron: string;
  createdAt: string;
  lastRun: string | null;
  lastStatus: 'success' | 'failed' | null;
  lastPages: number;
  lastDurationMs: number;
  nextRun: string;
  totalRuns: number;
  outputDir: string;
}

function getScheduleDir(): string {
  const dir = join(homedir(), '.micelio', 'schedules');
  mkdirSync(dir, { recursive: true });
  return dir;
}

function scheduleId(url: string, cron: string): string {
  const hostname = (() => {
    try {
      return new URL(url).hostname.replace(/\./g, '-');
    } catch {
      return 'unknown';
    }
  })();
  const hash = createHash('md5').update(`${url}:${cron}`).digest('hex').substring(0, 6);
  return `${hostname}-${hash}`;
}

function saveState(state: ScheduleState): void {
  const dir = getScheduleDir();
  const path = join(dir, `${state.id}.json`);
  const tmpPath = path + '.tmp';
  // Atomic write: write to temp file then rename to prevent corruption on crash
  writeFileSync(tmpPath, JSON.stringify(state, null, 2), 'utf-8');
  renameSync(tmpPath, path);
}

function loadState(id: string): ScheduleState | null {
  const path = join(getScheduleDir(), `${id}.json`);
  try {
    return JSON.parse(readFileSync(path, 'utf-8'));
  } catch {
    return null;
  }
}

export function listSchedules(): ScheduleState[] {
  const dir = getScheduleDir();
  const files = readdirSync(dir).filter((f) => f.endsWith('.json'));
  const schedules: ScheduleState[] = [];
  for (const file of files) {
    try {
      const state = JSON.parse(readFileSync(join(dir, file), 'utf-8'));
      schedules.push(state);
    } catch {
      // skip corrupt files
    }
  }
  return schedules.sort((a, b) => (a.nextRun || '').localeCompare(b.nextRun || ''));
}

// ── Schedule runner ─────────────────────────────────────

export interface ScheduleConfig {
  url: string;
  cron: string;
  maxRuns: number; // 0 = unlimited
  outputDir: string;
  webhook?: WebhookOptions;
  /** All crawl options to pass through to the crawl function */
  crawlArgs: string[];
}

/**
 * Build a timestamped output filename for a scheduled crawl.
 */
function buildOutputFilename(url: string, outputDir: string, format: 'jsonl' | 'csv'): string {
  const hostname = (() => {
    try {
      return new URL(url).hostname.replace(/\./g, '-');
    } catch {
      return 'crawl';
    }
  })();
  const now = new Date();
  const ts = now.toISOString().replace(/[:.]/g, '-').substring(0, 16);
  const ext = format === 'csv' ? 'csv' : 'jsonl';
  return join(outputDir, `${hostname}-${ts}.${ext}`);
}

/**
 * Run the schedule loop. This blocks until max runs are reached or SIGINT/SIGTERM.
 */
export async function runSchedule(config: ScheduleConfig): Promise<void> {
  const { url, cron: cronExpr, maxRuns, outputDir, webhook, crawlArgs } = config;

  // Parse cron expression
  let cron: CronExpression;
  try {
    cron = parseCron(cronExpr);
  } catch (err) {
    process.stderr.write(`Error: ${(err as Error).message}\n`);
    process.exit(1);
  }

  // Ensure output directory exists
  mkdirSync(outputDir, { recursive: true });

  // Initialize or load state
  const id = scheduleId(url, cronExpr);
  let state: ScheduleState = loadState(id) || {
    id,
    url,
    cron: cronExpr,
    createdAt: new Date().toISOString(),
    lastRun: null,
    lastStatus: null,
    lastPages: 0,
    lastDurationMs: 0,
    nextRun: '',
    totalRuns: 0,
    outputDir,
  };

  // Calculate initial next run
  const nextRunDate = nextRun(cron);
  state.nextRun = nextRunDate.toISOString();
  state.outputDir = outputDir;
  saveState(state);

  const description = describeCron(cronExpr);
  process.stderr.write(`\nSchedule: ${description}\n`);
  process.stderr.write(`  URL:      ${url}\n`);
  process.stderr.write(`  Cron:     ${cronExpr}\n`);
  process.stderr.write(`  Output:   ${resolve(outputDir)}/\n`);
  if (webhook) {
    process.stderr.write(`  Webhook:  ${webhook.url}\n`);
  }
  if (maxRuns > 0) {
    process.stderr.write(`  Max runs: ${maxRuns}\n`);
  }
  process.stderr.write(`  Next run: ${nextRunDate.toLocaleString()}\n`);
  process.stderr.write(`\nWaiting for next scheduled run... (Ctrl+C to stop)\n\n`);

  // Graceful shutdown
  let stopping = false;
  let currentlyCrawling = false;

  const shutdown = () => {
    if (currentlyCrawling) {
      process.stderr.write('\nShutdown requested — waiting for current crawl to finish...\n');
      stopping = true;
    } else {
      process.stderr.write('\nScheduler stopped.\n');
      process.exit(0);
    }
  };

  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);

  // Schedule loop — resume from persisted run count so --max-runs works across restarts
  let runCount = state.totalRuns;

  while (true) {
    if (stopping) break;
    if (maxRuns > 0 && runCount >= maxRuns) {
      process.stderr.write(`Reached max runs (${maxRuns}). Scheduler stopping.\n`);
      break;
    }

    // Wait until next run time
    const now = Date.now();
    const nextRunTime = new Date(state.nextRun).getTime();
    const waitMs = nextRunTime - now;

    if (waitMs > 0) {
      // Sleep in small increments to allow graceful shutdown
      const sleepChunk = 5000; // 5 seconds
      let remaining = waitMs;
      while (remaining > 0 && !stopping) {
        const sleepTime = Math.min(remaining, sleepChunk);
        await new Promise((r) => setTimeout(r, sleepTime));
        remaining -= sleepTime;
      }
      if (stopping) break;
    }

    // Time to crawl
    runCount++;
    currentlyCrawling = true;
    const runStart = Date.now();
    const runTimestamp = new Date().toISOString();

    process.stderr.write(`\n${'='.repeat(60)}\n`);
    process.stderr.write(`Scheduled run #${runCount} starting at ${new Date().toLocaleString()}\n`);
    process.stderr.write(`${'='.repeat(60)}\n\n`);

    // Determine output format from crawl args
    const isCsv = crawlArgs.includes('--csv');
    const outputFile = buildOutputFilename(url, outputDir, isCsv ? 'csv' : 'jsonl');

    // Build the crawl command arguments
    // The schedule command passes through all spider options; we just override the output path
    const childArgs = [...crawlArgs, '-o', outputFile];

    // Also generate HTML report with timestamped name
    const hasHtml = crawlArgs.includes('--html');
    if (hasHtml) {
      // HTML report auto-generates from output file path (same name, .html extension)
    }

    // Run crawl as child process to isolate memory
    let exitCode = 0;
    let pages = 0;

    try {
      const result = await runCrawlProcess(childArgs);
      exitCode = result.exitCode;
      pages = result.pages;
    } catch (err) {
      exitCode = 1;
      process.stderr.write(`\nCrawl error: ${(err as Error).message}\n`);
    }

    currentlyCrawling = false;
    const durationMs = Date.now() - runStart;
    const durationStr = (durationMs / 1000).toFixed(1) + 's';
    const status = exitCode === 0 ? 'success' : 'failed';

    process.stderr.write(`\nRun #${runCount} ${status} (${pages} pages, ${durationStr})\n`);
    process.stderr.write(`Output: ${outputFile}\n`);

    // Update state
    const nextRunAfter = nextRun(cron);
    state.lastRun = runTimestamp;
    state.lastStatus = status as 'success' | 'failed';
    state.lastPages = pages;
    state.lastDurationMs = durationMs;
    state.nextRun = nextRunAfter.toISOString();
    state.totalRuns++;
    saveState(state);

    // Send webhook
    if (webhook) {
      const htmlReportPath = hasHtml
        ? formatPath({ ...parsePath(outputFile), base: undefined, ext: '.html' })
        : undefined;

      const payload: WebhookPayload = {
        event: exitCode === 0 ? 'crawl_complete' : 'crawl_failed',
        url,
        timestamp: runTimestamp,
        duration: durationStr,
        pages,
        errors: exitCode === 0 ? 0 : 1,
        outputFile: resolve(outputFile),
        htmlReport: htmlReportPath ? resolve(htmlReportPath) : undefined,
        schedule: {
          cron: cronExpr,
          runNumber: runCount,
          nextRun: nextRunAfter.toISOString(),
        },
      };

      process.stderr.write(`Sending webhook notification...\n`);
      await sendWebhook(webhook, payload);
    }

    if (stopping) break;

    if (maxRuns > 0 && runCount >= maxRuns) {
      process.stderr.write(`\nReached max runs (${maxRuns}). Scheduler stopping.\n`);
      break;
    }

    process.stderr.write(`\nNext run: ${nextRunAfter.toLocaleString()}\n`);
    process.stderr.write(`Waiting... (Ctrl+C to stop)\n`);
  }

  process.stderr.write('Scheduler stopped.\n');
}

/**
 * Run a crawl as a child process and extract results.
 * This isolates crawl memory from the scheduler process.
 */
async function runCrawlProcess(
  args: string[],
): Promise<{ exitCode: number; pages: number }> {
  const { spawn } = await import('node:child_process');

  return new Promise((resolvePromise, reject) => {
    // Find the CLI entry point (use fileURLToPath for cross-platform safety)
    const thisFile = fileURLToPath(import.meta.url);
    const cliPath = thisFile.replace(/scheduler\.(ts|js)$/, 'cli.$1');

    // Use node to run the CLI with spider subcommand
    const child = spawn(process.execPath, [cliPath, ...args], {
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    let pages = 0;

    child.stdout?.on('data', () => {
      // discard stdout
    });

    child.stderr?.on('data', (chunk: Buffer) => {
      const text = chunk.toString();
      process.stderr.write(text);

      // Parse page count from crawl output: "Crawl complete: N pages"
      const match = text.match(/Crawl complete:\s+(\d+)\s+pages/);
      if (match) {
        pages = parseInt(match[1], 10);
      }
    });

    child.on('error', (err: Error) => {
      reject(err);
    });

    child.on('close', (code: number | null) => {
      resolvePromise({ exitCode: code ?? 1, pages });
    });
  });
}
