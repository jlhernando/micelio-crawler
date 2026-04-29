import type { EmbeddingStats } from './embeddings.js';
import type { CruxData } from './crux.js';
import type { PlausibleData } from './plausible.js';

export type { CruxData, PlausibleData };

export interface CrawlConfig {
  seedUrl: string;
  urls: string[];
  sitemapUrls: string[];
  mode: 'spider' | 'list' | 'sitemap';
  maxDepth: number;
  maxPages: number;
  concurrency: number;
  delayMs: number;
  delayExplicit: boolean;
  userAgent: string;
  jsRendering: boolean;
  outputPath: string;
  outputFormat: 'jsonl' | 'csv';
  customHeaders: Record<string, string>;
  cookies: string;
  includePatterns: RegExp[];
  excludePatterns: RegExp[];
  checkExternal: boolean;
  // Phase 3: Custom extraction
  customExtractions: CustomExtractionRule[];
  customSearches: CustomSearchRule[];
  snippetPaths: string[];
  // Phase 4: Integrations
  psi: boolean;
  psiKey: string;
  aiPrompt: string;
  aiProvider: 'openai' | 'anthropic' | 'ollama' | '';
  aiModel: string;
  aiKey: string;
  // Phase 5: HTML dashboard
  htmlReport: boolean;
  htmlOpen: boolean;
  // Phase 6: Page weight
  pageWeight: boolean;
  // Phase 11: GSC
  gsc: boolean;
  gscProperty: string;
  gscDays: number;
  gscKeyFile: string;
  gscBqDataset: string;
  // Phase 11: GA4
  ga4: boolean;
  ga4Property: string;
  ga4Days: number;
  ga4KeyFile: string;
  // Phase 12: Segmentation
  segmentRules: { name: string; pattern: string }[];
  // Phase 12: Database storage
  dbPath: string;
  resume: boolean;
  // Phase 12: Proxy
  proxy: string;
  // Phase 13: N-Grams
  ngrams: boolean;
  // Phase 13: Embeddings / Semantic Similarity
  embeddings: boolean;
  embeddingModel: string;
  similarityThreshold: number;
  // Link Intelligence
  linkIntelligence: boolean;
  liMaxSuggestions: number;
  liNoCentrality: boolean;
  // Sitemap generation
  sitemapOut: boolean;
  sitemapChangefreq: string;
  sitemapPriority: string;
  // CrUX integration
  crux: boolean;
  cruxKey: string;
  cruxFormFactor: 'PHONE' | 'DESKTOP' | '';
  // Plausible Analytics
  plausible: boolean;
  plausibleSiteId: string;
  plausibleApiKey: string;
  plausibleDays: number;
  plausibleHost: string;
  // Conditional termination
  maxErrors: number;
  timeoutSeconds: number;
  // Multi-subdomain crawling
  allowedDomains: string[];
  // Language for n-gram stopwords
  language: string;
  // Robots.txt settings
  respectRobots: boolean;
  showBlockedInternal: boolean;
}

export interface FetchResult {
  url: string;
  finalUrl: string;
  statusCode: number;
  redirectChain: RedirectHop[];
  headers: Record<string, string>;
  html: string;
  /** Original pre-render HTML (only set when JS rendering modified the page) */
  rawHtml?: string;
  contentType: string;
  responseTimeMs: number;
  transferSize: number;
  error?: string;
}

export interface RedirectHop {
  url: string;
  statusCode: number;
}

