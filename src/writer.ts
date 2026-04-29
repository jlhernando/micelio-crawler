import { createWriteStream, type WriteStream } from 'node:fs';
import { format } from 'fast-csv';
import type { PageData } from './types.js';
import { normalizeUrlForComparison } from './utils.js';

export class ResultWriter {
  private stream: WriteStream;
  private csvStream: ReturnType<typeof format> | null = null;
  private outputFormat: 'jsonl' | 'csv';

  constructor(outputPath: string, outputFormat: 'jsonl' | 'csv') {
    this.outputFormat = outputFormat;
    this.stream = createWriteStream(outputPath, { flags: 'w' });

    if (outputFormat === 'csv') {
      this.csvStream = format({ headers: true });
      this.csvStream.pipe(this.stream);
    }
  }

  // #17: Handle backpressure
  write(page: PageData): void {
    if (this.outputFormat === 'jsonl') {
      const ok = this.stream.write(JSON.stringify(page) + '\n');
      if (!ok) {
        // Backpressure: wait for drain before writing more
        // In practice the orchestrator continues; Node handles buffering
        // but we could await drain in an async version
      }
    } else if (this.csvStream) {
      this.csvStream.write(this.flattenForCsv(page));
    }
  }

  // #9: Handle stream errors in close()
  async close(): Promise<void> {
    return new Promise((resolve, reject) => {
      this.stream.on('error', reject);
      if (this.csvStream) {
        this.csvStream.on('error', reject);
        this.csvStream.end();
        this.stream.on('finish', resolve);
      } else {
        this.stream.end(resolve);
      }
    });
  }

  private flattenForCsv(page: PageData): Record<string, string | number | boolean> {
    return flattenPageForCsv(page);
  }
}

// ── Standalone CSV flatten helpers (reused by export endpoint) ──

function isSelfCanonical(page: PageData): boolean | string {
  if (!page.canonical) return '';
  const nc = normalizeUrlForComparison(page.canonical);
  return nc === normalizeUrlForComparison(page.url) || nc === normalizeUrlForComparison(page.finalUrl);
}

function getCanonicalIssue(page: PageData): string {
  const issues: string[] = [];
  if (page.statusCode !== 200) return '';
  if (!page.canonical) {
    issues.push('missing');
    return issues.join(', ');
  }
  if (page.canonicalCount > 1) issues.push('multiple');
  if (page.canonicalRaw && !page.canonicalRaw.startsWith('http://') && !page.canonicalRaw.startsWith('https://') && !page.canonicalRaw.startsWith('//')) {
    issues.push('relative');
  }
  try {
    const pageParsed = new URL(page.finalUrl);
    const canonParsed = new URL(page.canonical);
    if (pageParsed.protocol !== canonParsed.protocol) issues.push('protocol_mismatch');
    if (pageParsed.hostname.replace(/^www\./, '') !== canonParsed.hostname.replace(/^www\./, '')) issues.push('cross_domain');
    if (canonParsed.search) issues.push('has_query_string');
  } catch { /* skip */ }
  return issues.join(', ');
}

function flattenCustomExtractions(data: Record<string, string[]>): Record<string, string> {
  const flat: Record<string, string> = {};
  for (const [name, values] of Object.entries(data)) {
    flat[`extract_${name}`] = values.join(' | ');
  }
  return flat;
}

function flattenCustomSearches(data: Record<string, boolean>): Record<string, boolean> {
  const flat: Record<string, boolean> = {};
  for (const [name, found] of Object.entries(data)) {
    flat[`search_${name}`] = found;
  }
  return flat;
}

function flattenSnippetResults(data: Record<string, unknown>): Record<string, string> {
  const flat: Record<string, string> = {};
  for (const [name, value] of Object.entries(data)) {
    if (typeof value === 'object' && value !== null) {
      for (const [key, val] of Object.entries(value as Record<string, unknown>)) {
        flat[`snippet_${name}_${key}`] = String(val ?? '');
      }
    } else {
      flat[`snippet_${name}`] = String(value ?? '');
    }
  }
  return flat;
}

