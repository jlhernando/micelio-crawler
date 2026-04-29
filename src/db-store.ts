/**
 * SQLite-backed crawl storage for Database Storage Mode.
 *
 * Replaces in-memory arrays when --db flag is used.
 * Enables crawling millions of URLs without memory limits
 * and resuming interrupted crawls.
 */

import Database from 'better-sqlite3';
import type { PageData, CrawlConfig, QueueEntry } from './types.js';

export class CrawlStore {
  private db: Database.Database;

  // Prepared statements (cached for performance)
  private stmtInsertPage!: Database.Statement;
  private stmtUpdatePage!: Database.Statement;
  private stmtGetPage!: Database.Statement;
  private stmtGetAllPages!: Database.Statement;
  private stmtPageCount!: Database.Statement;
  private stmtMarkVisited!: Database.Statement;
  private stmtHasSeen!: Database.Statement;
  private stmtGetVisitedUrls!: Database.Statement;
  private stmtEnqueue!: Database.Statement;
  private stmtDequeue!: Database.Statement;
  private stmtPendingCount!: Database.Statement;
  private stmtTotalSeen!: Database.Statement;
  private stmtSetMeta!: Database.Statement;
  private stmtGetMeta!: Database.Statement;

  constructor(dbPath: string) {
    this.db = new Database(dbPath);

    // Enable WAL mode for better concurrent read/write performance
    this.db.pragma('journal_mode = WAL');
    this.db.pragma('synchronous = NORMAL');

    this.createTables();
    this.prepareStatements();
  }

  private createTables(): void {
    this.db.exec(`
      CREATE TABLE IF NOT EXISTS pages (
        url TEXT PRIMARY KEY,
        data TEXT NOT NULL,
        status_code INTEGER NOT NULL DEFAULT 0,
        crawled_at TEXT
      );

      CREATE TABLE IF NOT EXISTS queue (
        url TEXT PRIMARY KEY,
        depth INTEGER NOT NULL DEFAULT 0,
        referrer TEXT,
        status TEXT NOT NULL DEFAULT 'pending'
      );
      CREATE INDEX IF NOT EXISTS idx_queue_status ON queue(status);

      CREATE TABLE IF NOT EXISTS crawl_meta (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
      );
    `);
  }

  private prepareStatements(): void {
    this.stmtInsertPage = this.db.prepare(
      'INSERT OR REPLACE INTO pages (url, data, status_code, crawled_at) VALUES (?, ?, ?, ?)',
    );
    this.stmtUpdatePage = this.db.prepare(
      'UPDATE pages SET data = ?, status_code = ? WHERE url = ?',
    );
    this.stmtGetPage = this.db.prepare('SELECT data FROM pages WHERE url = ?');
    this.stmtGetAllPages = this.db.prepare('SELECT data FROM pages ORDER BY rowid');
    this.stmtPageCount = this.db.prepare('SELECT COUNT(*) as count FROM pages');
    this.stmtMarkVisited = this.db.prepare(
      `INSERT INTO queue (url, depth, referrer, status) VALUES (?, ?, ?, 'visited')
       ON CONFLICT(url) DO UPDATE SET status = 'visited'`,
    );
    this.stmtHasSeen = this.db.prepare('SELECT 1 FROM queue WHERE url = ?');
    this.stmtGetVisitedUrls = this.db.prepare('SELECT url FROM queue');
    this.stmtEnqueue = this.db.prepare(
      `INSERT INTO queue (url, depth, referrer, status) VALUES (?, ?, ?, 'pending')
       ON CONFLICT(url) DO NOTHING`,
    );
    this.stmtDequeue = this.db.prepare(
      `SELECT url, depth, referrer FROM queue WHERE status = 'pending' ORDER BY rowid LIMIT 1`,
    );
    this.stmtPendingCount = this.db.prepare(
      `SELECT COUNT(*) as count FROM queue WHERE status = 'pending'`,
    );
    this.stmtTotalSeen = this.db.prepare('SELECT COUNT(*) as count FROM queue');
    this.stmtSetMeta = this.db.prepare(
      'INSERT OR REPLACE INTO crawl_meta (key, value) VALUES (?, ?)',
    );
    this.stmtGetMeta = this.db.prepare('SELECT value FROM crawl_meta WHERE key = ?');
  }