export interface PageData {
  url: string;
  finalUrl: string;
  statusCode: number;
  redirectChain: RedirectHop[];
  responseTimeMs: number;
  title: { text: string; length: number } | null;
  metaDescription: { text: string; length: number } | null;
  canonical: string | null;
  canonicalCount: number;
  canonicalRaw: string | null;
  metaRobots: string | null;
  xRobotsTag: string | null;
  headings: HeadingData;
  internalLinks: string[];
  externalLinks: string[];
  images: ImageData[];
  depth: number;
  crawledAt: string;
  error?: string;
  // Phase 2: Enhanced SEO fields
  hreflang: HreflangEntry[];
  structuredData: StructuredDataEntry[];
  openGraph: Record<string, string>;
  twitterCard: Record<string, string>;
  wordCount: number;
  contentHash: string;
  anchors: AnchorData[];
  security: SecurityData;
  // Phase 3: Custom extraction results
  customExtractions: Record<string, string[]>;
  customSearches: Record<string, boolean>;
  snippetResults: Record<string, unknown>;
  // Phase 4: Integration results
  pagespeed: PageSpeedData | null;
  aiAnalysis: string | null;
  // Phase 6: Sitemap audit & page weight
  sitemapData: SitemapAuditData | null;
  pageWeight: PageWeightData | null;
  // Phase 7: Content & Indexability
  indexability: IndexabilityData;
  readability: ReadabilityData | null;
  urlIssues: string[];
  isSoft404: boolean;
  textToCodeRatio: number;
  simhashFingerprint: string;
  // Phase 9: Schema validation
  schemaValidation: SchemaValidationEntry[];
  // Phase 11: Google Search Console data
  gscData: GscData | null;
  // Phase 11: Google Analytics 4 data
  ga4Data: Ga4Data | null;
  // Phase 12: Segmentation
  segments: string[];
  // Phase 12: Render comparison
  renderDiffs: RenderDiff[] | null;
  // Link Intelligence
  linkIntelligence: LinkIntelligenceData | null;
  // CrUX data
  cruxData: CruxData | null;
  // Plausible Analytics data
  plausibleData: PlausibleData | null;
  // Template type detection
  templateType: string;
  // Inlinks count (always computed — unique pages linking to this page)
  inlinks: number;
  // Internal PageRank (always computed, 0-10 scale)
  pageRank: number;
  // Robots.txt blocked flag
  robotsBlocked: boolean;
  // URL Structure Analytics
  urlStructure?: UrlStructureData;
}

export interface UrlStructureData {
  scheme: string;
  hostname: string;
  port: string;
  pathDepth: number;
  pathSegments: string[];       // dir_1, dir_2, dir_3...
  lastSegment: string;
  queryParams: Record<string, string>;
  parameterCount: number;
  hasFragment: boolean;
  hasTrailingSlash: boolean;
  fileExtension: string;        // e.g. 'html', 'php', '' for none
}

export interface UrlStructureStats {
  totalUrls: number;
  avgPathDepth: number;
  maxPathDepth: number;
  depthDistribution: Record<number, number>;
  topDirectories: { directory: string; count: number }[];
  topParameters: { parameter: string; count: number }[];
  extensionDistribution: { extension: string; count: number }[];
  urlsWithParams: number;
  urlsWithTrailingSlash: number;
}

export interface LinkIntelligenceData {
  clickDepth: number | null;      // null = unreachable from homepage
  inDegree: number;               // total unique internal inlinks (deduplicated by source page)
  outDegree: number;              // total unique internal outlinks
  isNearOrphan: boolean;
  linkDilutionFactor: number;     // 1/outDegree
  hubScore: number;               // HITS hub score (0-10)
  authorityScore: number;         // HITS authority score (0-10)
  betweennessCentrality: number;  // 0-10 normalized (bridge page indicator)
  closenessCentrality: number;    // 0-10 normalized (connectivity indicator)
  contentLinksCount: number;      // inlinks from content area
  navLinksCount: number;          // inlinks from navigation
  footerLinksCount: number;       // inlinks from footer
  sidebarLinksCount: number;      // inlinks from sidebar
  headerLinksCount: number;       // inlinks from header
  otherLinksCount: number;        // inlinks from unclassified position
}

export interface SemanticLinkAnalysis {
  totalLinks: number;
  avgSemSimilarity: number;
  weakLinks: { source: string; target: string; similarity: number }[];
  weakLinksCount: number;
  strongLinks: { source: string; target: string; similarity: number }[];
  strongLinksCount: number;
}

export interface LinkSuggestion {
  sourceUrl: string;       // Page that should ADD a link
  targetUrl: string;       // Page that should RECEIVE the link
  score: number;           // 0-100 composite suggestion score
  reason: string;          // Human-readable explanation
  signals: {
    semanticSimilarity: number | null;  // 0-1 cosine similarity
    targetInDegree: number;             // current inlink count
    targetClickDepth: number | null;    // current click depth
    depthReduction: number | null;      // how much click depth would improve
    targetPageRank: number;             // target page value
  };
}

