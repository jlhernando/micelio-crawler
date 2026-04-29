import type { StructuredDataEntry, SchemaValidationEntry } from './types.js';

/**
 * A single validation issue found in structured data.
 */
export interface SchemaIssue {
  severity: 'error' | 'warning';
  message: string;
  path?: string;
}

// Use SchemaValidationEntry from types.ts as the canonical result type
type SchemaValidationResult = SchemaValidationEntry;

// ── Required properties per Schema.org type ─────────────

interface TypeSpec {
  required: string[];
  recommended: string[];
  /** Google rich result type name (null if not a rich result type) */
  richResultType: string | null;
}

/**
 * Hardcoded validation specs for common Schema.org types that
 * Google supports for rich results. Required fields follow
 * Google's documentation as of 2025.
 */
const TYPE_SPECS: Record<string, TypeSpec> = {
  Product: {
    required: ['name'],
    recommended: ['image', 'description', 'offers', 'brand', 'review', 'aggregateRating'],
    richResultType: 'Product',
  },
  Article: {
    required: ['headline', 'image', 'datePublished', 'author'],
    recommended: ['dateModified', 'publisher', 'description', 'mainEntityOfPage'],
    richResultType: 'Article',
  },
  NewsArticle: {
    required: ['headline', 'image', 'datePublished', 'author'],
    recommended: ['dateModified', 'publisher', 'description'],
    richResultType: 'Article',
  },
  BlogPosting: {
    required: ['headline', 'image', 'datePublished', 'author'],
    recommended: ['dateModified', 'publisher', 'description'],
    richResultType: 'Article',
  },
  FAQPage: {
    required: ['mainEntity'],
    recommended: [],
    richResultType: 'FAQ',
  },
  HowTo: {
    required: ['name', 'step'],
    recommended: ['image', 'totalTime', 'estimatedCost', 'supply', 'tool'],
    richResultType: 'HowTo',
  },
  Review: {
    required: ['itemReviewed', 'reviewRating'],
    recommended: ['author', 'datePublished', 'reviewBody'],
    richResultType: 'Review',
  },
  BreadcrumbList: {
    required: ['itemListElement'],
    recommended: [],
    richResultType: 'BreadcrumbList',
  },
  Event: {
    required: ['name', 'startDate', 'location'],
    recommended: ['endDate', 'image', 'description', 'offers', 'performer', 'organizer'],
    richResultType: 'Event',
  },
  Recipe: {
    required: ['name', 'image'],
    recommended: ['author', 'datePublished', 'description', 'prepTime', 'cookTime',
      'totalTime', 'recipeIngredient', 'recipeInstructions', 'nutrition'],
    richResultType: 'Recipe',
  },
  LocalBusiness: {
    required: ['name', 'address'],
    recommended: ['telephone', 'openingHours', 'image', 'url', 'geo', 'priceRange'],
    richResultType: 'LocalBusiness',
  },
  Organization: {
    required: ['name'],
    recommended: ['url', 'logo', 'sameAs', 'contactPoint'],
    richResultType: null,
  },
  Person: {
    required: ['name'],
    recommended: ['url', 'image', 'sameAs', 'jobTitle'],
    richResultType: null,
  },
  WebSite: {
    required: ['name', 'url'],
    recommended: ['potentialAction'],
    richResultType: null,
  },
  WebPage: {
    required: [],
    recommended: ['name', 'description', 'breadcrumb'],
    richResultType: null,
  },
  VideoObject: {
    required: ['name', 'description', 'thumbnailUrl', 'uploadDate'],
    recommended: ['contentUrl', 'duration', 'embedUrl'],
    richResultType: 'Video',
  },
  SoftwareApplication: {
    required: ['name'],
    recommended: ['offers', 'aggregateRating', 'operatingSystem', 'applicationCategory'],
    richResultType: 'SoftwareApp',
  },
  Course: {
    required: ['name', 'description', 'provider'],
    recommended: ['offers'],
    richResultType: 'Course',
  },
  JobPosting: {
    required: ['title', 'description', 'datePosted', 'hiringOrganization', 'jobLocation'],
    recommended: ['baseSalary', 'employmentType', 'validThrough'],
    richResultType: 'JobPosting',
  },
};

// ── Validation helpers ──────────────────────────────────

/** ISO 8601 date format: YYYY-MM-DD with optional time */
const ISO_DATE_REGEX = /^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}(:\d{2})?(\.\d+)?(Z|[+-]\d{2}:\d{2})?)?$/;

/**
 * Check if a value is present and non-empty.
 */
