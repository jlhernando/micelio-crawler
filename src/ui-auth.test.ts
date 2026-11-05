import { describe, it, expect } from 'vitest';
import type { IncomingMessage } from 'node:http';
import { checkUiAccess, isLoopbackHost } from './ui-auth.js';

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
