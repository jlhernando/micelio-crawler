import robotsParser from 'robots-parser';
import { request } from 'undici';
import * as cheerio from 'cheerio';
import type { SitemapEntry, SitemapType, NewsSitemapEntry, VideoSitemapEntry, ImageSitemapEntry } from './types.js';
import { normalizeUrl, isPrivateUrl } from './utils.js';

interface Robot {
  isAllowed(url: string, ua?: string): boolean | undefined;
  getCrawlDelay(ua?: string): number | undefined;
  getSitemaps(): string[];
}

export class RobotsChecker {
  private robots: Robot | null = null;
  private sitemapUrls: string[] = [];

  setSitemapUrls(urls: string[]): void {
    this.sitemapUrls = urls;
  }

  async init(seedUrl: string, userAgent: string): Promise<void> {
    const base = new URL(seedUrl);
    const robotsUrl = `${base.protocol}//${base.host}/robots.txt`;

    try {
      const { statusCode, body } = await request(robotsUrl, {
        headers: { 'User-Agent': userAgent },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      } as any);
      if (statusCode === 200) {
        const text = await body.text();
        this.robots = (robotsParser as unknown as (url: string, txt: string) => Robot)(robotsUrl, text);
        this.sitemapUrls = this.extractSitemapUrls(text);
      } else {
        await body.dump();
      }
    } catch {
      // robots.txt not available — allow all
    }
  }

  isAllowed(url: string, userAgent?: string): boolean {
    if (!this.robots) return true;
    return this.robots.isAllowed(url, userAgent) ?? true;
  }

  // 1.6: Get crawl-delay directive (in seconds) or null
  getCrawlDelay(userAgent?: string): number | null {
    if (!this.robots) return null;
    const delay = this.robots.getCrawlDelay(userAgent);
    return delay !== undefined ? delay : null;
  }

  // Phase 6: Store full sitemap entries for audit
  private sitemapEntries: SitemapEntry[] = [];
  private sitemapParseErrors: string[] = [];
  private sitemapValidationWarnings: string[] = [];

  async getSitemapUrls(userAgent: string): Promise<string[]> {
    const entries = await this.getSitemapEntries(userAgent);
    return entries.map((e) => e.url);
  }

