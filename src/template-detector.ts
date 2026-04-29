import type { PageData, AnchorData, StructuredDataEntry, SchemaValidationEntry, ImageData } from './types.js';

/** Subset of PageData fields used for template detection. */
export interface TemplateDetectionInput {
  url: string;
  finalUrl: string;
  depth: number;
  wordCount: number;
  internalLinks: string[];
  externalLinks: string[];
  images: ImageData[];
  headings: { h1: string[]; h2: string[]; h3: string[] };
  anchors: AnchorData[];
  structuredData: StructuredDataEntry[];
  schemaValidation: SchemaValidationEntry[];
  openGraph: Record<string, string>;
}

/**
 * Page template types detected by heuristic scoring.
 */
export type TemplateType =
  | 'homepage'
  | 'listing'
  | 'product'
  | 'article'
  | 'legal'
  | 'contact'
  | 'faq'
  | 'search'
  | 'login'
  | 'other';

interface ScoreMap {
  homepage: number;
  listing: number;
  product: number;
  article: number;
  legal: number;
  contact: number;
  faq: number;
  search: number;
  login: number;
}

/**
 * Detect the template type of a page using weighted heuristic scoring.
 *
 * Signals (by weight):
 * - URL patterns (high)
 * - Structured data / schema types (high)
 * - OG type (medium)
 * - Content metrics: word count, link density, heading patterns (medium)
 * - DOM-derived signals: forms, pagination, link ratios (low-medium)
 */
export function detectTemplateType(page: TemplateDetectionInput, seedUrl: string): TemplateType {
  const scores: ScoreMap = {
    homepage: 0,
    listing: 0,
    product: 0,
    article: 0,
    legal: 0,
    contact: 0,
    faq: 0,
    search: 0,
    login: 0,
  };

  scoreUrlPatterns(scores, page, seedUrl);
  scoreStructuredData(scores, page);
  scoreOgType(scores, page);
  scoreContentMetrics(scores, page);
  scoreLinkPatterns(scores, page);
  scoreHeadingPatterns(scores, page);

  // Find the highest scoring type
  let best: TemplateType = 'other';
  let bestScore = 0;
  for (const [type, score] of Object.entries(scores) as [keyof ScoreMap, number][]) {
    if (score > bestScore) {
      bestScore = score;
      best = type;
    }
  }

  // Require minimum confidence threshold to avoid false positives
  return bestScore >= 2 ? best : 'other';
}

// ── URL Pattern Scoring (weight: 3) ──────────────────────────────

const URL_PATTERNS: { type: keyof ScoreMap; patterns: RegExp[] }[] = [
  {
    type: 'homepage',
    patterns: [/^\/$/],
  },
  {
    type: 'listing',
    patterns: [
      /\/(category|categories|collection|collections|shop|catalog|archive|archives|tag|tags|browse|listings?)\b/i,
      /\/page\/\d+/i,
      /[?&]page=\d+/i,
    ],
  },
  {
    type: 'product',
    patterns: [
      /\/(product|item|p|pd|sku|goods)\//i,
      /\/(products|items)\/[^/]+$/i,
    ],
  },
  {
    type: 'article',
    patterns: [
      /\/(blog|article|articles|post|posts|news|stories|journal|magazine|editorial)\b/i,
      /\/\d{4}\/\d{2}\//,  // Date-based URLs like /2024/01/...
    ],
  },
  {
    type: 'legal',
    patterns: [
      /\/(privacy|terms|tos|legal|disclaimer|cookie|cookies|gdpr|imprint|impressum|agb|datenschutz|conditions|compliance|policy|policies)\b/i,
      /\/(terms-of-service|terms-of-use|privacy-policy|cookie-policy|data-protection|acceptable-use)\b/i,
    ],
  },
  {
    type: 'contact',
    patterns: [
      /\/(contact|contacto|kontakt|kontakta|get-in-touch|reach-us|support|help|feedback)\b/i,
    ],
  },
  {
    type: 'faq',
    patterns: [
      /\/(faq|faqs|frequently-asked|help-center|knowledge-base|kb|questions)\b/i,
    ],
  },
  {
    type: 'search',
    patterns: [
      /\/(search|results|buscar|suche|recherche)\b/i,
      /[?&](q|query|search|s|keyword)=/i,
    ],
  },
  {
    type: 'login',
    patterns: [
      /\/(login|signin|sign-in|log-in|register|signup|sign-up|auth|authenticate|account\/login|my-account)\b/i,
    ],
  },
];

function scoreUrlPatterns(scores: ScoreMap, page: TemplateDetectionInput, seedUrl: string): void {
  const url = page.finalUrl || page.url;

  // Homepage: check if URL is exactly the seed URL root
  try {
    const parsed = new URL(url);
    const seed = new URL(seedUrl);
    if (
      parsed.hostname === seed.hostname &&
      (parsed.pathname === '/' || parsed.pathname === '')
    ) {
      scores.homepage += 5; // Very strong signal
      return; // Homepage is definitive, skip other URL checks
    }
  } catch { /* skip */ }

  const pathAndQuery = url.replace(/^https?:\/\/[^/]+/, '');

  for (const { type, patterns } of URL_PATTERNS) {
    if (type === 'homepage') continue; // Already handled above
    for (const pattern of patterns) {
      if (pattern.test(pathAndQuery)) {
        scores[type] += 3;
        break; // Only count once per type
      }
    }
  }
}

// ── Structured Data Scoring (weight: 3) ──────────────────────────

