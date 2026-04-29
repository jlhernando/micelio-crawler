import type { PageData, UrlStructureData, UrlStructureStats } from './types.js';

/**
 * Decompose a URL into structured components for analysis.
 */
export function analyzeUrlStructure(url: string): UrlStructureData {
  try {
    const parsed = new URL(url);
    const path = parsed.pathname;
    // Split path into segments, filtering out empty strings from leading/trailing slashes
    const rawSegments = path.split('/').filter(Boolean);
    const lastSegment = rawSegments.length > 0 ? rawSegments[rawSegments.length - 1] : '';

    // Extract file extension from last segment
    let fileExtension = '';
    if (lastSegment.includes('.')) {
      const ext = lastSegment.split('.').pop()!.toLowerCase();
      // Only treat common web extensions as file extensions
      if (['html', 'htm', 'php', 'asp', 'aspx', 'jsp', 'cgi', 'xml', 'json', 'pdf', 'js', 'css'].includes(ext)) {
        fileExtension = ext;
      }
    }

    // Extract query parameters (deduplicate keys, keep first value)
    const queryParams: Record<string, string> = {};
    parsed.searchParams.forEach((value, key) => {
      if (!(key in queryParams)) {
        queryParams[key] = value;
      }
    });
    const parameterCount = Object.keys(queryParams).length;

    return {
      scheme: parsed.protocol.replace(':', ''),
      hostname: parsed.hostname,
      port: parsed.port || (parsed.protocol === 'https:' ? '443' : '80'),
      pathDepth: rawSegments.length,
      pathSegments: rawSegments,
      lastSegment,
      queryParams,
      parameterCount,
      hasFragment: parsed.hash.length > 1,
      hasTrailingSlash: path.length > 1 && path.endsWith('/'),
      fileExtension,
    };
  } catch {
    return {
      scheme: '', hostname: '', port: '',
      pathDepth: 0, pathSegments: [], lastSegment: '',
      queryParams: {}, parameterCount: 0,
      hasFragment: false, hasTrailingSlash: false, fileExtension: '',
    };
  }
}

/**
 * Build aggregate URL structure statistics from all crawled pages.
 */
export function buildUrlStructureStats(pages: PageData[]): UrlStructureStats | null {
  const pagesWithData = pages.filter(p => p.urlStructure && p.urlStructure.hostname);
  if (pagesWithData.length === 0) return null;

  const depthDistribution: Record<number, number> = {};
  const directoryCount: Record<string, number> = {};
  const paramFrequency: Record<string, number> = {};
  const extensionCount: Record<string, number> = {};
  let totalDepth = 0;
  let maxDepth = 0;
  let trailingSlashCount = 0;
  let withParamsCount = 0;

  for (const page of pagesWithData) {
    const us = page.urlStructure!;

    // Depth distribution
    depthDistribution[us.pathDepth] = (depthDistribution[us.pathDepth] || 0) + 1;
    totalDepth += us.pathDepth;
    if (us.pathDepth > maxDepth) maxDepth = us.pathDepth;

    // Top-level directory distribution
    if (us.pathSegments.length > 0) {
      const dir1 = '/' + us.pathSegments[0];
      directoryCount[dir1] = (directoryCount[dir1] || 0) + 1;
    } else {
      directoryCount['/'] = (directoryCount['/'] || 0) + 1;
    }

    // Query parameter frequency
    for (const key of Object.keys(us.queryParams)) {
      paramFrequency[key] = (paramFrequency[key] || 0) + 1;
    }
    if (us.parameterCount > 0) withParamsCount++;

    // File extension
    const ext = us.fileExtension || '(none)';
    extensionCount[ext] = (extensionCount[ext] || 0) + 1;

    if (us.hasTrailingSlash) trailingSlashCount++;
  }

  const avgDepth = pagesWithData.length > 0 ? Math.round((totalDepth / pagesWithData.length) * 10) / 10 : 0;

  // Sort directories by count descending, take top 15
  const topDirectories = Object.entries(directoryCount)
    .sort(([, a], [, b]) => b - a)
    .slice(0, 15)
    .map(([dir, count]) => ({ directory: dir, count }));

  // Sort parameters by frequency descending, take top 15
  const topParameters = Object.entries(paramFrequency)
    .sort(([, a], [, b]) => b - a)
    .slice(0, 15)
    .map(([param, count]) => ({ parameter: param, count }));

  // Sort extensions by count descending
  const extensionDistribution = Object.entries(extensionCount)
    .sort(([, a], [, b]) => b - a)
    .map(([ext, count]) => ({ extension: ext, count }));

  return {
    totalUrls: pagesWithData.length,
    avgPathDepth: avgDepth,
    maxPathDepth: maxDepth,
    depthDistribution,
    topDirectories,
    topParameters,
    extensionDistribution,
    urlsWithParams: withParamsCount,
    urlsWithTrailingSlash: trailingSlashCount,
  };
}
