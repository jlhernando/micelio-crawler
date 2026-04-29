/**
 * Google Search Console data via BigQuery Bulk Export.
 *
 * Queries the `searchdata_url_impression` table created by GSC's
 * bulk data export feature. This provides:
 * - No row limits (full data for large sites)
 * - Extra fields: is_organic, is_video, is_discover, is_job_listing
 * - Historical data beyond the 16-month API limit
 *
 * Prerequisites:
 * 1. User must enable bulk data export in GSC settings
 * 2. BigQuery dataset must be accessible (via service account or OAuth)
 *
 * Cost: Most sites stay in BigQuery free tier ($0).
 * Query uses exact URL list + date partitioning for minimal scanned data.
 */

import { BigQuery } from '@google-cloud/bigquery';
import type { GscData } from './types.js';
import { formatDateYmd } from './utils.js';

/** Allowed characters in BigQuery project and dataset names */
const SAFE_BQ_NAME = /^[\w.-]+$/;

interface GscBqOptions {
  /** BigQuery dataset in format: project.dataset */
  dataset: string;
  /** Number of days to look back (default: 90) */
  days: number;
  /** List of crawled URLs to fetch data for */
  urls: string[];
  /** Service account key file path (optional, uses ADC if omitted) */
  keyFile?: string;
}

/**
 * Fetch GSC data from BigQuery bulk export tables.
 * Queries searchdata_url_impression with URL filtering and date partitioning.
 * Returns Map<url, GscData> for merge with crawl data.
 */
export async function fetchGscFromBigQuery(options: GscBqOptions): Promise<Map<string, GscData>> {
  const { dataset, days, urls, keyFile } = options;

  // Early return for empty URL list (avoids invalid SQL with empty IN clause)
  if (urls.length === 0) return new Map();

  // Parse project and dataset
  const dotIdx = dataset.indexOf('.');
  if (dotIdx === -1) {
    throw new Error(
      `Invalid BigQuery dataset format "${dataset}". ` +
      'Expected format: project_id.dataset_name (e.g., my-project.searchconsole)'
    );
  }
  const projectId = dataset.substring(0, dotIdx);
  const datasetId = dataset.substring(dotIdx + 1);

  // Validate project and dataset names against safe character set
  if (!SAFE_BQ_NAME.test(projectId) || !SAFE_BQ_NAME.test(datasetId)) {
    throw new Error(
      `Invalid BigQuery project or dataset name. ` +
      'Names may only contain alphanumeric characters, hyphens, underscores, and dots.'
    );
  }

  // Create BigQuery client
  const bqOptions: { projectId: string; keyFilename?: string } = { projectId };
  if (keyFile) {
    bqOptions.keyFilename = keyFile;
  }
  const bigquery = new BigQuery(bqOptions);

  // Calculate date range
  const endDate = new Date();
  endDate.setDate(endDate.getDate() - 3); // GSC data has ~3 day lag
  const startDate = new Date(endDate);
  startDate.setDate(startDate.getDate() - days);

  const startStr = formatDateYmd(startDate);
  const endStr = formatDateYmd(endDate);

  // Build URL parameter list for IN clause
  // Batch in chunks of 1000 to avoid query size limits
  const results = new Map<string, GscData>();
  const batchSize = 1000;

  for (let i = 0; i < urls.length; i += batchSize) {
    const batch = urls.slice(i, i + batchSize);
    const urlParams = batch.map((_, idx) => `@url_${i + idx}`);

    const query = `
      SELECT
        url,
        SUM(impressions) as total_impressions,
        SUM(clicks) as total_clicks,
        SAFE_DIVIDE(SUM(clicks), SUM(impressions)) as avg_ctr,
        SAFE_DIVIDE(SUM(sum_position), SUM(impressions)) as avg_position
      FROM \`${projectId}.${datasetId}.searchdata_url_impression\`
      WHERE data_date BETWEEN @start_date AND @end_date
        AND url IN (${urlParams.join(', ')})
      GROUP BY url
    `;

    // Build params
    const params: Record<string, string> = {
      start_date: startStr,
      end_date: endStr,
    };
    for (let j = 0; j < batch.length; j++) {
      params[`url_${i + j}`] = batch[j];
    }

    try {
      const [rows] = await bigquery.query({
        query,
        params,
        location: undefined, // Auto-detect
      });

      for (const row of rows) {
        results.set(row.url, {
          impressions: Number(row.total_impressions) || 0,
          clicks: Number(row.total_clicks) || 0,
          ctr: Number(row.avg_ctr) || 0,
          position: Math.round((Number(row.avg_position) || 0) * 10) / 10,
        });
      }
    } catch (error: unknown) {
      const message = (error as { message?: string })?.message || String(error);
      if (message.includes('Not found: Table')) {
        throw new Error(
          `BigQuery table not found: ${projectId}.${datasetId}.searchdata_url_impression\n` +
          'Make sure bulk data export is enabled in Google Search Console settings.\n' +
          'Go to: Search Console > Settings > Bulk data export'
        );
      }
      if (message.includes('Access Denied') || message.includes('403')) {
        throw new Error(
          `BigQuery access denied for dataset ${dataset}.\n` +
          'Make sure the service account or authenticated user has BigQuery Data Viewer role.\n' +
          'Also ensure the dataset exists and bulk export is enabled.'
        );
      }
      throw new Error(`BigQuery query failed: ${message}`);
    }
  }

  return results;
}

/**
 * Estimate the cost of querying GSC BigQuery data.
 * Returns estimated bytes and cost (on-demand pricing: $5/TB).
 *
 * NOTE: This is a rough heuristic based on ~200 bytes/row and ~10 rows/URL/day.
 * Actual BigQuery billing depends on bytes scanned, column pruning, and
 * partitioning. For precise cost, use BigQuery's dry-run feature.
 * The first 1 TB/month of queries is free on the on-demand pricing plan.
 */
export function estimateQueryCost(urlCount: number, days: number): { estimatedMB: number; estimatedCost: string } {
  const estimatedRows = urlCount * days * 10;
  const estimatedBytes = estimatedRows * 200;
  const estimatedMB = Math.round(estimatedBytes / (1024 * 1024));
  const estimatedCostUSD = (estimatedBytes / (1024 * 1024 * 1024 * 1024)) * 5;

  let costStr: string;
  if (estimatedCostUSD < 0.001) {
    costStr = 'Free tier (< $0.001)';
  } else if (estimatedCostUSD < 0.01) {
    costStr = `~$${estimatedCostUSD.toFixed(3)}`;
  } else {
    costStr = `~$${estimatedCostUSD.toFixed(2)}`;
  }

  return { estimatedMB, estimatedCost: costStr };
}