  // ── Page Storage ──────────────────────────────────────────

  insertPage(page: PageData): void {
    this.stmtInsertPage.run(
      page.url,
      JSON.stringify(page),
      page.statusCode,
      page.crawledAt,
    );
  }

  updatePage(url: string, page: PageData): void {
    this.stmtUpdatePage.run(JSON.stringify(page), page.statusCode, url);
  }

  getPage(url: string): PageData | null {
    const row = this.stmtGetPage.get(url) as { data: string } | undefined;
    return row ? JSON.parse(row.data) as PageData : null;
  }

  getAllPages(): PageData[] {
    const rows = this.stmtGetAllPages.all() as { data: string }[];
    return rows.map((r) => JSON.parse(r.data) as PageData);
  }

  getPageCount(): number {
    return (this.stmtPageCount.get() as { count: number }).count;
  }

  // ── Queue Persistence ─────────────────────────────────────

  /**
   * Mark a URL as visited in the queue (used by CrawlQueue replacement).
   */
  markVisited(url: string, depth: number, referrer: string | null = null): void {
    this.stmtMarkVisited.run(url, depth, referrer);
  }

  /**
   * Check if a URL has been seen (either pending or visited).
   */
  hasSeen(url: string): boolean {
    return this.stmtHasSeen.get(url) !== undefined;
  }

  /**
   * Get all URLs that have been seen (for pre-populating the in-memory queue on resume).
   */
  getVisitedUrls(): string[] {
    const rows = this.stmtGetVisitedUrls.all() as { url: string }[];
    return rows.map(r => r.url);
  }

  /**
   * Add a URL to the pending queue (no-op if already seen).
   */
  enqueue(url: string, depth: number, referrer: string | null = null): boolean {
    const result = this.stmtEnqueue.run(url, depth, referrer);
    return result.changes > 0;
  }

  /**
   * Dequeue the next pending URL and mark it as visited (atomic transaction).
   */
  dequeue(): QueueEntry | undefined {
    return this.db.transaction(() => {
      const row = this.stmtDequeue.get() as { url: string; depth: number; referrer: string | null } | undefined;
      if (!row) return undefined;
      this.stmtMarkVisited.run(row.url, row.depth, row.referrer);
      return { url: row.url, depth: row.depth, referrer: row.referrer };
    })();
  }

  /**
   * Number of pending URLs in the queue.
   */
  get pendingCount(): number {
    return (this.stmtPendingCount.get() as { count: number }).count;
  }

  /**
   * Total URLs seen (pending + visited).
   */
  get totalSeen(): number {
    return (this.stmtTotalSeen.get() as { count: number }).count;
  }

  // ── Crawl Metadata ────────────────────────────────────────

  saveCrawlMeta(config: CrawlConfig, startTime: number): void {
    this.stmtSetMeta.run('config', JSON.stringify(config));
    this.stmtSetMeta.run('start_time', String(startTime));
    this.stmtSetMeta.run('status', 'running');
  }

  markCrawlComplete(): void {
    this.stmtSetMeta.run('status', 'complete');
  }

  getCrawlStatus(): string | null {
    const row = this.stmtGetMeta.get('status') as { value: string } | undefined;
    return row ? row.value : null;
  }

  getCrawlMeta(): { config: CrawlConfig; startTime: number } | null {
    const configRow = this.stmtGetMeta.get('config') as { value: string } | undefined;
    const startRow = this.stmtGetMeta.get('start_time') as { value: string } | undefined;
    if (!configRow || !startRow) return null;
    return {
      config: JSON.parse(configRow.value) as CrawlConfig,
      startTime: Number(startRow.value),
    };
  }

  // ── Lifecycle ─────────────────────────────────────────────

  close(): void {
    this.db.close();
  }

  /**
   * Batch update pages (wrapped in a transaction for performance).
   * Used for post-crawl enrichment (sitemap, page weight, GSC, GA4).
   */
  updatePages(pages: PageData[]): void {
    const update = this.db.transaction((pgs: PageData[]) => {
      for (const page of pgs) {
        this.stmtUpdatePage.run(JSON.stringify(page), page.statusCode, page.url);
      }
    });
    update(pages);
  }
}
