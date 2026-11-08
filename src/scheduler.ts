/**
 * Crawl scheduler — runs crawls on a cron schedule.
 * Designed to run as a foreground process (in terminal, tmux, screen, or systemd).
 */

import { writeFileSync, readFileSync, mkdirSync, readdirSync, renameSync, existsSync, openSync, closeSync, statSync, unlinkSync } from 'node:fs';
import { resolve, join, parse as parsePath, format as formatPath } from 'node:path';
import { fileURLToPath } from 'node:url';
import { homedir } from 'node:os';
import { createHash, randomBytes } from 'node:crypto';
import { parseCron, nextRun, describeCron, type CronExpression } from './cron-parser.js';
import { sendWebhook, type WebhookPayload, type WebhookOptions } from './webhook.js';
import { redactUrl } from './utils.js';

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
  const path = join(getScheduleDir(), `${state.id}.json`);
  // Atomic write: temp file then rename. The temp name is unique per write,
  // so two scheduler processes never truncate or rename each other's file.
  const tmpPath = `${path}.${process.pid}.${randomBytes(4).toString('hex')}.tmp`;
  try {
    writeFileSync(tmpPath, JSON.stringify(state, null, 2), { encoding: 'utf-8', flag: 'wx' });
    renameSync(tmpPath, path);
  } catch (err) {
    try { unlinkSync(tmpPath); } catch { /* not created or already renamed */ }
    throw err;
  }
}

/** A lock older than this belongs to a process that died while holding it. */
const STATE_LOCK_STALE_MS = 30_000;
/** Longest wait for another process to release the lock. */
const STATE_LOCK_WAIT_MS = 10_000;

function sleepSync(ms: number): void {
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
}

/** Runs fn while holding `<id>.lock` (exclusive create), shared by every scheduler process. */
function withStateLock<T>(id: string, fn: () => T): T {
  const lockPath = join(getScheduleDir(), `${id}.lock`);
  const deadline = Date.now() + STATE_LOCK_WAIT_MS;
  for (;;) {
    try {
      closeSync(openSync(lockPath, 'wx'));
      break;
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== 'EEXIST') throw err;
      try {
        if (Date.now() - statSync(lockPath).mtimeMs > STATE_LOCK_STALE_MS) {
          unlinkSync(lockPath);
          continue;
        }
      } catch {
        continue; // released between the open and the stat
      }
      if (Date.now() > deadline) throw new Error(`schedule state ${id} is locked by another process`);
      sleepSync(5 + Math.floor(Math.random() * 10));
    }
  }
  try {
    return fn();
  } finally {
    try { unlinkSync(lockPath); } catch { /* already gone */ }
  }
}

/**
 * Read-modify-write of a schedule's state under the cross-process lock: the
 * change is applied to the state on disk (or `initial()` when there is none),
 * so concurrent scheduler processes never lose each other's updates.
 */
export function updateScheduleState(
  id: string,
  initial: () => ScheduleState,
  mutate: (state: ScheduleState) => void,
): ScheduleState {
  return withStateLock(id, () => {
    const state = loadState(id) ?? initial();
    mutate(state);
    saveState(state);
    return state;
  });
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
 * Build a unique output filename for a scheduled crawl: host, schedule id,
 * timestamp to the millisecond and a random suffix, so two schedules (or two
 * scheduler processes) starting in the same minute never share a file. An
 * existing file (or its HTML report) is never reused.
 */
export function buildOutputFilename(
  url: string,
  outputDir: string,
  format: 'jsonl' | 'csv',
  id: string = '',
  now: Date = new Date(),
): string {
  const hostname = (() => {
    try {
      return new URL(url).hostname.replace(/\./g, '-');
    } catch {
      return 'crawl';
    }
  })();
  const ts = now.toISOString().replace(/[:.]/g, '-').replace(/Z$/, '');
  const ext = format === 'csv' ? 'csv' : 'jsonl';
  const prefix = id && !id.startsWith(hostname) ? `${hostname}-${id}` : (id || hostname);
  for (;;) {
    const name = `${prefix}-${ts}-${randomBytes(3).toString('hex')}`;
    const path = join(outputDir, `${name}.${ext}`);
    if (!existsSync(path) && !existsSync(join(outputDir, `${name}.html`))) return path;
  }
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
  const freshState = (): ScheduleState => ({
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
  });

  // Calculate initial next run
  const nextRunDate = nextRun(cron);
  const applyStart = (s: ScheduleState) => {
    s.nextRun = nextRunDate.toISOString();
    s.outputDir = outputDir;
  };
  let state: ScheduleState;
  try {
    state = updateScheduleState(id, freshState, applyStart);
  } catch (err) {
    // A state file problem must not stop the scheduler: keep going in memory
    process.stderr.write(`Warning: cannot save schedule state: ${(err as Error).message}\n`);
    state = loadState(id) ?? freshState();
    applyStart(state);
  }

  const description = describeCron(cronExpr);
  process.stderr.write(`\nSchedule: ${description}\n`);
  // Logs may be kept for long (systemd journal): never print credentials.
  process.stderr.write(`  URL:      ${redactUrl(url)}\n`);
  process.stderr.write(`  Cron:     ${cronExpr}\n`);
  process.stderr.write(`  Output:   ${resolve(outputDir)}/\n`);
  if (webhook) {
    process.stderr.write(`  Webhook:  ${redactUrl(webhook.url, 'origin')}\n`);
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
    const outputFile = buildOutputFilename(url, outputDir, isCsv ? 'csv' : 'jsonl', id);

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
    const applyRun = (s: ScheduleState) => {
      s.lastRun = runTimestamp;
      s.lastStatus = status as 'success' | 'failed';
      s.lastPages = pages;
      s.lastDurationMs = durationMs;
      s.nextRun = nextRunAfter.toISOString();
      s.totalRuns++;
    };
    try {
      const current = state;
      state = updateScheduleState(id, () => current, applyRun);
    } catch (err) {
      applyRun(state);
      process.stderr.write(`Warning: cannot save schedule state: ${(err as Error).message}\n`);
    }

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
