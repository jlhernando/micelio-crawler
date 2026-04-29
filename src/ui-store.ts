import Database from 'better-sqlite3';
import { mkdirSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { randomUUID } from 'node:crypto';

const UI_DIR = join(homedir(), '.micelio', 'ui');
const CRAWLS_DIR = join(UI_DIR, 'crawls');
const DB_PATH = join(UI_DIR, 'micelio-ui.db');

export interface CrawlJob {
  id: string;
  config: Record<string, unknown>;
  status: 'pending' | 'running' | 'paused' | 'completed' | 'failed' | 'cancelled';
  startedAt: string;
  completedAt: string | null;
  pageCount: number;
  errorCount: number;
  durationMs: number | null;
  seedUrl: string;
  mode: 'spider' | 'list' | 'sitemap';
  dbPath: string;
}

export interface SavedPreset {
  id: string;
  name: string;
  config: Record<string, unknown>;
  builtIn: boolean;
  createdAt: string;
  updatedAt: string;
}

export class UiStore {
  private db: Database.Database;

  constructor() {
    mkdirSync(CRAWLS_DIR, { recursive: true });
    this.db = new Database(DB_PATH);
    this.db.pragma('journal_mode = WAL');
    this.migrate();
    this.seedPresets();
  }

  private migrate(): void {
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS crawl_jobs (
        id TEXT PRIMARY KEY,
        config TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'pending',
        started_at TEXT NOT NULL,
        completed_at TEXT,
        page_count INTEGER NOT NULL DEFAULT 0,
        error_count INTEGER NOT NULL DEFAULT 0,
        duration_ms INTEGER,
        seed_url TEXT NOT NULL,
        mode TEXT NOT NULL DEFAULT 'spider',
        db_path TEXT NOT NULL
      );
      CREATE INDEX IF NOT EXISTS idx_crawl_jobs_status ON crawl_jobs(status);
      CREATE INDEX IF NOT EXISTS idx_crawl_jobs_started ON crawl_jobs(started_at);

      CREATE TABLE IF NOT EXISTS presets (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL UNIQUE,
        config TEXT NOT NULL,
        built_in INTEGER NOT NULL DEFAULT 0,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
      );

      CREATE TABLE IF NOT EXISTS settings (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
      );
    `);
  }

  private seedPresets(): void {
    const now = new Date().toISOString();
    const builtIn = [
      { name: 'Quick Scan', config: { depth: 1, limit: 100, concurrency: 3, htmlReport: true } },
      { name: 'Full Audit', config: { depth: 10, limit: 100000, concurrency: 3, psi: true, ngrams: true, embeddings: true, linkIntelligence: true, liMaxSuggestions: 100, checkExternal: true, pageWeight: true, sitemapOut: true, htmlReport: true } },
      { name: 'Content Audit', config: { depth: 3, limit: 2000, concurrency: 3, ngrams: true, embeddings: true, htmlReport: true } },
      { name: 'Technical Audit', config: { depth: 3, limit: 2000, concurrency: 3, checkExternal: true, linkIntelligence: true, pageWeight: true, htmlReport: true } },
      { name: 'Internal Linking Audit', config: { depth: 10, limit: 50000, concurrency: 3, linkIntelligence: true, liMaxSuggestions: 200, embeddings: true, ngrams: true, sitemapOut: true, checkExternal: true, htmlReport: true } },
    ];

    // INSERT OR IGNORE ensures we only add missing presets (unique name constraint)
    const insert = this.db.prepare(
      'INSERT OR IGNORE INTO presets (id, name, config, built_in, created_at, updated_at) VALUES (?, ?, ?, 1, ?, ?)',
    );
    for (const p of builtIn) {
      insert.run(randomUUID(), p.name, JSON.stringify(p.config), now, now);
    }
  }

  // ── Crawl Jobs ──

  createCrawlJob(config: Record<string, unknown>): CrawlJob {
    const id = randomUUID();
    const now = new Date().toISOString();
    const seedUrl = (config.seedUrl as string) || (config.sitemapUrls as string[])?.[0] || 'list-mode';
    const mode = (config.mode as string) || 'spider';
    const dbPath = join(CRAWLS_DIR, `${id}.db`);

    this.db.prepare(
      'INSERT INTO crawl_jobs (id, config, status, started_at, seed_url, mode, db_path) VALUES (?, ?, ?, ?, ?, ?, ?)',
    ).run(id, JSON.stringify(config), 'pending', now, seedUrl, mode, dbPath);

    return { id, config, status: 'pending', startedAt: now, completedAt: null, pageCount: 0, errorCount: 0, durationMs: null, seedUrl, mode: mode as CrawlJob['mode'], dbPath };
  }

  getCrawlJob(id: string): CrawlJob | null {
    const row = this.db.prepare('SELECT * FROM crawl_jobs WHERE id = ?').get(id) as Record<string, unknown> | undefined;
    if (!row) return null;
    return this.rowToJob(row);
  }

  listCrawlJobs(): CrawlJob[] {
    const rows = this.db.prepare('SELECT * FROM crawl_jobs ORDER BY started_at DESC').all() as Record<string, unknown>[];
    return rows.map(r => this.rowToJob(r));
  }

  updateCrawlJob(id: string, updates: Partial<Pick<CrawlJob, 'status' | 'pageCount' | 'errorCount' | 'durationMs' | 'completedAt'>>): void {
    const sets: string[] = [];
    const values: unknown[] = [];

    if (updates.status !== undefined) { sets.push('status = ?'); values.push(updates.status); }
    if (updates.pageCount !== undefined) { sets.push('page_count = ?'); values.push(updates.pageCount); }
    if (updates.errorCount !== undefined) { sets.push('error_count = ?'); values.push(updates.errorCount); }
    if (updates.durationMs !== undefined) { sets.push('duration_ms = ?'); values.push(updates.durationMs); }
    if (updates.completedAt !== undefined) { sets.push('completed_at = ?'); values.push(updates.completedAt); }

    if (sets.length === 0) return;
    values.push(id);
    this.db.prepare(`UPDATE crawl_jobs SET ${sets.join(', ')} WHERE id = ?`).run(...values);
  }

  deleteCrawlJob(id: string): boolean {
    const result = this.db.prepare('DELETE FROM crawl_jobs WHERE id = ?').run(id);
    return result.changes > 0;
  }

  private rowToJob(row: Record<string, unknown>): CrawlJob {
    return {
      id: row.id as string,
      config: JSON.parse(row.config as string),
      status: row.status as CrawlJob['status'],
      startedAt: row.started_at as string,
      completedAt: (row.completed_at as string) || null,
      pageCount: row.page_count as number,
      errorCount: row.error_count as number,
      durationMs: (row.duration_ms as number) || null,
      seedUrl: row.seed_url as string,
      mode: row.mode as CrawlJob['mode'],
      dbPath: row.db_path as string,
    };
  }

  // ── Presets ──

  listPresets(): SavedPreset[] {
    const rows = this.db.prepare('SELECT * FROM presets ORDER BY built_in DESC, name ASC').all() as Record<string, unknown>[];
    return rows.map(r => ({
      id: r.id as string,
      name: r.name as string,
      config: JSON.parse(r.config as string),
      builtIn: (r.built_in as number) === 1,
      createdAt: r.created_at as string,
      updatedAt: r.updated_at as string,
    }));
  }

  savePreset(name: string, config: Record<string, unknown>): SavedPreset {
    const id = randomUUID();
    const now = new Date().toISOString();
    this.db.prepare(
      'INSERT INTO presets (id, name, config, built_in, created_at, updated_at) VALUES (?, ?, ?, 0, ?, ?)',
    ).run(id, name, JSON.stringify(config), now, now);
    return { id, name, config, builtIn: false, createdAt: now, updatedAt: now };
  }

  deletePreset(id: string): boolean {
    const result = this.db.prepare('DELETE FROM presets WHERE id = ? AND built_in = 0').run(id);
    return result.changes > 0;
  }

  // ── Settings ──

  getSetting(key: string): string | null {
    const row = this.db.prepare('SELECT value FROM settings WHERE key = ?').get(key) as { value: string } | undefined;
    return row?.value ?? null;
  }

  setSetting(key: string, value: string): void {
    this.db.prepare('INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)').run(key, value);
  }

  getSettings(): Record<string, unknown> {
    const rows = this.db.prepare('SELECT * FROM settings').all() as { key: string; value: string }[];
    const result: Record<string, unknown> = {};
    for (const row of rows) {
      try { result[row.key] = JSON.parse(row.value); } catch { result[row.key] = row.value; }
    }
    return result;
  }

  updateSettings(settings: Record<string, unknown>): void {
    const stmt = this.db.prepare('INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)');
    for (const [key, value] of Object.entries(settings)) {
      stmt.run(key, JSON.stringify(value));
    }
  }

  close(): void {
    this.db.close();
  }
}
