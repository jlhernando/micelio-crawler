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
 * Text of a JSON-LD value that should be a string (a @type, a URL).
 * Crawled JSON is untrusted: String() on an object can throw (a "toString"
 * key that is not a function), so only primitives are converted.
 */
function textOf(value: unknown): string {
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  return '';
}

/** @type as a list of names: a string or an array of strings. */
function typeNames(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(textOf).filter(Boolean);
  const t = textOf(value);
  return t ? [t] : [];
}

/** True when a JSON-LD @context points at schema.org (string, array or @vocab object). */
function contextIsSchemaOrg(context: unknown): boolean {
  if (typeof context === 'string') return context.toLowerCase().includes('schema.org');
  if (Array.isArray(context)) return context.some(contextIsSchemaOrg);
  if (context && typeof context === 'object') {
    return contextIsSchemaOrg((context as Record<string, unknown>)['@vocab']);
  }
  return false;
}

/** Warning for a missing or non-schema.org @context, or null when it is fine. */
function contextIssue(context: unknown): SchemaIssue | null {
  if (!context) return { severity: 'warning', message: 'Missing @context (should be "https://schema.org")' };
  if (!contextIsSchemaOrg(context)) {
    const shown = typeof context === 'string' ? context : (JSON.stringify(context) ?? '').slice(0, 100);
    return { severity: 'warning', message: `@context "${shown}" does not reference schema.org` };
  }
  return null;
}

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
    if (typeNames(o['@type']).includes('AggregateOffer')) {
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
    const qType = typeNames(q['@type']).join(', ');
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
  let parsed: unknown;
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

  // A top-level array is valid JSON-LD: each element is a node of its own
  if (Array.isArray(parsed)) {
    if (parsed.length === 0) return [notANode('JSON-LD array is empty')];
    const results: SchemaValidationResult[] = [];
    for (const item of parsed) {
      results.push(...(isNode(item) ? validateJsonLdNode(item, undefined) : [notANode('Non-object item in JSON-LD array')]));
    }
    return results;
  }
  // Valid JSON that is not an object (null, a string, a number)
  if (!isNode(parsed)) return [notANode('JSON-LD is not an object')];

  return validateJsonLdNode(parsed, undefined);
}

function isNode(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function notANode(message: string): SchemaValidationResult {
  return {
    type: 'Unknown',
    format: 'json-ld',
    issues: [{ severity: 'warning', message }],
    richResultEligible: false,
    richResultType: null,
  };
}

/**
 * Validate a JSON-LD object, or each child of its @graph.
 * @param inherited - see validateJsonLdObject
 */
function validateJsonLdNode(data: Record<string, unknown>, inherited: SchemaIssue | null | undefined): SchemaValidationResult[] {
  // Handle @graph arrays — return individual results per item for granular reporting
  const graph = data['@graph'];
  if (!Array.isArray(graph)) return [validateJsonLdObject(data, inherited)];

  if (graph.length === 0) {
    return [{
      type: 'Graph',
      format: 'json-ld',
      issues: [{ severity: 'warning', message: '@graph array is empty' }],
      richResultEligible: false,
      richResultType: null,
    }];
  }
  // Children inherit the container's @context: check it here so each child
  // gets the verdict a standalone object would get.
  const containerContext = data['@context'] !== undefined || inherited === undefined
    ? contextIssue(data['@context'])
    : inherited;
  const results: SchemaValidationResult[] = [];
  for (const item of graph) {
    results.push(isNode(item)
      ? validateJsonLdObject(item, containerContext)
      : notANode('Non-object item in @graph array'));
  }
  return results;
}

/**
 * Validate a single JSON-LD object (not a @graph container).
 * @param inherited - @context verdict of the enclosing @graph container
 *   (null: it references schema.org). Undefined for a top-level object,
 *   which must carry its own @context. A node's own @context always wins.
 */
function validateJsonLdObject(data: Record<string, unknown>, inherited?: SchemaIssue | null): SchemaValidationResult {
  const issues: SchemaIssue[] = [];

  const ctxIssue = data['@context'] !== undefined || inherited === undefined
    ? contextIssue(data['@context'])
    : inherited;
  if (ctxIssue) issues.push(ctxIssue);

  // Check @type: a string or an array of strings (e.g. ["Product", "ItemPage"])
  const types = typeNames(data['@type']);
  if (types.length === 0) {
    issues.push({ severity: 'error', message: data['@type'] ? '@type must be a string or an array of strings' : 'Missing @type' });
    return { type: 'Unknown', format: 'json-ld', issues, richResultEligible: false, richResultType: null };
  }
  const type = types[0];

  // Without a schema.org @context the type names mean nothing to Google
  let richResultEligible = ctxIssue === null;
  let richResultType: string | null = null;

  // Find the best matching spec
  let spec: TypeSpec | undefined;
  for (const t of types) {
    if (Object.hasOwn(TYPE_SPECS, t)) {
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
  const primaryType = types.find(t => Object.hasOwn(TYPE_SPECS, t)) || type;
  switch (primaryType) {
    case 'Product':
      // Google product snippets need at least one of offers, review or aggregateRating
      if (!hasValue(data, 'offers') && !hasValue(data, 'review') && !hasValue(data, 'aggregateRating')) {
        issues.push({ severity: 'error', message: 'Product needs at least one of "offers", "review" or "aggregateRating"', path: 'offers' });
      }
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
      // One odd block must never cost the page its whole extraction
      try {
        results.push(...validateJsonLd(entry.raw));
      } catch {
        results.push({
          type: 'Unknown',
          format: 'json-ld',
          issues: [{ severity: 'error', message: 'JSON-LD could not be validated' }],
          richResultEligible: false,
          richResultType: null,
        });
      }
    } else if (entry.format === 'microdata') {
      // Microdata: basic validation (we only have the type, not full properties)
      const type = entry.type;
      const spec = Object.hasOwn(TYPE_SPECS, type) ? TYPE_SPECS[type] : undefined;
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
