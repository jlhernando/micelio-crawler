import { describe, it, expect } from 'vitest';
import { createHash } from 'node:crypto';
import { networkInterfaces } from 'node:os';
import { connect } from 'node:net';
import { createPkcePair, waitForOAuthCode } from './gsc-auth.js';

/** Start the callback server on a free port; resolves once it listens. */
async function startFlow(state: string, timeoutMs = 5_000) {
  let port = 0;
  let listening!: () => void;
  const ready = new Promise<void>((r) => { listening = r; });
  const code = waitForOAuthCode({ state, port: 0, timeoutMs, onListening: (p) => { port = p; listening(); } });
  code.catch(() => { /* asserted by the tests that expect a rejection */ });
  await ready;
  return { code, get: (path: string) => fetch(`http://127.0.0.1:${port}${path}`), port: () => port };
}

describe('GSC OAuth callback (gsc-auth)', () => {
  it('PKCE challenge is the S256 of the verifier', () => {
    const { verifier, challenge } = createPkcePair();
    expect(verifier.length).toBeGreaterThanOrEqual(43);
    expect(challenge).toBe(createHash('sha256').update(verifier).digest('base64url'));
    expect(createPkcePair().verifier).not.toBe(verifier);
  });

  it('ignores codes and errors with a wrong or missing state, accepts the right one', async () => {
    const flow = await startFlow('s3cret-state');
    // Another page racing an attacker-issued code in, or aborting the flow
    expect((await flow.get('/oauth2callback?code=attacker')).status).toBe(400);
    expect((await flow.get('/oauth2callback?code=attacker&state=wrong')).status).toBe(400);
    expect((await flow.get('/oauth2callback?error=access_denied')).status).toBe(400);
    // Wrong path, even with the right state
    expect((await flow.get('/other?code=x&state=s3cret-state')).status).toBe(404);
    // The real callback
    expect((await flow.get('/oauth2callback?code=good&state=s3cret-state')).status).toBe(200);
    await expect(flow.code).resolves.toBe('good');
  });

  it('an error callback with the right state ends the flow', async () => {
    const flow = await startFlow('st');
    await flow.get('/oauth2callback?error=access_denied&state=st');
    await expect(flow.code).rejects.toThrow(/access_denied/);
  });

  it('times out', async () => {
    const flow = await startFlow('st', 100);
    await expect(flow.code).rejects.toThrow(/timed out/);
  });

  it('listens on loopback only', async () => {
    const lan = Object.values(networkInterfaces()).flat()
      .find((i) => i && i.family === 'IPv4' && !i.internal)?.address;
    const flow = await startFlow('st', 2_000);
    try {
      if (lan) {
        const refused = await new Promise<boolean>((resolve) => {
          const s = connect({ host: lan, port: flow.port() });
          s.once('connect', () => { s.destroy(); resolve(false); });
          s.once('error', () => resolve(true));
        });
        expect(refused).toBe(true);
      }
    } finally {
      await flow.get('/oauth2callback?code=done&state=st');
    }
  });
});
