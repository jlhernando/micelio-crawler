/**
 * undici dispatcher for crawls that go through a user-configured proxy
 * (CLI --proxy, dashboard --trusted-proxy), used instead of undici's
 * ProxyAgent.
 *
 * ProxyAgent hands the proxy the target's host name, and the proxy resolves
 * it again: a name that answered with a public address for our check can
 * answer with an address inside the proxy's network the second time (DNS
 * rebinding). Here every target is resolved and checked locally with the
 * SSRF guard, and the proxy only ever gets the checked IP address:
 *
 * - https targets (any proxy) and http targets through a SOCKS proxy: a
 *   tunnel to <ip>:<port> (HTTP CONNECT, SOCKS5 IPv4/IPv6, SOCKS4 IPv4),
 *   then TLS with servername = the original host, so SNI and the
 *   certificate check use the real name. Requests keep Host = original host.
 * - http targets through an HTTP(S) proxy: an absolute-URI request
 *   (GET http://<ip>:<port>/path) with Host = the original host.
 *
 * A proxy that refuses IP-address targets makes the request fail; there is
 * no fallback to sending the name.
 */
import { connect as tlsConnect, type ConnectionOptions } from 'node:tls';
import { isIP } from 'node:net';
import { Agent, Dispatcher, type buildConnector } from 'undici';
import { basicAuth, hostForUrl, openUpstreamTunnel, whenConnected } from './guard-proxy.js';
import { bareHost, resolvePublicAddress } from './net-guard.js';

export interface PinnedProxyOptions {
  /** Extra TLS options for the origin connection (tests pass a test CA). */
  tls?: ConnectionOptions;
}

const DEFAULT_PORTS: Record<string, number> = { 'http:': 80, 'https:': 443 };

type HeaderList = string[];

/** Flatten undici's header shapes (object, flat array, iterable of pairs). */
function flattenHeaders(headers: Dispatcher.DispatchOptions['headers']): HeaderList {
  const out: HeaderList = [];
  if (!headers) return out;
  if (Array.isArray(headers)) {
    if (headers.length && Array.isArray(headers[0])) {
      for (const [k, v] of headers as unknown as [string, string][]) out.push(String(k), String(v));
    } else {
      for (let i = 0; i + 1 < headers.length; i += 2) out.push(String(headers[i]), String(headers[i + 1]));
    }
    return out;
  }
  if (typeof (headers as Iterable<unknown>)[Symbol.iterator] === 'function') {
    for (const [k, v] of headers as Iterable<[string, string | string[] | undefined]>) {
      if (v === undefined) continue;
      for (const one of Array.isArray(v) ? v : [v]) out.push(String(k), String(one));
    }
    return out;
  }
  for (const [k, v] of Object.entries(headers as Record<string, string | string[] | undefined>)) {
    if (v === undefined) continue;
    for (const one of Array.isArray(v) ? v : [v]) out.push(k, String(one));
  }
  return out;
}

/** Report an error to an undici handler of either API generation. */
function failHandler(handler: Dispatcher.DispatchHandler, err: Error): void {
  const h = handler as Dispatcher.DispatchHandler & {
    onError?: (err: Error) => void;
    onResponseError?: (controller: unknown, err: Error) => void;
  };
  if (typeof h.onError === 'function') {
    h.onError(err);
    return;
  }
  const controller = { aborted: true, paused: false, reason: err, abort() {}, pause() {}, resume() {} };
  h.onResponseError?.(controller, err);
}

export class PinnedProxyAgent extends Dispatcher {
  readonly #upstream: URL;
  /** Origin connections tunnelled through the proxy to a checked IP. */
  readonly #tunnelAgent: Agent;
  /** Plain connections to the HTTP(S) proxy for absolute-URI requests. */
  readonly #proxyAgent: Agent | null;
  readonly #proxyOrigin: string;

