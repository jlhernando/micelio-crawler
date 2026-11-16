import { test, expect } from '@playwright/test';
import { mockApi } from './fixtures';

for (const width of [1440, 390]) {
  test(`Accordion and select chevrons align at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1100 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await mockApi(page);
    await page.goto('/#/setup');
    const trigger = page.getByRole('button', { name: 'HTTP & Authentication', exact: true });
    await trigger.click();
    await page.locator('#uaPreset').selectOption('Claude-SearchBot — Anthropic (search)');
    const arrow = trigger.locator('svg');
    await expect(arrow).toHaveCSS('flex-shrink', '0');
    const triggerBox = (await trigger.boundingBox())!;
    const arrowBox = (await arrow.boundingBox())!;
    const select = page.locator('#uaPreset');
    const selectBox = (await select.boundingBox())!;
    // Match the accordion column to the nested select's 16px icon and inset.
    expect(Math.abs(arrowBox.x + arrowBox.width / 2 - (selectBox.x + selectBox.width - 24))).toBeLessThanOrEqual(1);
    expect(Math.abs(arrowBox.y + arrowBox.height / 2 - (triggerBox.y + triggerBox.height / 2))).toBeLessThanOrEqual(1);
    await expect(arrow).toHaveAttribute('aria-hidden', 'true');
    await expect(select).toHaveCSS('appearance', 'none');
    await expect(select).toHaveCSS('background-size', '16px 16px');
    await expect(select).toHaveCSS('padding-inline-end', '44px');
    // Styling preserves the native accessible, focusable control.
    await select.focus();
    await expect(select).toBeFocused();
    await expect(select).toHaveRole('combobox');
    await trigger.click();
    await expect(trigger).toHaveAttribute('aria-expanded', 'false');
    await trigger.click();
    await expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(await page.locator('.workspace-content').evaluate(el => el.scrollWidth > el.clientWidth + 1)).toBe(false);
    await select.selectOption('ChatGPT-User — OpenAI (user request)');
    await trigger.locator('..').evaluate(el => el.scrollIntoView({ block: 'center' }));
    if (process.env.ARROW_SCREENSHOTS) {
      const box = (await trigger.locator('..').boundingBox())!;
      const top = Math.max(0, box.y - 55);
      await page.screenshot({ path: `${process.env.ARROW_SCREENSHOTS}/arrows-${width}.png`, clip: { x: box.x, y: top, width: box.width, height: Math.min(1100-top, box.height+110) } });
    }
  });
}

test('Wrapped accordion labels cannot shrink the icon; high contrast keeps native arrows', async ({ page }) => {
  await mockApi(page);
  await page.setViewportSize({ width: 320, height: 1100 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/#/setup');
  const trigger = page.getByRole('button', { name: 'HTTP & Authentication', exact: true });
  await trigger.click();
  await trigger.locator('span').evaluate((el: HTMLElement) => { el.style.maxWidth = '100px'; });
  await expect(trigger.locator('svg')).toHaveCSS('width', '16px');
  const [button, icon] = await Promise.all([trigger.boundingBox(), trigger.locator('svg').boundingBox()]);
  expect(Math.abs(icon!.y + icon!.height/2 - (button!.y + button!.height/2))).toBeLessThanOrEqual(1);
  await page.emulateMedia({ forcedColors: 'active' });
  await expect(page.locator('#uaPreset')).toHaveCSS('appearance', 'auto');
  await expect(page.locator('#uaPreset')).toHaveCSS('background-image', 'none');
});
