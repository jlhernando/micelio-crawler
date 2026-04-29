import { describe, it, expect } from 'vitest';
import { normalizeUrl, normalizeUrlForComparison, isPrivateUrl, formatError, formatDateYmd } from './utils.js';

describe('normalizeUrl', () => {
  it('strips hash fragments', () => {
    expect(normalizeUrl('https://example.com/page#section')).toBe('https://example.com/page');
  });

  it('strips trailing slash from non-root paths', () => {
    expect(normalizeUrl('https://example.com/page/')).toBe('https://example.com/page');
  });

  it('preserves root trailing slash', () => {
    expect(normalizeUrl('https://example.com/')).toBe('https://example.com/');
  });

  it('removes default port 443 for HTTPS', () => {
    expect(normalizeUrl('https://example.com:443/page')).toBe('https://example.com/page');
  });

  it('removes default port 80 for HTTP', () => {
    expect(normalizeUrl('http://example.com:80/page')).toBe('http://example.com/page');
  });

  it('preserves non-default ports', () => {
    expect(normalizeUrl('https://example.com:8080/page')).toBe('https://example.com:8080/page');
  });

  it('sorts query parameters alphabetically', () => {
    expect(normalizeUrl('https://example.com/page?z=1&a=2')).toBe('https://example.com/page?a=2&z=1');
  });

  it('returns null for non-HTTP protocols', () => {
    expect(normalizeUrl('ftp://example.com/file')).toBeNull();
    expect(normalizeUrl('mailto:user@example.com')).toBeNull();
  });

  it('returns null for invalid URLs', () => {
    expect(normalizeUrl('not a url')).toBeNull();
    expect(normalizeUrl('')).toBeNull();
  });

  it('handles URLs with no path', () => {
    const result = normalizeUrl('https://example.com');
    expect(result).toBe('https://example.com/');
  });

  it('preserves empty query string', () => {
    const result = normalizeUrl('https://example.com/page?');
    // URL parser preserves the trailing ? even with no params
    expect(result).toBe('https://example.com/page?');
  });
});

describe('normalizeUrlForComparison', () => {
  it('strips www prefix', () => {
    expect(normalizeUrlForComparison('https://www.example.com/page')).toBe('example.com/page');
  });

  it('strips protocol', () => {
    expect(normalizeUrlForComparison('http://example.com/page')).toBe('example.com/page');
    expect(normalizeUrlForComparison('https://example.com/page')).toBe('example.com/page');
  });

  it('strips trailing slash', () => {
    expect(normalizeUrlForComparison('https://example.com/page/')).toBe('example.com/page');
  });

  it('preserves root path as /', () => {
    expect(normalizeUrlForComparison('https://example.com/')).toBe('example.com/');
  });

  it('sorts query parameters', () => {
    expect(normalizeUrlForComparison('https://example.com/page?z=1&a=2')).toBe('example.com/page?a=2&z=1');
  });

  it('handles invalid URLs gracefully', () => {
    expect(normalizeUrlForComparison('not-a-url')).toBe('not-a-url');
  });

  it('matches HTTP and HTTPS versions of same URL', () => {
    const http = normalizeUrlForComparison('http://example.com/page');
    const https = normalizeUrlForComparison('https://example.com/page');
    expect(http).toBe(https);
  });

  it('matches www and non-www versions', () => {
    const www = normalizeUrlForComparison('https://www.example.com/page');
    const noWww = normalizeUrlForComparison('https://example.com/page');
    expect(www).toBe(noWww);
  });
});

describe('isPrivateUrl', () => {
  it('detects localhost', () => {
    expect(isPrivateUrl('http://localhost/path')).toBe(true);
    expect(isPrivateUrl('http://127.0.0.1/path')).toBe(true);
  });

  it('detects IPv6 loopback', () => {
    expect(isPrivateUrl('http://[::1]/path')).toBe(true);
  });

  it('detects 0.0.0.0', () => {
    expect(isPrivateUrl('http://0.0.0.0/path')).toBe(true);
  });

  it('detects IPv6 link-local (fe80::)', () => {
    expect(isPrivateUrl('http://[fe80::1]/path')).toBe(true);
  });

  it('detects IPv6 unique-local (fc00::/fd00::)', () => {
    expect(isPrivateUrl('http://[fc00::1]/path')).toBe(true);
    expect(isPrivateUrl('http://[fd12::1]/path')).toBe(true);
  });

  it('detects 10.x.x.x range', () => {
    expect(isPrivateUrl('http://10.0.0.1/path')).toBe(true);
    expect(isPrivateUrl('http://10.255.255.255/path')).toBe(true);
  });

  it('detects 192.168.x.x range', () => {
    expect(isPrivateUrl('http://192.168.0.1/path')).toBe(true);
    expect(isPrivateUrl('http://192.168.255.255/path')).toBe(true);
  });

  it('detects 172.16-31.x.x range', () => {
    expect(isPrivateUrl('http://172.16.0.1/path')).toBe(true);
    expect(isPrivateUrl('http://172.31.255.255/path')).toBe(true);
  });

  it('does not flag 172.32.x.x as private', () => {
    expect(isPrivateUrl('http://172.32.0.1/path')).toBe(false);
  });

  it('detects AWS metadata endpoint', () => {
    expect(isPrivateUrl('http://169.254.169.254/latest/meta-data')).toBe(true);
  });

  it('returns false for public IPs', () => {
    expect(isPrivateUrl('https://8.8.8.8/dns')).toBe(false);
    expect(isPrivateUrl('https://example.com')).toBe(false);
  });

  it('returns false for invalid URLs', () => {
    expect(isPrivateUrl('not a url')).toBe(false);
  });
});

describe('formatError', () => {
  it('extracts message from Error instances', () => {
    expect(formatError(new Error('test error'))).toBe('test error');
  });

  it('converts non-Error values to string', () => {
    expect(formatError('string error')).toBe('string error');
    expect(formatError(42)).toBe('42');
    expect(formatError(null)).toBe('null');
  });
});

describe('formatDateYmd', () => {
  it('formats date as YYYY-MM-DD', () => {
    expect(formatDateYmd(new Date('2024-03-15T10:30:00Z'))).toBe('2024-03-15');
  });

  it('handles different dates', () => {
    expect(formatDateYmd(new Date('2023-01-01T00:00:00Z'))).toBe('2023-01-01');
    expect(formatDateYmd(new Date('2024-12-31T23:59:59Z'))).toBe('2024-12-31');
  });

  it('converts non-UTC dates to UTC before formatting', () => {
    // A date in UTC+5 at 00:30 is still previous day in UTC
    expect(formatDateYmd(new Date('2024-01-01T00:30:00+05:00'))).toBe('2023-12-31');
  });
});
