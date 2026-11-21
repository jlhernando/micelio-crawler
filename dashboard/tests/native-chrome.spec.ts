import { test, expect } from '@playwright/test';
import { mockApi } from './fixtures';

test('Native Chrome submits the browser transport with compatible settings', async ({page}) => {
  const writes = await mockApi(page);
  await page.goto('/#/setup');
  await page.locator('#url').fill('https://example.com/');
  await page.getByLabel('Native Chrome', { exact: true }).check();
  await expect(page.getByTestId('native-chrome-help')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Preview crawler view', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: 'Start Crawl', exact: true }).click();
  await expect.poll(() => writes.filter(w => w.path === '/api/crawl/start').length).toBe(1);
  const body = writes.find(w => w.path === '/api/crawl/start')!.body;
  expect(body).toMatchObject({ nativeChrome: true, concurrency: 1, delay: 1000, respectRobots: true, noAgentCheck: true, noSoft404Probe: true, checkExternal: false, discoverSitemaps: false, pageWeight: false });
  expect(body).not.toHaveProperty('device');
  expect(body).not.toHaveProperty('cookies');
  expect(body).not.toHaveProperty('customHeaders');
});

test('Changing away from Spider clears native mode', async ({page}) => {
  await mockApi(page);
  await page.goto('/#/setup');
  await page.getByLabel('Native Chrome', { exact: true }).check();
  await page.getByRole('button', {name:'URL List', exact:true}).click();
  await page.getByRole('button', {name:'Spider', exact:true}).click();
  await expect(page.getByLabel('Native Chrome', { exact: true })).not.toBeChecked();
});
