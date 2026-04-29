import type { QueueEntry } from './types.js';
import { normalizeUrl } from './utils.js';

export class CrawlQueue {
  private pending: QueueEntry[] = [];
  private visited = new Set<string>();
  private seedDomain: string;
  private maxDepth: number;
  private maxPages: number;
  private includePatterns: RegExp[];
  private excludePatterns: RegExp[];
  private enforceInternal: boolean;
  private allowedDomains: string[];

  constructor(
    seedUrl: string,
    maxDepth: number,
    maxPages: number,
    options: {
      includePatterns?: RegExp[];
      excludePatterns?: RegExp[];
      enforceInternal?: boolean;
      allowedDomains?: string[];
    } = {},
  ) {
    const parsed = new URL(seedUrl);
    this.seedDomain = parsed.hostname;
    this.maxDepth = maxDepth;
    this.maxPages = maxPages;
    this.includePatterns = options.includePatterns || [];
    this.excludePatterns = options.excludePatterns || [];
    this.enforceInternal = options.enforceInternal ?? true;
    this.allowedDomains = options.allowedDomains || [];
  }

  enqueue(url: string, depth: number, referrer: string | null = null): boolean {
    const normalized = this.normalize(url);
    if (!normalized) return false;
    if (this.visited.has(normalized)) return false;
    if (depth > this.maxDepth) return false;
    if (this.visited.size >= this.maxPages) return false;
    if (this.enforceInternal && !this.isInternalUrl(normalized)) return false;
    if (!this.matchesFilters(normalized)) return false;

    // Store the original URL for fetching; use normalized key only for dedup
    this.pending.push({ url, depth, referrer });
    this.visited.add(normalized);
    return true;
  }

  dequeue(): QueueEntry | undefined {
    return this.pending.shift();
  }

  has(url: string): boolean {
    const normalized = this.normalize(url);
    return normalized ? this.visited.has(normalized) : false;
  }

  get size(): number {
    return this.pending.length;
  }

  get totalSeen(): number {
    return this.visited.size;
  }

  markVisited(url: string): void {
    const normalized = this.normalize(url);
    if (normalized) this.visited.add(normalized);
  }

  /** Update the seed domain (e.g., after a www redirect) */
  updateSeedDomain(newDomain: string): void {
    this.seedDomain = newDomain;
  }

  getSeedDomain(): string {
    return this.seedDomain;
  }

  private matchesFilters(url: string): boolean {
    // If include patterns exist, URL must match at least one
    if (this.includePatterns.length > 0) {
      const matches = this.includePatterns.some((p) => p.test(url));
      if (!matches) return false;
    }
    // If exclude patterns exist, URL must not match any
    if (this.excludePatterns.length > 0) {
      const excluded = this.excludePatterns.some((p) => p.test(url));
      if (excluded) return false;
    }
    return true;
  }

  private normalize(url: string): string | null {
    return normalizeUrl(url);
  }

  private isInternalUrl(url: string): boolean {
    try {
      const parsed = new URL(url);
      if (parsed.hostname === this.seedDomain) return true;
      if (this.allowedDomains.length > 0) {
        return this.allowedDomains.some(d => parsed.hostname === d);
      }
      return false;
    } catch {
      return false;
    }
  }
}