function hasValue(obj: Record<string, unknown>, key: string): boolean {
  const val = obj[key];
  if (val === undefined || val === null) return false;
  if (typeof val === 'string' && val.trim() === '') return false;
  if (Array.isArray(val) && val.length === 0) return false;
  return true;
}

/**
 * Validate Offer/AggregateOffer nested inside a Product.
 */
function validateOffers(offers: unknown, issues: SchemaIssue[]): void {
  if (!offers) return;
  const offerList = Array.isArray(offers) ? offers : [offers];
  for (const offer of offerList) {
    if (typeof offer !== 'object' || offer === null) continue;
    const o = offer as Record<string, unknown>;
    const type = String(o['@type'] || 'Offer');
    if (type === 'AggregateOffer') {
      if (!hasValue(o, 'lowPrice') && !hasValue(o, 'highPrice')) {
        issues.push({ severity: 'warning', message: 'AggregateOffer missing lowPrice or highPrice', path: 'offers' });
      }
    } else {
      if (!hasValue(o, 'price') && !hasValue(o, 'priceSpecification')) {
        issues.push({ severity: 'warning', message: 'Offer missing price', path: 'offers.price' });
      }
    }
    if (!hasValue(o, 'priceCurrency')) {
      issues.push({ severity: 'warning', message: 'Offer missing priceCurrency', path: 'offers.priceCurrency' });
    }
    if (!hasValue(o, 'availability')) {
      issues.push({ severity: 'warning', message: 'Offer missing availability', path: 'offers.availability' });
    }
  }
}

/**
 * Validate FAQ mainEntity structure.
 */
function validateFAQ(data: Record<string, unknown>, issues: SchemaIssue[]): void {
  const mainEntity = data.mainEntity;
  if (!mainEntity) {
    issues.push({ severity: 'error', message: 'FAQPage missing mainEntity', path: 'mainEntity' });
    return;
  }
  const questions = Array.isArray(mainEntity) ? mainEntity : [mainEntity];
  if (questions.length === 0) {
    issues.push({ severity: 'error', message: 'FAQPage mainEntity is empty', path: 'mainEntity' });
    return;
  }
  let validQuestions = 0;
  for (let i = 0; i < questions.length; i++) {
    const q = questions[i] as Record<string, unknown>;
    if (!q || typeof q !== 'object') continue;
    const qType = String(q['@type'] || '');
    if (qType !== 'Question') {
      issues.push({ severity: 'warning', message: `mainEntity[${i}] should be @type Question, got "${qType}"`, path: `mainEntity[${i}].@type` });
    }
    if (!hasValue(q, 'name')) {
      issues.push({ severity: 'error', message: `mainEntity[${i}] missing question name`, path: `mainEntity[${i}].name` });
    }
    const answer = q.acceptedAnswer as Record<string, unknown> | undefined;
    if (!answer) {
      issues.push({ severity: 'error', message: `mainEntity[${i}] missing acceptedAnswer`, path: `mainEntity[${i}].acceptedAnswer` });
    } else if (!hasValue(answer, 'text')) {
      issues.push({ severity: 'error', message: `mainEntity[${i}].acceptedAnswer missing text`, path: `mainEntity[${i}].acceptedAnswer.text` });
    } else {
      validQuestions++;
    }
  }
  if (validQuestions === 0) {
    issues.push({ severity: 'error', message: 'FAQPage has no valid question/answer pairs' });
  }
}

/**
 * Validate HowTo step structure.
 */
function validateHowToSteps(data: Record<string, unknown>, issues: SchemaIssue[]): void {
  const steps = data.step;
  if (!steps) {
    issues.push({ severity: 'error', message: 'HowTo missing step', path: 'step' });
    return;
  }
  const stepList = Array.isArray(steps) ? steps : [steps];
  if (stepList.length === 0) {
    issues.push({ severity: 'error', message: 'HowTo step array is empty', path: 'step' });
    return;
  }
  for (let i = 0; i < stepList.length; i++) {
    const step = stepList[i] as Record<string, unknown>;
    if (!step || typeof step !== 'object') continue;
    if (!hasValue(step, 'text') && !hasValue(step, 'name') && !hasValue(step, 'itemListElement')) {
      issues.push({ severity: 'warning', message: `step[${i}] missing text or name`, path: `step[${i}]` });
    }
  }
}

/**
 * Validate BreadcrumbList itemListElement structure.
 */
