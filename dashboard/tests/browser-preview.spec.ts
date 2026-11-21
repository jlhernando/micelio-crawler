import { test, expect } from '@playwright/test';
import { mockApi } from './fixtures';

const frame = {
  jpeg: '', url: 'https://example.com/', browserUrl: 'https://example.com/',
  title: 'Source heading', statusCode: 200, width: 1920, height: 1080,
  mode: 'html', userAgent: 'Micelio/1.0', capturedAt: '2026-11-13T18:00:00Z',
};

for (const width of [1440, 390]) {
  test(`Crawler preview is a still image and follows JavaScript settings at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1000 });
    await mockApi(page);
    const requests: Array<Record<string, unknown>> = [];
    await page.route('**/api/browser-preview', async route => {
      expect(route.request().method()).toBe('POST');
      const config = route.request().postDataJSON().config;
      requests.push(config);
      return route.fulfill({ json: { frame: {
        ...frame, mode: config.jsRendering ? 'javascript' : 'html',
        title: config.jsRendering ? 'Rendered heading' : 'Source heading',
      } } });
    });
    await page.goto('/#/setup');
    const viewer = page.getByTestId('browser-preview');
    await expect(viewer.getByRole('button', { name: 'Preview crawler view', exact: true })).toBeDisabled();
    await page.locator('#url').fill('https://example.com/category?q=1');
    await viewer.getByRole('button', { name: 'Preview crawler view', exact: true }).click();
    await expect(viewer.getByText('Source heading', { exact: true })).toBeVisible();
    expect(requests[0]).toMatchObject({ seedUrl: 'https://example.com/category?q=1', jsRendering: false, nativeChrome: false, respectRobots: true });
    expect(requests[0]).not.toHaveProperty('snippetPaths');
    await expect(viewer.getByTestId('preview-url')).toHaveText('https://example.com/');
    const image = viewer.locator('img');
    await expect(image).toHaveCount(1);
    await expect(viewer.locator('button img')).toHaveCount(0);
    await expect(viewer.getByRole('button', { name: /Scroll|Reload page|Refresh image/ })).toHaveCount(0);
    await page.waitForTimeout(2100);
    expect(requests).toHaveLength(1);
    await page.getByText('JavaScript Rendering', { exact: true }).click();
    await page.getByLabel('Enable JS rendering (Playwright)', { exact: true }).check();
    await expect(viewer.getByTestId('preview-stale')).toBeVisible();
    await viewer.getByRole('button', { name: 'Update preview', exact: true }).click();
    await expect(viewer.getByText('Rendered heading', { exact: true })).toBeVisible();
    await expect(viewer.getByTestId('preview-stale')).toHaveCount(0);
    expect(requests[1].jsRendering).toBe(true);
    await viewer.getByRole('button', { name: 'Close preview', exact: true }).click();
    await expect(image).toHaveCount(0);
    expect(requests).toHaveLength(2);
  });
}

test('Crawler preview shows block responses and reports capture failures', async ({ page }) => {
  await mockApi(page);
  let fail = true;
  await page.route('**/api/browser-preview', async route => {
    if (fail) return route.fulfill({ status: 502, json: { error: 'Chrome unavailable' } });
    return route.fulfill({ json: { frame: { ...frame, statusCode: 403 } } });
  });
  await page.goto('/#/setup');
  await page.locator('#url').fill('https://example.com/');
  const viewer = page.getByTestId('browser-preview');
  await viewer.getByRole('button', { name: 'Preview crawler view', exact: true }).click();
  await expect(viewer.getByRole('alert')).toContainText('Chrome unavailable');
  fail = false;
  await viewer.getByRole('button', { name: 'Preview crawler view', exact: true }).click();
  await expect(viewer.getByText('HTTP 403', { exact: true })).toBeVisible();
  await expect(viewer.getByRole('status')).toContainText('The site blocked this request');
});

test('Crawler preview keeps native mode available and flags changes during capture', async ({ page }) => {
  await mockApi(page);
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  let submitted = false;
  await page.route('**/api/browser-preview', async route => {
    expect(route.request().postDataJSON().config.nativeChrome).toBe(true);
    submitted = true;
    await pending;
    return route.fulfill({ json: { frame: { ...frame, mode: 'native' } } });
  });
  await page.goto('/#/setup');
  await page.locator('#url').fill('https://example.com/');
  await page.getByLabel('Native Chrome', { exact: true }).check();
  const viewer = page.getByTestId('browser-preview');
  await expect(viewer.getByTestId('preview-mode')).toHaveText('Native Chrome');
  await viewer.getByRole('button', { name: 'Preview crawler view', exact: true }).click();
  await expect.poll(() => submitted).toBe(true);
  await page.locator('#url').fill('https://other.example/');
  release();
  await expect(viewer.getByText('HTTP 200', { exact: true })).toBeVisible();
  await expect(viewer.getByTestId('preview-stale')).toBeVisible();
  await expect(viewer.getByTestId('preview-url')).toHaveText('https://example.com/');
});

test('Closing an in-flight preview cancels it and ignores its late response', async ({ page }) => {
  await mockApi(page);
  let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  let submitted = false;
  await page.route('**/api/browser-preview', async route => {
    submitted = true;
    await pending;
    await route.fulfill({ json: { frame } }).catch(() => {});
  });
  await page.goto('/#/setup');
  await page.locator('#url').fill('https://example.com/');
  const viewer = page.getByTestId('browser-preview');
  await viewer.getByRole('button', { name: 'Preview crawler view', exact: true }).click();
  await expect.poll(() => submitted).toBe(true);
  await viewer.getByRole('button', { name: 'Cancel preview', exact: true }).click();
  release();
  await expect(viewer.getByRole('button', { name: 'Preview crawler view', exact: true })).toBeEnabled();
  await expect(viewer.locator('img')).toHaveCount(0);
  await expect(viewer.getByRole('alert')).toHaveCount(0);
});
