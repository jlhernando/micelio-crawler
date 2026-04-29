import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { validateSettings, applyEnvFallbacks } from './ui-api.js';

describe('validateSettings', () => {
  it('accepts valid settings with correct types', () => {
    const { valid, rejected } = validateSettings({
      defaultDepth: 5,
      defaultLimit: 1000,
      defaultUserAgent: 'MyBot/1.0',
      defaultEmbeddings: true,
    });
    expect(valid).toEqual({
      defaultDepth: 5,
      defaultLimit: 1000,
      defaultUserAgent: 'MyBot/1.0',
      defaultEmbeddings: true,
    });
    expect(rejected).toEqual([]);
  });

  it('rejects unknown keys', () => {
    const { valid, rejected } = validateSettings({
      defaultDepth: 5,
      unknownKey: 'value',
      anotherBadKey: 42,
    });
    expect(valid).toEqual({ defaultDepth: 5 });
    expect(rejected).toContain('unknownKey');
    expect(rejected).toContain('anotherBadKey');
  });

  it('rejects values with wrong type', () => {
    const { valid, rejected } = validateSettings({
      defaultDepth: 'not a number',  // should be number
      defaultUserAgent: 42,          // should be string
      defaultEmbeddings: 'yes',      // should be boolean
    });
    expect(valid).toEqual({});
    expect(rejected).toContain('defaultDepth');
    expect(rejected).toContain('defaultUserAgent');
    expect(rejected).toContain('defaultEmbeddings');
  });

  it('validates all expected number fields', () => {
    const numberFields = [
      'defaultDepth', 'defaultLimit', 'defaultConcurrency', 'defaultDelay',
      'gscDays', 'ga4Days', 'similarityThreshold', 'liMaxSuggestions',
    ];
    const input: Record<string, unknown> = {};
    for (const field of numberFields) input[field] = 42;
    const { valid, rejected } = validateSettings(input);
    expect(rejected).toEqual([]);
    for (const field of numberFields) {
      expect(valid[field]).toBe(42);
    }
  });

  it('validates all expected string fields', () => {
    // File path fields require absolute paths, so use special values
    const stringFields = [
      'defaultUserAgent', 'psiKey', 'aiProvider', 'aiModel', 'aiKey',
      'gscProperty', 'ga4Property',
      'cruxKey', 'embeddingModel', 'defaultOutputFormat',
    ];
    const input: Record<string, unknown> = {};
    for (const field of stringFields) input[field] = 'test';
    // File path fields need absolute paths
    input.gscKeyFile = '/path/to/gsc.json';
    input.ga4KeyFile = '/path/to/ga4.json';
    const { valid, rejected } = validateSettings(input);
    expect(rejected).toEqual([]);
    for (const field of stringFields) {
      expect(valid[field]).toBe('test');
    }
    expect(valid.gscKeyFile).toBe('/path/to/gsc.json');
    expect(valid.ga4KeyFile).toBe('/path/to/ga4.json');
  });

  it('validates all expected boolean fields', () => {
    const boolFields = [
      'defaultEmbeddings', 'defaultNgrams', 'defaultLinkIntelligence',
      'liNoCentrality', 'defaultSitemapOut', 'defaultHtmlReport',
      'defaultCheckExternal', 'defaultJsRendering',
    ];
    const input: Record<string, unknown> = {};
    for (const field of boolFields) input[field] = true;
    const { valid, rejected } = validateSettings(input);
    expect(rejected).toEqual([]);
    for (const field of boolFields) {
      expect(valid[field]).toBe(true);
    }
  });

  // File path validation
  it('accepts absolute file paths', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: '/home/user/credentials.json',
      ga4KeyFile: '/etc/secrets/ga4.json',
    });
    expect(rejected).toEqual([]);
    expect(valid.gscKeyFile).toBe('/home/user/credentials.json');
    expect(valid.ga4KeyFile).toBe('/etc/secrets/ga4.json');
  });

  it('rejects relative file paths', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: 'relative/path.json',
    });
    expect(rejected).toContain('gscKeyFile');
    expect(valid.gscKeyFile).toBeUndefined();
  });

  it('rejects file paths with directory traversal', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: '/home/user/../../../etc/passwd',
    });
    expect(rejected).toContain('gscKeyFile');
    expect(valid.gscKeyFile).toBeUndefined();
  });

  it('rejects file paths with backslash traversal', () => {
    const { valid, rejected } = validateSettings({
      ga4KeyFile: '/home\\..\\etc\\passwd',
    });
    // Backslashes are normalized to / before checking
    expect(rejected).toContain('ga4KeyFile');
  });

  it('accepts empty file path strings', () => {
    const { valid, rejected } = validateSettings({
      gscKeyFile: '',
    });
    // Empty string is OK (means not configured)
    expect(rejected).toEqual([]);
    expect(valid.gscKeyFile).toBe('');
  });

  it('handles empty input', () => {
    const { valid, rejected } = validateSettings({});
    expect(valid).toEqual({});
    expect(rejected).toEqual([]);
  });
});

describe('applyEnvFallbacks', () => {
  const originalEnv = process.env;

  beforeEach(() => {
    process.env = { ...originalEnv };
  });

  afterEach(() => {
    process.env = originalEnv;
  });

  it('does nothing when no env vars are set', () => {
    delete process.env.MICELIO_PSI_KEY;
    delete process.env.MICELIO_CRUX_KEY;
    delete process.env.MICELIO_AI_KEY;

    const result = applyEnvFallbacks({ defaultDepth: 5 });
    expect(result).toEqual({ defaultDepth: 5 });
  });

  it('fills missing keys from env vars', () => {
    process.env.MICELIO_PSI_KEY = 'env-psi-key';
    process.env.MICELIO_CRUX_KEY = 'env-crux-key';
    process.env.MICELIO_AI_KEY = 'env-ai-key';

    const result = applyEnvFallbacks({});
    expect(result.psiKey).toBe('env-psi-key');
    expect(result.cruxKey).toBe('env-crux-key');
    expect(result.aiKey).toBe('env-ai-key');
  });

  it('does not override existing values', () => {
    process.env.MICELIO_PSI_KEY = 'env-psi-key';

    const result = applyEnvFallbacks({ psiKey: 'user-psi-key' });
    expect(result.psiKey).toBe('user-psi-key');
  });

  it('does not mutate the input object', () => {
    process.env.MICELIO_PSI_KEY = 'env-psi-key';

    const input = { defaultDepth: 5 };
    const result = applyEnvFallbacks(input);
    expect(input).toEqual({ defaultDepth: 5 }); // unchanged
    expect(result.psiKey).toBe('env-psi-key');
  });

  it('skips falsy env var values', () => {
    process.env.MICELIO_PSI_KEY = '';

    const result = applyEnvFallbacks({});
    expect(result.psiKey).toBeUndefined();
  });
});