function validateBreadcrumbList(data: Record<string, unknown>, issues: SchemaIssue[]): void {
  const items = data.itemListElement;
  if (!items) {
    issues.push({ severity: 'error', message: 'BreadcrumbList missing itemListElement', path: 'itemListElement' });
    return;
  }
  const itemList = Array.isArray(items) ? items : [items];
  if (itemList.length === 0) {
    issues.push({ severity: 'error', message: 'BreadcrumbList itemListElement is empty', path: 'itemListElement' });
    return;
  }
  for (let i = 0; i < itemList.length; i++) {
    const item = itemList[i] as Record<string, unknown>;
    if (!item || typeof item !== 'object') continue;
    if (!hasValue(item, 'name') && !hasValue(item, 'item')) {
      issues.push({ severity: 'warning', message: `itemListElement[${i}] missing name and item`, path: `itemListElement[${i}]` });
    }
    if (!hasValue(item, 'position')) {
      issues.push({ severity: 'warning', message: `itemListElement[${i}] missing position`, path: `itemListElement[${i}].position` });
    }
  }
}

/**
 * Validate Review reviewRating structure.
 */
function validateReview(data: Record<string, unknown>, issues: SchemaIssue[]): void {
  const rating = data.reviewRating as Record<string, unknown> | undefined;
  if (!rating) {
    issues.push({ severity: 'error', message: 'Review missing reviewRating', path: 'reviewRating' });
    return;
  }
  if (!hasValue(rating, 'ratingValue')) {
    issues.push({ severity: 'error', message: 'reviewRating missing ratingValue', path: 'reviewRating.ratingValue' });
  }
  if (!hasValue(rating, 'bestRating')) {
    issues.push({ severity: 'warning', message: 'reviewRating missing bestRating (defaults to 5)', path: 'reviewRating.bestRating' });
  }
}

// ── Main validation function ────────────────────────────

/**
 * Validate a single JSON-LD structured data entry.
 * Returns an array because @graph containers produce one result per item.
 */
function validateJsonLd(raw: string): SchemaValidationResult[] {
  let parsed: Record<string, unknown>;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [{
      type: 'ParseError',
      format: 'json-ld',
      issues: [{ severity: 'error', message: 'Invalid JSON in JSON-LD script' }],
      richResultEligible: false,
      richResultType: null,
    }];
  }

  // Handle @graph arrays — return individual results per item for granular reporting
  if (Array.isArray(parsed['@graph'])) {
    if (parsed['@graph'].length === 0) {
      return [{
        type: 'Graph',
        format: 'json-ld',
        issues: [{ severity: 'warning', message: '@graph array is empty' }],
        richResultEligible: false,
        richResultType: null,
      }];
    }
    const results: SchemaValidationResult[] = [];
    for (const item of parsed['@graph']) {
      if (typeof item === 'object' && item !== null) {
        // Graph children inherit @context from parent, so skip that check
        const subResult = validateJsonLdObject(item as Record<string, unknown>, true);
        results.push(subResult);
      } else {
        results.push({
          type: 'Unknown',
          format: 'json-ld',
          issues: [{ severity: 'warning', message: 'Non-object item in @graph array' }],
          richResultEligible: false,
          richResultType: null,
        });
      }
    }
    return results;
  }

  return [validateJsonLdObject(parsed, false)];
}

/**
 * Validate a single JSON-LD object (not a @graph container).
 * @param isGraphChild - if true, skip @context check (inherited from parent)
 */