const SCHEMA_TYPE_MAP: Record<string, keyof ScoreMap> = {
  Product: 'product',
  Offer: 'product',
  AggregateOffer: 'product',
  ProductGroup: 'product',
  Article: 'article',
  NewsArticle: 'article',
  BlogPosting: 'article',
  TechArticle: 'article',
  ScholarlyArticle: 'article',
  Report: 'article',
  FAQPage: 'faq',
  QAPage: 'faq',
  Question: 'faq',
  SearchResultsPage: 'search',
  CollectionPage: 'listing',
  ItemList: 'listing',
  ContactPage: 'contact',
};

function scoreStructuredData(scores: ScoreMap, page: TemplateDetectionInput): void {
  for (const sd of page.structuredData) {
    const mapped = SCHEMA_TYPE_MAP[sd.type];
    if (mapped) {
      scores[mapped] += 3;
    }
  }

  // Also check schema validation entries (more reliable, validated types)
  for (const sv of page.schemaValidation) {
    const mapped = SCHEMA_TYPE_MAP[sv.type];
    if (mapped) {
      scores[mapped] += 1; // Bonus for validated schema
    }
  }
}

// ── Open Graph Type Scoring (weight: 2) ──────────────────────────

function scoreOgType(scores: ScoreMap, page: TemplateDetectionInput): void {
  const ogType = page.openGraph['og:type']?.toLowerCase();
  if (!ogType) return;

  if (ogType === 'article' || ogType === 'blog') {
    scores.article += 2;
  } else if (ogType === 'product' || ogType === 'product.item') {
    scores.product += 2;
  } else if (ogType === 'website') {
    // 'website' is the default, slight boost to homepage if depth 0
    if (page.depth === 0) {
      scores.homepage += 1;
    }
  }
}

// ── Content Metrics Scoring (weight: 1-2) ────────────────────────

function scoreContentMetrics(scores: ScoreMap, page: TemplateDetectionInput): void {
  const wordCount = page.wordCount;
  const internalLinkCount = page.internalLinks.length;

  // Long-form content → likely article
  if (wordCount > 800) {
    scores.article += 2;
  } else if (wordCount > 400) {
    scores.article += 1;
  }

  // Very short content with many links → listing
  if (wordCount < 300 && internalLinkCount > 15) {
    scores.listing += 2;
  }

  // Medium content with some links → could be product
  if (wordCount >= 100 && wordCount <= 600 && page.images.length >= 2) {
    scores.product += 1;
  }

  // Low word count, deep page → could be login/search
  if (wordCount < 100 && page.depth > 1) {
    scores.login += 1;
    scores.search += 1;
  }
}

// ── Link Pattern Scoring (weight: 1-2) ───────────────────────────

function scoreLinkPatterns(scores: ScoreMap, page: TemplateDetectionInput): void {
  const internalLinkCount = page.internalLinks.length;
  const externalLinkCount = page.externalLinks.length;

  // High internal link density → listing or homepage
  if (internalLinkCount > 30) {
    scores.listing += 2;
    scores.homepage += 1;
  } else if (internalLinkCount > 15) {
    scores.listing += 1;
  }

  // Low link count → detail page (product/article) or legal
  if (internalLinkCount < 5 && page.wordCount > 200) {
    scores.legal += 1;
  }

  // Anchor position analysis (if link intelligence data available)
  if (page.anchors.length > 0) {
    const contentAnchors = page.anchors.filter(a => a.isInternal && a.position === 'content');
    const navAnchors = page.anchors.filter(a => a.isInternal && a.position === 'navigation');

    // Many content-area internal links → listing page
    if (contentAnchors.length > 10) {
      scores.listing += 1;
    }

    // High ratio of navigation links → homepage or listing
    if (navAnchors.length > 10 && contentAnchors.length < 5) {
      scores.homepage += 1;
    }
  }

  // External links analysis - articles tend to cite external sources
  if (externalLinkCount > 3 && page.wordCount > 500) {
    scores.article += 1;
  }
}

// ── Heading Pattern Scoring (weight: 1-2) ────────────────────────

function scoreHeadingPatterns(scores: ScoreMap, page: TemplateDetectionInput): void {
  const h1Count = page.headings.h1.length;
  const h2Count = page.headings.h2.length;
  const h3Count = page.headings.h3.length;
  const h1Text = (page.headings.h1[0] || '').toLowerCase();

  // Single H1 + multiple H2s → well-structured article
  if (h1Count === 1 && h2Count >= 3) {
    scores.article += 2;
  }

  // Many H2/H3 subheadings → FAQ-like structure
  if (h2Count >= 5 || h3Count >= 8) {
    scores.faq += 1;
  }

  // H1 text-based detection
  if (/\b(faq|frequently\s+asked|questions)\b/i.test(h1Text)) {
    scores.faq += 3;
  }
  if (/\b(contact|get\s+in\s+touch|reach\s+us)\b/i.test(h1Text)) {
    scores.contact += 3;
  }
  if (/\b(login|log\s+in|sign\s+in|register|sign\s+up|create\s+account)\b/i.test(h1Text)) {
    scores.login += 3;
  }
  if (/\b(search\s+results?|results?\s+for)\b/i.test(h1Text)) {
    scores.search += 3;
  }
  if (/\b(privacy|terms|legal|disclaimer|cookie)\b/i.test(h1Text)) {
    scores.legal += 2;
  }
}

/**
 * Build template type distribution stats from crawled pages.
 */
export function buildTemplateStats(pages: PageData[]): Record<string, number> {
  const distribution: Record<string, number> = {};
  for (const page of pages) {
    const type = page.templateType || 'other';
    distribution[type] = (distribution[type] || 0) + 1;
  }
  return distribution;
}