export interface GscData {
  impressions: number;
  clicks: number;
  ctr: number;
  position: number;
}

export interface Ga4Data {
  sessions: number;
  pageviews: number;
  bounceRate: number;
  conversions: number;
  activeUsers: number;
  engagementRate: number;
  avgSessionDuration: number;
}

export interface RenderDiff {
  field: string;
  original: string;
  rendered: string;
}

export interface SchemaValidationEntry {
  type: string;
  format: 'json-ld' | 'microdata';
  issues: { severity: 'error' | 'warning'; message: string; path?: string }[];
  richResultEligible: boolean;
  richResultType: string | null;
}

export interface HreflangEntry {
  lang: string;
  href: string;
}

export interface StructuredDataEntry {
  type: string;
  format: 'json-ld' | 'microdata';
  raw: string;
}

// Link Intelligence: Link position classification
export type LinkPosition = 'content' | 'navigation' | 'footer' | 'sidebar' | 'header' | 'other';

export interface AnchorData {
  href: string;
  text: string;
  isInternal: boolean;
  isNonDescriptive: boolean;
  rel: string | null;
  position: LinkPosition;
}

export interface SecurityData {
  isHttps: boolean;
  hasMixedContent: boolean;
  mixedContentUrls: string[];
  hasHsts: boolean;
  hasXFrameOptions: boolean;
  hasCsp: boolean;
}

// Phase 3: Custom extraction config types
export interface CustomExtractionRule {
  name: string;
  type: 'css';
  selector: string;
}

export interface CustomSearchRule {
  name: string;
  pattern: string;
  isRegex: boolean;
}

// Phase 6: Sitemap audit types

export interface NewsSitemapEntry {
  title: string;
  publicationName: string;
  publicationLanguage: string;
  publicationDate?: string;
  keywords?: string;
  genres?: string;
  stockTickers?: string;
}

export interface VideoSitemapEntry {
  thumbnailLoc: string;
  title: string;
  description: string;
  contentLoc?: string;
  playerLoc?: string;
  duration?: number;
  expirationDate?: string;
  rating?: number;
  viewCount?: number;
  familyFriendly?: boolean;
  platform?: string;
  live?: boolean;
}

export interface ImageSitemapEntry {
  loc: string;
  caption?: string;
  geoLocation?: string;
  title?: string;
  license?: string;
}

export type SitemapType = 'standard' | 'news' | 'video' | 'image' | 'mixed';

export interface SitemapEntry {
  url: string;
  lastmod?: string;
  changefreq?: string;
  priority?: string;
  source: string;
  sitemapType?: SitemapType;
  news?: NewsSitemapEntry[];
  videos?: VideoSitemapEntry[];
  images?: ImageSitemapEntry[];
}

export interface SitemapAuditData {
  inSitemap: boolean;
  sitemapLastmod?: string;
}

export interface SitemapExtensionCounts {
  news: number;
  video: number;
  image: number;
}

export interface SitemapStats {
  totalSitemapUrls: number;
  orphanUrls: string[];
  missingFromSitemap: string[];
  nonIndexableInSitemap: string[];
  sitemapErrors: string[];
  newsEntryCount: number;
  videoEntryCount: number;
  imageEntryCount: number;
  // Phase 6.2: Validation & Audit
  validationWarnings: string[];
  lastmodStats: { missing: number; stale: number; future: number; invalid: number };
  redirectsInSitemap: string[];
  statusBreakdown: Record<number, number>;
  // Phase 6.3: Crawl-vs-Sitemap Comparison
  duplicateAcrossSitemaps: { url: string; sources: string[] }[];
  uncrawledSitemapUrls: string[];
  coverage: { crawledAndInSitemap: number; crawledNotInSitemap: number; inSitemapNotCrawled: number };
}

// Phase 6: Page weight types
export interface ResourceEntry {
  url: string;
  type: 'html' | 'image' | 'script' | 'stylesheet' | 'font' | 'video' | 'audio' | 'other';
  sizeBytes: number | null;
}

export interface PageWeightData {
  totalBytes: number;
  htmlBytes: number;
  byType: Record<string, { count: number; bytes: number }>;
  resources: ResourceEntry[];
}

// Phase 7: Content & Indexability types
export interface IndexabilityData {
  indexable: boolean;
  reason: string;
}