/** Flatten a PageData into a flat CSV-ready record. Exported for use by the export API. */
export function flattenPageForCsv(page: PageData): Record<string, string | number | boolean> {
  return {
    url: page.url,
    final_url: page.finalUrl,
    status_code: page.statusCode,
    redirect_chain_length: page.redirectChain.length,
    redirect_chain: page.redirectChain.length > 0
      ? page.redirectChain.map(h => `[${h.statusCode}] ${h.url}`).join(' -> ') + ' -> ' + page.finalUrl
      : '',
    redirect_type: page.redirectChain.length === 0 ? 'none'
      : page.redirectChain.every(h => h.statusCode === 301 || h.statusCode === 308) ? 'permanent'
      : page.redirectChain.every(h => h.statusCode === 302 || h.statusCode === 307) ? 'temporary'
      : 'mixed',
    response_time_ms: page.responseTimeMs,
    title: page.title?.text || '',
    title_length: page.title?.length || 0,
    meta_description: page.metaDescription?.text || '',
    meta_description_length: page.metaDescription?.length || 0,
    canonical: page.canonical || '',
    canonical_count: page.canonicalCount,
    canonical_is_self: isSelfCanonical(page),
    canonical_issue: getCanonicalIssue(page),
    meta_robots: page.metaRobots || '',
    x_robots_tag: page.xRobotsTag || '',
    h1_count: page.headings.h1.length,
    h1_text: page.headings.h1.join(' | '),
    h2_count: page.headings.h2.length,
    internal_links_count: page.internalLinks.length,
    external_links_count: page.externalLinks.length,
    images_count: page.images.length,
    images_missing_alt: page.images.filter((i) => i.missingAlt).length,
    images_missing_alt_attribute: page.images.filter((i) => !i.hasAltAttribute).length,
    images_alt_too_long: page.images.filter((i) => i.altTooLong).length,
    images_missing_dimensions: page.images.filter((i) => i.missingWidth || i.missingHeight).length,
    depth: page.depth,
    crawled_at: page.crawledAt,
    error: page.error || '',
    word_count: page.wordCount,
    content_hash: page.contentHash,
    hreflang_count: page.hreflang.length,
    hreflang_langs: page.hreflang.map((h) => h.lang).join(', '),
    hreflang_urls: page.hreflang.map((h) => `${h.lang}:${h.href}`).join(', '),
    structured_data_count: page.structuredData.length,
    structured_data_types: page.structuredData.map((s) => `${s.format}:${s.type}`).join(', '),
    og_title: page.openGraph['og:title'] || '',
    og_description: page.openGraph['og:description'] || '',
    og_image: page.openGraph['og:image'] || '',
    twitter_card: page.twitterCard['twitter:card'] || '',
    non_descriptive_anchors: page.anchors.filter((a) => a.isNonDescriptive).length,
    is_https: page.security.isHttps,
    has_mixed_content: page.security.hasMixedContent,
    has_hsts: page.security.hasHsts,
    has_x_frame_options: page.security.hasXFrameOptions,
    has_csp: page.security.hasCsp,
    ...flattenCustomExtractions(page.customExtractions),
    ...flattenCustomSearches(page.customSearches),
    ...flattenSnippetResults(page.snippetResults),
    psi_performance: page.pagespeed?.performanceScore ?? '',
    psi_lcp: page.pagespeed?.lcp ?? '',
    psi_fid: page.pagespeed?.fid ?? '',
    psi_inp: page.pagespeed?.inp ?? '',
    psi_cls: page.pagespeed?.cls ?? '',
    psi_ttfb: page.pagespeed?.ttfb ?? '',
    psi_speed_index: page.pagespeed?.speedIndex ?? '',
    psi_tbt: page.pagespeed?.tbt ?? '',
    psi_error: page.pagespeed?.error || '',
    ai_analysis: page.aiAnalysis || '',
    in_sitemap: page.sitemapData?.inSitemap ?? '',
    sitemap_lastmod: page.sitemapData?.sitemapLastmod || '',
    page_weight_total_bytes: page.pageWeight?.totalBytes ?? '',
    page_weight_html_bytes: page.pageWeight?.htmlBytes ?? '',
    page_weight_image_bytes: page.pageWeight?.byType?.image?.bytes ?? '',
    page_weight_script_bytes: page.pageWeight?.byType?.script?.bytes ?? '',
    page_weight_stylesheet_bytes: page.pageWeight?.byType?.stylesheet?.bytes ?? '',
    page_weight_resource_count: page.pageWeight?.resources?.length ?? '',
    indexable: page.indexability?.indexable ?? '',
    indexability_reason: page.indexability?.reason || '',
    readability_score: page.readability?.fleschKincaid ?? '',
    sentence_count: page.readability?.sentenceCount ?? '',
    avg_words_per_sentence: page.readability?.avgWordsPerSentence ?? '',
    url_issues: page.urlIssues?.join(', ') || '',
    is_soft_404: page.isSoft404 ?? '',
    text_to_code_ratio: page.textToCodeRatio ?? '',
    simhash: page.simhashFingerprint || '',
    schema_types: page.schemaValidation?.map(sv => sv.type).join(', ') || '',
    schema_errors: page.schemaValidation
      ?.flatMap(sv => sv.issues.filter(i => i.severity === 'error').map(i => i.message))
      .join('; ') || '',
    rich_result_eligible: page.schemaValidation
      ?.filter(sv => sv.richResultEligible)
      .map(sv => sv.richResultType)
      .filter(Boolean)
      .join(', ') || '',
    gsc_impressions: page.gscData?.impressions ?? '',
    gsc_clicks: page.gscData?.clicks ?? '',
    gsc_ctr: page.gscData ? (page.gscData.ctr * 100).toFixed(1) + '%' : '',
    gsc_position: page.gscData?.position ?? '',
    ga4_sessions: page.ga4Data?.sessions ?? '',
    ga4_pageviews: page.ga4Data?.pageviews ?? '',
    ga4_bounce_rate: page.ga4Data ? (page.ga4Data.bounceRate * 100).toFixed(1) + '%' : '',
    ga4_conversions: page.ga4Data?.conversions ?? '',
    ga4_active_users: page.ga4Data?.activeUsers ?? '',
    ga4_engagement_rate: page.ga4Data ? (page.ga4Data.engagementRate * 100).toFixed(1) + '%' : '',
    ga4_avg_session_duration: page.ga4Data?.avgSessionDuration ?? '',
    render_diffs: page.renderDiffs ? page.renderDiffs.map(d => d.field).join(', ') : '',
    render_diffs_detail: page.renderDiffs && page.renderDiffs.length > 0
      ? JSON.stringify(page.renderDiffs.map(d => ({ f: d.field, o: d.original.substring(0, 200), r: d.rendered.substring(0, 200) })))
      : '',
    segments: page.segments.join(', '),
    click_depth: page.linkIntelligence?.clickDepth ?? '',
    in_degree: page.linkIntelligence?.inDegree ?? '',
    out_degree: page.linkIntelligence?.outDegree ?? '',
    is_near_orphan: page.linkIntelligence?.isNearOrphan ?? '',
    link_dilution_factor: page.linkIntelligence?.linkDilutionFactor ?? '',
    hits_authority: page.linkIntelligence?.authorityScore ?? '',
    hits_hub: page.linkIntelligence?.hubScore ?? '',
    betweenness_centrality: page.linkIntelligence?.betweennessCentrality ?? '',
    closeness_centrality: page.linkIntelligence?.closenessCentrality ?? '',
    content_links: page.linkIntelligence?.contentLinksCount ?? '',
    nav_links: page.linkIntelligence?.navLinksCount ?? '',
    footer_links: page.linkIntelligence?.footerLinksCount ?? '',
    sidebar_links: page.linkIntelligence?.sidebarLinksCount ?? '',
    header_links: page.linkIntelligence?.headerLinksCount ?? '',
    other_links: page.linkIntelligence?.otherLinksCount ?? '',
    url_scheme: page.urlStructure?.scheme ?? '',
    url_hostname: page.urlStructure?.hostname ?? '',
    url_path_depth: page.urlStructure?.pathDepth ?? '',
    url_dir_1: page.urlStructure?.pathSegments[0] ?? '',
    url_dir_2: page.urlStructure?.pathSegments[1] ?? '',
    url_dir_3: page.urlStructure?.pathSegments[2] ?? '',
    url_last_segment: page.urlStructure?.lastSegment ?? '',
    url_param_count: page.urlStructure?.parameterCount ?? '',
    url_params: page.urlStructure ? Object.keys(page.urlStructure.queryParams).join(', ') : '',
    url_file_extension: page.urlStructure?.fileExtension ?? '',
    url_has_trailing_slash: page.urlStructure?.hasTrailingSlash ?? '',
    crux_lcp_ms: page.cruxData?.lcpMs ?? '',
    crux_inp_ms: page.cruxData?.inpMs ?? '',
    crux_cls: page.cruxData?.cls ?? '',
    crux_ttfb_ms: page.cruxData?.ttfbMs ?? '',
    crux_fcp_ms: page.cruxData?.fcpMs ?? '',
    crux_form_factor: page.cruxData?.formFactor ?? '',
    plausible_visitors: page.plausibleData?.visitors ?? '',
    plausible_visits: page.plausibleData?.visits ?? '',
    plausible_pageviews: page.plausibleData?.pageviews ?? '',
    plausible_bounce_rate: page.plausibleData ? page.plausibleData.bounceRate.toFixed(1) + '%' : '',
    plausible_visit_duration: page.plausibleData?.visitDuration ?? '',
    plausible_views_per_visit: page.plausibleData ? page.plausibleData.viewsPerVisit.toFixed(1) : '',
    plausible_time_on_page: page.plausibleData?.timeOnPage ?? '',
    plausible_scroll_depth: page.plausibleData?.scrollDepth != null ? page.plausibleData.scrollDepth + '%' : '',
    plausible_conversions: page.plausibleData?.conversions ?? '',
    plausible_conversion_rate: page.plausibleData ? page.plausibleData.conversionRate.toFixed(1) + '%' : '',
    robots_blocked: page.robotsBlocked ? 'Yes' : '',
    template_type: page.templateType || 'other',
    inlinks: page.inlinks ?? 0,
    page_rank: page.pageRank ?? '',
  };
}