  async getSitemapEntries(userAgent: string): Promise<SitemapEntry[]> {
    if (this.sitemapEntries.length > 0) return this.sitemapEntries;

    const entries: SitemapEntry[] = [];
    const errors: string[] = [];
    const warnings: string[] = [];
    const visited = new Set<string>();

    const VALID_CHANGEFREQ = new Set(['always', 'hourly', 'daily', 'weekly', 'monthly', 'yearly', 'never']);

    const parseSitemap = async (sitemapUrl: string): Promise<void> => {
      if (visited.has(sitemapUrl)) return;
      visited.add(sitemapUrl);

      try {
        // Follow redirects manually for sitemap fetching
        let currentSitemapUrl = sitemapUrl;
        let sitemapRedirects = 0;
        let sitemapStatusCode = 0;
        let sitemapText = '';
        while (sitemapRedirects < 5) {
          const resp = await request(currentSitemapUrl, {
            headers: { 'User-Agent': userAgent },
            maxRedirections: 0,
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          } as any);
          sitemapStatusCode = resp.statusCode;
          if (sitemapStatusCode >= 300 && sitemapStatusCode < 400) {
            const location = resp.headers.location as string | undefined;
            await resp.body.dump();
            if (location) {
              const redirectTarget = new URL(location, currentSitemapUrl).toString();
              if (isPrivateUrl(redirectTarget)) {
                errors.push(`Sitemap ${sitemapUrl} redirected to private address (blocked)`);
                return;
              }
              currentSitemapUrl = redirectTarget;
              sitemapRedirects++;
              continue;
            }
          }
          if (sitemapStatusCode === 200) {
            sitemapText = await resp.body.text();
          } else {
            await resp.body.dump();
          }
          break;
        }
        if (sitemapStatusCode !== 200) {
          errors.push(`Sitemap ${sitemapUrl} returned ${sitemapStatusCode}`);
          return;
        }

        const text = sitemapText;

        // Validate size limit: 50MB
        if (Buffer.byteLength(text, 'utf-8') > 50 * 1024 * 1024) {
          errors.push(`Sitemap ${sitemapUrl} exceeds 50MB size limit`);
        }

        const $ = cheerio.load(text, { xmlMode: true });

        // Phase 6.2: Validate XML namespace
        const root = $.root().children().first();
        const rootTag = root.prop('tagName')?.toLowerCase();
        const rootAttrs = root.attr() || {};
        if (rootTag === 'urlset') {
          const xmlns = rootAttrs['xmlns'] || '';
          if (!xmlns.includes('sitemaps.org/schemas/sitemap/')) {
            warnings.push(`Sitemap ${sitemapUrl}: missing or invalid xmlns namespace`);
          }
        }

        // Check for sitemap index
        const nestedSitemaps = $('sitemap > loc').map((_, el) => $(el).text().trim()).get();
        for (const nested of nestedSitemaps) {
          await parseSitemap(nested);
        }

        // Count URLs for limit validation
        const urlElements = $('url');
        if (urlElements.length > 50000) {
          errors.push(`Sitemap ${sitemapUrl} exceeds 50,000 URL limit (${urlElements.length} URLs)`);
        }

        // Detect sitemap extension namespaces from root element attributes
        const attrValues = Object.values(rootAttrs).join(' ');
        const hasNewsNs = attrValues.includes('sitemap-news') || $('news\\:news').length > 0;
        const hasVideoNs = attrValues.includes('sitemap-video') || $('video\\:video').length > 0;
        const hasImageNs = attrValues.includes('sitemap-image') || $('image\\:image').length > 0;

        urlElements.each((_, el) => {
          const loc = $(el).find('> loc').text().trim();
          if (!loc) return;

          // Parse news extension: <news:news>
          let newsEntries: NewsSitemapEntry[] | undefined;
          if (hasNewsNs) {
            const newsEls = $(el).find('news\\:news, news');
            if (newsEls.length > 0) {
              newsEntries = [];
              newsEls.each((__, newsEl) => {
                const pub = $(newsEl).find('news\\:publication, publication');
                newsEntries!.push({
                  title: $(newsEl).find('news\\:title, title').text().trim(),
                  publicationName: pub.find('news\\:name, name').text().trim(),
                  publicationLanguage: pub.find('news\\:language, language').text().trim(),
                  publicationDate: $(newsEl).find('news\\:publication_date, publication_date').text().trim() || undefined,
                  keywords: $(newsEl).find('news\\:keywords, keywords').text().trim() || undefined,
                  genres: $(newsEl).find('news\\:genres, genres').text().trim() || undefined,
                  stockTickers: $(newsEl).find('news\\:stock_tickers, stock_tickers').text().trim() || undefined,
                });
              });
            }
          }

          // Parse video extension: <video:video>
          let videoEntries: VideoSitemapEntry[] | undefined;
          if (hasVideoNs) {
            const videoEls = $(el).find('video\\:video, video');
            if (videoEls.length > 0) {
              videoEntries = [];
              videoEls.each((__, videoEl) => {
                const durationText = $(videoEl).find('video\\:duration, duration').text().trim();
                const ratingText = $(videoEl).find('video\\:rating, rating').text().trim();
                const viewCountText = $(videoEl).find('video\\:view_count, view_count').text().trim();
                // Google spec defines yes/no for boolean fields
                const familyText = $(videoEl).find('video\\:family_friendly, family_friendly').text().trim().toLowerCase();
                const liveText = $(videoEl).find('video\\:live, live').text().trim().toLowerCase();
                const parsedDuration = durationText ? parseInt(durationText, 10) : NaN;
                const parsedRating = ratingText ? parseFloat(ratingText) : NaN;
                const parsedViewCount = viewCountText ? parseInt(viewCountText, 10) : NaN;
                videoEntries!.push({
                  thumbnailLoc: $(videoEl).find('video\\:thumbnail_loc, thumbnail_loc').text().trim(),
                  title: $(videoEl).find('video\\:title, title').text().trim(),
                  description: $(videoEl).find('video\\:description, description').text().trim(),
                  contentLoc: $(videoEl).find('video\\:content_loc, content_loc').text().trim() || undefined,
                  playerLoc: $(videoEl).find('video\\:player_loc, player_loc').text().trim() || undefined,
                  duration: Number.isNaN(parsedDuration) ? undefined : parsedDuration,
                  expirationDate: $(videoEl).find('video\\:expiration_date, expiration_date').text().trim() || undefined,
                  rating: Number.isNaN(parsedRating) ? undefined : parsedRating,
                  viewCount: Number.isNaN(parsedViewCount) ? undefined : parsedViewCount,
                  familyFriendly: familyText ? familyText === 'yes' : undefined,
                  platform: $(videoEl).find('video\\:platform, platform').text().trim() || undefined,
                  live: liveText ? liveText === 'yes' : undefined,
                });
              });
            }
          }

          // Parse image extension: <image:image>
          let imageEntries: ImageSitemapEntry[] | undefined;
          if (hasImageNs) {
            const imageEls = $(el).find('image\\:image, image');
            if (imageEls.length > 0) {
              imageEntries = [];
              imageEls.each((__, imageEl) => {
                imageEntries!.push({
                  loc: $(imageEl).find('image\\:loc, loc').text().trim(),
                  caption: $(imageEl).find('image\\:caption, caption').text().trim() || undefined,
                  geoLocation: $(imageEl).find('image\\:geo_location, geo_location').text().trim() || undefined,
                  title: $(imageEl).find('image\\:title, title').text().trim() || undefined,
                  license: $(imageEl).find('image\\:license, license').text().trim() || undefined,
                });
              });
            }
          }

          // Determine sitemap type for this entry
          const types: string[] = [];
          if (newsEntries?.length) types.push('news');
          if (videoEntries?.length) types.push('video');
          if (imageEntries?.length) types.push('image');
          let sitemapType: SitemapType = 'standard';
          if (types.length > 1) sitemapType = 'mixed';
          else if (types.length === 1) sitemapType = types[0] as SitemapType;

          // Phase 6.2: Extract and validate optional fields
          const lastmodRaw = $(el).find('> lastmod').text().trim() || undefined;
          const changefreqRaw = $(el).find('> changefreq').text().trim() || undefined;
          const priorityRaw = $(el).find('> priority').text().trim() || undefined;

          if (changefreqRaw && !VALID_CHANGEFREQ.has(changefreqRaw.toLowerCase())) {
            warnings.push(`Invalid changefreq "${changefreqRaw}" for ${loc}`);
          }
          if (priorityRaw) {
            const pVal = parseFloat(priorityRaw);
            if (Number.isNaN(pVal) || pVal < 0 || pVal > 1) {
              warnings.push(`Invalid priority "${priorityRaw}" for ${loc}`);
            }
          }
          if (lastmodRaw) {
            const d = new Date(lastmodRaw);
            if (Number.isNaN(d.getTime())) {
              warnings.push(`Invalid lastmod date "${lastmodRaw}" for ${loc}`);
            }
          }

          entries.push({
            url: loc,
            lastmod: lastmodRaw,
            changefreq: changefreqRaw,
            priority: priorityRaw,
            source: sitemapUrl,
            sitemapType,
            news: newsEntries,
            videos: videoEntries,
            images: imageEntries,
          });
        });
      } catch {
        errors.push(`Failed to fetch sitemap: ${sitemapUrl}`);
      }
    };

    for (const sitemapUrl of this.sitemapUrls) {
      await parseSitemap(sitemapUrl);
    }

    this.sitemapEntries = entries;
    this.sitemapParseErrors = errors;
    this.sitemapValidationWarnings = warnings;
    return entries;
  }

