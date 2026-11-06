/**
 * Console Errors & Warnings Detection
 *
 * Captures JavaScript console errors and warnings that occur during page load.
 * Useful for finding broken scripts, missing resources, and JS errors.
 *
 * TypeScript crawler only (it listens to page events and reloads the page):
 * Usage: npx tsx src/cli.ts spider https://example.com --snippet snippets/console-errors.js
 * The Go crawler records JavaScript errors itself with --js (jsErrors).
 */
export default async (page) => {
  const errors = [];
  const warnings = [];

  // Listen for console messages
  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      errors.push(msg.text().substring(0, 200));
    } else if (msg.type() === 'warning') {
      warnings.push(msg.text().substring(0, 200));
    }
  });

  // Listen for page errors (unhandled exceptions)
  page.on('pageerror', (err) => {
    errors.push(err.message.substring(0, 200));
  });

  // Re-navigate to capture console output from page load
  const url = page.url();
  await page.goto(url, { waitUntil: 'networkidle', timeout: 15000 });

  // Wait a moment for any deferred JS to execute
  await page.waitForTimeout(1000);

  return {
    error_count: errors.length,
    warning_count: warnings.length,
    errors: errors.slice(0, 10).join(' || '),
    warnings: warnings.slice(0, 5).join(' || '),
    has_js_errors: errors.length > 0,
  };
};
