/**
 * Performance Metrics
 *
 * Extracts Core Web Vitals and performance metrics from the browser:
 * LCP, CLS, TTFB, DOM size, render-blocking resources, and more.
 *
 * Usage: micelio spider https://example.com --snippet snippets/performance.js
 */
export default async (page) => {
  // Wait for page to fully settle
  await page.waitForTimeout(2000);

  return await page.evaluate(() => {
    const perf = performance.getEntriesByType('navigation')[0];
    const paintEntries = performance.getEntriesByType('paint');

    // First Contentful Paint
    const fcp = paintEntries.find((e) => e.name === 'first-contentful-paint');

    // Largest Contentful Paint (from PerformanceObserver if available)
    let lcp = null;
    try {
      const lcpEntries = performance.getEntriesByType('largest-contentful-paint');
      if (lcpEntries.length > 0) {
        lcp = Math.round(lcpEntries[lcpEntries.length - 1].startTime);
      }
    } catch {}

    // DOM metrics
    const domNodes = document.querySelectorAll('*').length;
    const domDepth = (() => {
      let maxDepth = 0;
      const walk = (node, depth) => {
        if (depth > maxDepth) maxDepth = depth;
        for (const child of node.children) walk(child, depth + 1);
      };
      walk(document.documentElement, 0);
      return maxDepth;
    })();

    // Resource counts
    const resources = performance.getEntriesByType('resource');
    const scripts = resources.filter((r) => r.initiatorType === 'script');
    const stylesheets = resources.filter((r) => r.initiatorType === 'link' || r.initiatorType === 'css');
    const images = resources.filter((r) => r.initiatorType === 'img');

    // Render-blocking: scripts without async/defer in <head>
    const blockingScripts = Array.from(document.head.querySelectorAll('script[src]:not([async]):not([defer])'));

    return {
      ttfb_ms: perf ? Math.round(perf.responseStart - perf.requestStart) : null,
      fcp_ms: fcp ? Math.round(fcp.startTime) : null,
      lcp_ms: lcp,
      dom_content_loaded_ms: perf ? Math.round(perf.domContentLoadedEventEnd) : null,
      load_event_ms: perf ? Math.round(perf.loadEventEnd) : null,
      dom_nodes: domNodes,
      dom_depth: domDepth,
      total_resources: resources.length,
      total_scripts: scripts.length,
      total_stylesheets: stylesheets.length,
      total_images: images.length,
      total_transfer_kb: Math.round(resources.reduce((sum, r) => sum + (r.transferSize || 0), 0) / 1024),
      render_blocking_scripts: blockingScripts.length,
      render_blocking_urls: blockingScripts.map((s) => s.src).slice(0, 5).join(' | '),
    };
  });
};