  getSitemapErrors(): string[] {
    return this.sitemapParseErrors;
  }

  getSitemapValidationWarnings(): string[] {
    return this.sitemapValidationWarnings;
  }

  getSitemapEntryMap(): Map<string, SitemapEntry> {
    const map = new Map<string, SitemapEntry>();
    for (const entry of this.sitemapEntries) {
      // Store both the raw URL and normalized version for reliable matching
      // against crawled pages (which go through URL normalization)
      const normalized = normalizeUrl(entry.url);
      if (normalized && normalized !== entry.url) {
        map.set(normalized, entry);
      }
      map.set(entry.url, entry);
    }
    return map;
  }

  private extractSitemapUrls(robotsTxt: string): string[] {
    const urls: string[] = [];
    for (const line of robotsTxt.split('\n')) {
      const match = line.match(/^Sitemap:\s*(.+)/i);
      if (match) urls.push(match[1].trim());
    }
    return urls;
  }
}

// ── Multi-agent robots.txt testing ──────────────────────

export const KNOWN_USER_AGENTS = [
  'Googlebot',
  'Googlebot-Image',
  'Googlebot-News',
  'Googlebot-Video',
  'Bingbot',
  'Slurp',         // Yahoo
  'DuckDuckBot',
  'Baiduspider',
  'YandexBot',
  'GPTBot',
  'ChatGPT-User',
  'CCBot',
  'Applebot',
  'AhrefsBot',
  'SemrushBot',
  'MJ12bot',       // Majestic
  'PetalBot',      // Huawei/Aspiegel
  'facebookexternalhit',
  'Twitterbot',
] as const;

