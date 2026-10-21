// Labels and help text for automatic segment dimensions (see internal/analysis/autosegment.go).

export interface SegmentValue {
  value: string;
  pages: number;
  indexablePct: number;
  errorPct: number;
  avgWords: number;
  avgInlinks: number;
  avgResponseMs: number;
  sampleUrl?: string;
}

export interface SegmentDimension {
  dimension: string;
  values: SegmentValue[];
  unassigned?: number;
  totalValues: number;
}

const DIMENSION_LABELS: Record<string, string> = {
  template: 'Template',
  dir1: 'Directory L1',
  dir2: 'Directory L2',
  dir3: 'Directory L3',
  extension: 'Extension',
  textAmount: 'Text amount',
  linkProfile: 'Link profile',
  locale: 'Language',
  urlParams: 'URL parameters',
  depthBand: 'Click depth',
  authority: 'Internal authority',
  indexState: 'Index state',
  schemaType: 'Schema type',
  subdomain: 'Host',
  freshness: 'Freshness',
};

const DIMENSION_HELP: Record<string, string> = {
  template: 'Page type detected from URL patterns, structured data and page layout. Unclassified pages that share a layout are grouped as "group N".',
  dir1: 'First folder in the URL path. Pages at the root sit under "/".',
  dir2: 'First two folders in the URL path. Pages without a second folder keep their first-level folder.',
  dir3: 'First three folders in the URL path. Pages without a third folder keep their parent folder.',
  extension: 'File extension at the end of the URL. "/" means the URL ends with a slash; "none" means no extension.',
  textAmount: 'Visible word count compared with the rest of this site, split into thirds.',
  linkProfile: 'Share of internal links that sit in the main content (including related posts and comments) versus navigation, header, footer and sidebar.',
  locale: 'Language from the html lang attribute, a self-referencing hreflang, or a language folder like /es/.',
  urlParams: 'What the query string does: facets and sorting, pagination, internal search, tracking, or other parameters.',
  depthBand: 'Clicks from the start URL.',
  authority: 'Internal PageRank (or inlinks when PageRank is off), split into thirds across pages that returned 200. "None" means no internal links point here.',
  indexState: 'Whether the page can be indexed, and why not when it cannot.',
  schemaType: 'Main structured data type, ignoring site-wide types like BreadcrumbList or WebSite.',
  subdomain: 'Host name of the URL.',
  freshness: 'Age of the publish or modified date found on the page.',
};

const TEMPLATE_LABELS: Record<string, string> = {
  plp: 'PLP (product listing)',
  pdp: 'PDP (product detail)',
  listing: 'Listing',
  category: 'Category',
  blog_index: 'Blog index',
  blog_post: 'Blog post',
  ad_detail: 'Ad detail',
  article: 'Article',
  homepage: 'Homepage',
  legal: 'Legal / privacy',
  contact: 'Contact',
  faq: 'FAQ',
  search: 'Search results',
  login: 'Login / account',
  other: 'Other',
};

export function segmentDimensionLabel(dim: string): string {
  return DIMENSION_LABELS[dim] ?? dim;
}

export function segmentDimensionHelp(dim: string): string {
  return DIMENSION_HELP[dim] ?? '';
}

export function segmentValueLabel(dim: string, value: string): string {
  if (dim === 'template') return TEMPLATE_LABELS[value] ?? value;
  if (dim === 'depthBand') return `${value} clicks`;
  if (value.length > 0 && /^[a-z]/.test(value) && dim !== 'locale' && dim !== 'subdomain' && dim !== 'extension') {
    return value.charAt(0).toUpperCase() + value.slice(1);
  }
  return value;
}
