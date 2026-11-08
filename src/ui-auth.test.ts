import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { createServer, type IncomingMessage, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';
import {
  AuthLimiter, authTokenProblem, checkUiAccess, MIN_AUTH_TOKEN_LENGTH, clientKey, handleLogin, isLoopbackHost, sessionCookieName, sessionValue, type UiAccessOptions,
} from './ui-auth.js';

function req(method: string, headers: Record<string, string>, url = '/api/crawls'): IncomingMessage {
  return { method, url, headers } as unknown as IncomingMessage;
}

describe('isLoopbackHost', () => {
  it('accepts loopback names only', () => {
    for (const h of ['localhost', '127.0.0.1', '::1', '[::1]', '127.0.0.2']) expect(isLoopbackHost(h)).toBe(true);
    for (const h of ['0.0.0.0', '192.168.1.5', 'evil.com', '']) expect(isLoopbackHost(h)).toBe(false);
  });
});

describe('checkUiAccess without a token', () => {
  const opts = { authToken: '' };

  it('allows same-origin dashboard requests', () => {
    expect(checkUiAccess(req('GET', { host: 'localhost:3100' }), opts, 'api')).toBeNull();
    expect(checkUiAccess(req('POST', {
      host: 'localhost:3100', origin: 'http://localhost:3100', 'sec-fetch-site': 'same-origin',
    }), opts, 'api')).toBeNull();
    // curl / scripts send no Origin
    expect(checkUiAccess(req('POST', { host: '127.0.0.1:3100' }), opts, 'api')).toBeNull();
  });

  it('refuses a rebinding Host header', () => {
    expect(checkUiAccess(req('GET', { host: 'attacker.example:3100' }), opts, 'api')?.status).toBe(403);
    expect(checkUiAccess(req('GET', {}), opts, 'api')?.status).toBe(403);
  });

  it('refuses cross-origin state changes and WebSockets', () => {
    expect(checkUiAccess(req('POST', { host: 'localhost:3100', origin: 'https://evil.com' }), opts, 'api')?.status).toBe(403);
    expect(checkUiAccess(req('POST', { host: 'localhost:3100', 'sec-fetch-site': 'cross-site' }), opts, 'api')?.status).toBe(403);
    expect(checkUiAccess(req('DELETE', { host: 'localhost:3100', origin: 'null' }), opts, 'api')?.status).toBe(403);
    expect(checkUiAccess(req('GET', { host: 'localhost:3100', origin: 'https://evil.com' }, '/ws'), opts, 'ws')?.status).toBe(403);
  });
});

describe('checkUiAccess with a token', () => {
  const opts = { authToken: 's3cret-token' };

  it('requires the bearer token on the API', () => {
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100' }), opts, 'api')?.status).toBe(401);
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100', authorization: 'Bearer wrong' }), opts, 'api')?.status).toBe(401);
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100', authorization: 'Bearer s3cret-token' }), opts, 'api')).toBeNull();
  });

  it('accepts the token as a query parameter only for WebSockets', () => {
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100' }, '/ws?token=s3cret-token'), opts, 'ws')).toBeNull();
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100' }, '/api/crawls?token=s3cret-token'), opts, 'api')?.status).toBe(401);
  });
});

describe('dashboard login (POST /login + session cookie)', () => {
  const token = 's3cret-token';
  const cookieName = sessionCookieName(3100);
  let server: Server;
  let base = '';
  let opts: UiAccessOptions;

  beforeEach(async () => {
    opts = { authToken: token, cookieName, limiter: new AuthLimiter() };
    server = createServer((rq, rs) => { void handleLogin(rq, rs, opts); });
    await new Promise<void>((r) => server.listen(0, '127.0.0.1', r));
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  });
  afterEach(async () => {
    await new Promise((r) => server.close(r));
  });

  const login = (t: string, headers: Record<string, string> = {}) => fetch(`${base}/login`, {
    method: 'POST', body: new URLSearchParams({ token: t }), headers,
  });

  it('sets an HttpOnly SameSite=Strict cookie that never holds the token, and the API accepts it', async () => {
    const res = await login(token);
    expect(res.status).toBe(204);
    const setCookie = res.headers.get('set-cookie') || '';
    expect(setCookie).toMatch(new RegExp(`^${cookieName}=[0-9a-f]{64}; Path=/; HttpOnly; SameSite=Strict$`));
    expect(setCookie).not.toContain(token);
    const cookie = setCookie.split(';')[0];
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100', cookie }), opts, 'api')).toBeNull();
    // The browser WebSocket sends the same cookie
    expect(checkUiAccess(req('GET', {
      host: '10.0.0.5:3100', origin: 'http://10.0.0.5:3100', cookie,
    }, '/ws'), opts, 'ws')).toBeNull();
  });

  it('refuses a wrong token, a cross-origin post and a forged cookie', async () => {
    expect((await login('wrong')).status).toBe(401);
    expect((await login(token, { origin: 'https://evil.example', 'sec-fetch-site': 'cross-site' })).status).toBe(403);
    for (const cookie of [`${cookieName}=${token}`, `${cookieName}=${'0'.repeat(64)}`, `other=${sessionValue(token)}`]) {
      expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100', cookie }), opts, 'api')?.status).toBe(401);
    }
  });

  it('throttles wrong tokens only: the right token always gets in, even while blocked', async () => {
    let now = 1_000_000;
    opts.limiter = new AuthLimiter(() => now);
    for (let i = 0; i < AuthLimiter.FREE_FAILS; i++) expect((await login('wrong')).status).toBe(401);
    expect((await login('wrong')).status).toBe(401); // first strike past the free ones
    const blocked = await login('wrong');
    expect(blocked.status).toBe(429);
    expect(blocked.headers.get('retry-after')).toBe('1');
    // Same blocked address, right token: logged in
    expect((await login(token)).status).toBe(204);
    now += 1_001;
    expect((await login('wrong')).status).toBe(401);
  });

  it('a blocked address (reverse proxy, overflow bucket) still passes with a valid Bearer or cookie', () => {
    const limiter = new AuthLimiter(() => 0);
    const bearerOpts: UiAccessOptions = { authToken: token, cookieName, limiter };
    // Every client shares one address (no socket here: key '')
    for (let i = 0; i <= AuthLimiter.FREE_FAILS + 3; i++) {
      checkUiAccess(req('GET', { host: '10.0.0.5:3100', authorization: 'Bearer wrong' }), bearerOpts, 'api');
    }
    expect(limiter.blockedMs('')).toBeGreaterThan(0);
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100', authorization: 'Bearer wrong' }), bearerOpts, 'api')?.status).toBe(429);
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100', authorization: `Bearer ${token}` }), bearerOpts, 'api')).toBeNull();
    const cookie = `${cookieName}=${sessionValue(token)}`;
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100', cookie }), bearerOpts, 'api')).toBeNull();
    expect(checkUiAccess(req('GET', { host: '10.0.0.5:3100' }, `/ws?token=${token}`), bearerOpts, 'ws')).toBeNull();
  });

  it('groups IPv6 clients by /64 and IPv4-mapped addresses by IPv4', () => {
    const at = (remoteAddress: string) => clientKey({ socket: { remoteAddress } } as unknown as IncomingMessage);
    expect(at('2001:db8:1:2:aaaa::1')).toBe(at('2001:db8:1:2:bbbb:cccc:dddd:eeee'));
    expect(at('2001:db8:1:2::1')).toBe('2001:db8:1:2::/64');
    expect(at('2001:db8:1:3::1')).not.toBe(at('2001:db8:1:2::1'));
    expect(at('::ffff:192.0.2.7')).toBe('192.0.2.7');
  });

  it('has nothing to log into without a token', async () => {
    opts = { authToken: '' };
    expect((await login('x')).status).toBe(404);
  });
});

describe('authTokenProblem', () => {
  it('accepts no token (loopback-only dashboards)', () => {
    expect(authTokenProblem('')).toBeNull();
  });
  it('refuses a token shorter than the minimum', () => {
    const p = authTokenProblem('s3cret-token');
    expect(p).toContain(`at least ${MIN_AUTH_TOKEN_LENGTH} characters`);
    expect(p).toContain('got 12');
    expect(p).not.toContain('s3cret');
  });
  it('accepts a token of the minimum length or longer', () => {
    expect(authTokenProblem('a'.repeat(MIN_AUTH_TOKEN_LENGTH))).toBeNull();
    expect(authTokenProblem('b'.repeat(64))).toBeNull();
  });
});