export interface ReadabilityData {
  fleschKincaid: number;
  sentenceCount: number;
  avgWordsPerSentence: number;
  syllableCount: number;
}

// Phase 4: PageSpeed Insights data
export interface PageSpeedData {
  performanceScore: number;
  lcp: number;
  fid: number;
  inp: number;
  cls: number;
  ttfb: number;
  speedIndex: number;
  tbt: number;
  error?: string;
}

export interface HeadingData {
  h1: string[];
  h2: string[];
  h3: string[];
  h4: string[];
  h5: string[];
  h6: string[];
}

export interface ImageData {
  src: string;
  alt: string | null;
  missingAlt: boolean;
  // Phase 14.2: Image accessibility deep audit
  hasAltAttribute: boolean;
  altLength: number;
  altTooLong: boolean;
  missingWidth: boolean;
  missingHeight: boolean;
  width: string | null;
  height: string | null;
}

export interface ExternalLinkResult {
  url: string;
  statusCode: number;
  error?: string;
  foundOn: string[];
}

export interface CrawlStats {
  totalPages: number;
  robotsBlockedCount: number;
  statusCodes: Record<number, number>;
  pagesWithoutTitle: number;
  pagesWithoutDescription: number;
  pagesWithoutH1: number;
  redirectChains: { url: string; chain: RedirectHop[] }[];
  brokenLinks: { url: string; statusCode: number; foundOn: string[] }[];
  brokenExternalLinks: ExternalLinkResult[];
  imagesMissingAlt: number;
  totalImages: number;
  totalInternalLinks: number;
  totalExternalLinks: number;
  crawlDurationMs: number;
  depthDistribution: Record<number, number>;
  responseTimePercentiles: { p50: number; p90: number; p99: number };
  slowPages: { url: string; responseTimeMs: number }[];
  longRedirectChains: { url: string; hops: number }[];
  orphanPages: string[];
  // Phase 2 stats
  hreflangIssues: { url: string; issue: string }[];
  pagesWithStructuredData: number;
  pagesWithoutOg: number;
  thinContentPages: { url: string; wordCount: number }[];
  duplicateContentGroups: { hash: string; urls: string[] }[];
  nonDescriptiveAnchors: { url: string; text: string; foundOn: string }[];
  mixedContentPages: string[];
  nonHttpsPages: string[];
  // Phase 3 stats
  customSearchSummary: Record<string, { found: number; total: number }>;
  // Phase 4 stats
  performanceScores: { avg: number; min: number; max: number } | null;
  pagesWithAiAnalysis: number;
  // Internal PageRank
  pageRankScores: Map<string, number>;
  // Phase 6: Sitemap audit stats
  sitemapStats: SitemapStats | null;
  // Phase 6: Page weight stats
  pageWeightStats: {
    avgTotalBytes: number;
    heaviestPages: { url: string; totalBytes: number }[];
    byType: Record<string, { count: number; bytes: number }>;
    oversizedPages: { url: string; totalBytes: number }[];
  } | null;
  // Phase 7: Content & Indexability stats
  indexabilityStats: {
    indexable: number;
    nonIndexable: number;
    reasons: Record<string, number>;
  };
  nearDuplicateGroups: { urls: string[]; similarity: number }[];
  readabilityStats: {
    avgScore: number;
    difficult: { url: string; score: number }[];
    veryEasy: { url: string; score: number }[];
  } | null;
  urlIssueStats: Record<string, string[]>;
  soft404Pages: string[];
  linkAnalysis: {
    deadEndPages: string[];
    nofollowedInternalLinks: number;
    followedInternalLinks: number;
    linksToNonIndexable: { from: string; to: string }[];
  };
  textToCodeStats: {
    avgRatio: number;
    contentPoor: { url: string; ratio: number }[];
  } | null;
  // Phase 9: Schema validation stats
  schemaValidationStats: {
    pagesWithSchema: number;
    pagesWithValidSchema: number;
    pagesWithErrors: number;
    richResultEligible: Record<string, number>;
    topIssues: { message: string; count: number }[];
    typeDistribution: Record<string, number>;
  } | null;
  // Phase 11: GSC stats
  gscStats: {
    pagesWithData: number;
    totalImpressions: number;
    totalClicks: number;
    avgCtr: number;
    avgPosition: number;
    days: number; // lookback period
    topByClicks: { url: string; clicks: number; impressions: number; position: number }[];
    zombiePages: string[]; // pages with 0 clicks despite being indexed
  } | null;
  // Phase 11: GA4 stats
  ga4Stats: {
    pagesWithData: number;
    totalSessions: number;
    totalPageviews: number;
    totalConversions: number;
    avgBounceRate: number;
    avgEngagementRate: number;
    days: number;
    topByPageviews: { url: string; pageviews: number; sessions: number; bounceRate: number }[];
    noTrafficPages: string[]; // indexed + indexable but 0 sessions
  } | null;
  // Phase 12: Render comparison stats
  renderCompareStats: {
    pagesCompared: number;
    pagesWithDiffs: number;
    fieldDiffCounts: Record<string, number>;
    criticalDiffs: { url: string; field: string; original: string; rendered: string }[];
  } | null;
  // Phase 12: Segmentation stats
  segmentStats: {
    name: string;
    pageCount: number;
    statusCodes: Record<number, number>;
    avgResponseTimeMs: number;
    indexable: number;
    nonIndexable: number;
    avgWordCount: number;
    totalInternalLinks: number;
    totalExternalLinks: number;
    pagesWithErrors: number;
    totalImpressions: number;
    totalClicks: number;
    totalSessions: number;
    totalPageviews: number;
  }[] | null;
  // Phase 14.2: Image accessibility audit stats
  imageAuditStats: {
    totalImages: number;
    missingAltAttribute: number;
    emptyAlt: number;
    altTooLong: { url: string; src: string; altLength: number }[];
    missingDimensions: number;
    oversizedImages: { url: string; src: string; sizeBytes: number }[];
    totalImageBytes: number;
  } | null;
  // Link Intelligence stats
  linkIntelligenceStats: {
    avgClickDepth: number;
    maxClickDepth: number;
    clickDepthDistribution: Record<number, number>;
    unreachablePages: string[];
    unreachablePagesCount: number;       // Full count before truncation
    nearOrphans: { url: string; inDegree: number; worstSourceDepth: number | null }[];
    nearOrphansCount: number;            // Full count before truncation
    dilutionWarnings: { url: string; outDegree: number; warning: 'excessive' | 'high' }[];
    dilutionWarningsCount: number;       // Full count before truncation
    linkPositionDistribution: Record<string, number>;
    pagesWithNoContentLinks: string[];
    pagesWithNoContentLinksCount: number; // Full count before truncation
    // Step 2: HITS
    topAuthorities: { url: string; score: number }[];
    topHubs: { url: string; score: number }[];
    // Step 2: Link Suggestions
    linkSuggestions: LinkSuggestion[];
    linkSuggestionsCount: number;
    // Step 3: Centrality
    topBridges: { url: string; score: number }[];
    singlePointOfFailure: string[];       // betweenness > 8.0
    mostConnected: { url: string; score: number }[];
    mostIsolated: { url: string; score: number }[];
    centralitySkipped: boolean;           // true when >20K pages or --li-no-centrality
    centralitySkipReason: string;
    // Step 3: Semantic Link Distance
    semanticLinkAnalysis: SemanticLinkAnalysis | null;
  } | null;
  // Phase 13: Embedding similarity stats
  embeddingStats: EmbeddingStats | null;
  // Phase 13: N-Grams stats
  ngramStats: {
    unigrams: { term: string; count: number; pages: number; tfidf: number }[];
    bigrams: { term: string; count: number; pages: number; tfidf: number }[];
    trigrams: { term: string; count: number; pages: number; tfidf: number }[];
    totalPages: number;
    totalTokens: number;
  } | null;
  // Redirect chain analysis
  redirectStats: {
    totalRedirects: number;         // pages with at least 1 redirect
    totalHops: number;              // sum of all hops across all chains
    avgChainLength: number;         // average hops per redirecting page
    maxChainLength: number;         // longest chain
    byStatusCode: Record<number, number>;  // count of hops by HTTP status
    httpToHttps: { url: string; chain: RedirectHop[] }[];    // HTTP->HTTPS upgrades
    wwwNormalization: { url: string; chain: RedirectHop[] }[];  // www add/remove
    trailingSlash: { url: string; chain: RedirectHop[] }[];   // trailing slash add/remove
    crossDomain: { url: string; chain: RedirectHop[] }[];     // domain change
    selfRedirects: string[];        // pages that redirect to themselves
    chainLoops: { url: string; chain: RedirectHop[] }[];      // chains with loops (A->B->A)
    temporaryRedirects: { url: string; chain: RedirectHop[] }[];  // chains with 302/307
    longestChains: { url: string; chain: RedirectHop[]; finalUrl: string }[];  // top 10 longest
    topRedirectTargets: { url: string; count: number }[];     // URLs receiving most redirects
  } | null;
  // Canonical tag validation
  canonicalStats: {
    totalWithCanonical: number;
    totalWithoutCanonical: number;     // 200 OK pages missing canonical
    selfReferencing: number;           // canonical = own URL
    canonicalized: number;             // canonical points elsewhere
    multipleCanonicals: number;        // pages with >1 canonical tag
    canonicalChains: { url: string; chain: string[] }[];          // A -> B -> C
    canonicalLoops: { url: string; loop: string[] }[];            // A -> B -> A
    canonicalToNon200: { url: string; canonical: string; targetStatus: number }[];
    canonicalToNonIndexable: { url: string; canonical: string }[];
    crossDomain: { url: string; canonical: string }[];
    httpHttpsMismatch: { url: string; canonical: string }[];      // HTTPS page -> HTTP canonical or vice versa
    relativeCanonicals: { url: string; rawHref: string }[];       // canonical was a relative URL
    canonicalWithQueryString: { url: string; canonical: string }[];
    topCanonicalTargets: { url: string; count: number }[];        // URLs receiving most canonicalization
  } | null;
  // Template type distribution
  templateTypeDistribution: Record<string, number> | null;
  // URL Structure Analytics stats
  urlStructureStats: UrlStructureStats | null;
  // CrUX stats
  cruxStats: {
    pagesWithData: number;
    formFactor: string;
    avgLcpMs: number | null;
    avgInpMs: number | null;
    avgCls: number | null;
    avgTtfbMs: number | null;
    avgFcpMs: number | null;
    goodLcp: number;
    poorLcp: number;
    goodInp: number;
    poorInp: number;
    goodCls: number;
    poorCls: number;
    worstLcp: { url: string; lcpMs: number }[];
    worstInp: { url: string; inpMs: number }[];
    worstCls: { url: string; cls: number }[];
  } | null;
  plausibleStats: {
    pagesWithData: number;
    totalVisitors: number;
    totalVisits: number;
    totalPageviews: number;
    totalConversions: number;
    avgBounceRate: number;
    avgVisitDuration: number;
    avgScrollDepth: number | null;
    days: number;
    topByPageviews: { url: string; pageviews: number; visitors: number; bounceRate: number }[];
    noTrafficPages: string[];
  } | null;
}

