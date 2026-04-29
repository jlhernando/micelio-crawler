/**
 * Analytics & Tag Manager Detection
 *
 * Detects Google Analytics (UA + GA4), GTM, Meta Pixel, LinkedIn Insight,
 * Microsoft Clarity, Hotjar, and other common analytics/tracking tools.
 *
 * Usage: micelio spider https://example.com --snippet snippets/analytics.js
 */
export default async (page) => {
  return await page.evaluate(() => {
    const scripts = Array.from(document.querySelectorAll('script[src]'))
      .map((s) => s.src);
    const inlineScripts = Array.from(document.querySelectorAll('script:not([src])'))
      .map((s) => s.textContent || '');
    const allSource = scripts.join(' ') + ' ' + inlineScripts.join(' ');

    // Google Analytics
    const hasUA = /UA-\d{4,10}-\d{1,4}/.test(allSource);
    const uaId = allSource.match(/UA-\d{4,10}-\d{1,4}/)?.[0] || null;

    // GA4
    const hasGA4 = /G-[A-Z0-9]+/.test(allSource) || typeof window.gtag === 'function';
    const ga4Id = allSource.match(/G-[A-Z0-9]+/)?.[0] || null;

    // GTM
    const hasGTM = /GTM-[A-Z0-9]+/.test(allSource) || !!document.querySelector('noscript iframe[src*="googletagmanager"]');
    const gtmId = allSource.match(/GTM-[A-Z0-9]+/)?.[0] || null;

    // Meta Pixel (Facebook)
    const hasMetaPixel = /fbq\(/.test(allSource) || scripts.some((s) => s.includes('connect.facebook.net'));
    const metaPixelId = allSource.match(/fbq\(['"]init['"],\s*['"](\d+)['"]\)/)?.[1] || null;

    // LinkedIn Insight
    const hasLinkedIn = scripts.some((s) => s.includes('snap.licdn.com'));

    // Microsoft Clarity
    const hasClarity = /clarity\.ms/.test(allSource) || typeof window.clarity === 'function';

    // Hotjar
    const hasHotjar = /hotjar\.com/.test(allSource) || !!window.hj;

    // DataLayer
    const hasDataLayer = Array.isArray(window.dataLayer);
    const dataLayerSize = hasDataLayer ? window.dataLayer.length : 0;

    return {
      google_analytics_ua: hasUA,
      ua_id: uaId,
      google_analytics_ga4: hasGA4,
      ga4_id: ga4Id,
      google_tag_manager: hasGTM,
      gtm_id: gtmId,
      meta_pixel: hasMetaPixel,
      meta_pixel_id: metaPixelId,
      linkedin_insight: hasLinkedIn,
      microsoft_clarity: hasClarity,
      hotjar: hasHotjar,
      has_datalayer: hasDataLayer,
      datalayer_entries: dataLayerSize,
    };
  });
};