  constructor(proxyUrl: string, options: PinnedProxyOptions = {}) {
    super();
    const upstream = new URL(proxyUrl);
    const supported = ['http:', 'https:', 'socks4:', 'socks4a:', 'socks5:', 'socks5h:'];
    if (!supported.includes(upstream.protocol)) {
      throw new Error(`unsupported proxy scheme ${upstream.protocol}`);
    }
    this.#upstream = upstream;
    this.#proxyOrigin = `${upstream.protocol}//${upstream.host}`;
    const httpProxy = upstream.protocol === 'http:' || upstream.protocol === 'https:';
    this.#proxyAgent = httpProxy ? new Agent() : null;
    this.#tunnelAgent = new Agent({
      connect: (opts: buildConnector.Options, cb: buildConnector.Callback) => {
        this.#connectOrigin(opts, options.tls).then((s) => cb(null, s), (err: Error) => cb(err, null));
      },
    });
  }

  async #connectOrigin(opts: buildConnector.Options, tls: ConnectionOptions | undefined) {
    const host = bareHost(opts.hostname);
    const port = opts.port ? Number(opts.port) : DEFAULT_PORTS[opts.protocol] ?? 80;
    const tunnel = await openUpstreamTunnel(this.#upstream, { host, port });
    if (opts.protocol !== 'https:') return tunnel;
    // TLS end to end with the origin: SNI and certificate name are the
    // original host, whatever IP the tunnel reaches
    const servername = opts.servername || (isIP(host) ? undefined : host);
    try {
      return await whenConnected(
        tlsConnect({ ...tls, socket: tunnel, servername, ALPNProtocols: ['http/1.1'] }),
        'secureConnect',
      );
    } catch (err) {
      tunnel.destroy();
      throw err;
    }
  }

  dispatch(opts: Dispatcher.DispatchOptions, handler: Dispatcher.DispatchHandler): boolean {
    let origin: URL;
    try {
      origin = new URL(String(opts.origin));
    } catch (err) {
      failHandler(handler, err as Error);
      return false;
    }
    if (origin.protocol !== 'http:' || !this.#proxyAgent) {
      return this.#tunnelAgent.dispatch(opts, handler);
    }
    // Plain http through an HTTP proxy: absolute URI with the checked IP
    const proxyAgent = this.#proxyAgent;
    const port = origin.port ? Number(origin.port) : 80;
    resolvePublicAddress(origin.hostname).then(({ address }) => {
      const headers = flattenHeaders(opts.headers).filter((_, i, all) => {
        const name = all[i - (i % 2)].toLowerCase();
        return name !== 'host' && name !== 'proxy-authorization';
      });
      headers.push('host', origin.host);
      if (this.#upstream.username) headers.push('proxy-authorization', `Basic ${basicAuth(this.#upstream)}`);
      proxyAgent.dispatch({
        ...opts,
        origin: this.#proxyOrigin,
        path: `http://${hostForUrl(address)}:${port}${opts.path}`,
        headers,
      }, handler);
    }, (err: Error) => failHandler(handler, err));
    return true;
  }

  close(): Promise<void>;
  close(callback: () => void): void;
  close(callback?: () => void): Promise<void> | void {
    const p = Promise.all([this.#tunnelAgent.close(), this.#proxyAgent?.close()]).then(() => undefined);
    if (callback) {
      p.then(callback, callback);
      return;
    }
    return p;
  }

  destroy(): Promise<void>;
  destroy(err: Error | null): Promise<void>;
  destroy(callback: () => void): void;
  destroy(err: Error | null, callback: () => void): void;
  destroy(a?: Error | null | (() => void), b?: () => void): Promise<void> | void {
    const err = typeof a === 'function' ? null : a ?? null;
    const callback = typeof a === 'function' ? a : b;
    const p = Promise.all([this.#tunnelAgent.destroy(err), this.#proxyAgent?.destroy(err)]).then(() => undefined);
    if (callback) {
      p.then(callback, callback);
      return;
    }
    return p;
  }
}

/** Dispatcher for a proxy URL that pins every request to a checked IP. */
export function pinnedProxyDispatcher(proxyUrl: string, options: PinnedProxyOptions = {}): PinnedProxyAgent {
  return new PinnedProxyAgent(proxyUrl, options);
}
