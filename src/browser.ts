import type { Browser, BrowserContext } from 'playwright';
import { assertPublicUrl } from './net-guard.js';
import { startGuardedProxy, type GuardedProxy, type GuardedProxyOptions } from './guard-proxy.js';

let browser: Browser | null = null;
let launching: Promise<Browser> | null = null;
let guardProxy: GuardedProxy | null = null;
let currentProxy: string | undefined;
let testProxyOptions: Pick<GuardedProxyOptions, 'connectAddress'> = {};

/**
 * Trusted proxy (CLI --proxy, or the dashboard's --trusted-proxy). Chromium
 * never talks to it directly: the local guarded proxy chains to it after
 * checking each target.
 */
export function setBrowserProxy(proxy: string): void {
  if (browser || launching) {
    process.stderr.write('Warning: setBrowserProxy called after browser was already launched. Proxy will not take effect.\n');
  }
  currentProxy = proxy || undefined;
}

/** Test seam: options for the guarded proxy of the next browser launch. */
export function setGuardedProxyOptionsForTests(opts: Pick<GuardedProxyOptions, 'connectAddress'>): void {
  testProxyOptions = opts;
}

/**
 * Headless Chromium whose every connection (pages, redirects, frames,
 * subresources, workers, WebSockets, loopback included) goes through the
 * local guarded proxy, which resolves and checks each target and dials the
 * checked address. Chromium never resolves crawl hosts itself.
 */
export async function getBrowser(): Promise<Browser> {
  if (browser) return browser;
  if (!launching) {
    launching = (async () => {
      const proxy = await startGuardedProxy({ ...testProxyOptions, upstream: currentProxy });
      try {
        const { chromium } = await import('playwright');
        const b = await chromium.launch({
          headless: true,
          args: [`--proxy-server=${proxy.url}`, '--proxy-bypass-list=<-loopback>'],
        });
        b.on('disconnected', () => {
          if (browser === b) browser = null;
          if (guardProxy === proxy) guardProxy = null;
          void proxy.close();
        });
        guardProxy = proxy;
        browser = b;
        return b;
      } catch (err) {
        await proxy.close();
        throw err;
      }
    })().finally(() => {
      launching = null;
    });
  }
  return launching;
}

/** Whether the browser may load this URL (SSRF guard, after DNS). */
export async function browserUrlAllowed(rawUrl: string): Promise<boolean> {
  let protocol: string;
  try {
    protocol = new URL(rawUrl).protocol;
  } catch {
    return false;
  }
  // data:, blob: and about: never touch the network
  if (protocol === 'data:' || protocol === 'blob:' || protocol === 'about:') return true;
  if (protocol === 'ws:' || protocol === 'wss:') {
    rawUrl = rawUrl.replace(/^ws/, 'http');
  }
  try {
    await assertPublicUrl(rawUrl);
    return true;
  } catch {
    return false;
  }
}

/**
 * New browser context whose every request (navigations, redirects, frames,
 * subresources, WebSockets) is checked by the SSRF guard first. Service
 * workers are blocked so no request can bypass the interception. This is
 * the first layer: each connection is checked again, after DNS, by the
 * guarded proxy the browser was launched with (DNS rebinding cannot slip
 * between this check and Chromium's own lookup).
 */
export async function newGuardedContext(userAgent: string): Promise<BrowserContext> {
  const b = await getBrowser();
  const context = await b.newContext({ userAgent, serviceWorkers: 'block' });
  await context.route('**/*', async (route) => {
    if (await browserUrlAllowed(route.request().url())) {
      await route.continue();
    } else {
      await route.abort('blockedbyclient');
    }
  });
  await context.routeWebSocket(/.*/, async (ws) => {
    if (await browserUrlAllowed(ws.url())) {
      ws.connectToServer();
    } else {
      await ws.close({ code: 1008, reason: 'Blocked: private address' });
    }
  });
  return context;
}

export async function renderPage(url: string, userAgent: string): Promise<{ html: string; statusCode: number }> {
  const context = await newGuardedContext(userAgent);
  const page = await context.newPage();

  try {
    const response = await page.goto(url, {
      waitUntil: 'networkidle',
      timeout: 30000,
    });

    const html = await page.content();
    const statusCode = response?.status() ?? 0;

    return { html, statusCode };
  } finally {
    await context.close();
  }
}

/** Close the browser and its guarded proxy. */
export async function closeBrowser(): Promise<void> {
  if (launching) await launching.catch(() => undefined);
  const b = browser;
  const proxy = guardProxy;
  browser = null;
  guardProxy = null;
  if (b) await b.close();
  if (proxy) await proxy.close();
}

export function isSpaLikely(html: string): boolean {
  // Heuristic: if the body has very little text content but has framework root divs
  const bodyMatch = html.match(/<body[^>]*>([\s\S]*?)<\/body>/i);
  if (!bodyMatch) return false;

  const body = bodyMatch[1];

  // Check for common SPA root elements
  const hasFrameworkRoot =
    /<div\s+id=["'](root|app|__next|__nuxt)["']/.test(body);

  if (!hasFrameworkRoot) return false;

  // Check if body text content is minimal (stripped of tags)
  const textContent = body.replace(/<[^>]+>/g, '').replace(/\s+/g, ' ').trim();
  return textContent.length < 200;
}
