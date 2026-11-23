import { test, expect } from '@playwright/test';
import { mockApi } from './fixtures';

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
  { width: 390, height: 844 },
  { width: 320, height: 568 },
]) {
  test(`Setup actions stay at the viewport bottom at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await mockApi(page);
    await page.goto('/#/setup');
    const start = page.getByRole('button', { name: 'Start Crawl', exact: true });
    await expect(start).toBeVisible();
    const bar = page.locator('.workspace-page .sticky');
    const content = page.locator('.workspace-content');
    for (const position of ['top', 'middle', 'bottom']) {
      await content.evaluate((el, position) => {
        el.scrollTop = position === 'top' ? 0 : position === 'middle' ? el.scrollHeight / 2 : el.scrollHeight;
      }, position);
      await expect.poll(async () => {
        const box = await bar.boundingBox();
        return Math.abs(viewport.height - box!.y - box!.height);
      }).toBeLessThan(1);
      await expect(start).toBeInViewport();
    }
    // Expanding the last panel must leave its controls and the action reachable.
    await page.getByText('Output & Storage', { exact: true }).click();
    await expect(page.getByLabel('Output Format', { exact: true })).toBeInViewport();
    await expect(start).toBeInViewport();
    expect(await content.evaluate(el => el.scrollWidth > el.clientWidth + 1)).toBe(false);

    // Other routes retain their normal page spacing.
    await page.goto('/#/settings');
    await expect(page.getByRole('heading', { name: 'Settings', exact: true })).toBeVisible();
    expect(await content.evaluate(el => parseFloat(getComputedStyle(el).paddingBottom))).toBeGreaterThan(0);
  });
}
