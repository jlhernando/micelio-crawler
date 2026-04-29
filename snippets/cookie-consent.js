/**
 * Cookie Consent Platform Detection
 *
 * Detects common cookie consent management platforms (CMPs):
 * OneTrust, Cookiebot, CookieYes, Osano, TrustArc, Quantcast, etc.
 *
 * Usage: micelio spider https://example.com --snippet snippets/cookie-consent.js
 */
export default async (page) => {
  return await page.evaluate(() => {
    const html = document.documentElement.outerHTML;
    const scripts = Array.from(document.querySelectorAll('script[src]'))
      .map((s) => s.src.toLowerCase());

    const platforms = {
      onetrust: scripts.some((s) => s.includes('onetrust')) || !!document.getElementById('onetrust-consent-sdk'),
      cookiebot: scripts.some((s) => s.includes('cookiebot')) || !!window.Cookiebot,
      cookieyes: scripts.some((s) => s.includes('cookieyes')) || !!document.querySelector('.cky-consent-container'),
      osano: scripts.some((s) => s.includes('osano')) || !!window.Osano,
      trustarc: scripts.some((s) => s.includes('trustarc') || s.includes('truste')),
      quantcast: scripts.some((s) => s.includes('quantcast')) || !!window.__tcfapi,
      iubenda: scripts.some((s) => s.includes('iubenda')),
      termly: scripts.some((s) => s.includes('termly')),
      complianz: html.includes('complianz') || !!document.querySelector('.cmplz-cookiebanner'),
      cookie_notice: !!document.querySelector('[class*="cookie-notice"], [id*="cookie-notice"], [class*="cookie-banner"], [id*="cookie-banner"]'),
    };

    const detected = Object.entries(platforms)
      .filter(([, found]) => found)
      .map(([name]) => name);

    return {
      has_cookie_consent: detected.length > 0,
      platforms_detected: detected.join(', ') || 'none',
      platform_count: detected.length,
      has_tcf_api: typeof window.__tcfapi === 'function',
    };
  });
};
