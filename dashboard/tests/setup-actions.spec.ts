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

for (const viewport of [
  { width: 1440, height: 900 },
  { width: 390, height: 844 },
  { width: 320, height: 568 },
  { width: 844, height: 390 },
]) {
  test(`Schedule form scrolls without hiding actions at ${viewport.width}x${viewport.height}`, async ({ page }) => {
    await page.setViewportSize(viewport);
    const writes = await mockApi(page);
    await page.goto('/#/setup');
    const start = page.getByRole('button', { name: 'Start Crawl', exact: true });
    await start.click();
    await expect(page.getByText('Please enter a URL or file path', { exact: true })).toBeVisible();
    await page.locator('#url').fill('https://schedule.example');
    const toggle = page.getByRole('button', { name: 'Schedule…', exact: true });
    await toggle.click();
    const region = page.getByRole('region', { name: 'Schedule this crawl' });
    await expect(region.locator('#sf-name')).toBeFocused();
    await expect(region.locator('#sf-name')).toBeInViewport();
    expect(await page.locator('.app-shell').evaluate(el => el.scrollTop)).toBe(0);
    const hide = page.getByRole('button', { name: 'Hide schedule', exact: true });
    await expect(hide).toHaveAttribute('aria-expanded', 'true');
    for (const position of ['top', 'bottom']) {
      await page.locator('.workspace-content').evaluate((el, position) => {
        el.scrollTop = position === 'top' ? 0 : el.scrollHeight;
      }, position);
      await expect.poll(async () => {
        const box = await page.locator('.workspace-page .sticky').boundingBox();
        return Math.abs(viewport.height - box!.y - box!.height);
      }).toBeLessThan(1);
      expect(await start.evaluate(el => {
        const box = el.getBoundingClientRect();
        const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
        return hit === el || el.contains(hit);
      })).toBe(true);
    }
    await region.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(region).toHaveCount(0);
    await expect(toggle).toBeFocused();
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await toggle.click();
    await region.locator('#sf-name').fill('Mobile schedule');
    await region.getByLabel('When', { exact: true }).selectOption('custom');
    await region.getByLabel('Cron expression', { exact: true }).fill('30 1 * * *');
    await region.getByRole('button', { name: 'Create schedule', exact: true }).click();
    await expect(page).toHaveURL(/#\/schedules$/);
    expect(writes.find(write => write.path === '/api/schedules')?.body).toMatchObject({
      name: 'Mobile schedule', cron: '30 1 * * *', config: { seedUrl: 'https://schedule.example' },
    });
  });
}
