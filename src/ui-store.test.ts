import { describe, it, expect } from 'vitest';
import { mkdtempSync, mkdirSync, writeFileSync, statSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { restrictStorePermissions } from './ui-store.js';

const mode = (p: string) => statSync(p).mode & 0o777;

describe.skipIf(process.platform === 'win32')('restrictStorePermissions', () => {
  it('makes an existing store directory and the DB files owner-only', () => {
    const root = mkdtempSync(join(tmpdir(), 'micelio-ui-'));
    try {
      const dir = join(root, 'ui');
      const crawls = join(dir, 'crawls');
      mkdirSync(crawls, { recursive: true, mode: 0o755 });
      const db = join(dir, 'micelio-ui.db');
      for (const f of [db, `${db}-wal`]) writeFileSync(f, '', { mode: 0o644 });

      restrictStorePermissions([dir, crawls], db);

      expect(mode(dir)).toBe(0o700);
      expect(mode(crawls)).toBe(0o700);
      expect(mode(db)).toBe(0o600);
      expect(mode(`${db}-wal`)).toBe(0o600);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  it('ignores missing paths', () => {
    expect(() => restrictStorePermissions(['/nonexistent/micelio'], '/nonexistent/micelio/x.db')).not.toThrow();
  });
});