// HEAD-only crawl result (lightweight — no body download)
export interface HeadResult {
  url: string;
  finalUrl: string;
  statusCode: number;
  redirectChain: RedirectHop[];
  responseTimeMs: number;
  contentType: string;
  contentLength: number | null;
  server: string;
  // SEO-relevant headers
  xRobotsTag: string | null;
  linkCanonical: string | null;     // from Link: <url>; rel="canonical" header
  hsts: boolean;
  csp: boolean;
  xFrameOptions: string | null;
  referrerPolicy: string | null;
  cacheControl: string | null;
  // All response headers for reference
  headers: Record<string, string>;
  error?: string;
}

export type CrawlState = 'pending' | 'crawling' | 'completed' | 'failed';

// Phase 8: Crawl Diff types
export interface DiffFieldChange {
  field: string;
  oldValue: string | number | boolean;
  newValue: string | number | boolean;
}

export interface DiffUrlChange {
  url: string;
  changes: DiffFieldChange[];
}

export interface DiffResult {
  oldFile: string;
  newFile: string;
  oldCount: number;
  newCount: number;
  addedUrls: string[];
  removedUrls: string[];
  changedUrls: DiffUrlChange[];
  unchangedCount: number;
  urlMappingsApplied: number;
  fieldSummary: Record<string, number>;
}

export interface QueueEntry {
  url: string;
  depth: number;
  referrer: string | null;
}