export interface RobotsTestResult {
  robotstxtUrl: string;
  robotstxtStatus: number;
  userAgents: string[];
  urls: string[];
  results: { url: string; agent: string; allowed: boolean }[];
  directives: { agent: string; directive: string; path: string }[];
  sitemapUrls: string[];
}

export async function testRobotsMultiAgent(
  siteUrl: string,
  userAgents: string[],
  testUrls?: string[],
): Promise<RobotsTestResult> {
  const base = new URL(siteUrl);
  const robotsUrl = `${base.protocol}//${base.host}/robots.txt`;

  let robotsTxt = '';
  let statusCode = 0;

  try {
    const resp = await request(robotsUrl, {
      headers: { 'User-Agent': 'Micelio/1.0' },
      signal: AbortSignal.timeout(10_000),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    statusCode = resp.statusCode;
    if (statusCode === 200) {
      robotsTxt = await resp.body.text();
    } else {
      await resp.body.dump();
    }
  } catch {
    // robots.txt not available or timed out
  }

  // Parse directives for display
  const directives: { agent: string; directive: string; path: string }[] = [];
  if (robotsTxt) {
    let currentAgent = '*';
    for (const line of robotsTxt.split('\n')) {
      const trimmed = line.trim();
      if (!trimmed || trimmed.startsWith('#')) continue;
      const uaMatch = trimmed.match(/^User-agent:\s*(.+)/i);
      if (uaMatch) {
        currentAgent = uaMatch[1].trim();
        continue;
      }
      const directiveMatch = trimmed.match(/^(Allow|Disallow|Crawl-delay):\s*(.*)/i);
      if (directiveMatch) {
        directives.push({
          agent: currentAgent,
          directive: directiveMatch[1],
          path: directiveMatch[2].trim(),
        });
      }
    }
  }

  // Extract sitemaps
  const sitemapUrls: string[] = [];
  if (robotsTxt) {
    for (const line of robotsTxt.split('\n')) {
      const match = line.match(/^Sitemap:\s*(.+)/i);
      if (match) sitemapUrls.push(match[1].trim());
    }
  }

  // Create parser for testing
  const robot = robotsTxt
    ? (robotsParser as unknown as (url: string, txt: string) => Robot)(robotsUrl, robotsTxt)
    : null;

  // Default test URLs if none provided
  const urls = testUrls && testUrls.length > 0
    ? testUrls
    : [
        `${base.protocol}//${base.host}/`,
        `${base.protocol}//${base.host}/robots.txt`,
        `${base.protocol}//${base.host}/sitemap.xml`,
        `${base.protocol}//${base.host}/admin`,
        `${base.protocol}//${base.host}/wp-admin/`,
        `${base.protocol}//${base.host}/api/`,
        `${base.protocol}//${base.host}/search`,
        `${base.protocol}//${base.host}/login`,
      ];

  // Test each URL against each user-agent
  const results: { url: string; agent: string; allowed: boolean }[] = [];
  for (const url of urls) {
    for (const agent of userAgents) {
      const allowed = robot ? (robot.isAllowed(url, agent) ?? true) : true;
      results.push({ url, agent, allowed });
    }
  }

  return {
    robotstxtUrl: robotsUrl,
    robotstxtStatus: statusCode,
    userAgents,
    urls,
    results,
    directives,
    sitemapUrls,
  };
}
