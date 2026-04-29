import { createWriteStream, type WriteStream } from 'node:fs';
import { format } from 'fast-csv';
import type { HeadResult } from './types.js';

export class HeadResultWriter {
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

  write(result: HeadResult): void {
    if (this.outputFormat === 'jsonl') {
      this.stream.write(JSON.stringify(result) + '\n');
    } else if (this.csvStream) {
      this.csvStream.write(this.flattenForCsv(result));
    }
  }

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

  private flattenForCsv(result: HeadResult): Record<string, string | number | boolean | null> {
    return {
      url: result.url,
      final_url: result.finalUrl,
      status_code: result.statusCode,
      redirect_chain_length: result.redirectChain.length,
      redirect_chain: result.redirectChain.map(h => `${h.statusCode}:${h.url}`).join(' -> ') || '',
      response_time_ms: result.responseTimeMs,
      content_type: result.contentType,
      content_length: result.contentLength ?? '',
      server: result.server,
      x_robots_tag: result.xRobotsTag || '',
      link_canonical: result.linkCanonical || '',
      hsts: result.hsts,
      csp: result.csp,
      x_frame_options: result.xFrameOptions || '',
      referrer_policy: result.referrerPolicy || '',
      cache_control: result.cacheControl || '',
      error: result.error || '',
    };
  }
}
