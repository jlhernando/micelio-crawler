/**
 * Technology Stack Detection
 *
 * Detects frontend frameworks, CMS, CDNs, and other technologies
 * by inspecting DOM, scripts, meta tags, and HTTP headers.
 *
 * Usage: micelio spider https://example.com --snippet snippets/tech-stack.js
 */
export default async (page) => {
  return await page.evaluate(() => {
    const html = document.documentElement.outerHTML;
    const scripts = Array.from(document.querySelectorAll('script[src]'))
      .map((s) => s.src.toLowerCase());
    const meta = Object.fromEntries(
      Array.from(document.querySelectorAll('meta[name]'))
        .map((m) => [m.getAttribute('name'), m.getAttribute('content')])
    );

    const detect = {
      // Frameworks
      react: !!document.querySelector('[data-reactroot], [data-reactid]') || !!window.__REACT_DEVTOOLS_GLOBAL_HOOK__,
      vue: !!window.__VUE__ || !!document.querySelector('[data-v-]'),
      angular: !!window.ng || !!document.querySelector('[ng-version], [_nghost]'),
      svelte: !!document.querySelector('[class*="svelte-"]'),
      next_js: !!window.__NEXT_DATA__ || !!document.getElementById('__next'),
      nuxt: !!window.__NUXT__ || !!document.getElementById('__nuxt'),
      gatsby: !!document.getElementById('___gatsby'),
      astro: !!document.querySelector('[data-astro-cid]') || html.includes('astro'),

      // CMS
      wordpress: !!meta.generator?.includes('WordPress') || html.includes('wp-content'),
      shopify: html.includes('Shopify') || scripts.some((s) => s.includes('shopify')),
      webflow: html.includes('webflow') || !!meta.generator?.includes('Webflow'),
      wix: html.includes('wix.com') || !!window.wixBiSession,
      squarespace: html.includes('squarespace') || !!meta.generator?.includes('Squarespace'),
      drupal: !!meta.generator?.includes('Drupal') || html.includes('drupal'),
      ghost: !!meta.generator?.includes('Ghost'),

      // Libraries
      jquery: !!window.jQuery,
      bootstrap: !!document.querySelector('link[href*="bootstrap"]') || scripts.some((s) => s.includes('bootstrap')),
      tailwind: !!document.querySelector('[class*="tw-"], [class*="flex "], [class*="grid "]'),

      // CDN / hosting
      cloudflare: !!document.querySelector('script[src*="cloudflare"]') || html.includes('cf-ray'),
      vercel: html.includes('vercel') || scripts.some((s) => s.includes('vercel')),
      netlify: html.includes('netlify'),
      aws: scripts.some((s) => s.includes('amazonaws')),
    };

    const detected = Object.entries(detect)
      .filter(([, found]) => found)
      .map(([name]) => name);

    return {
      technologies: detected.join(', ') || 'none detected',
      tech_count: detected.length,
      cms: meta.generator || 'unknown',
      has_jquery: detect.jquery,
      has_react: detect.react,
      is_spa: detect.react || detect.vue || detect.angular || detect.svelte,
    };
  });
};
