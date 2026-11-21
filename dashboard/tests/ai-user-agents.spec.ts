import { test, expect } from '@playwright/test';
import { mockApi } from './fixtures';
import { aiUserAgentPresets } from '../src/lib/aiUserAgents';

for (const jsRendering of [false, true]) {
  test(`AI presets submit their User-Agent with JS ${jsRendering ? 'enabled' : 'disabled'}`, async ({ page }) => {
    const writes = await mockApi(page);
    expect(aiUserAgentPresets).toHaveLength(8);
    for (const preset of aiUserAgentPresets) {
      await page.goto('/#/setup');
      await page.locator('#url').fill('https://studio.example');
      await page.getByRole('button', { name: 'HTTP & Authentication', exact: true }).click();
      await expect(page.locator('optgroup[label="AI agents"] option')).toHaveCount(8);
      await page.locator('#uaPreset').selectOption(preset.label);
      await expect(page.locator('#ua')).toHaveValue(preset.value);
      await expect(page.getByTestId('ai-user-agent-help')).toContainText('not the provider');
      await expect(page.getByRole('link', { name: 'Provider documentation' })).toHaveAttribute('href', preset.source);
      if (jsRendering) {
        await page.getByRole('button', { name: 'JavaScript Rendering', exact: true }).click();
        await page.getByLabel('Enable JS rendering (Playwright)', { exact: true }).check();
      }
      const before = writes.filter(w => w.path === '/api/crawl/start').length;
      await page.getByRole('button', { name: 'Start Crawl', exact: true }).click();
      await expect(page).toHaveURL(/monitor\/live/);
      const requests = writes.filter(w => w.path === '/api/crawl/start');
      expect(requests).toHaveLength(before + 1);
      expect(requests.at(-1)!.body).toMatchObject({ userAgent: preset.value, jsRendering, nativeChrome: false, respectRobots: true });
    }
  });
}

test('A saved AI User-Agent is recognized and remains editable', async ({ page }) => {
  await mockApi(page);
  await page.route('**/api/settings', route => route.fulfill({ json: { defaultUserAgent: 'Claude-User' } }));
  await page.goto('/#/setup');
  await page.getByRole('button', { name: 'HTTP & Authentication', exact: true }).click();
  await expect(page.locator('#uaPreset')).toHaveValue('Claude-User — Anthropic (user request)');
  await page.locator('#ua').fill('MyAudit/1.0');
  await expect(page.locator('#uaPreset')).toHaveValue('__custom');
  await expect(page.getByTestId('ai-user-agent-help')).not.toBeVisible();
  await page.locator('#uaPreset').selectOption('__default');
  await expect(page.locator('#ua')).not.toBeVisible();
});

test('Native Chrome disables AI presets and omits an earlier custom User-Agent', async ({ page }) => {
  const writes = await mockApi(page);
  await page.goto('/#/setup');
  await page.locator('#url').fill('https://studio.example');
  await page.getByRole('button', { name: 'HTTP & Authentication', exact: true }).click();
  await page.locator('#uaPreset').selectOption('Perplexity-User — Perplexity (user request)');
  await page.getByLabel('Native Chrome', { exact: true }).check();
  await expect(page.locator('#uaPreset')).toBeDisabled();
  await expect(page.getByTestId('ai-user-agent-help')).not.toBeVisible();
  await page.getByRole('button', { name: 'Start Crawl', exact: true }).click();
  await expect(page).toHaveURL(/monitor\/live/);
  expect(writes.find(w => w.path === '/api/crawl/start')!.body).not.toHaveProperty('userAgent');
});