function validateJsonLdObject(data: Record<string, unknown>, isGraphChild: boolean = false): SchemaValidationResult {
  const issues: SchemaIssue[] = [];
  const type = String(data['@type'] || 'Unknown');

  // Check @context (skip for @graph children which inherit from parent)
  if (!isGraphChild) {
    const context = data['@context'];
    if (!context) {
      issues.push({ severity: 'warning', message: 'Missing @context (should be "https://schema.org")' });
    } else {
      const ctxStr = String(context).toLowerCase();
      if (!ctxStr.includes('schema.org')) {
        issues.push({ severity: 'warning', message: `@context "${context}" does not reference schema.org` });
      }
    }
  }

  // Check @type
  if (!data['@type']) {
    issues.push({ severity: 'error', message: 'Missing @type' });
    return { type: 'Unknown', format: 'json-ld', issues, richResultEligible: false, richResultType: null };
  }

  // Handle array @type (e.g. ["Product", "ItemPage"])
  const types = Array.isArray(data['@type']) ? data['@type'].map(String) : [type];

  let richResultEligible = true;
  let richResultType: string | null = null;

  // Find the best matching spec
  let spec: TypeSpec | undefined;
  for (const t of types) {
    if (TYPE_SPECS[t]) {
      spec = TYPE_SPECS[t];
      break;
    }
  }

  if (!spec) {
    // Unknown type — can't validate deeply but not an error
    return {
      type: types.join(', '),
      format: 'json-ld',
      issues,
      richResultEligible: false,
      richResultType: null,
    };
  }

  richResultType = spec.richResultType;

  // Check required properties
  for (const prop of spec.required) {
    if (!hasValue(data, prop)) {
      issues.push({ severity: 'error', message: `Missing required property "${prop}"`, path: prop });
      richResultEligible = false;
    }
  }

  // Check recommended properties
  for (const prop of spec.recommended) {
    if (!hasValue(data, prop)) {
      issues.push({ severity: 'warning', message: `Missing recommended property "${prop}"`, path: prop });
    }
  }

  // Type-specific deep validation
  const primaryType = types.find(t => TYPE_SPECS[t]) || type;
  switch (primaryType) {
    case 'Product':
      validateOffers(data.offers, issues);
      break;
    case 'FAQPage':
      validateFAQ(data, issues);
      break;
    case 'HowTo':
      validateHowToSteps(data, issues);
      break;
    case 'BreadcrumbList':
      validateBreadcrumbList(data, issues);
      break;
    case 'Review':
      validateReview(data, issues);
      break;
    case 'Event': {
      // Validate date format
      const startDate = data.startDate;
      if (startDate && typeof startDate === 'string') {
        if (!ISO_DATE_REGEX.test(startDate)) {
          issues.push({ severity: 'error', message: 'startDate is not valid ISO 8601 (expected YYYY-MM-DD)', path: 'startDate' });
          richResultEligible = false;
        }
      }
      // Validate location
      const location = data.location as Record<string, unknown> | undefined;
      if (location && typeof location === 'object') {
        if (!hasValue(location, 'name') && !hasValue(location, 'address')) {
          issues.push({ severity: 'warning', message: 'location missing name and address', path: 'location' });
        }
      }
      break;
    }
    case 'Article':
    case 'NewsArticle':
    case 'BlogPosting': {
      // Validate author (can be a single object or array)
      const author = data.author;
      if (author && typeof author === 'object' && author !== null) {
        const authorList = Array.isArray(author) ? author : [author];
        for (let ai = 0; ai < authorList.length; ai++) {
          const a = authorList[ai] as Record<string, unknown>;
          if (a && typeof a === 'object' && !hasValue(a, 'name')) {
            const path = authorList.length > 1 ? `author[${ai}].name` : 'author.name';
            issues.push({ severity: 'warning', message: 'author missing name', path });
          }
        }
      }
      // Validate datePublished format
      const datePublished = data.datePublished;
      if (datePublished && typeof datePublished === 'string') {
        if (!ISO_DATE_REGEX.test(datePublished)) {
          issues.push({ severity: 'error', message: 'datePublished is not valid ISO 8601 (expected YYYY-MM-DD)', path: 'datePublished' });
          richResultEligible = false;
        }
      }
      break;
    }
  }

  // If there are error-severity issues, not eligible for rich results
  if (issues.some(i => i.severity === 'error')) {
    richResultEligible = false;
  }

  return {
    type: types.join(', '),
    format: 'json-ld',
    issues,
    richResultEligible: richResultType !== null && richResultEligible,
    richResultType: richResultType,
  };
}

// ── Public API ──────────────────────────────────────────

/**
 * Validate all structured data entries for a page.
 * JSON-LD entries get full validation; Microdata gets basic type checking.
 */
export function validateStructuredData(entries: StructuredDataEntry[]): SchemaValidationEntry[] {
  const results: SchemaValidationResult[] = [];

  for (const entry of entries) {
    if (entry.format === 'json-ld') {
      results.push(...validateJsonLd(entry.raw));
    } else if (entry.format === 'microdata') {
      // Microdata: basic validation (we only have the type, not full properties)
      const type = entry.type;
      const spec = TYPE_SPECS[type];
      const issues: SchemaIssue[] = [];
      if (!spec) {
        // Unknown type — not an error, just can't validate
        results.push({
          type,
          format: 'microdata',
          issues,
          richResultEligible: false,
          richResultType: null,
        });
      } else {
        // We know the type is valid, but can't validate properties from microdata extraction
        issues.push({
          severity: 'warning',
          message: 'Microdata detected — property-level validation requires JSON-LD format',
        });
        results.push({
          type,
          format: 'microdata',
          issues,
          richResultEligible: false,
          richResultType: spec.richResultType,
        });
      }
    }
  }

  return results;
}
