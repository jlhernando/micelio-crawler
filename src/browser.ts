import type { Browser, BrowserContext, Page } from 'playwright';
import { assertPublicUrl } from './net-guard.js';

let browser: Browser | null = null;
let currentProxy: string | undefined;
let proxyAppliedAtLaunch = false;

export function setBrowserProxy(proxy: string): void {
  if (browser) {
    // Browser already launched without this proxy — warn
    process.stderr.write('Warning: setBrowserProxy called after browser was already launched. Proxy will not take effect.\n');
  }
  currentProxy = proxy || undefined;
}

export async function getBrowser(): Promise<Browser> {
  if (!browser) {
    const { chromium } = await import('playwright');
    const launchOptions: Record<string, unknown> = { headless: true };
    if (currentProxy) {
      try {
        const proxyUrl = new URL(currentProxy);
        const proxyConfig: Record<string, string> = {
          server: `${proxyUrl.protocol}//${proxyUrl.host}`,
        };
        if (proxyUrl.username) proxyConfig.username = decodeURIComponent(proxyUrl.username);
        if (proxyUrl.password) proxyConfig.password = decodeURIComponent(proxyUrl.password);
        launchOptions.proxy = proxyConfig;
      } catch {
        // If URL parsing fails, pass the raw string as server
        launchOptions.proxy = { server: currentProxy };
      }
    }
    browser = await chromium.launch(launchOptions);
  }
  return browser;
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
 * workers are blocked so no request can bypass the interception.
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

export async function closeBrowser(): Promise<void> {
  if (browser) {
    await browser.close();
    browser = null;
  }
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
